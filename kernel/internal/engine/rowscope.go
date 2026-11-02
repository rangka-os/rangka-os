package engine

import (
	"context"
	"sort"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/cel"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// RowScope limits which records a member sees (contract 0010 §5): a record of a model with one of Fields
// is visible when one of them holds a value in Values; a record of a model those fields link to is visible
// when its name is in Values; other models are not limited.
type RowScope struct {
	Fields  []string
	Values  map[string]bool
	targets map[string]bool // models the scope fields link to (outlet)
}

// RowScope returns the actor's row scope, or nil when none of its roles has one. A from expression that
// fails or does not return a list gives an empty scope (fail closed). Several scoped roles add up.
func (e *Engine) RowScope(a Actor) *RowScope {
	reg := e.Reg()
	var s *RowScope
	for _, role := range a.Roles {
		rs := reg.RoleScope(role)
		if rs == nil {
			continue
		}
		if s == nil {
			s = &RowScope{Values: map[string]bool{}}
		}
		for _, f := range rs.Fields {
			if !contains(s.Fields, f) {
				s.Fields = append(s.Fields, f)
			}
		}
		user := UserVar(a)
		v, err := cel.Eval(rs.From, &cel.Env{
			Vars: func(name string) (any, bool) {
				if name == "user" {
					return user, true
				}
				return nil, false
			},
			Today: e.Today, Now: e.Now, Apps: reg.IsInstalled,
		})
		if err != nil {
			continue
		}
		list, _ := v.([]any)
		for _, x := range list {
			if n := str(x); n != "" {
				s.Values[n] = true
			}
		}
	}
	if s != nil {
		s.targets = e.scopeTargets(s)
	}
	return s
}

// scopeTargets finds the models the scope fields link to on any installed model (outlet for outlet,
// from_outlet, to_outlet). A scope field that is a link nowhere has no target.
func (e *Engine) scopeTargets(s *RowScope) map[string]bool {
	out := map[string]bool{}
	for _, m := range e.Reg().Models {
		for _, fn := range s.Fields {
			if f := m.Field(fn); f != nil && f.Type == "link" && f.To != "" {
				out[f.To] = true
			}
		}
	}
	return out
}

// Visible reports whether rec of model m is inside the scope. A nil scope sees everything.
func (s *RowScope) Visible(m *meta.Model, rec map[string]any) bool {
	if s == nil || m == nil {
		return true
	}
	has := false
	for _, fn := range s.Fields {
		if m.Field(fn) == nil {
			continue
		}
		has = true
		if v := str(rec[fn]); v != "" && s.Values[v] {
			return true
		}
	}
	if has {
		return false
	}
	if s.targets[m.Name] {
		return s.Values[str(rec["name"])]
	}
	return true
}

// Guard returns Denied(model, action) when rec is outside the actor's row scope. Public entry points call
// it on the record they act on; print, import and attachment endpoints call it before acting.
func (e *Engine) Guard(ctx context.Context, a Actor, model string, rec store.Record, action string) error {
	s := e.RowScope(a)
	if s == nil {
		return nil
	}
	if !s.Visible(e.Reg().Model(model), rec) {
		return Denied(model, action)
	}
	return nil
}

// scopeKey identifies a scope in cache keys: "" when unscoped, else the sorted fields and values
// ("outlet=OUT-01,OUT-02"), so an empty scope does not share the unscoped entry.
func scopeKey(s *RowScope) string {
	if s == nil {
		return ""
	}
	fields := append([]string{}, s.Fields...)
	sort.Strings(fields)
	vals := make([]string, 0, len(s.Values))
	for v := range s.Values {
		vals = append(vals, v)
	}
	sort.Strings(vals)
	return strings.Join(fields, "+") + "=" + strings.Join(vals, ",")
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// eventFields returns the values of every row-scope field (of any role) that exists on m, for the record's
// event. nil when no role has a scope or none of its fields is on the model.
func (e *Engine) eventFields(m *meta.Model, rec store.Record) map[string]string {
	reg := e.Reg()
	var out map[string]string
	for role := range reg.Roles {
		rs := reg.RoleScope(role)
		if rs == nil {
			continue
		}
		for _, fn := range rs.Fields {
			if m.Field(fn) == nil {
				continue
			}
			if out == nil {
				out = map[string]string{}
			}
			out[fn] = str(rec[fn])
		}
	}
	return out
}

// EventVisible returns the filter for the actor's SSE stream: nil when the actor is unscoped (all events),
// else a func that keeps record events inside the row scope. Other event types pass.
func (e *Engine) EventVisible(a Actor) func(store.Event) bool {
	s := e.RowScope(a)
	if s == nil {
		return nil
	}
	return func(ev store.Event) bool {
		if ev.Type != "record" {
			return true
		}
		m := e.Reg().Model(ev.Model)
		if m == nil {
			return false
		}
		rec := map[string]any{"name": ev.Name}
		for k, v := range ev.Fields {
			rec[k] = v
		}
		return s.Visible(m, rec)
	}
}
