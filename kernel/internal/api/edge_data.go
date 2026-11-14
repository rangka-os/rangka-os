package api

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
	"github.com/rangka-os/rangka-os/kernel/internal/engine"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Config and reference data for paired devices (contract 0057 §7, kernel side of 0006 §4).
//
// GET /api/v1/edge/config answers {version, outlet, device, screens, data_sets, sync}; version is a hash of the rest
// and the ETag, so If-None-Match with it answers 304. GET /api/v1/edge/data/<set>?cursor= answers
// {items: [{name, deleted, row}], next_cursor, done} in pages of edgePage rows ordered by name; a missing cursor
// starts a full copy. Every pull is a full copy, so deleted is always false: the device swaps its copy of a set only
// when done (0006 §4), which drops rows deleted since.
//
// Which screens and sets. The outlet's staff roles are the roles of its members (non-owner, outlets naming it) that
// may use a PIN: only they can work on the device (0057 §2). The screens are the work screens those roles may open
// (0030 §3, Engine.Work). The data sets are the models the reference slots of their counter screens name
// (catalogue.model, catalogue.options.model and its group model per 0048 §1, catalogue.available.model per 0039,
// customer.table.model per 0048 §2), then tax (the org's tax settings, the CEL tax global) and staff. A model a staff
// role may not read unconditionally is left out. Slot filters are not applied: a set is the whole model (the device
// filters as the screen says), so two screens over one model share one set.
//
// What the rows carry. Rows are read through the engine as the system actor, then: a model with an outlet scope
// field (outlet, or a role's scope field, 0010 §5) keeps the device outlet's rows only; a field hidden_for (0027) any
// staff role is dropped, inside table rows too. Hiding for any role, not every role, is stricter than a member's own
// reads: the device is shared, so what one staff role may not see never reaches it. staff carries {id, name, role} of
// the members who can unlock the device (auth.PinStaff): no e-mail, no PIN material.

const (
	edgePage       = 500
	edgeSetTax     = "tax"
	edgeSetStaff   = "staff"
	edgeCursorPfx  = "c1."
	msgEdgeNoSet   = "Data ini tidak dikirim ke perangkat ini."
	msgEdgeBadPage = "Penanda halaman tidak dikenal; mulai salin ulang tanpa cursor."
)

// edgePlan is what a device gets: its outlet's staff roles, screens and data sets.
type edgePlan struct {
	roles   []string
	screens []string
	sets    []string
}

// edgeStaffRoles lists the roles of the outlet's members that may use a PIN, sorted.
func (s *Server) edgeStaffRoles(ctx context.Context, d *auth.Device) ([]string, error) {
	res, err := s.DB.Query(ctx, `select distinct role from rk_members where org_id = $1 and $2 = any(outlets) and role <> 'owner'`,
		d.Org, d.Outlet)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, r := range res.Rows {
		if role, _ := r[0].(string); role != "" && s.pinRole(role) {
			out = append(out, role)
		}
	}
	slices.Sort(out)
	return out, nil
}

func edgeDeviceRef(d *auth.Device) *engine.DeviceRef {
	return &engine.DeviceRef{ID: d.ID, Code: d.Code, Outlet: d.Outlet}
}

func (s *Server) edgePlan(ctx context.Context, d *auth.Device) (*edgePlan, error) {
	roles, err := s.edgeStaffRoles(ctx, d)
	if err != nil {
		return nil, err
	}
	p := &edgePlan{roles: roles, screens: []string{}, sets: []string{}}
	if len(roles) == 0 {
		p.sets = append(p.sets, edgeSetStaff)
		return p, nil
	}
	reg := s.Engine.Reg()
	staff := engine.Actor{Org: d.Org, ID: "device:" + d.ID, Roles: roles, Outlets: []string{d.Outlet}, Device: edgeDeviceRef(d)}
	add := func(model string) {
		if model == "" || slices.Contains(p.sets, model) || model == edgeSetTax || model == edgeSetStaff {
			return
		}
		m := reg.Model(model)
		if m == nil || m.IsChild || !s.edgeReadable(d, roles, model) {
			return
		}
		p.sets = append(p.sets, model)
	}
	for _, w := range s.Engine.Work(staff) {
		p.screens = append(p.screens, w.Screen)
		sc := reg.Screen(w.Screen)
		if sc == nil || sc.Type != "counter" {
			continue
		}
		b := sc.Block()
		cat := mapOf(b["catalogue"])
		add(str(cat["model"]))
		if opt := mapOf(cat["options"]); opt != nil {
			om := str(opt["model"])
			add(om)
			if m := reg.Model(om); m != nil {
				if f := m.Field(str(opt["link"])); f != nil && f.Type == "link" {
					add(f.To) // the option group model
				}
			}
		}
		add(str(mapOf(cat["available"])["model"]))
		add(str(mapOf(mapOf(b["customer"])["table"])["model"]))
	}
	if len(p.screens) > 0 {
		p.sets = append(p.sets, edgeSetTax)
	}
	p.sets = append(p.sets, edgeSetStaff)
	return p, nil
}

// edgeReadable: some staff role reads every record of model (no if: on its grant).
func (s *Server) edgeReadable(d *auth.Device, roles []string, model string) bool {
	for _, r := range roles {
		if s.Engine.Unconditional(engine.Actor{Org: d.Org, Roles: []string{r}}, model, "read") {
			return true
		}
	}
	return false
}

func mapOf(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

// GET /api/v1/edge/config
func (s *Server) edgeConfig(w http.ResponseWriter, r *http.Request, d *auth.Device) {
	if !edgeVersion(w, r) {
		return
	}
	ctx := r.Context()
	p, err := s.edgePlan(ctx, d)
	if err != nil {
		fail(w, err)
		return
	}
	outlet := map[string]any{"code": d.Outlet, "name": d.Outlet, "timezone": s.Engine.Location().String()}
	if s.Engine.Reg().Model("outlet") != nil {
		sys := engine.Actor{Org: d.Org, ID: "system", System: true}
		rec, err := s.Engine.Get(ctx, sys, "outlet", d.Outlet)
		if err == nil && rec != nil {
			if c := str(rec["code"]); c != "" {
				outlet["code"] = c
			}
			if t := s.Engine.Title(ctx, sys, "outlet", rec, 0); t != "" {
				outlet["name"] = t
			}
		}
	}
	body := map[string]any{
		"outlet":    outlet,
		"device":    map[string]any{"id": d.ID, "code": d.Code},
		"screens":   p.screens,
		"data_sets": p.sets,
		"sync":      edgeSync,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		fail(w, err)
		return
	}
	sum := sha256.Sum256(raw)
	version := "cfg_" + hex.EncodeToString(sum[:])[:20]
	w.Header().Set("ETag", `"`+version+`"`)
	w.Header().Set("Cache-Control", "no-cache")
	if etagMatch(r.Header.Get("If-None-Match"), version) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	body["version"] = version
	writeJSON(w, http.StatusOK, body)
}

// etagMatch reports whether an If-None-Match header names version ("v", W/"v", a list, or *).
func etagMatch(h, version string) bool {
	for _, t := range strings.Split(h, ",") {
		t = strings.TrimPrefix(strings.TrimSpace(t), "W/")
		if t == "*" || (t != "" && strings.Trim(t, `"`) == version) {
			return true
		}
	}
	return false
}

type edgeItem struct {
	Name    string `json:"name"`
	Deleted bool   `json:"deleted"`
	Row     any    `json:"row"`
}

// GET /api/v1/edge/data/{set}?cursor=
func (s *Server) edgeData(w http.ResponseWriter, r *http.Request, d *auth.Device) {
	if !edgeVersion(w, r) {
		return
	}
	ctx := r.Context()
	set := r.PathValue("set")
	after := ""
	if c := r.URL.Query().Get("cursor"); c != "" {
		b, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(c, edgeCursorPfx))
		if !strings.HasPrefix(c, edgeCursorPfx) || err != nil || len(b) == 0 {
			writeError(w, http.StatusBadRequest, "bad_cursor", msgEdgeBadPage)
			return
		}
		after = string(b)
	}
	p, err := s.edgePlan(ctx, d)
	if err != nil {
		fail(w, err)
		return
	}
	if !slices.Contains(p.sets, set) {
		writeError(w, http.StatusNotFound, "not_found", msgEdgeNoSet)
		return
	}
	var items []edgeItem
	switch set {
	case edgeSetStaff:
		staff, err := s.Auth.PinStaff(ctx, d, s.pinRole)
		if err != nil {
			fail(w, err)
			return
		}
		for _, st := range staff {
			items = append(items, edgeItem{Name: strconv.FormatInt(st.ID, 10), Row: st})
		}
	case edgeSetTax:
		items = []edgeItem{{Name: edgeSetTax, Row: s.Engine.Reg().TaxVar(d.Org)}}
	default:
		items, err = s.edgeRows(ctx, d, p.roles, set)
		if err != nil {
			fail(w, err)
			return
		}
	}
	slices.SortStableFunc(items, func(a, b edgeItem) int { return strings.Compare(a.Name, b.Name) })
	start := 0
	if after != "" {
		start, _ = slices.BinarySearchFunc(items, after, func(it edgeItem, n string) int {
			if it.Name <= n {
				return -1
			}
			return 1
		})
	}
	page := items[start:]
	done := len(page) <= edgePage
	if !done {
		page = page[:edgePage]
	}
	var next any
	if !done {
		next = edgeCursorPfx + base64.RawURLEncoding.EncodeToString([]byte(page[len(page)-1].Name))
	}
	if page == nil {
		page = []edgeItem{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": page, "next_cursor": next, "done": done})
}

// edgeRows reads every row of model for the device: system read, the outlet's rows only, fields hidden from any
// staff role dropped.
func (s *Server) edgeRows(ctx context.Context, d *auth.Device, roles []string, model string) ([]edgeItem, error) {
	sys := engine.Actor{Org: d.Org, ID: "system", System: true}
	recs, _, err := s.Engine.List(ctx, sys, model, engine.ListOpts{Limit: 1 << 30, Children: true})
	if err != nil {
		return nil, err
	}
	m := s.Engine.Reg().Model(model)
	scope := s.Engine.OutletScope(d.Outlet)
	out := make([]edgeItem, 0, len(recs))
	for _, rec := range recs {
		if !scope.Visible(m, rec) {
			continue
		}
		edgeRedact(s.Engine, d, roles, m, rec)
		out = append(out, edgeItem{Name: str(rec["name"]), Row: rec})
	}
	return out, nil
}

// edgeRedact drops from rec the fields hidden from any of roles (contract 0027).
func edgeRedact(e *engine.Engine, d *auth.Device, roles []string, m *meta.Model, rec store.Record) {
	for _, r := range roles {
		e.Redact(engine.Actor{Org: d.Org, Roles: []string{r}}, m, rec)
	}
}
