package api

import (
	"net/http"

	"github.com/rangka-os/rangka-os/kernel/internal/pg"
)

// hook serves a connector webhook. /hooks/<org>/<connector>/<trigger> names the org (several orgs on one site,
// contract 0017 §7); /hooks/<connector>/<trigger> is the site's default org.
func (s *Server) hook(w http.ResponseWriter, r *http.Request) {
	if s.Connectors == nil {
		writeError(w, 404, "not_found", "connector webhooks are not enabled")
		return
	}
	org := r.PathValue("org")
	if org == "" {
		org = s.Org
	}
	// the ruang coba takes no webhooks: nothing from outside is routed to it (contract 0052 §2)
	res, err := s.DB.Query(r.Context(), "select 1 from rk_orgs where id = $1 and not sandbox", org)
	if err != nil {
		fail(w, err)
		return
	}
	if len(res.Rows) == 0 {
		writeError(w, 404, "not_found", "no organisation "+org)
		return
	}
	ctx := pg.WithOrg(r.Context(), org)
	out := s.Connectors.HandleHook(ctx, org, r.PathValue("connector"), r.PathValue("trigger"), r)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(out.Status)
	w.Write([]byte(out.Body))
}
