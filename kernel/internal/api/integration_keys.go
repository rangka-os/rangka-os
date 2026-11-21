package api

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/plan"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Integration keys (contract 0075): an API token held by a service member, limited by its role and by api: scopes.

const (
	msgKeyRoute     = "Kunci integrasi ini hanya bisa dipakai untuk membaca dan menulis data lewat API data."
	msgKeyMintOwner = "Kunci integrasi hanya bisa dibuat oleh pemilik usaha yang masuk lewat browser."
	msgKeyViaToken  = "Gunakan kunci integrasi (POST /api/v1/integration-keys) untuk scope api:."
)

// keyLimited refuses an integration key outside what its scopes allow. It is an errScopeLimited (403 scope_limited)
// so every caller that already maps that error answers it; /mcp tells it apart (mcp.Handler.Forbidden).
type keyLimited struct{ msg string }

func (e *keyLimited) Error() string { return e.msg }
func (e *keyLimited) Is(target error) bool {
	return target == errScopeLimited || target == errKeyLimited
}

// errKeyLimited matches every integration-key refusal with errors.Is.
var errKeyLimited = &keyLimited{msg: msgKeyRoute}

// keyRoute classifies a request an integration key may make: the action its scope must allow ("read" or "write")
// and the model, or an empty model for a route without one (openapi.json). ok is false for every other route: the
// allow list is the guard (contract 0075 §4).
func keyRoute(r *http.Request) (action, model string, ok bool) {
	rest, found := strings.CutPrefix(r.URL.EscapedPath(), "/api/v1/")
	if !found {
		return "", "", false
	}
	read := r.Method == http.MethodGet || r.Method == http.MethodHead
	seg := strings.Split(rest, "/")
	for i := range seg {
		u, err := url.PathUnescape(seg[i])
		if err != nil || u == "" {
			return "", "", false
		}
		seg[i] = u
	}
	switch {
	case len(seg) == 1 && seg[0] == "openapi.json" && read:
		return "", "", true
	case len(seg) == 2 && seg[0] == "aggregate" && read:
		return "read", seg[1], true
	case seg[0] != "r" || len(seg) < 2:
		return "", "", false
	}
	model = seg[1]
	switch len(seg) {
	case 2: // list, create
		switch {
		case read:
			return "read", model, true
		case r.Method == http.MethodPost:
			return "write", model, true
		}
	case 3: // one record, or submit many
		switch {
		case read:
			return "read", model, true
		case r.Method == http.MethodPatch || r.Method == http.MethodDelete:
			return "write", model, true
		case r.Method == http.MethodPost && seg[2] == "submit":
			return "write", model, true
		}
	case 4: // lifecycle verb; attachments and print are not for keys
		if r.Method == http.MethodPost && seg[3] != "attachments" && seg[3] != "print" && seg[3] != "actions" {
			return "write", model, true
		}
	case 5: // action
		if r.Method == http.MethodPost && seg[3] == "actions" {
			return "write", model, true
		}
	}
	return "", "", false
}

// keyGuard refuses an integration key outside the routes and models its scopes name. It runs where the kernel
// resolves the caller, so a new route cannot miss it.
func keyGuard(p *auth.Principal, r *http.Request) error {
	action, model, ok := keyRoute(r)
	if !ok {
		return errKeyLimited
	}
	if model != "" && !p.APIAllows(action, model) {
		return &keyLimited{msg: fmt.Sprintf("Kunci integrasi ini tidak punya izin api:%s:%s.", action, model)}
	}
	return nil
}

// keyOwner admits an owner with a browser session; it answers when it refuses.
func keyOwner(w http.ResponseWriter, c *caller) bool {
	if !c.p.Human() || !isOwner(c) {
		writeError(w, http.StatusForbidden, "forbidden", msgKeyMintOwner)
		return false
	}
	return true
}

// createIntegrationKey is POST /api/v1/integration-keys.
func (s *Server) createIntegrationKey(w http.ResponseWriter, r *http.Request, c *caller) {
	if !keyOwner(w, c) {
		return
	}
	var body struct {
		Name          string   `json:"name"`
		Role          string   `json:"role"`
		Outlets       []string `json:"outlets"`
		Scopes        []string `json:"scopes"`
		ExpiresInDays int      `json:"expires_in_days"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "bad_request", "Isi permintaan harus JSON yang valid.")
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" || body.ExpiresInDays < 0 {
		writeError(w, http.StatusUnprocessableEntity, "bad_request", "Nama kunci wajib diisi.")
		return
	}
	if err := s.checkRole(body.Role); err != nil {
		memberFail(w, err)
		return
	}
	reg := s.Engine.Reg()
	seen := map[string]bool{}
	scopes := []string{}
	for _, sc := range body.Scopes {
		_, model, ok := auth.SplitAPIScope(sc)
		if !ok || (model != "*" && reg.Model(model) == nil) {
			writeError(w, http.StatusUnprocessableEntity, "unknown_scope", fmt.Sprintf(`Scope "%s" tidak dikenal. Pakai api:read:<model>, api:write:<model> atau api:read:*.`, sc))
			return
		}
		if !seen[sc] {
			seen[sc] = true
			scopes = append(scopes, sc)
		}
	}
	if len(scopes) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "unknown_scope", "Pilih minimal satu scope untuk kunci ini.")
		return
	}
	org, ctx := c.actor.Org, r.Context()
	var tok string
	var id int64
	err := s.memberTx(ctx, func(tx *pg.Tx) error {
		if err := plan.CheckIntegrationKey(ctx, tx, reg); err != nil {
			return err
		}
		for _, o := range body.Outlets {
			found := false
			if reg.Model("outlet") != nil {
				var err error
				if found, err = store.Exists(ctx, tx, org, "outlet", o); err != nil {
					return err
				}
			}
			if !found {
				return &memberError{http.StatusUnprocessableEntity, "unknown_outlet", fmt.Sprintf(`Outlet "%s" tidak ditemukan.`, o)}
			}
		}
		var ttl time.Duration
		if body.ExpiresInDays > 0 {
			ttl = time.Duration(body.ExpiresInDays) * 24 * time.Hour
		}
		var err error
		tok, id, err = s.Auth.CreateIntegrationKey(ctx, tx, org, body.Name, body.Role, body.Outlets, scopes, ttl)
		if err != nil {
			return err
		}
		store.Audit(ctx, tx, org, c.p.User.Email, "auth.integration_key_created", "integration-key/"+body.Name, "Integration key created", map[string]any{"role": body.Role, "scopes": scopes})
		return nil
	})
	if err != nil {
		memberFail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "name": body.Name, "role": body.Role, "outlets": nonNil(body.Outlets), "scopes": scopes,
		"token": tok, "note": "shown once; send as Authorization: Bearer " + tok[:6] + "…"})
}

// listIntegrationKeys is GET /api/v1/integration-keys.
func (s *Server) listIntegrationKeys(w http.ResponseWriter, r *http.Request, c *caller) {
	if !keyOwner(w, c) {
		return
	}
	ctx := r.Context()
	res, err := s.DB.Query(ctx, `select t.id, t.name, m.role, m.outlets, t.scopes, t.created_at, t.last_used_at,
			case when t.req_day = current_date then t.req_count else 0 end
		from rk_tokens t join rk_users u on u.id = t.user_id join rk_members m on m.user_id = u.id and m.org_id = t.org_id
		where u.service and t.org_id = $1 order by t.id`, c.actor.Org)
	if err != nil {
		fail(w, err)
		return
	}
	rows := []map[string]any{}
	for _, row := range res.Rows {
		rows = append(rows, map[string]any{"id": row[0], "name": row[1], "role": row[2], "outlets": textList(row[3]), "scopes": textList(row[4]),
			"created_at": isoTime(row[5]), "last_used_at": isoTime(row[6]), "requests_today": row[7]})
	}
	var limit any
	if s.Engine != nil {
		if snap := s.Engine.Plan.Peek(s.DB, s.Engine.Reg(), s.Engine.Now(), s.logf); snap != nil {
			if lim, ok := snap.Current.Limit(plan.IntegrationKeys); ok {
				limit = lim
			}
		}
	}
	used, err := plan.IntegrationKeysUsed(ctx, s.DB)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": rows, "limit": limit, "used": used})
}

// deleteIntegrationKey is DELETE /api/v1/integration-keys/{id}: the service member goes, and its membership and
// token with it. Idempotent.
func (s *Server) deleteIntegrationKey(w http.ResponseWriter, r *http.Request, c *caller) {
	if !keyOwner(w, c) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	ctx := r.Context()
	res, err := s.DB.Query(ctx, `delete from rk_users u using rk_tokens t where t.id = $1 and t.org_id = $2 and t.user_id = u.id and u.service returning t.name`, id, c.actor.Org)
	if err != nil {
		fail(w, err)
		return
	}
	if len(res.Rows) > 0 {
		store.Audit(ctx, s.DB, c.actor.Org, c.p.User.Email, "auth.integration_key_revoked", "integration-key/"+fmt.Sprint(res.Rows[0][0]), "Integration key revoked", nil)
	}
	w.WriteHeader(http.StatusNoContent)
}

func isoTime(v any) any {
	if t, ok := v.(time.Time); ok {
		return t.UTC().Format(time.RFC3339)
	}
	return nil
}
