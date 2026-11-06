package api

import (
	"net/http"

	"github.com/rangka-os/rangka-os/kernel/internal/plan"
)

// getPlan is GET /api/v1/plan (contract 0027 §5): the tenant's plan, every plan of features.rk, usage against the
// limits (ok, near from 80%, full, over), notices for a banner and what is read-only after a downgrade. Any
// signed-in member may read it: a member in read-only mode needs to see why.
func (s *Server) getPlan(w http.ResponseWriter, r *http.Request, c *caller) {
	reg := s.Engine.Reg()
	now := s.Engine.Now()
	snap, err := plan.Take(r.Context(), s.DB, reg, now)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snap.Report(reg, plan.Period(reg, now)))
}
