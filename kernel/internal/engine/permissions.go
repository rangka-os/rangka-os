package engine

import (
	"context"
	"sort"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/cel"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Actions are the permission actions of a model's permissions: list (schema 0.4 $defs.permission).
var Actions = []string{"read", "create", "write", "delete", "submit", "cancel", "amend", "export", "import", "print", "customize"}

var knownAction = func() map[string]bool {
	out := map[string]bool{}
	for _, a := range Actions {
		out[a] = true
	}
	return out
}()

// PermissionError is returned when the actor may not perform action on model: model permissions (contract 0005
// §4) and row scope (contract 0010 §6) alike. The API answers it with the one 403 permission_denied body.
type PermissionError struct {
	Model, Action string
	Label         string // the model's label for the message ("Faktur penjualan"); "" falls back to nouns or Model
}

// actionWords are the verbs of the 403 message per action; unknown actions show as given.
var actionWords = map[string]string{
	"read": "melihat", "query": "melihat", "create": "membuat", "write": "mengubah", "update": "mengubah",
	"submit": "mengajukan", "cancel": "membatalkan", "amend": "merevisi", "delete": "menghapus", "print": "mencetak",
	"export": "mengekspor", "import": "mengimpor", "approve": "menyetujui", "reject": "menolak",
	"customize": "menyesuaikan", "run": "menjalankan", "switch": "masuk ke",
}

// nouns name the non-model targets of a 403 (organisations, flows, jobs).
var nouns = map[string]string{"org": "organisasi ini", "flow": "alur ini", "job": "tugas ini"}

// Error is the human message of the 403 body, in Bahasa Indonesia since the web app shows it ("Kamu tidak punya
// izin untuk melihat faktur penjualan"); code, model and action stay machine-readable (contract 0010 §6).
func (p *PermissionError) Error() string {
	verb := actionWords[p.Action]
	if strings.HasPrefix(p.Action, "action:") {
		verb = "menjalankan aksi " + strings.TrimPrefix(p.Action, "action:") + " pada"
	}
	if verb == "" {
		verb = p.Action
	}
	what := lowerFirst(p.Label)
	if what == "" {
		what = nouns[p.Model]
	}
	if what == "" {
		what = p.Model
	}
	return "Kamu tidak punya izin untuk " + verb + " " + what
}

// lowerFirst lower-cases the first letter for use mid-sentence ("Faktur penjualan" → "faktur penjualan").
func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	return strings.ToLower(string(r[0])) + string(r[1:])
}

// Denied builds the error for an action the caller may not perform (row scope, contract 0010 §6, uses it too).
// Engine.Deny also fills the model's label.
func Denied(model, action string) *PermissionError {
	return &PermissionError{Model: model, Action: action}
}

// Deny is Denied with the model's label from the registry, for the message.
func (e *Engine) Deny(model, action string) *PermissionError {
	pe := Denied(model, action)
	if m := e.Reg().Model(model); m != nil {
		pe.Label = m.Label.Text()
	}
	return pe
}

// grant is one entry of a model's permissions: list.
type grant struct {
	role    string
	cond    string // if: (CEL over the record), "" = unconditional
	actions map[string]bool
}

// modelPerms are the parsed permissions of one model. annotated is false for a model without permissions:,
// which every actor may use (behaviour before permissions were enforced).
type modelPerms struct {
	annotated bool
	grants    []grant
}

// perms parses and caches m.Raw["permissions"] (overlays already merged) per model pointer; a registry
// reload makes new model pointers, and SetRegistry drops the cache.
func (e *Engine) perms(m *meta.Model) *modelPerms {
	if v, ok := e.permCache.Load(m); ok {
		return v.(*modelPerms)
	}
	raw, present := m.Raw["permissions"]
	mp := &modelPerms{annotated: present && raw != nil}
	for _, gv := range meta.AsList(raw) {
		g := meta.AsMap(gv)
		if g == nil {
			continue
		}
		gr := grant{role: meta.Str(g, "role"), cond: strings.TrimSpace(meta.Str(g, "if")), actions: map[string]bool{}}
		for _, a := range Actions {
			if b, ok := g[a].(bool); ok && b {
				gr.actions[a] = true
			}
		}
		mp.grants = append(mp.grants, gr)
	}
	e.permCache.Store(m, mp)
	return mp
}

// permModel resolves a model name to the model whose permissions apply: a child model uses its parent's
// (the first model with a table field of it).
func (e *Engine) permModel(name string) *meta.Model {
	reg := e.Reg()
	m := reg.Model(name)
	if m == nil || !m.IsChild {
		return m
	}
	if p, ok := e.parentCache.Load(m); ok {
		return p.(*meta.Model)
	}
	parent := m
	names := make([]string, 0, len(reg.Models))
	for n := range reg.Models {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		pm := reg.Models[n]
		for _, f := range pm.Fields {
			if f.Type == "table" && f.Of == m.Name && parent == m {
				parent = pm
			}
		}
	}
	e.parentCache.Store(m, parent)
	return parent
}

func hasRole(a Actor, role string) bool {
	for _, r := range a.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// unrestricted reports actors every check lets through: kernel-internal actors and owners.
func unrestricted(a Actor) bool { return a.System || hasRole(a, "owner") }

// decision is the model-level answer for one actor and action: all (some grant applies without if:), or the
// if: conditions of the conditional grants that apply. Neither means denied.
type decision struct {
	all   bool
	conds []string
}

func (d decision) maybe() bool { return d.all || len(d.conds) > 0 }

func (e *Engine) decide(a Actor, m *meta.Model, action string) decision {
	if !knownAction[action] {
		return decision{}
	}
	if unrestricted(a) {
		return decision{all: true}
	}
	mp := e.perms(m)
	if !mp.annotated {
		return decision{all: true}
	}
	var d decision
	for _, g := range mp.grants {
		if !g.actions[action] || !hasRole(a, g.role) {
			continue
		}
		if g.cond == "" {
			return decision{all: true}
		}
		d.conds = append(d.conds, g.cond)
	}
	return d
}

// recordAllowed evaluates a decision on a record: some condition must be true (errors count as false).
func (e *Engine) recordAllowed(ctx context.Context, q pg.Querier, a Actor, m *meta.Model, d decision, rec store.Record, links map[string]map[string]any) bool {
	if d.all {
		return true
	}
	if len(d.conds) == 0 {
		return false
	}
	s := e.newScope(ctx, q, a, m, rec, links)
	for _, c := range d.conds {
		if v, err := s.eval(c); err == nil && cel.Truthy(v) {
			return true
		}
	}
	return false
}

func (e *Engine) authorize(ctx context.Context, q pg.Querier, links map[string]map[string]any, a Actor, model, action string, rec store.Record) error {
	m := e.permModel(model)
	if m == nil {
		if knownAction[action] && unrestricted(a) {
			return nil
		}
		return e.Deny(model, action)
	}
	d := e.decide(a, m, action)
	if rec == nil {
		if d.maybe() {
			return nil
		}
	} else if e.recordAllowed(ctx, q, a, m, d, rec, links) {
		return nil
	}
	return e.Deny(m.Name, action)
}

// Authorize decides whether a may perform action on model: nil, or a *PermissionError. With a record the
// grants' if: conditions are evaluated on it (the stored record, or the new record after defaults for create);
// without one (rec == nil) it is a model-level check where a conditional grant counts as allowed. A child
// model uses its parent's permissions; pass the parent record.
func (e *Engine) Authorize(ctx context.Context, a Actor, model, action string, rec store.Record) error {
	return e.authorize(ctx, e.DB, nil, a, model, action, rec)
}

// authorize checks inside an operation (link lookups in if: run in its transaction).
func (o *op) authorize(m *meta.Model, action string, rec store.Record) error {
	return o.e.authorize(o.ctx, o.tx, o.links, o.actor, m.Name, action, rec)
}

// Can is Authorize as a bool.
func (e *Engine) Can(ctx context.Context, a Actor, model, action string, rec store.Record) bool {
	return e.Authorize(ctx, a, model, action, rec) == nil
}

// Unconditional reports whether a may perform action on every record of model: no if: has to be evaluated.
// SSE uses it to pass events (deletes included) without loading the record.
func (e *Engine) Unconditional(a Actor, model, action string) bool {
	m := e.permModel(model)
	if m == nil {
		return knownAction[action] && unrestricted(a)
	}
	return e.decide(a, m, action).all
}

// Allowed gives the model-level answer for every action (conditional grants count as allowed), for /meta.
func (e *Engine) Allowed(a Actor, model string) map[string]bool {
	out := make(map[string]bool, len(Actions))
	for _, act := range Actions {
		out[act] = e.authorize(context.Background(), e.DB, nil, a, model, act, nil) == nil
	}
	return out
}

// permKey identifies what an actor may see for caches shared between actors (dashboards): everyone
// unrestricted shares one entry; others are keyed by user and roles, since if: may read user.name.
func permKey(a Actor) string {
	if unrestricted(a) {
		return "*"
	}
	roles := append([]string{}, a.Roles...)
	sort.Strings(roles)
	return a.UserName + "|" + a.ID + "|" + strings.Join(roles, ",")
}

// JobArgs is the actor as stored in a queued job's args (a flow resumed after wait:), so the job runs as
// the person who started it.
func (a Actor) JobArgs() map[string]any {
	return map[string]any{"id": a.ID, "user": a.UserName, "roles": a.Roles, "system": a.System}
}

// JobActor rebuilds the actor of a queued flow job from its args["actor"]. A job without one was started by
// the kernel (schedules) and runs as System.
func JobActor(org string, v any) Actor {
	m := meta.AsMap(v)
	if m == nil {
		return Actor{Org: org, ID: "system", System: true}
	}
	sys, _ := m["system"].(bool)
	return Actor{Org: org, ID: meta.Str(m, "id"), UserName: meta.Str(m, "user"), Roles: meta.StrList(m["roles"]), System: sys}
}
