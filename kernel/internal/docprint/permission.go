package docprint

import (
	"slices"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/cel"
	"github.com/rangka-os/rangka-os/kernel/internal/engine"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
)

// Allowed reports whether actor a may print rec of model m. A model without permissions allows everyone,
// owner always passes; otherwise a permission entry for one of the actor's roles needs print: true and,
// when it has an if expression, that expression must evaluate to true over the record's fields and user.
// A CEL error counts as false.
func Allowed(m *meta.Model, a engine.Actor, rec map[string]any) bool {
	perms, _ := m.Raw["permissions"].([]any)
	if len(perms) == 0 || slices.Contains(a.Roles, "owner") {
		return true
	}
	user := engine.UserVar(a)
	env := &cel.Env{
		Vars: func(name string) (any, bool) {
			if name == "user" {
				return user, true
			}
			v, ok := rec[name]
			return v, ok
		},
		Today: func() string { return time.Now().Format("2006-01-02") },
		Now:   time.Now,
	}
	for _, p := range perms {
		e, ok := p.(map[string]any)
		if !ok {
			continue
		}
		role, _ := e["role"].(string)
		if !slices.Contains(a.Roles, role) || e["print"] != true {
			continue
		}
		cond, _ := e["if"].(string)
		if cond == "" {
			return true
		}
		if v, err := cel.Eval(cond, env); err == nil && v == true {
			return true
		}
	}
	return false
}
