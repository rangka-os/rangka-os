package api

import (
	"net/http"
	"sort"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/plan"
)

// getPlan is GET /api/v1/plan (contract 0028 §5): the tenant's plan, every plan of features.rk, usage against the
// limits (ok, near from 80%, full, over), notices for a banner and what is read-only after a downgrade. Any
// signed-in member may read it: a member in read-only mode needs to see why. read_only lists only the outlets of
// the caller's organisations, and other people's emails only to an owner of an organisation they belong to (audit F4, N2).
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
	// an owner sees the read-only people of the organisations they own (audit N2); anyone else only themselves
	shared := map[string]bool{}
	if owner {
		var owned []string
		for _, m := range ms {
			for _, role := range m.Roles {
				if role == "owner" {
					owned = append(owned, m.ID)
				}
			}
		}
		res, err := s.DB.Query(r.Context(), "select distinct u.email from rk_members m join rk_users u on u.id = m.user_id where m.org_id = any($1)", pg.Array(owned))
		if err != nil {
			fail(w, err)
			return
		}
		for _, row := range res.Rows {
			shared[strings.ToLower(row[0].(string))] = true
		}
	}
	me := strings.ToLower(c.p.User.Email)
	for p := range snap.ExcessPeople {
		if shared[p] || p == me {
			users = append(users, p)
		}
	}
	rep := snap.Report(reg, plan.Period(reg, now))
	sort.Strings(outlets)
	sort.Strings(users)
	rep["read_only"] = map[string]any{"outlets": outlets, "users": users, "apps": planKeys(snap.ExcessApps)}
	// traffic shaping (contract 0040 §9): this process's counters for today, approximate under several instances
	usage := map[string]any{}
	if ms, ok := rep["usage"].(map[string]plan.Meter); ok {
		for k, v := range ms {
			usage[k] = v
		}
	}
	throttled, waitedMS, refused := s.limiter.Today()
	usage["rate"] = map[string]any{"throttled_today": throttled, "throttled_ms_today": waitedMS, "refused_today": refused, "approximate": true}
	rep["usage"] = usage
	writeJSON(w, http.StatusOK, rep)
}
