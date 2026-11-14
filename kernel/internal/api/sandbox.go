package api

import (
	"net/http"

	"github.com/rangka-os/rangka-os/kernel/internal/sandbox"
)

// Ruang coba (contract 0052 §1): an owner of the real org creates, resets and deletes the site's sandbox org.
// The assistant never reaches these routes (authed refuses its writes with ai_forbidden).

func sandboxOwner(c *caller) sandbox.Owner {
	u := c.p.User
	return sandbox.Owner{UserID: u.ID, Email: u.Email, Record: u.Record, Roles: u.Roles}
}

// readFixtures reads {"fixtures": {model: [rows]}}; an empty body gives nil fixtures.
func readFixtures(w http.ResponseWriter, r *http.Request) (sandbox.Fixtures, bool) {
	var body struct {
		Fixtures sandbox.Fixtures `json:"fixtures"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", "body must be {\"fixtures\": {model: [rows]}}: "+err.Error())
		return nil, false
	}
	return body.Fixtures, true
}

// getSandbox is GET /api/v1/sandbox: the real org's sandbox, if any.
func (s *Server) getSandbox(w http.ResponseWriter, r *http.Request, c *caller) {
	info, err := sandbox.Info(r.Context(), s.DB, c.actor.Org)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// createSandbox is POST /api/v1/sandbox {fixtures}.
func (s *Server) createSandbox(w http.ResponseWriter, r *http.Request, c *caller) {
	fx, ok := readFixtures(w, r)
	if !ok {
		return
	}
	out, err := sandbox.Create(r.Context(), s.Engine, c.actor.Org, sandboxOwner(c), fx)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

// resetSandbox is POST /api/v1/sandbox/reset {fixtures?}.
func (s *Server) resetSandbox(w http.ResponseWriter, r *http.Request, c *caller) {
	fx, ok := readFixtures(w, r)
	if !ok {
		return
	}
	out, err := sandbox.Reset(r.Context(), s.Engine, c.actor.Org, sandboxOwner(c), fx)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// deleteSandbox is DELETE /api/v1/sandbox.
func (s *Server) deleteSandbox(w http.ResponseWriter, r *http.Request, c *caller) {
	if err := sandbox.Delete(r.Context(), s.Engine, c.actor.Org, sandboxOwner(c)); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
