package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
	"github.com/rangka-os/rangka-os/kernel/internal/engine"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Events from paired devices (contract 0058 §4, kernel side of 0006 §3).
//
// POST /api/v1/edge/events {events: [{key, kind, seq, occurred_at, actor, payload}]} with Rangka-Edge-Sync: v1, from
// a paired device (rk_device cookie or rke_ token) or a PIN session on one. Each event is handled on its own, in seq
// order, and answered in request order: accepted, duplicate (the key is stored with the same kind and payload) or
// rejected with a code. Every outcome is stored in rk_edge_events by (org, key); an accepted write's row is stored
// inside the write's transaction. A transient failure (edgeTransient: lost race, database unreachable, deadline)
// answers the whole request 503: the device sends the batch again with the same keys, and what was done comes back
// as duplicate. Any other fault is that event's rejected / internal_error, stored, so one event never blocks the
// device's queue.
//
// screen.write payloads:
//   - create {screen, model, body, name?, submit?}: submit: true creates and submits in one unit (a paid sale);
//     name is the device name, used at submit (at create for a model that is not submittable)
//   - submit {model, name, draft, body?}: draft is the key of an earlier create event of the same device, or a draft
//     name the device got online
//   - cancel {model, name}
//   - action {model, name, action, args?}

const (
	edgeSyncHeader  = "Rangka-Edge-Sync"
	edgeSyncVersion = "v1"
	edgeMaxItems    = 100 // push_max_items (contract 0058 §2 register answer)
	edgeMaxKey      = 200
)

// edgeAckKinds are stored and acknowledged only; their handling is later work.
var edgeAckKinds = map[string]bool{"print.done": true, "print.failed": true, "device.read": true}

type edgeEventIn struct {
	Key        string          `json:"key"`
	Kind       string          `json:"kind"`
	Seq        *json.Number    `json:"seq"`
	OccurredAt string          `json:"occurred_at"`
	Actor      json.RawMessage `json:"actor"`
	Payload    json.RawMessage `json:"payload"`
}

type screenWrite struct {
	Screen string         `json:"screen"`
	Model  string         `json:"model"`
	Op     string         `json:"op"`
	Name   string         `json:"name"`
	Draft  string         `json:"draft"`
	Action string         `json:"action"`
	Submit bool           `json:"submit"`
	Body   map[string]any `json:"body"`
	Args   map[string]any `json:"args"`
}

type edgeResult struct {
	Key     string `json:"key"`
	Status  string `json:"status"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	Ref     string `json:"ref,omitempty"`
	Record  any    `json:"record,omitempty"`
}

// withEdge wraps the edge routes: a paired device alone, or a cashier's PIN session on one (contract 0058 §4). A
// person's full session is refused, even on a paired browser: its writes go through the record API.
func (s *Server) withEdge(h func(w http.ResponseWriter, r *http.Request, d *auth.Device)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := s.principal(r)
		switch {
		case errors.Is(err, auth.ErrDeviceRevoked):
			writeError(w, http.StatusUnauthorized, "device_revoked", auth.DeviceRevokedMessage)
			return
		case errors.Is(err, errScopeLimited):
			writeError(w, http.StatusForbidden, "scope_limited", err.Error())
			return
		case err != nil && !errors.Is(err, auth.ErrUnauthenticated):
			fail(w, err)
			return
		}
		if p == nil || p.Device == nil || (p.Via != "device" && p.Via != "pin") {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "Hanya perangkat yang sudah dipasangkan yang bisa mengirim data ini.")
			return
		}
		h(w, r.WithContext(pg.WithOrg(r.Context(), p.Device.Org)), p.Device)
	}
}

// edgeVersion answers 400 unsupported_sync_version unless the request speaks v1 (0006 §1).
func edgeVersion(w http.ResponseWriter, r *http.Request) bool {
	if strings.TrimSpace(r.Header.Get(edgeSyncHeader)) == edgeSyncVersion {
		return true
	}
	writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"code": "unsupported_sync_version",
		"message": "Versi sinkronisasi tidak didukung; perbarui aplikasi di perangkat ini.", "supported": []string{edgeSyncVersion}}})
	return false
}

// errEdgeRetry is a failure the device must retry: nothing is stored for the event.
type errEdgeRetry struct{ err error }

func (e errEdgeRetry) Error() string { return e.err.Error() }

func (s *Server) edgeEvents(w http.ResponseWriter, r *http.Request, d *auth.Device) {
	if !edgeVersion(w, r) {
		return
	}
	var body struct {
		Events []json.RawMessage `json:"events"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", "body must be {events: [...]}: "+err.Error())
		return
	}
	if len(body.Events) > edgeMaxItems {
		writeError(w, http.StatusBadRequest, "too_many_events", "Paling banyak "+strconv.Itoa(edgeMaxItems)+" kejadian per kiriman.")
		return
	}
	s.pruneIdempotency()
	evs := make([]edgeEventIn, len(body.Events))
	bad := make([]bool, len(body.Events))
	for i, raw := range body.Events {
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.UseNumber()
		if dec.Decode(&evs[i]) != nil {
			bad[i] = true
			var k struct{ Key any }
			json.Unmarshal(raw, &k)
			evs[i] = edgeEventIn{Key: str(k.Key)}
		}
		evs[i].Key = strings.TrimSpace(evs[i].Key) // the key as stored, in the answer too
	}
	// a device's events run in seq order (0006 §3); the answer keeps request order
	order := make([]int, len(evs))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int {
		sa, sb := seqOf(evs[a]), seqOf(evs[b])
		switch {
		case sa < sb:
			return -1
		case sa > sb:
			return 1
		}
		return 0
	})
	results := make([]edgeResult, len(evs))
	for _, i := range order {
		if bad[i] {
			results[i] = edgeResult{Key: evs[i].Key, Status: "rejected", Code: "payload_invalid", Message: "Kejadian ini bukan objek JSON yang benar."}
			continue
		}
		res, err := s.edgeEvent(r.Context(), d, evs[i])
		if err != nil && !edgeTransient(err) { // a fault of this event, not of the moment: answered, never blocks the queue
			res, err = s.edgeInternal(r.Context(), d, evs[i], err)
		}
		if err != nil {
			if s.Log != nil {
				s.Log("edge events: %s: %v", evs[i].Key, err)
			}
			w.Header().Set("Retry-After", idemRetrySecond)
			writeError(w, http.StatusServiceUnavailable, "retry_later", "Sistem sedang sibuk, kiriman akan dicoba lagi.")
			return
		}
		results[i] = res
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

func seqOf(e edgeEventIn) int64 {
	if e.Seq == nil {
		return 0
	}
	n, _ := e.Seq.Int64()
	return n
}

// edgeHash covers the canonical JSON of {kind, payload}: a replay must send the same, whitespace and key order aside.
func edgeHash(kind string, payload json.RawMessage) string {
	var p any
	if len(payload) > 0 {
		dec := json.NewDecoder(strings.NewReader(string(payload)))
		dec.UseNumber()
		dec.Decode(&p)
	}
	b, _ := json.Marshal(map[string]any{"kind": kind, "payload": p})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// edgeEvent handles one event. An error is transient: the request answers 503 and the device retries.
func (s *Server) edgeEvent(ctx context.Context, d *auth.Device, ev edgeEventIn) (edgeResult, error) {
	key := strings.TrimSpace(ev.Key)
	res := edgeResult{Key: ev.Key}
	if key == "" || len(key) > edgeMaxKey {
		res.Status, res.Code, res.Message = "rejected", "payload_invalid", "Kunci kejadian wajib diisi, paling panjang 200 karakter."
		return res, nil
	}
	hash := edgeHash(ev.Kind, ev.Payload)
	if done, ok, err := s.edgeStored(ctx, d, ev, hash); err != nil || ok {
		return done, err
	}
	seq := seqOf(ev)
	at, atErr := time.Parse(time.RFC3339, ev.OccurredAt)
	base := store.EdgeEvent{Org: d.Org, Key: key, Device: d.ID, Seq: seq, Kind: ev.Kind, Hash: hash}
	if atErr == nil {
		base.OccurredAt = at
	}
	reject := func(code, msg string) (edgeResult, error) {
		x := base
		x.Status, x.Code, x.Message = "rejected", code, msg
		if err := store.InsertEdgeEvent(ctx, s.DB, x); errors.Is(err, store.ErrEdgeKeyTaken) {
			done, _, err := s.edgeStored(ctx, d, ev, hash) // sent twice at once: the other one stored it first
			return done, err
		} else if err != nil {
			return res, err
		}
		res.Status, res.Code, res.Message = "rejected", code, msg
		return res, nil
	}
	if ev.Seq == nil || seq < 0 {
		return reject("payload_invalid", "Nomor urut (seq) wajib diisi, bilangan bulat 0 atau lebih.")
	}
	if ev.Kind != "screen.write" && !edgeAckKinds[ev.Kind] {
		return reject("unknown_kind", "Jenis kejadian \""+ev.Kind+"\" tidak dikenal.")
	}
	now := s.Engine.Now()
	if atErr != nil || at.After(now.Add(occurredAhead)) {
		return reject("payload_invalid", "occurred_at harus waktu RFC 3339, paling jauh 5 menit ke depan.")
	}
	if at.Before(now.Add(-occurredBack)) {
		return reject("occurred_at_too_old", "Kejadian ini lebih dari 30 hari yang lalu; kantor pusat yang memutuskan.")
	}
	if edgeAckKinds[ev.Kind] {
		x := base
		x.Status = "accepted"
		if err := store.InsertEdgeEvent(ctx, s.DB, x); errors.Is(err, store.ErrEdgeKeyTaken) {
			done, _, err := s.edgeStored(ctx, d, ev, hash)
			return done, err
		} else if err != nil {
			return res, err
		}
		res.Status = "accepted"
		return res, nil
	}
	var sw screenWrite
	if err := json.Unmarshal(ev.Payload, &sw); err != nil {
		return reject("payload_invalid", "Isi screen.write tidak sesuai: "+err.Error())
	}
	if msg := sw.check(); msg != "" {
		return reject("payload_invalid", msg)
	}
	a, ok, err := s.edgeActor(ctx, d, ev.Actor)
	if err != nil {
		return res, err
	}
	if !ok {
		return reject("actor_not_allowed", "Kasir ini bukan anggota outlet perangkat ini.")
	}
	draft := ""
	if sw.Op == "submit" {
		draft, ok, err = s.edgeDraft(ctx, d, a, sw)
		if err != nil {
			return res, err
		}
		if !ok {
			return reject("payload_invalid", "Draf "+sw.Draft+" tidak dikenal untuk perangkat ini.")
		}
	}
	// the write, with the event row stored in its transaction
	var ref string
	// Money: the computed currency fields the device sent with a create or submit body, top level and per row
	// (DeviceMoney); a submit without them keeps what its draft stored when this device made it. The engine keeps
	// the ones the actor may see and stores them as the device charged them (contract 0058 §10).
	var charged map[string]any
	if sw.Op == "create" || sw.Op == "submit" {
		charged = s.Engine.DeviceMoney(sw.Model, sw.Body)
	}
	dw := engine.DeviceWrite{Model: sw.Model, Name: sw.Name, OccurredAt: at, Money: charged, Event: key,
		Commit: func(ctx context.Context, q pg.Querier, target string) error {
			_, name, _ := strings.Cut(target, "/")
			x := base
			x.Status, x.Ref = "accepted", name
			if err := store.InsertEdgeEvent(ctx, q, x); err != nil {
				return err
			}
			ref = name
			return nil
		}}
	wctx := engine.WithDeviceWrite(engine.WithOccurredAt(ctx, at), dw)
	// A device write is never held for approval: the engine refuses it (approval_needed_offline) before anything is
	// saved (contract 0058 §10, ruling R7). Submits run with force = true: warnings must not block a sale the outlet
	// already made (§10, rules on replay); only errors that stay refusals there reject it.
	var record any
	switch sw.Op {
	case "create":
		var out *engine.Result
		if sw.Submit {
			out, err = s.Engine.CreateAndSubmit(wctx, a, sw.Model, sw.Body, true)
		} else {
			out, err = s.Engine.Create(wctx, a, sw.Model, sw.Body)
		}
		if err == nil {
			record = recordOut(out)
		}
	case "submit":
		var out *engine.Result
		if out, err = s.Engine.Submit(wctx, a, sw.Model, draft, true); err == nil {
			record = recordOut(out)
		}
	case "cancel":
		var out *engine.Result
		if out, err = s.Engine.Cancel(wctx, a, sw.Model, sw.Name); err == nil {
			record = recordOut(out)
		}
	case "action":
		var out *engine.ActionResult
		if out, err = s.Engine.RunAction(wctx, a, sw.Model, sw.Name, sw.Action, sw.Args, engine.ActionOpts{}); err == nil {
			record = out.Result
		}
	}
	if errors.Is(err, store.ErrEdgeKeyTaken) { // sent twice at once: the other write won, this one rolled back
		done, _, err := s.edgeStored(ctx, d, ev, hash)
		return done, err
	}
	if err != nil {
		code, msg, refusal := edgeRefusal(err)
		if !refusal {
			if edgeTransient(err) {
				return res, err
			}
			if s.Log != nil {
				s.Log("edge events: %s: internal error: %v", key, err)
			}
			return reject("internal_error", msgEdgeInternal)
		}
		return reject(code, msg)
	}
	res.Status, res.Ref, res.Record = "accepted", ref, record
	return res, nil
}

// check reports what is wrong with a screen.write payload ("" when nothing is).
func (sw *screenWrite) check() string {
	if sw.Model == "" {
		return "screen.write wajib menyebut model."
	}
	switch sw.Op {
	case "create":
		if sw.Body == nil {
			sw.Body = map[string]any{}
		}
	case "submit":
		if sw.Name == "" || sw.Draft == "" {
			return "submit wajib menyebut name (nomor dari perangkat) dan draft."
		}
	case "cancel":
		if sw.Name == "" {
			return "cancel wajib menyebut name dokumen yang dibatalkan."
		}
	case "action":
		if sw.Name == "" || sw.Action == "" {
			return "action wajib menyebut name dan action."
		}
		if sw.Args == nil {
			sw.Args = map[string]any{}
		}
	default:
		return "op harus create, submit, cancel atau action."
	}
	return ""
}

// edgeStored answers an event whose key is already stored: duplicate for the same kind and payload from the same
// device (with the record as it is now), the stored rejection again, else key_conflict. ok is false when the key is
// not stored.
func (s *Server) edgeStored(ctx context.Context, d *auth.Device, ev edgeEventIn, hash string) (edgeResult, bool, error) {
	res := edgeResult{Key: ev.Key}
	x, err := store.EdgeEventOf(ctx, s.DB, d.Org, strings.TrimSpace(ev.Key))
	if err != nil || x == nil {
		return res, false, err
	}
	switch {
	case x.Hash != hash || x.Device != d.ID:
		res.Status, res.Code, res.Message = "rejected", "key_conflict", "Kunci kejadian ini sudah dipakai untuk isi yang lain."
	case x.Status == "rejected":
		res.Status, res.Code, res.Message = "rejected", x.Code, x.Message
	default:
		res.Status, res.Ref = "duplicate", x.Ref
		res.Record = s.edgeRecord(ctx, d, ev, x.Ref)
	}
	return res, true, nil
}

// edgeRecord is the record a duplicate screen.write wrote, as its actor reads it now (nil when it cannot be read).
func (s *Server) edgeRecord(ctx context.Context, d *auth.Device, ev edgeEventIn, ref string) any {
	var sw screenWrite
	if ref == "" || ev.Kind != "screen.write" || json.Unmarshal(ev.Payload, &sw) != nil || sw.Model == "" || sw.Op == "action" {
		return nil
	}
	a, ok, err := s.edgeActor(ctx, d, ev.Actor)
	if err != nil || !ok {
		return nil
	}
	rec, err := s.Engine.Get(ctx, a, sw.Model, ref)
	if err != nil {
		return nil
	}
	return rec
}

// edgeActor is the engine actor of the user id an event names: a member of the device's org whose outlets include
// the device's outlet, narrowed to it, under the device (contract 0058 §4). ok is false for anyone else.
func (s *Server) edgeActor(ctx context.Context, d *auth.Device, raw json.RawMessage) (engine.Actor, bool, error) {
	var v any
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if len(raw) == 0 || dec.Decode(&v) != nil {
		return engine.Actor{}, false, nil
	}
	id, err := strconv.ParseInt(strings.TrimSpace(str(v)), 10, 64)
	if err != nil || id <= 0 {
		return engine.Actor{}, false, nil
	}
	u, err := auth.LoadUser(ctx, s.DB, id, d.Org)
	if errors.Is(err, auth.ErrBadLogin) || errors.Is(err, auth.ErrNoMembership) {
		return engine.Actor{}, false, nil
	}
	if err != nil {
		return engine.Actor{}, false, err
	}
	// only someone who can unlock this device with a PIN can have pressed the button on it (contract 0058 §2): never
	// the owner, never a desk role
	if u.Org != d.Org || !slices.Contains(u.Outlets, d.Outlet) || len(u.Roles) == 0 || !auth.PinAllowedFor(u.Roles, s.pinRole) {
		return engine.Actor{}, false, nil
	}
	u.Outlets = []string{d.Outlet}
	return Actor(&auth.Principal{User: u, Via: "device", Device: d}), true, nil
}

// edgeDraft is the draft a submit event names: the record an earlier accepted create event of this device wrote
// (draft = its key), or an existing record of the model (draft = its name).
func (s *Server) edgeDraft(ctx context.Context, d *auth.Device, a engine.Actor, sw screenWrite) (string, bool, error) {
	x, err := store.EdgeEventOf(ctx, s.DB, d.Org, sw.Draft)
	if err != nil {
		return "", false, err
	}
	if x != nil {
		return x.Ref, x.Device == d.ID && x.Status == "accepted" && x.Ref != "", nil
	}
	if _, err := s.Engine.Get(ctx, a, sw.Model, sw.Draft); err != nil {
		if _, _, refusal := edgeRefusal(err); refusal {
			return "", false, nil
		}
		return "", false, err
	}
	return sw.Draft, true, nil
}

// edgeRefusal maps an engine error to a rejection with its code and message. refusal is false for anything else:
// a transient failure (edgeTransient) or a fault the kernel cannot name (internal_error).
func edgeRefusal(err error) (code, msg string, refusal bool) {
	var ve *engine.ValidationError
	var se *engine.StatusError
	var pe *engine.PermissionError
	switch {
	case edgeTransient(err):
		return "", "", false
	case errors.As(err, &pe):
		return "permission_denied", pe.Error(), true
	case errors.As(err, &ve):
		return "validation_failed", ve.Error(), true
	case errors.As(err, &se):
		if se.Status >= 500 {
			return "", "", false
		}
		return se.Code, se.Msg, true
	case errors.Is(err, store.ErrNotFound):
		return "not_found", err.Error(), true
	}
	return "", "", false
}

// edgeTransient reports a failure of the moment, not of the event (ruling R10): the request ends, the transaction
// lost a race or the database is unreachable (pg.Transient), or the engine says so (a write conflict after its
// retries, a busy key, a timeout, 429, 503). Only these answer the request 503 so the device sends it again.
func edgeTransient(err error) bool {
	var se *engine.StatusError
	if errors.As(err, &se) {
		switch se.Code {
		case "write_conflict", "idempotency_in_progress", "timeout":
			return true
		}
		return se.Status == http.StatusTooManyRequests || se.Status == http.StatusServiceUnavailable
	}
	return pg.Transient(err)
}

const msgEdgeInternal = "Terjadi kesalahan di server; kirim ulang atau hubungi kantor pusat."

// edgeInternal answers an event whose handling failed with an error that is neither a refusal nor transient:
// rejected / internal_error, stored so a replay answers the same and the device's queue moves on; the detail is
// logged here only. A failure to store it is returned (the request then answers 503).
func (s *Server) edgeInternal(ctx context.Context, d *auth.Device, ev edgeEventIn, cause error) (edgeResult, error) {
	if s.Log != nil {
		s.Log("edge events: %s: internal error: %v", ev.Key, cause)
	}
	res := edgeResult{Key: ev.Key, Status: "rejected", Code: "internal_error", Message: msgEdgeInternal}
	if ev.Key == "" || len(ev.Key) > edgeMaxKey {
		return res, nil
	}
	hash := edgeHash(ev.Kind, ev.Payload)
	x := store.EdgeEvent{Org: d.Org, Key: ev.Key, Device: d.ID, Seq: seqOf(ev), Kind: ev.Kind, Hash: hash,
		Status: "rejected", Code: "internal_error", Message: msgEdgeInternal}
	if at, err := time.Parse(time.RFC3339, ev.OccurredAt); err == nil {
		x.OccurredAt = at
	}
	if err := store.InsertEdgeEvent(ctx, s.DB, x); errors.Is(err, store.ErrEdgeKeyTaken) {
		done, _, err := s.edgeStored(ctx, d, ev, hash)
		return done, err
	} else if err != nil {
		return res, err
	}
	return res, nil
}

// str is v as text: a JSON string or number ("" for anything else).
func str(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return ""
}
