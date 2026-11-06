package engine

import (
	"context"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/rk"
)

// Contract 0024 audit (6 Nov): the export grant filters before the cut, a dashboard needs an unconditional export
// grant, and a link title is written only for a record the actor can see (row scope and read grant).
func TestExportGrantAndTitles(t *testing.T) {
	e, owner := setup(t)
	ctx := context.Background()
	e.Reg().Roles["outlet_staff"] = &rk.Doc{Data: map[string]any{"name": "outlet_staff", "scope": map[string]any{"field": "outlet", "from": "user.outlets"}}}
	all, _, err := e.List(ctx, owner, "sales_invoice", ListOpts{Limit: 100000, Sort: "name"})
	if err != nil || len(all) < 2 {
		t.Fatalf("need two invoices: %d %v", len(all), err)
	}
	first := all[0]["name"].(string)
	inv := e.Reg().Models["sales_invoice"]
	inv.Raw["permissions"] = []any{
		map[string]any{"role": "clerk_x", "read": true, "export": true, "if": "name == '" + first + "'"},
		map[string]any{"role": "clerk_x", "read": true},
		map[string]any{"role": "outlet_staff", "read": true},
		map[string]any{"role": "owner", "read": true, "export": true},
	}
	e.permCache.Delete(inv)
	clerk := Actor{Org: owner.Org, ID: "clerk@example.test", UserName: "USR-902", Roles: []string{"clerk_x"}}
	out := e.Reg().Models["outlet"]
	out.Raw["permissions"] = []any{map[string]any{"role": "outlet_staff", "read": true}, map[string]any{"role": "owner", "read": true}}
	e.permCache.Delete(out)
	staff := Actor{Org: owner.Org, ID: "staff@example.test", UserName: "USR-901", Roles: []string{"outlet_staff"}, Outlets: []string{"OUT-01", "OUT-02"}}

	// sorted so the allowed row comes last: a cut before the export filter would lose it
	_, rows, more, err := e.Export(ctx, clerk, "sales_invoice", ListOpts{Sort: "-name"})
	if err != nil {
		t.Fatal(err)
	}
	if more || len(rows) != 1 || rows[0]["name"] != first {
		t.Fatalf("export = %d rows (more %v), want only %s", len(rows), more, first)
	}
	if _, _, _, err := e.Export(ctx, staff, "sales_invoice", ListOpts{}); err == nil {
		t.Fatal("read without an export grant must not export")
	}
	if e.CanExportAll(clerk, "sales_invoice") || !e.CanExportAll(owner, "sales_invoice") {
		t.Fatal("a conditional export grant is not an export of every record (dashboards)")
	}

	// titles: OUT-01 is outside a staff member's scope once their outlets are only OUT-02
	only2 := staff
	only2.Outlets = []string{"OUT-02"}
	tt := e.Titles(ctx, only2)
	if got := tt.Of("outlet", "OUT-01"); got != "" {
		t.Fatalf("title of an outlet outside the row scope = %q", got)
	}
	if got := e.Titles(ctx, owner).Of("outlet", "OUT-01"); got == "" {
		t.Fatal("the owner sees the outlet's title")
	}
}
