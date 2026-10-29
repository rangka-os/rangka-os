package engine

import (
	"context"
	"sort"

	"github.com/rangka-os/rangka-os/kernel/internal/cel"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// scope evaluates expressions against one record: fields by name, links load their record on member
// access, table fields are lists of rows, plus user and any extra variables (event, flow variables).
type scope struct {
	ctx   context.Context
	q     pg.Querier
	e     *Engine
	org   string
	m     *meta.Model
	rec   store.Record
	actor Actor
	extra map[string]any
	links map[string]map[string]any // shared cache of loaded link records (model/name → scope map)
}

func (e *Engine) newScope(ctx context.Context, q pg.Querier, a Actor, m *meta.Model, rec store.Record, links map[string]map[string]any) *scope {
	if links == nil {
		links = map[string]map[string]any{}
	}
	return &scope{ctx: ctx, q: q, e: e, org: a.Org, m: m, rec: rec, actor: a, links: links}
}

func (o *op) scope(m *meta.Model, rec store.Record) *scope {
	return o.e.newScope(o.ctx, o.tx, o.actor, m, rec, o.links)
}

// UserVar is the CEL user global.
func UserVar(a Actor) map[string]any {
	roles := make([]any, len(a.Roles))
	for i, r := range a.Roles {
		roles[i] = r
	}
	name := a.UserName
	if name == "" {
		name = a.ID
	}
	return map[string]any{"name": name, "email": a.ID, "roles": roles}
}

func (s *scope) env() *cel.Env {
	return &cel.Env{
		Vars:  s.lookup,
		Today: s.e.Today,
		Now:   s.e.Now,
		Option: func(field, attr string) (any, error) {
			f := s.m.Field(field)
			if f == nil {
				return nil, nil
			}
			o := f.Option(str(s.rec[field]))
			if o == nil {
				return nil, nil
			}
			return o.Attrs[attr], nil
		},
		Apps: s.e.Reg().IsInstalled,
	}
}

func (s *scope) lookup(name string) (any, bool) {
	if f := s.m.Field(name); f != nil {
		v := s.rec[name]
		switch f.Type {
		case "link":
			n := str(v)
			if n == "" {
				return "", true
			}
			return s.link(f.To, n), true
		case "table":
			if l, ok := v.([]any); ok {
				return l, true
			}
			return []any{}, true
		}
		if v == nil {
			return store.Zero(f), true
		}
		return v, true
	}
	if v, ok := s.extra[name]; ok {
		return v, true
	}
	switch name {
	case "user":
		return UserVar(s.actor), true
	case "name", "created_by", "created_at", "modified_at", "amended_from":
		return str(s.rec[name]), true
	case "docstatus":
		return toInt(s.rec["docstatus"]), true
	case "status":
		if st := Status(s.m, s.rec, s.e.Today()); st != "" {
			return st, true
		}
		return "", true
	}
	return nil, false
}

func (s *scope) link(model, name string) *cel.Link {
	return &cel.Link{Model: model, Name: name, Load: func() (map[string]any, error) {
		key := model + "/" + name
		if r, ok := s.links[key]; ok {
			return r, nil
		}
		tm := s.e.Reg().Models[model]
		out := map[string]any{"name": name}
		if tm == nil {
			return out, nil
		}
		rec, err := store.Get(s.ctx, s.q, s.e.Reg(), s.org, tm, name)
		if err != nil && err != store.ErrNotFound {
			return nil, err
		}
		if rec == nil {
			rec = store.Record{"name": name}
		}
		inner := s.e.newScope(s.ctx, s.q, s.actor, tm, rec, s.links)
		for _, f := range tm.Fields {
			v, _ := inner.lookup(f.Name)
			out[f.Name] = v
		}
		for _, k := range []string{"created_by", "created_at", "modified_at", "docstatus", "amended_from", "status"} {
			if _, ok := out[k]; !ok {
				if v, ok := inner.lookup(k); ok {
					out[k] = v
				}
			}
		}
		s.links[key] = out
		return out, nil
	}}
}

// eval evaluates src in this scope.
func (s *scope) eval(src string) (any, error) { return cel.Eval(src, s.env()) }

// ---------- defaults, compute

func missing(v any) bool {
	if v == nil {
		return true
	}
	s, ok := v.(string)
	return ok && s == ""
}

// applyDefaults fills missing fields from their default expressions (fields with compute are skipped);
// a default that fails (it needs a field not set yet) is left out, like the prototype.
func (s *scope) applyDefaults() {
	for _, f := range s.m.Fields {
		if f.Default == "" || f.Compute != "" || !missing(s.rec[f.Name]) {
			continue
		}
		v, err := s.eval(f.Default)
		if err != nil || v == nil {
			continue
		}
		switch v.(type) {
		case map[string]any, []any, cel.Duration:
			continue
		}
		if c, err := store.Coerce(f, v); err == nil {
			s.rec[f.Name] = c
		}
	}
}

// fill gives every field a value (missing → zero value) so expressions see CONTRACTS' missing values.
func fill(m *meta.Model, rec store.Record) {
	for _, f := range m.Fields {
		if _, ok := rec[f.Name]; !ok || rec[f.Name] == nil {
			rec[f.Name] = store.Zero(f)
		}
	}
}

// computeOrder sorts computed fields so each comes after the computed fields it reads (declaration order
// otherwise); cycles keep declaration order (validate reports them).
func computeOrder(m *meta.Model) []*meta.Field {
	var comp []*meta.Field
	idx := map[string]int{}
	for _, f := range m.Fields {
		if f.Compute != "" {
			idx[f.Name] = len(comp)
			comp = append(comp, f)
		}
	}
	deps := map[string][]string{}
	for _, f := range comp {
		refs, _ := cel.FreeVars(f.Compute)
		for _, r := range refs {
			if _, ok := idx[r.Head]; ok && r.Kind == "ref" && r.Head != f.Name {
				deps[f.Name] = append(deps[f.Name], r.Head)
			}
		}
	}
	var out []*meta.Field
	state := map[string]int{}
	var visit func(f *meta.Field)
	visit = func(f *meta.Field) {
		if state[f.Name] != 0 {
			return
		}
		state[f.Name] = 1
		ds := deps[f.Name]
		sort.SliceStable(ds, func(i, j int) bool { return idx[ds[i]] < idx[ds[j]] })
		for _, d := range ds {
			if state[d] == 0 {
				visit(comp[idx[d]])
			}
		}
		state[f.Name] = 2
		out = append(out, f)
	}
	for _, f := range comp {
		visit(f)
	}
	return out
}

// compute recomputes child rows first, then the record's computed fields (errors give the zero value).
func (s *scope) compute() {
	for _, f := range s.m.Fields {
		if f.Type != "table" {
			continue
		}
		cm := s.e.Reg().Models[f.Of]
		if cm == nil {
			continue
		}
		for _, row := range rows(s.rec[f.Name]) {
			child := s.e.newScope(s.ctx, s.q, s.actor, cm, row, s.links)
			child.compute()
		}
	}
	for _, f := range computeOrder(s.m) {
		v, err := s.eval(f.Compute)
		if err != nil || v == nil {
			s.rec[f.Name] = store.Zero(f)
			continue
		}
		c, err := store.Coerce(f, v)
		if err != nil {
			c = store.Zero(f)
		}
		s.rec[f.Name] = c
	}
}

func rows(v any) []map[string]any {
	l, _ := v.([]any)
	out := make([]map[string]any, 0, len(l))
	for _, x := range l {
		if m, ok := x.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// Status derives the invoice-style status of a submittable record (prototype status()); "" otherwise.
func Status(m *meta.Model, rec map[string]any, today string) string {
	if !m.IsSubmittable || m.Field("status") != nil {
		return ""
	}
	switch toInt(rec["docstatus"]) {
	case 0:
		return "draft"
	case 2:
		return "cancelled"
	}
	total, paid := num(rec["grand_total"]), num(rec["paid_amount"])
	if total > 0 && paid >= total {
		return "paid"
	}
	if d := str(rec["due_date"]); d != "" && d < today {
		return "overdue"
	}
	if paid > 0 {
		return "partly"
	}
	return "submitted"
}

func num(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int64:
		return float64(x)
	case int:
		return float64(x)
	}
	return 0
}
