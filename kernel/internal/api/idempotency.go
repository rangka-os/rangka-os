package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/engine"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Idempotent writes (contract 0030 §6, §7).
//
// A create, submit, cancel, amend or action request may carry `Idempotency-Key: <client id>` (1..128 printable
// ASCII). The first request with a key claims it (rk_idempotency, per org and actor, kept 7 days) and runs; the engine
// marks the key committed in the writing transaction, and a 2xx answer is stored. A later request with the same key:
//   - same method, path and body, answer stored: the stored answer, header Idempotency-Replayed: true
//   - same request, committed but no stored answer after 2 minutes (the server stopped in between): the record it
//     wrote, 200, replayed (before that: still running)
//   - same request still running: 409 idempotency_in_progress (Retry-After: 2)
//   - a different method, path or body: 409 idempotency_conflict
// A request that fails (4xx, 5xx) before writing releases its key, so a retry runs again.
//
// The body may also carry `_occurred_at` (RFC 3339): when the user pressed the button for a write that waited in a
// queue. It is removed from the body, kept in the audit of the records written, and must not lie more than 5 minutes
// ahead or 30 days back (422 occurred_at).

const (
	maxIdemKey      = 128
	occurredAhead   = 5 * time.Minute
	occurredBack    = 30 * 24 * time.Hour
	idemPruneEvery  = time.Hour
	idemRetrySecond = "2"
)

var (
	idemPruneMu sync.Mutex
	idemPruned  time.Time
)

func validIdemKey(k string) bool {
	if k == "" || len(k) > maxIdemKey {
		return false
	}
	for _, c := range k {
		if c < 0x21 || c > 0x7e {
			return false
		}
	}
	return true
}

// captureWriter keeps the answer of the wrapped handler so it can be stored before it is sent.
type captureWriter struct {
	h      http.Header
	status int
	buf    bytes.Buffer
}

func (c *captureWriter) Header() http.Header { return c.h }
func (c *captureWriter) WriteHeader(s int) {
	if c.status == 0 {
		c.status = s
	}
}
func (c *captureWriter) Write(b []byte) (int, error) {
	if c.status == 0 {
		c.status = 200
	}
	return c.buf.Write(b)
}

func (c *captureWriter) flush(w http.ResponseWriter) {
	for k, v := range c.h {
		w.Header()[k] = v
	}
	if c.status == 0 {
		c.status = 200
	}
	w.WriteHeader(c.status)
	w.Write(c.buf.Bytes())
}

// occurredAt takes `_occurred_at` out of a JSON object body. ok is false when the value is not a valid time in range
// (the 422 is written).
func (s *Server) occurredAt(w http.ResponseWriter, body []byte) ([]byte, time.Time, bool) {
	if !bytes.Contains(body, []byte(`"_occurred_at"`)) {
		return body, time.Time{}, true
	}
	var m map[string]any
	if json.Unmarshal(body, &m) != nil {
		return body, time.Time{}, true // readObject answers bad_json
	}
	raw, has := m["_occurred_at"]
	if !has {
		return body, time.Time{}, true
	}
	delete(m, "_occurred_at")
	str, _ := raw.(string)
	t, err := time.Parse(time.RFC3339, str)
	now := s.Engine.Now()
	if err != nil || t.After(now.Add(occurredAhead)) || t.Before(now.Add(-occurredBack)) {
		writeError(w, http.StatusUnprocessableEntity, "occurred_at", "_occurred_at must be an RFC 3339 time, at most 5 minutes ahead and 30 days back")
		return nil, time.Time{}, false
	}
	out, _ := json.Marshal(m)
	return out, t, true
}

// idempotent wraps a write handler with Idempotency-Key and _occurred_at handling.
func (s *Server) idempotent(h func(http.ResponseWriter, *http.Request, *caller)) func(http.ResponseWriter, *http.Request, *caller) {
	return func(w http.ResponseWriter, r *http.Request, c *caller) {
		key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
		body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_body", err.Error())
			return
		}
		raw := body
		body, at, ok := s.occurredAt(w, body)
		if !ok {
			return
		}
		ctx := r.Context()
		if !at.IsZero() {
			ctx = engine.WithOccurredAt(ctx, at)
		}
		r = r.WithContext(ctx)
		r.Body = io.NopCloser(bytes.NewReader(body))
		if key == "" {
			h(w, r, c)
			return
		}
		if !validIdemKey(key) {
			writeError(w, http.StatusBadRequest, "idempotency_key", "Idempotency-Key must be 1 to 128 printable ASCII characters")
			return
		}
		s.pruneIdempotency()
		sum := sha256.Sum256([]byte(r.Method + " " + r.URL.Path + "\n" + string(raw)))
		hash := hex.EncodeToString(sum[:])
		org, actor := c.actor.Org, c.actor.ID
		it, err := store.ClaimIdempotency(ctx, s.DB, org, actor, key, hash)
		if err != nil && !errors.Is(err, store.ErrIdemTaken) {
			fail(w, err)
			return
		}
		if err != nil { // taken: replay, refuse, or wait
			s.idemReplay(w, r, c, it, hash)
			return
		}
		cw := &captureWriter{h: http.Header{}}
		k := engine.IdemKey{Org: org, Actor: actor, Key: key}
		h(cw, r.WithContext(engine.WithIdempotency(ctx, k)), c)
		bg := context.WithoutCancel(ctx)
		if cw.status >= 200 && cw.status < 300 {
			if err := store.FinishIdempotency(bg, s.DB, org, actor, key, cw.status, json.RawMessage(bytes.TrimSpace(cw.buf.Bytes()))); err != nil && s.Log != nil {
				s.Log("idempotency: store answer for %s: %v", key, err)
			}
		} else if err := store.ReleaseIdempotency(bg, s.DB, org, actor, key); err != nil && s.Log != nil {
			s.Log("idempotency: release %s: %v", key, err)
		}
		cw.flush(w)
	}
}

func (s *Server) idemReplay(w http.ResponseWriter, r *http.Request, c *caller, it *store.Idem, hash string) {
	if it.Hash != hash {
		writeError(w, http.StatusConflict, "idempotency_conflict", "this Idempotency-Key was used for a different request")
		return
	}
	switch it.State {
	case "done":
		w.Header().Set("Idempotency-Replayed", "true")
		if it.Status == http.StatusCreated && it.Target != "" {
			w.Header().Set("Location", "/api/v1/r/"+it.Target)
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(it.Status)
		w.Write(append(it.Response, '\n'))
	case "committed":
		if time.Since(it.Created) < store.IdempotencyStale { // written, the answer is on its way
			w.Header().Set("Retry-After", idemRetrySecond)
			writeError(w, http.StatusConflict, "idempotency_in_progress", "the first request with this key is still running")
			return
		}
		model, name, ok := strings.Cut(it.Target, "/")
		if !ok {
			writeError(w, http.StatusConflict, "idempotency_unknown", "the first request with this key wrote no record; check before you send it again")
			return
		}
		rec, err := s.Engine.Get(r.Context(), c.actor, model, name)
		if err != nil {
			fail(w, err)
			return
		}
		w.Header().Set("Idempotency-Replayed", "true")
		writeJSON(w, http.StatusOK, rec)
	default:
		w.Header().Set("Retry-After", idemRetrySecond)
		writeError(w, http.StatusConflict, "idempotency_in_progress", "the first request with this key is still running")
	}
}

// pruneIdempotency deletes expired keys at most once an hour per process.
func (s *Server) pruneIdempotency() {
	idemPruneMu.Lock()
	due := time.Since(idemPruned) > idemPruneEvery
	if due {
		idemPruned = time.Now()
	}
	idemPruneMu.Unlock()
	if due {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := store.PruneIdempotency(ctx, s.DB); err != nil && s.Log != nil {
				s.Log("idempotency: prune: %v", err)
			}
		}()
	}
}
