package api

import (
	"context"
	"crypto/subtle"
	"net/http"
)

// Internal is what the hosting side may call under /_rangka/ (contract 0040 §1): only with the header
// X-Rangka-Internal set to Token. The hosting adapter strips that header from public requests and the router strips
// /_rangka/ paths, so the public cannot reach these endpoints.
type Internal struct {
	Token string
	// Tick runs the scheduler and the queue once and returns its report.
	Tick func(ctx context.Context) (any, error)
}

// internalHeader carries the internal token.
const internalHeader = "X-Rangka-Internal"

func (s *Server) internalOnly(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get(internalHeader)
		if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(s.Internal.Token)) != 1 {
			// The same answer as a missing route, so a caller learns nothing about the endpoint.
			writeError(w, http.StatusNotFound, "not_found", "no such endpoint "+r.URL.Path)
			return
		}
		h(w, r)
	}
}

// POST /_rangka/tick
func (s *Server) tick(w http.ResponseWriter, r *http.Request) {
	if s.Internal.Tick == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "tick is not available")
		return
	}
	rep, err := s.Internal.Tick(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "tick_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rep)
}
