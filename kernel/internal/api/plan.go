package api

import (
	"net/http"
	"sort"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/plan"
)

// getPlan is GET /api/v1/plan (contract 0027 §5): the tenant's plan, every plan of features.rk, usage against the
// limits (ok, near from 80%, full, over), notices for a banner and what is read-only after a downgrade. Any
// signed-in member may read it: a member in read-only mode needs to see why. read_only lists only the outlets of
// the caller's organisations, and other people's emails only to an owner (audit F4).
func (s *Server) getPlan(w http.ResponseWriter, r *http.Request, c *caller) {
	reg := s.Engine.Reg()
	now := s.Engine.Now()
	snap, err := plan.Take(r.Context(), s.DB, reg, now)
	if err != nil {
		fail(w, err)
		return
	}
	ms, err := s.memberships(r.Context(), c)
	if err != nil {
		fail(w, err)
		return
	}
	mine, owner := map[string]bool{}, false
	for _, m := range ms {
		mine[m.ID] = true
		for _, role := range m.Roles {
			owner = owner || role == "owner"
		}
	}
	outlets, users := []string{}, []string{}
	for o := range snap.ExcessOutlets {
		if org, _, ok := strings.Cut(o, "/"); ok && mine[org] {
			outlets = append(outlets, o)
		}
	}
	me := strings.ToLower(c.p.User.Email)
	for p := range snap.ExcessPeople {
		if owner || p == me {
			users = append(users, p)
		}
	}
	rep := snap.Report(reg, plan.Period(reg, now))
	sort.Strings(outlets)
	sort.Strings(users)
	rep["read_only"] = map[string]any{"outlets": outlets, "users": users}
	writeJSON(w, http.StatusOK, rep)
}
