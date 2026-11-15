package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/connectors"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Test and confirm a company connector (contract 0033 §3, §4). The owner runs one GET action of the connector against
// the real vendor; a good answer makes it tested, and an owner may then confirm its write actions within 24 hours.
// The answer is scrubbed and cut before it is stored or returned; the audit entry never holds the body.

const msgConnectorTestPerson = "Uji dan konfirmasi penghubung hanya bisa dilakukan orang yang masuk lewat Console, bukan token API."

const (
	maxTestRows  = 20
	maxTestBytes = 4096
)

// connectorTestGuard refuses the caller and the connector, and answers when either is refused. The assistant may read
// the test (write false) so Raka can explain it; it never runs or confirms one.
func (s *Server) connectorTestGuard(w http.ResponseWriter, c *caller, name string, write bool) bool {
	switch c.p.Via {
	case "ai":
		if write {
			writeError(w, 403, "ai_forbidden", "Raka dan helper tidak bisa menguji atau mengonfirmasi penghubung; minta pemilik usaha.")
			return false
		}
	case "oidc":
		writeError(w, 403, "ai_forbidden", "Raka dan helper tidak bisa menguji atau mengonfirmasi penghubung; minta pemilik usaha.")
		return false
	}
	if write && !c.p.Human() { // a token carries its minter's roles, but a test and a confirmation are a person's act
		writeError(w, 403, "forbidden", msgConnectorTestPerson)
		return false
	}
	if !s.mayManageSecrets(c) {
		writeError(w, 403, "forbidden", msgConnectorAuthForbidden)
		return false
	}
	if s.Connectors == nil {
		writeError(w, 501, "connectors_disabled", "connector runtime is not configured on this kernel")
		return false
	}
	if !meta.IsCompanyConnector(name) {
		writeError(w, 404, "not_company_connector", "Hanya penghubung milik perusahaan (nama diawali x_) yang perlu diuji.")
		return false
	}
	return s.connectorExists(w, name)
}

func (s *Server) postConnectorTest(w http.ResponseWriter, r *http.Request, c *caller) {
	name := r.PathValue("connector")
	if !s.connectorTestGuard(w, c, name, true) {
		return
	}
	var body struct {
		Action string         `json:"action"`
		Input  map[string]any `json:"input"`
	}
	if err := readJSON(r, &body); err != nil || body.Action == "" {
		writeError(w, 400, "bad_json", `kirim {"action": "…", "input": {…}}`)
		return
	}
	ctx := r.Context()
	_, act, err := s.Connectors.Action(name, body.Action)
	if err != nil {
		writeError(w, 404, "unknown_action", "Penghubung ini tidak punya aksi "+body.Action+".")
		return
	}
	if m, _ := meta.AsMap(act["request"])["method"].(string); strings.ToUpper(m) != "GET" {
		writeError(w, 422, "test_get_only", "Uji hanya memakai aksi yang membaca (GET); aksi yang menulis dikonfirmasi pemilik setelah uji berhasil.")
		return
	}
	state, hash, _, err := s.Connectors.State(ctx, c.actor.Org, name)
	if err != nil {
		fail(w, err)
		return
	}
	if state == "needs_auth" {
		writeError(w, 409, "connector_needs_auth", "Isi dulu kunci penghubung ini (Pengaturan → Penghubung), lalu uji.")
		return
	}
	out, callErr := s.Connectors.Call(connectors.WithTesting(ctx), c.actor.Org, name, body.Action, body.Input)
	ok, status := callErr == nil, 200
	var result any
	if ok {
		result = cutTestResult(out)
	} else {
		msg, code := callErr.Error(), "call_failed"
		var ce *connectors.CallError
		if errors.As(callErr, &ce) {
			status = ce.Status
		}
		if sc, e := s.Connectors.Scrubber(ctx, c.actor.Org, name); e == nil {
			msg = sc.String(msg)
		}
		if len(msg) > 500 {
			msg = msg[:500] + "…"
		}
		result = map[string]any{"error": code, "message": msg}
	}
	raw, _ := json.Marshal(result)
	if err := store.SaveConnectorTest(ctx, s.DB, c.actor.Org, name, hash, c.actor.ID, ok, raw); err != nil {
		fail(w, err)
		return
	}
	store.Audit(ctx, s.DB, c.actor.Org, c.actor.ID, "connector.test", "connector/"+name, body.Action,
		map[string]any{"action": body.Action, "ok": ok, "status": status})
	st, _, _, err := s.Connectors.State(ctx, c.actor.Org, name) // a failed re-test keeps a tested connector tested
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"state": st, "ok": ok, "result": json.RawMessage(raw)})
}

// cutTestResult keeps a test answer small: at most 20 items of the first list in the output (keys in sorted order),
// then items from the end of that list until the JSON is within 4096 bytes. An answer with no list that is still too
// big, or a list that does not fit even empty, becomes {"truncated": true, "bytes": N}.
func cutTestResult(out map[string]any) any {
	key := ""
	keys := make([]string, 0, len(out))
	for k := range out {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if _, ok := out[k].([]any); ok {
			key = k
			break
		}
	}
	size := func(v any) int { b, _ := json.Marshal(v); return len(b) }
	cut := map[string]any{}
	for k, v := range out {
		cut[k] = v
	}
	if key != "" {
		items := out[key].([]any)
		if len(items) > maxTestRows {
			items = items[:maxTestRows]
		}
		for {
			cut[key] = items
			if size(cut) <= maxTestBytes || len(items) == 0 {
				break
			}
			items = items[:len(items)-1]
		}
	}
	if n := size(cut); n > maxTestBytes {
		return map[string]any{"truncated": true, "bytes": n}
	}
	return cut
}

func (s *Server) getConnectorTest(w http.ResponseWriter, r *http.Request, c *caller) {
	name := r.PathValue("connector")
	if !s.connectorTestGuard(w, c, name, false) {
		return
	}
	ctx := r.Context()
	state, hash, writesOK, err := s.Connectors.State(ctx, c.actor.Org, name)
	if err != nil {
		fail(w, err)
		return
	}
	out := map[string]any{"state": state, "hash": hash, "tested_at": nil, "tested_by": "", "result": nil,
		"writes_confirmed_at": nil, "write_actions": s.writeActions(name)}
	row, err := store.GetConnectorCheck(ctx, s.DB, c.actor.Org, name)
	if err != nil {
		fail(w, err)
		return
	}
	if row != nil && row.Hash == hash { // a test of an older file says nothing about this one
		out["tested_at"], out["tested_by"] = row.TestedAt, row.TestedBy
		if len(row.LastResult) > 0 {
			out["result"] = row.LastResult
		}
		if writesOK {
			out["writes_confirmed_at"] = row.WritesConfirmedAt
		}
	}
	writeJSON(w, 200, out)
}

func (s *Server) writeActions(name string) []string {
	if c := s.Engine.Reg().Connectors[name]; c != nil {
		return meta.WriteActions(meta.AsMap(c.Data))
	}
	return []string{}
}

func (s *Server) postConnectorConfirmWrites(w http.ResponseWriter, r *http.Request, c *caller) {
	name := r.PathValue("connector")
	// the assistant is refused first: it acts with the roles of the owner who minted its token
	if c.p.Via == "ai" || c.p.Via == "oidc" {
		writeError(w, 403, "ai_forbidden", "Raka dan helper tidak bisa menguji atau mengonfirmasi penghubung; minta pemilik usaha.")
		return
	}
	if !c.p.Human() {
		writeError(w, 403, "forbidden", msgConnectorTestPerson)
		return
	}
	if !isOwner(c) {
		writeError(w, 403, "owner_required", "Hanya pemilik usaha yang bisa mengonfirmasi aksi tulis penghubung.")
		return
	}
	if s.Connectors == nil {
		writeError(w, 501, "connectors_disabled", "connector runtime is not configured on this kernel")
		return
	}
	if !meta.IsCompanyConnector(name) {
		writeError(w, 404, "not_company_connector", "Hanya penghubung milik perusahaan (nama diawali x_) yang perlu dikonfirmasi.")
		return
	}
	if !s.connectorExists(w, name) {
		return
	}
	ctx := r.Context()
	state, hash, _, err := s.Connectors.State(ctx, c.actor.Org, name)
	if err != nil {
		fail(w, err)
		return
	}
	if state == "needs_auth" {
		writeError(w, 409, "connector_needs_auth", "Isi dulu kunci penghubung ini (Pengaturan → Penghubung), lalu uji.")
		return
	}
	const notTested = "Uji penghubung ini dulu; konfirmasi hanya berlaku dalam 24 jam setelah uji yang berhasil."
	if state != "tested" {
		writeError(w, 409, "not_tested", notTested)
		return
	}
	if err := store.ConfirmConnectorWrites(ctx, s.DB, c.actor.Org, name, hash, c.actor.ID); errors.Is(err, store.ErrConnectorNotTested) {
		writeError(w, 409, "not_tested", notTested)
		return
	} else if err != nil {
		fail(w, err)
		return
	}
	actions := s.writeActions(name)
	store.Audit(ctx, s.DB, c.actor.Org, c.actor.ID, "connector.writes_confirmed", "connector/"+name, strings.Join(actions, ", "),
		map[string]any{"write_actions": actions})
	row, err := store.GetConnectorCheck(ctx, s.DB, c.actor.Org, name)
	if err != nil || row == nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"state": "tested", "writes_confirmed_at": row.WritesConfirmedAt, "write_actions": actions})
}
