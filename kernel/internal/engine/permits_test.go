package engine

import (
	"context"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

func TestPermits(t *testing.T) {
	e, _ := setup(t)
	ctx := context.Background()
	deal := store.Record{"name": "DEAL-1", "owner": "USR-007"}
	act := func(roles ...string) Actor {
		return Actor{Org: "kopi-senja", ID: "x@y.id", UserName: "USR-007", Roles: roles}
	}
	rep := act("sales_rep")
	other := act("sales_rep")
	other.UserName = "USR-008"
	cases := []struct {
		name  string
		a     Actor
		model string
		rec   store.Record
		do    string
		want  bool
	}{
		{"rep owner writes", rep, "deal", deal, "write", true},
		{"rep other cannot write", other, "deal", deal, "write", false},
		{"rep cannot delete", rep, "deal", deal, "delete", false},
		{"manager writes", act("sales_manager"), "deal", deal, "write", true},
		{"owner role", act("owner"), "deal", deal, "write", true},
		{"system role", act("system"), "deal", deal, "write", true},
		{"flow actor", Actor{Org: "kopi-senja", ID: "flow:x"}, "deal", deal, "write", true},
		{"seed actor", Actor{Org: "kopi-senja", ID: "seed"}, "deal", deal, "write", true},
		{"viewer denied", act("viewer"), "deal", deal, "write", false},
		{"unknown model", act("owner"), "nope", deal, "read", false},
	}
	for _, c := range cases {
		if got := e.Permits(ctx, c.a, c.model, c.rec, c.do); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
	// a model without permissions: is open to any role
	if m := e.Reg().Model("customer"); m == nil || len(meta.AsList(m.Raw["permissions"])) != 0 {
		t.Fatal("customer is expected to have no permissions:")
	}
	if !e.Permits(ctx, act("viewer"), "customer", store.Record{"name": "C-1"}, "write") {
		t.Error("model without permissions should be open")
	}
}
