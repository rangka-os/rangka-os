package api

import (
	"context"
	"net/http"
	"reflect"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/changeset"
	"github.com/rangka-os/rangka-os/kernel/internal/engine"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
)

// Live preview of a change set (contract 0015 §4): the metadata of a sandbox copy with the ops applied, read
// against the live database without migrating it.

const maxPreviewEngines = 8

type previewEntry struct {
	id, updated string
	live        *meta.Registry // the live registry this was built against
	eng         *engine.Engine
	deny        error // permissions differ from the live installation: no preview
}

var errPreviewPermissions = &changeset.Error{Status: 409, Code: "preview_permissions",
	Msg: "Pratinjau tidak tersedia untuk perubahan hak akses; setujui dulu untuk melihat hasilnya"}

// permissionsChanged reports whether the preview registry grants differently from the live one: any role
// document added, removed or changed, or a model in both with another effective permissions value.
func permissionsChanged(live, prev *meta.Registry) bool {
	roles := func(r *meta.Registry) map[string]string {
		out := map[string]string{}
		for p, d := range r.Docs {
			if strings.Contains(p, "/roles/") {
				out[p] = d.Text
			}
		}
		return out
	}
	lr, pr := roles(live), roles(prev)
	if len(lr) != len(pr) {
		return true
	}
	for p, t := range lr {
		if o, ok := pr[p]; !ok || o != t {
			return true
		}
	}
	for _, m := range prev.InstalledModels() {
		if lm := live.Model(m.Name); lm != nil && !reflect.DeepEqual(lm.Raw["permissions"], m.Raw["permissions"]) {
			return true
		}
	}
	return false
}

// changesetsMeta is meta.changesets (contract 0015 §1).
func (s *Server) changesetsMeta(a engine.Actor) map[string]any {
	reg := s.Engine.Reg()
	isApprover := false
	ap := s.Changes.Approvers()
	for _, r := range a.Roles {
		if ap[r] {
			isApprover = true
		}
	}
	proposer := isApprover
	for _, want := range meta.StrList(reg.SiteConfig("ai.proposers")) {
		for _, r := range a.Roles {
			if r == want {
				proposer = true
			}
		}
	}
	owner := false
	for _, r := range a.Roles {
		if r == "owner" {
			owner = true
		}
	}
	auto := "never"
	if v, ok := reg.SiteConfig("ai.auto_apply").(string); ok && v != "" {
		auto = v
	}
	return map[string]any{"propose": a.Human && proposer, "approve": a.Human && isApprover, "approve_high": a.Human && owner, "auto_apply": auto}
}

// previewEngine returns the engine over the change set's registry, built once per id and updated_at.
func (s *Server) previewEngine(ctx context.Context, org, id string) (*engine.Engine, *changeset.ChangeSet, error) {
	cs, err := s.Changes.Get(ctx, org, id)
	if err != nil {
		return nil, nil, err
	}
	if err := changeset.CheckPreviewable(cs); err != nil {
		return nil, cs, err
	}
	liveReg := s.Engine.Reg()
	s.previewMu.Lock()
	for i, p := range s.previews {
		if p.id == cs.ID && p.updated == cs.UpdatedAt && p.live == liveReg {
			s.previews = append(append(s.previews[:i:i], s.previews[i+1:]...), p) // most recently used last
			s.previewMu.Unlock()
			return p.eng, cs, p.deny
		}
	}
	s.previewMu.Unlock()
	reg, fresh, err := s.Changes.SandboxRegistry(ctx, org, id)
	if err != nil {
		return nil, cs, err
	}
	e := engine.New(s.DB, reg)
	e.Now, e.Log = s.Engine.Now, s.Engine.Log
	e.Cache, e.TolerateMissingTables = nil, true
	var deny error
	if permissionsChanged(liveReg, reg) {
		deny, e = errPreviewPermissions, nil
	}
	for k, h := range s.Engine.Hooks {
		if e != nil {
			e.Hooks[k] = h
		}
	}
	s.previewMu.Lock()
	defer s.previewMu.Unlock()
	kept := s.previews[:0:0]
	for _, p := range s.previews {
		if p.id != fresh.ID {
			kept = append(kept, p)
		}
	}
	kept = append(kept, &previewEntry{id: fresh.ID, updated: fresh.UpdatedAt, live: liveReg, eng: e, deny: deny})
	if len(kept) > maxPreviewEngines {
		kept = kept[len(kept)-maxPreviewEngines:]
	}
	s.previews = kept
	return e, fresh, deny
}

type previewHandler func(w http.ResponseWriter, r *http.Request, c *caller, e *engine.Engine, cs *changeset.ChangeSet)

func (s *Server) previewGuard(h previewHandler) func(w http.ResponseWriter, r *http.Request, c *caller) {
	return func(w http.ResponseWriter, r *http.Request, c *caller) {
		id := r.PathValue("id")
		s.previewResolve(w, r, c, id, h)
	}
}

func (s *Server) previewResolve(w http.ResponseWriter, r *http.Request, c *caller, id string, h previewHandler) {
	w.Header().Set("X-Rangka-Preview", id)
	if c.actor.Org == "" || s.changesetsMeta(c.actor)["propose"] != true {
		writeError(w, 403, "permission_denied", "Pratinjau perubahan hanya untuk yang boleh mengusulkan perubahan")
		return
	}
	e, cs, err := s.previewEngine(r.Context(), c.actor.Org, id)
	if err != nil {
		csFail(w, err)
		return
	}
	h(w, r, c, e, cs)
}

func (s *Server) previewRoot(w http.ResponseWriter, r *http.Request, c *caller) {
	id := r.URL.Query().Get("changeset")
	if id == "" {
		writeError(w, 400, "bad_request", "changeset is required")
		return
	}
	s.previewResolve(w, r, c, id, func(w http.ResponseWriter, r *http.Request, c *caller, e *engine.Engine, cs *changeset.ChangeSet) {
		m, err := s.metaFor(r.Context(), e, c.actor)
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{
			"changeset": map[string]any{"id": cs.ID, "title": cs.Title, "status": cs.Status, "risk": cs.Risk, "layers": cs.Layers},
			"meta":      m})
	})
}

func (s *Server) previewMeta(w http.ResponseWriter, r *http.Request, c *caller, e *engine.Engine, cs *changeset.ChangeSet) {
	m, err := s.metaFor(r.Context(), e, c.actor)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, m)
}

// missingTable and missingColumn are the Postgres errors of a preview reading a model or field the live
// database has not been migrated to yet.
func missingTable(err error) bool  { return pg.IsCode(err, "42P01") }
func missingColumn(err error) bool { return pg.IsCode(err, "42703") }

func (s *Server) previewList(w http.ResponseWriter, r *http.Request, c *caller, e *engine.Engine, cs *changeset.ChangeSet) {
	model, opts := r.PathValue("model"), listOptsOf(r)
	rows, total, err := e.List(r.Context(), c.actor, model, opts)
	switch {
	case missingTable(err):
		rows, total, err = nil, 0, nil
	case missingColumn(err):
		rows, total, err = s.Engine.List(r.Context(), c.actor, model, opts)
	}
	if err != nil {
		fail(w, err)
		return
	}
	if rows == nil {
		writeJSON(w, 200, map[string]any{"data": []any{}, "total": total})
		return
	}
	writeJSON(w, 200, map[string]any{"data": rows, "total": total})
}

func (s *Server) previewGet(w http.ResponseWriter, r *http.Request, c *caller, e *engine.Engine, cs *changeset.ChangeSet) {
	model, name := r.PathValue("model"), r.PathValue("name")
	rec, err := e.Get(r.Context(), c.actor, model, name)
	switch {
	case missingTable(err):
		writeError(w, 404, "not_found", model+" "+name+" not found")
		return
	case missingColumn(err):
		rec, err = s.Engine.Get(r.Context(), c.actor, model, name)
	}
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, rec)
}

func (s *Server) previewDashboard(w http.ResponseWriter, r *http.Request, c *caller, e *engine.Engine, cs *changeset.ChangeSet) {
	name, period := r.PathValue("name"), intParam(r, "period", 0)
	d, cached, err := e.Dashboard(r.Context(), c.actor, name, period)
	if missingColumn(err) {
		d, cached, err = s.Engine.Dashboard(r.Context(), c.actor, name, period)
	}
	if err != nil {
		fail(w, err)
		return
	}
	if cached {
		w.Header().Set("X-Cache", "hit")
	} else {
		w.Header().Set("X-Cache", "miss")
	}
	writeJSON(w, 200, d)
}

func (s *Server) previewReadOnly(w http.ResponseWriter, r *http.Request, c *caller) {
	if id, _, _ := strings.Cut(r.PathValue("path"), "/"); id != "" {
		w.Header().Set("X-Rangka-Preview", id)
	}
	w.Header().Set("Allow", "GET")
	writeError(w, http.StatusMethodNotAllowed, "preview_read_only", "a preview is read-only: change the data in the app itself")
}
