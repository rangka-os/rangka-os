package api

import (
	"net/http"
	"strconv"

	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Head-office exceptions (contract 0058 §10): the list and resolve of what outlets' offline work left behind.

const (
	msgExceptionsForbidden = "Hanya pemilik atau kantor pusat yang bisa melihat pengecualian."
	msgResolveForbidden    = "Masuk sebagai pemilik atau kantor pusat untuk menyelesaikan pengecualian."
	msgExceptionNotFound   = "Pengecualian tidak ditemukan."
)

// mayReadExceptions: owner or head office, never a PIN session or a device (contract 0058 §10).
func mayReadExceptions(c *caller) bool {
	return c.p.Via != "device" && mayPairDevices(c)
}

// GET /api/v1/exceptions?outlet=&kind=&open=1 → {items}
func (s *Server) listExceptions(w http.ResponseWriter, r *http.Request, c *caller) {
	if !mayReadExceptions(c) {
		writeError(w, http.StatusForbidden, "forbidden", msgExceptionsForbidden)
		return
	}
	q := r.URL.Query()
	items, err := store.ListExceptions(r.Context(), s.DB, c.actor.Org, q.Get("outlet"), q.Get("kind"), q.Get("open") == "1")
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// POST /api/v1/exceptions/{id}/resolve → 200: a person signed in fully resolves it (an API token may read, not resolve).
func (s *Server) resolveException(w http.ResponseWriter, r *http.Request, c *caller) {
	if !c.p.Human() || !mayReadExceptions(c) {
		writeError(w, http.StatusForbidden, "forbidden", msgResolveForbidden)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", msgExceptionNotFound)
		return
	}
	ok, err := store.ResolveException(r.Context(), s.DB, c.actor.Org, id, c.actor.ID)
	if err != nil {
		fail(w, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", msgExceptionNotFound)
		return
	}
	store.Audit(r.Context(), s.DB, c.actor.Org, c.actor.ID, "exception.resolved", "exception/"+strconv.FormatInt(id, 10), "exception resolved", nil)
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "resolved": true})
}
