package api

import (
	"context"
	"net/http"

	"github.com/rangka-os/rangka-os/kernel/internal/engine"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// eventAllow is the SSE filter for one caller: a record event passes when the caller may read the record.
// The record is loaded only when the model-level grant is conditional (if:); a deleted record then fails to
// load and is dropped, while with an unconditional read it passes. Other events (change sets, jobs) pass.
// Further checks (org, outlet) belong here too.
func (s *Server) eventAllow(ctx context.Context, a engine.Actor) func(store.Event) bool {
	return func(ev store.Event) bool {
		if ev.Type != "record" {
			return true
		}
		e := s.Engine
		if e.Unconditional(a, ev.Model, "read") {
			return true
		}
		if !e.Can(ctx, a, ev.Model, "read", nil) {
			return false
		}
		_, err := e.Get(ctx, a, ev.Model, ev.Name) // Get checks read on the stored record
		return err == nil
	}
}

// recordHandler handles a route on one record after recordGuard let the caller through.
type recordHandler func(w http.ResponseWriter, r *http.Request, c *caller, rec store.Record)

// recordGuard protects routes on /api/v1/r/{model}/{name}/...: it loads the record through engine.Get (which
// checks read), then authorizes action on it, and only then runs next. A refusal is the 403 body.
func (s *Server) recordGuard(action string, next recordHandler) http.HandlerFunc {
	return s.authed(func(w http.ResponseWriter, r *http.Request, c *caller) {
		model := r.PathValue("model")
		rec, err := s.Engine.Get(r.Context(), c.actor, model, r.PathValue("name"))
		if err != nil {
			fail(w, err)
			return
		}
		if action != "read" {
			if err := s.Engine.Authorize(r.Context(), c.actor, model, action, rec); err != nil {
				fail(w, err)
				return
			}
		}
		next(w, r, c, rec)
	})
}

// notImplemented is the guarded routes' handler until their streams add one (print: S3, attachments: S6):
// the same 404 as the /api/v1/ catch-all.
func notImplemented(w http.ResponseWriter, r *http.Request, _ *caller, _ store.Record) {
	writeError(w, 404, "not_found", "no such endpoint "+r.URL.Path)
}
