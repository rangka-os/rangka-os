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
	if input == nil || m == nil || unrestricted(a) || len(e.hiddenFields(a, m)) == 0 && !hasTable(m) {
		return input
	}
	out := make(map[string]any, len(input))
	for k, v := range input {
		out[k] = v
	}
	for _, f := range m.Fields {
		if f.Type == "table" {
			cm := e.Reg().Model(f.Of)
			if l, ok := out[f.Name].([]any); ok && cm != nil {
				rows := make([]any, len(l))
				for i, x := range l {
					if row, ok := x.(map[string]any); ok {
						rows[i] = e.dropHidden(a, cm, row)
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
