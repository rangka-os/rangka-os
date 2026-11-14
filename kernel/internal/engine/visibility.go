package engine

import (
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Field visibility (contract 0027): a field with hidden_for: [roles] is hidden from an actor whose every role is in
// that list. The owner and kernel actors see every field. A money field's IDR twin follows its field.

// FieldHidden reports whether field f of m is hidden from a.
func (e *Engine) FieldHidden(a Actor, m *meta.Model, f *meta.Field) bool {
	if unrestricted(a) || len(a.Roles) == 0 || m == nil || f == nil {
		return false
	}
	if base := f.MoneyOf(); base != "" {
		if bf := m.Field(base); bf != nil && bf != f {
			return e.FieldHidden(a, m, bf)
		}
	}
	hidden := meta.StrList(f.Raw["hidden_for"])
	if len(hidden) == 0 {
		return false
	}
	in := map[string]bool{}
	for _, r := range hidden {
		in[r] = true
	}
	for _, r := range a.Roles {
		if !in[r] {
			return false // a role that may see it is enough (grants add up)
		}
	}
	return true
}

// hiddenFields is the set of field names of m hidden from a (nil when none).
func (e *Engine) hiddenFields(a Actor, m *meta.Model) map[string]bool {
	var out map[string]bool
	if m == nil || unrestricted(a) {
		return nil
	}
	for _, f := range m.Fields {
		if e.FieldHidden(a, m, f) {
			if out == nil {
				out = map[string]bool{}
			}
			out[f.Name] = true
		}
	}
	return out
}

// Redact removes from rec, in place, the fields of m hidden from a, also inside the rows of its table fields, and
// returns rec. Records leaving the kernel to a caller (Get, List, export, the result of a write) go through it.
func (e *Engine) Redact(a Actor, m *meta.Model, rec store.Record) store.Record {
	if rec == nil || m == nil || unrestricted(a) {
		return rec
	}
	for _, f := range m.Fields {
		if f.Type == "table" {
			cm := e.Reg().Model(f.Of)
			if cm == nil {
				continue
			}
			if l, ok := rec[f.Name].([]any); ok {
				for _, x := range l {
					if row, ok := x.(map[string]any); ok {
						e.Redact(a, cm, row)
					}
				}
			}
			continue
		}
		if e.FieldHidden(a, m, f) {
			delete(rec, f.Name)
		}
	}
	return rec
}

// redactResult redacts the record of a write's result for its caller.
func (e *Engine) redactResult(a Actor, model string, res *Result) *Result {
	if res != nil {
		e.Redact(a, e.Reg().Model(model), res.Record)
	}
	return res
}

// dropHidden removes from a client's input the fields of m hidden from a (and inside table rows): a person who
// cannot see a field does not set it either.
func (e *Engine) dropHidden(a Actor, m *meta.Model, input map[string]any) map[string]any {
	return e.dropHiddenIn(e.Reg(), a, m, input)
}

// dropHiddenIn is dropHidden against reg (a unit of work's pinned registry).
func (e *Engine) dropHiddenIn(reg *meta.Registry, a Actor, m *meta.Model, input map[string]any) map[string]any {
	if input == nil || m == nil || unrestricted(a) || len(e.hiddenFields(a, m)) == 0 && !hasTable(m) {
		return input
	}
	out := make(map[string]any, len(input))
	for k, v := range input {
		out[k] = v
	}
	for _, f := range m.Fields {
		if f.Type == "table" {
			cm := reg.Model(f.Of)
			if l, ok := out[f.Name].([]any); ok && cm != nil {
				rows := make([]any, len(l))
				for i, x := range l {
					if row, ok := x.(map[string]any); ok {
						rows[i] = e.dropHiddenIn(reg, a, cm, row)
					} else {
						rows[i] = x
					}
				}
				out[f.Name] = rows
			}
			continue
		}
		if e.FieldHidden(a, m, f) {
			delete(out, f.Name)
		}
	}
	return out
}

func hasTable(m *meta.Model) bool {
	for _, f := range m.Fields {
		if f.Type == "table" {
			return true
		}
	}
	return false
}

// DashboardAllowed reports whether a may open dashboard d: a dashboard with roles: opens only for an actor holding
// one of them (and for the owner and kernel actors).
func (e *Engine) DashboardAllowed(a Actor, d *meta.Dashboard) bool {
	if d == nil || unrestricted(a) {
		return true
	}
	roles := meta.StrList(d.Raw["roles"])
	if len(roles) == 0 {
		return true
	}
	for _, r := range roles {
		if hasRole(a, r) {
			return true
		}
	}
	return false
}

// keepHidden is dropHidden for an update: the patch keeps the stored value of every field of m hidden from a, also
// in the rows of table fields (matched by row name; a new row gets none), so a person who cannot see a field
// neither sets nor clears it when they save the form (audit 2026-11-07: a child row's hidden cost became 0).
func (e *Engine) keepHidden(a Actor, m *meta.Model, patch map[string]any, cur store.Record) map[string]any {
	return e.keepHiddenIn(e.Reg(), a, m, patch, cur)
}

// keepHiddenIn is keepHidden against reg (a unit of work's pinned registry).
func (e *Engine) keepHiddenIn(reg *meta.Registry, a Actor, m *meta.Model, patch map[string]any, cur store.Record) map[string]any {
	if patch == nil || m == nil || unrestricted(a) {
		return patch
	}
	out := make(map[string]any, len(patch))
	for k, v := range patch {
		out[k] = v
	}
	for _, f := range m.Fields {
		if f.Type == "table" {
			cm := reg.Model(f.Of)
			l, ok := out[f.Name].([]any)
			if !ok || cm == nil || e.hiddenFields(a, cm) == nil {
				continue
			}
			stored := map[string]map[string]any{}
			for _, x := range rows(cur[f.Name]) {
				stored[str(x["name"])] = x
			}
			kept := make([]any, len(l))
			for i, x := range l {
				row, ok := x.(map[string]any)
				if !ok {
					kept[i] = x
					continue
				}
				nr := e.dropHiddenIn(reg, a, cm, row)
				if old := stored[str(row["name"])]; old != nil && str(row["name"]) != "" {
					for _, cf := range cm.Fields {
						if e.FieldHidden(a, cm, cf) {
							if v, ok := old[cf.Name]; ok {
								nr[cf.Name] = v
							}
						}
					}
				}
				kept[i] = nr
			}
			out[f.Name] = kept
			continue
		}
		if e.FieldHidden(a, m, f) {
			delete(out, f.Name) // the merge keeps the stored value
		}
	}
	return out
}
