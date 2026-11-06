package engine

import (
	"context"
	"math/big"
	"sort"

	"github.com/rangka-os/rangka-os/kernel/internal/cel"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/money"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// scope evaluates expressions against one record: fields by name, links load their record on member
// access, table fields are lists of rows, plus user and any extra variables (event, flow variables).
type scope struct {
	ctx    context.Context
	q      pg.Querier
	e      *Engine
	org    string
	m      *meta.Model
	rec    store.Record
	actor  Actor
	extra  map[string]any
	links  map[string]map[string]any // shared cache of loaded link records (model/name → scope map)
	parent *scope                    // the document of a child row (nil for a document)
	// client marks an expression the caller wrote (a list ?filter=): a link it walks loads only a record the
	// actor could read with GET /r/{model}/{name} (readableLink, row scope rs); any other record reads as a
	// missing one. Metadata expressions (permissions, compute, hooks, flows) keep client false and see every
	// record. Client loads are cached under their own keys so the two never share an entry.
	client bool
	rs     *RowScope
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

// newClientScope is newScope for an expression the caller supplied (see scope.client); rs is the actor's row scope.
func (e *Engine) newClientScope(ctx context.Context, a Actor, m *meta.Model, rec store.Record, rs *RowScope, links map[string]map[string]any) *scope {
	s := e.newScope(ctx, e.DB, a, m, rec, links)
	s.client, s.rs = true, rs
	return s
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
	outlets := make([]any, len(a.Outlets))
	for i, o := range a.Outlets {
		outlets[i] = o
	}
	return map[string]any{"name": name, "email": a.ID, "roles": roles, "outlets": outlets}
}

// GlobalVar resolves the read-only globals user, org and tax for a (contract 0010 §8): org and tax come
// from the actor's org settings. ok is false for any other name.
func GlobalVar(reg *meta.Registry, a Actor, name string) (any, bool) {
	switch name {
	case "user":
		return UserVar(a), true
	case "org":
		return reg.OrgVar(a.Org), true
	case "tax":
		return reg.TaxVar(a.Org), true
	}
	return nil, false
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
	case "org", "tax":
		return GlobalVar(s.e.Reg(), s.actor, name)
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
		if s.client {
			key = "client:" + key
		}
		if r, ok := s.links[key]; ok {
			return r, nil
		}
		tm := s.e.Reg().Models[model]
		out := map[string]any{"name": name}
		if tm == nil {
			return out, nil
		}
		var rec store.Record
		if s.client {
			// nil when missing or not readable: both read as a missing record, so a filter cannot tell them apart; a
			// database error fails the expression (the row is left out), as for a metadata expression
			var err error
			rec, _, err = s.e.readableLinkErr(s.ctx, s.actor, s.rs, model, name, s.links)
			if err != nil {
				return nil, err
			}
		} else {
			var err error
			rec, err = store.Get(s.ctx, s.q, s.e.Reg(), s.org, tm, name)
			if err != nil && err != store.ErrNotFound {
				return nil, err
			}
		}
		if rec == nil {
			rec = store.Record{"name": name}
		}
		inner := s.e.newScope(s.ctx, s.q, s.actor, tm, rec, s.links)
		inner.client, inner.rs = s.client, s.rs
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
			if r.Kind != "ref" {
				continue
			}
			heads := []string{r.Head}
			// an IDR twin depends on its money field, currency and rate
			if tw := m.Field(r.Head); tw != nil && tw.MoneyOf() != "" {
				if mf := m.Field(tw.MoneyOf()); mf != nil {
					heads = []string{mf.Name, mf.CurrencyField(), mf.RateField()}
				}
			}
			for _, h := range heads {
				if _, ok := idx[h]; ok && h != f.Name {
					deps[f.Name] = append(deps[f.Name], h)
				}
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

// compute recomputes child rows first, then the record's computed fields (errors give the zero value). The
// IDR twins are settled before the computes and again after each one, so a compute reading <field>_idr (its
// own record's, or a row's) sees the twin of the current amount and rate.
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
			child.parent = s
			child.compute()
		}
	}
	s.settleOwn()
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
		if d, ok := c.(money.Decimal); ok && money.IsIDR(s.currencyOf(f)) {
			c = d.Round(0) // IDR documents stay whole rupiah; later computes see the rounded value
		}
		s.rec[f.Name] = c
		s.settleOwn()
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
	total, paid := amount(rec["grand_total"]), amount(rec["paid_amount"])
	if total.Sign() > 0 && paid.Cmp(total) >= 0 {
		return "paid"
	}
	if d := str(rec["due_date"]); d != "" && d < today {
		return "overdue"
	}
	if paid.Sign() > 0 {
		return "partly"
	}
	return "submitted"
}

// amount is the exact value of a number or money decimal (0 for anything else).
func amount(v any) *big.Rat {
	if r, ok := exact(v); ok {
		return r
	}
	return new(big.Rat)
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
