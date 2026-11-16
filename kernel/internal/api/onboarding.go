package api

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/canvas"
	"github.com/rangka-os/rangka-os/kernel/internal/engine"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
)

// Mulai di sini (contract 0053 §2–3): the steps of the business's process with what is done, what is open and
// what is locked until another model has data, plus the org's hide flag.

// onboardingProcess picks the process Mulai di sini follows (0053 §2), or nil:
//  1. the site process named in config.onboarding.process;
//  2. the only site process, when the site has exactly one;
//  3. the first declared process of an installed app, by name;
//  4. the inferred process of the first installed app in config.apps order.
func onboardingProcess(reg *meta.Registry) *meta.Process {
	all := canvas.Processes(reg) // declared processes of installed units and the site, plus inferred ones, by name
	if name, _ := reg.SiteConfig("onboarding.process").(string); name != "" {
		for _, p := range all {
			if p.Name == name && p.App == "" && p.File != "" {
				return p
			}
		}
	}
	var site []*meta.Process
	for _, p := range all {
		if p.App == "" && p.File != "" {
			site = append(site, p)
		}
	}
	if len(site) == 1 {
		return site[0]
	}
	for _, p := range all {
		if p.App != "" && p.File != "" {
			return p
		}
	}
	for _, app := range reg.AppOrder() {
		for _, p := range all {
			if p.File == "" && p.App == app {
				return p
			}
		}
	}
	return nil
}

// dataCheck answers "has the org any record of model" once per model per request (one EXISTS each).
type dataCheck struct {
	ctx  context.Context
	e    *engine.Engine
	org  string
	seen map[string]bool
}

func (d *dataCheck) has(model string) (bool, error) {
	if ok, done := d.seen[model]; done {
		return ok, nil
	}
	ok, err := d.e.HasRecords(d.ctx, d.org, model)
	if err != nil {
		return false, err
	}
	d.seen[model] = ok
	return ok, nil
}

// needs is LinkNeeds of m without the models the org already has records of; never nil.
func (d *dataCheck) needs(reg *meta.Registry, m *meta.Model) ([]string, error) {
	out := []string{}
	for _, n := range engine.LinkNeeds(reg, m) {
		ok, err := d.has(n)
		if err != nil {
			return nil, err
		}
		if !ok {
			out = append(out, n)
		}
	}
	return out, nil
}

func labelsOrEmpty(ls []meta.Label) []meta.Label {
	if ls == nil {
		return []meta.Label{}
	}
	return ls
}

// prerequisitesFirst orders the stages so that a stage whose model needs another stage's model (a required link,
// engine.LinkNeeds) comes after it (0053 §2 amended, t_4e93e013): an inferred app process follows the app's menu,
// where sewa_alat lists Pesanan sewa first and Pelanggan last, and every step before Pelanggan is locked. A stable
// topological order: the earliest stage whose prerequisites among the steps are placed goes next, so a process that
// already respects them keeps its order; on a cycle the rest follow in process order.
func prerequisitesFirst(reg *meta.Registry, stages []meta.Stage) []meta.Stage {
	at := map[string]bool{}
	for _, st := range stages {
		at[st.Model] = true
	}
	deps := make([][]string, len(stages))
	for i, st := range stages {
		for _, n := range engine.LinkNeeds(reg, reg.Model(st.Model)) {
			if at[n] && n != st.Model {
				deps[i] = append(deps[i], n)
			}
		}
	}
	out := make([]meta.Stage, 0, len(stages))
	placed, used := map[string]bool{}, make([]bool, len(stages))
	for len(out) < len(stages) {
		next := -1
		for i := range stages {
			if used[i] {
				continue
			}
			ready := true
			for _, d := range deps[i] {
				if !placed[d] {
					ready = false
					break
				}
			}
			if ready {
				next = i
				break
			}
		}
		if next < 0 { // a cycle: the rest in process order
			for i := range stages {
				if !used[i] {
					used[i] = true
					out = append(out, stages[i])
				}
			}
			break
		}
		used[next], placed[stages[next].Model] = true, true
		out = append(out, stages[next])
	}
	return out
}

// Onboarding is the answer of GET /api/v1/onboarding for the caller (also read by the MCP tool rangka_onboarding).
func (s *Server) Onboarding(ctx context.Context, a engine.Actor) (map[string]any, error) {
	reg := s.Engine.Reg()
	canRead := s.canReader(&caller{actor: a})
	hidden, err := s.Engine.OnboardingHidden(ctx, a.Org)
	if err != nil {
		return nil, err
	}
	d := &dataCheck{ctx: ctx, e: s.Engine, org: a.Org, seen: map[string]bool{}}
	steps := []map[string]any{}
	var current any
	procName := any(nil)
	if p := onboardingProcess(reg); p != nil {
		var stages []meta.Stage
		for _, st := range p.Stages {
			m := reg.Model(st.Model)
			if m == nil || m.IsChild || !canRead(st.Model) {
				continue
			}
			stages = append(stages, st)
		}
		for _, st := range prerequisitesFirst(reg, stages) {
			m := reg.Model(st.Model)
			procName = p.Name
			step := map[string]any{"stage": st.Name, "label": st.Label, "model": st.Model, "answers": labelsOrEmpty(st.Answers)}
			if st.Label == nil {
				step["label"] = m.Label
			}
			if st.Hint != nil {
				step["hint"] = st.Hint
			}
			done, at, err := s.Engine.StageDone(ctx, a.Org, m, st.When)
			if err != nil {
				return nil, err
			}
			needs, err := d.needs(reg, m)
			if err != nil {
				return nil, err
			}
			step["needs"] = needs
			switch {
			case done:
				step["state"] = "done"
				if !at.IsZero() {
					step["done_at"] = at.UTC().Format(time.RFC3339)
				}
			case len(needs) > 0:
				step["state"] = "locked"
			default:
				step["state"] = "open"
				if current == nil {
					current = st.Name
				}
			}
			steps = append(steps, step)
		}
	}
	locks := map[string]any{}
	names := make([]string, 0, len(reg.Models))
	for n := range reg.Models {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		m := reg.Model(n)
		if m == nil || m.IsChild || !canRead(n) {
			continue
		}
		needs, err := d.needs(reg, m)
		if err != nil {
			return nil, err
		}
		if len(needs) > 0 {
			locks[n] = map[string]any{"reason": "needs_data", "needs": needs}
		}
	}
	return map[string]any{"process": procName, "hidden": hidden, "steps": steps, "current": current, "locks": locks}, nil
}

func (s *Server) getOnboarding(w http.ResponseWriter, r *http.Request, c *caller) {
	out, err := s.Onboarding(r.Context(), c.actor)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// postOnboardingHide is POST /api/v1/onboarding/hide {hidden}: the owner only (0053 §3).
func (s *Server) postOnboardingHide(w http.ResponseWriter, r *http.Request, c *caller) {
	if !isOwner(c) {
		permissionDenied(w, engine.Denied("onboarding", "hide"))
		return
	}
	var body struct {
		Hidden *bool `json:"hidden"`
	}
	if err := readJSON(r, &body); err != nil || body.Hidden == nil {
		writeError(w, http.StatusBadRequest, "bad_json", `body must be {"hidden": true|false}`)
		return
	}
	if err := s.Engine.SetOnboardingHidden(r.Context(), c.actor.Org, *body.Hidden, c.actor.ID); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"hidden": *body.Hidden})
}

// tourName is a tour of the web app: "kenalan", "langkah:s_barang", "halaman:r/item" (contract 0053 §7).
var tourName = regexp.MustCompile(`^[a-z0-9][a-z0-9_.:/-]{0,63}$`)

// getTours is GET /api/v1/onboarding/tours: the caller's own tour states, {tours: {name: {state, at}}}.
func (s *Server) getTours(w http.ResponseWriter, r *http.Request, c *caller) {
	out, err := s.Engine.Tours(r.Context(), c.actor.Org, c.actor.ID)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tours": out})
}

// putTour is PUT /api/v1/onboarding/tours/{tour} {state: "seen" | "skipped" | ""}: the caller's own state of one
// tour; "" forgets it so the tour can start again. Raka never calls it (the assistant writes nothing here).
func (s *Server) putTour(w http.ResponseWriter, r *http.Request, c *caller) {
	tour := r.PathValue("tour")
	if !tourName.MatchString(tour) {
		writeError(w, http.StatusBadRequest, "bad_tour", "tour must match "+tourName.String())
		return
	}
	var body struct {
		State *string `json:"state"`
	}
	if err := readJSON(r, &body); err != nil || body.State == nil || (*body.State != "seen" && *body.State != "skipped" && *body.State != "") {
		writeError(w, http.StatusBadRequest, "bad_json", `body must be {"state": "seen" | "skipped" | ""}`)
		return
	}
	ok, err := s.Engine.SetTour(r.Context(), c.actor.Org, c.actor.ID, tour, *body.State)
	if err != nil {
		fail(w, err)
		return
	}
	if !ok {
		writeError(w, http.StatusConflict, "too_many_tours", fmt.Sprintf("at most %d tours per person", engine.MaxTours))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tour": tour, "state": *body.State})
}
