package engine

import (
	"context"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Permits reports whether the actor may do action (read, create, write, delete, export) on a record of the
// model, following the model's permissions: list (contract 0011 §5). A model without permissions is open;
// owners and system actors (system, seed, flows) always pass. A rule's if: is evaluated on the record.
func (e *Engine) Permits(ctx context.Context, a Actor, model string, rec store.Record, action string) bool {
	m := e.Reg().Model(model)
	if m == nil {
		return false
	}
	perms := meta.AsList(m.Raw["permissions"])
	if len(perms) == 0 {
		return true
	}
	if a.ID == "system" || a.ID == "seed" || strings.HasPrefix(a.ID, "flow:") {
		return true
	}
	for _, r := range a.Roles {
		if r == "owner" || r == "system" {
			return true
		}
	}
	for _, p := range perms {
		pm := meta.AsMap(p)
		if pm == nil || pm[action] != true {
			continue
		}
		role := meta.Str(pm, "role")
		has := false
		for _, r := range a.Roles {
			if r == role {
				has = true
				break
			}
		}
		if !has {
			continue
		}
		cond := meta.Str(pm, "if")
		if cond == "" {
			return true
		}
		if v, err := e.newScope(ctx, e.DB, a, m, rec, nil).eval(cond); err == nil && v == true {
			return true
		}
	}
	return false
}
