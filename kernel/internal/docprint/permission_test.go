package docprint

import (
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/engine"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
)

func permModel(perms ...any) *meta.Model {
	raw := map[string]any{}
	if len(perms) > 0 {
		raw["permissions"] = perms
	}
	return &meta.Model{Name: "sales_invoice", Raw: raw}
}

func TestAllowedNoPermissions(t *testing.T) {
	if !Allowed(permModel(), engine.Actor{Roles: []string{"sales_rep"}}, nil) {
		t.Fatal("a model without permissions must allow printing")
	}
}

func TestAllowedRole(t *testing.T) {
	m := permModel(
		map[string]any{"role": "sales_manager", "print": true},
		map[string]any{"role": "sales_rep", "read": true},
	)
	cases := []struct {
		role string
		want bool
	}{{"sales_manager", true}, {"sales_rep", false}, {"owner", true}, {"stranger", false}}
	for _, c := range cases {
		if got := Allowed(m, engine.Actor{ID: "a@contoh.test", Roles: []string{c.role}}, nil); got != c.want {
			t.Errorf("role %s: got %v want %v", c.role, got, c.want)
		}
	}
}

func TestAllowedIf(t *testing.T) {
	m := permModel(map[string]any{"role": "sales_rep", "print": true, "if": "owner == user.email"})
	a := engine.Actor{ID: "rep@contoh.test", Roles: []string{"sales_rep"}}
	if !Allowed(m, a, map[string]any{"owner": "rep@contoh.test"}) {
		t.Error("own record must be allowed")
	}
	if Allowed(m, a, map[string]any{"owner": "other@contoh.test"}) {
		t.Error("someone else's record must be refused")
	}
	if Allowed(m, a, map[string]any{}) {
		t.Error("a CEL error must count as false")
	}
}
