package engine

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/schema"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

// memberRegistry loads testdata/member-apps: module slip grants the member pseudo-role (contract 0074) read and print
// of the person's own released slip and their own kasbon requests; app staf binds screen ajukan for every member.
func memberRegistry(t *testing.T) *meta.Registry {
	t.Helper()
	reg, rep := meta.ValidateOpts(meta.Options{AppsDir: "../../testdata/member-apps/apps", SiteDir: "../../testdata/member-apps/site"}, nil, schema.Embedded())
	if rep.Errors() > 0 {
		t.Fatalf("metadata errors: %v", rep.All())
	}
	return reg
}

var (
	memberKasir    = Actor{Org: "o", ID: "k@example.test", UserName: "USR-002", Roles: []string{"kasir"}}
	memberGudang   = Actor{Org: "o", ID: "g@example.test", UserName: "USR-003", Roles: []string{"gudang"}}
	memberKeuangan = Actor{Org: "o", ID: "f@example.test", UserName: "USR-004", Roles: []string{"keuangan"}}
	memberNoRecord = Actor{Org: "o", ID: "x@example.test", Roles: []string{"kasir"}} // no employee record
	memberNoRole   = Actor{Org: "o", ID: "y@example.test", UserName: "USR-009"}      // not a member of the org
	memberSystem   = Actor{Org: "o", ID: "seed", UserName: "USR-002", System: true}
)

// TestMemberGrantAuthorize: a member grant is unioned with the person's own role, reaches only their own records,
// and gives only the member actions (contract 0074 §2).
func TestMemberGrantAuthorize(t *testing.T) {
	e := New(nil, memberRegistry(t))
	ctx := context.Background()
	mine := store.Record{"name": "SLP-1", "employee": "USR-002", "amount": int64(5000000), "released": true, "docstatus": int64(1)}
	draft := store.Record{"name": "SLP-2", "employee": "USR-002", "amount": int64(5000000), "released": false, "docstatus": int64(0)}
	theirs := store.Record{"name": "SLP-3", "employee": "USR-003", "amount": int64(4000000), "released": true, "docstatus": int64(1)}
	kasbon := store.Record{"name": "KSB-1", "requested_by": "USR-002", "amount": int64(100000), "docstatus": int64(0)}
	for _, c := range []struct {
		name   string
		a      Actor
		model  string
		action string
		rec    store.Record
		want   bool
	}{
		{"cashier reads own released slip", memberKasir, "slip", "read", mine, true},
		{"cashier prints own released slip", memberKasir, "slip", "print", mine, true},
		{"cashier cannot read own unreleased slip", memberKasir, "slip", "read", draft, false},
		{"cashier cannot read another's slip", memberKasir, "slip", "read", theirs, false},
		{"cashier cannot write own slip", memberKasir, "slip", "write", mine, false},
		{"cashier cannot export slips", memberKasir, "slip", "export", nil, false},
		{"warehouse staff reads own slip", memberGudang, "slip", "read", theirs, true},
		{"warehouse staff not another's", memberGudang, "slip", "read", mine, false},
		{"finance reads every slip by role", memberKeuangan, "slip", "read", theirs, true},
		{"member model-level read is conditional", memberKasir, "slip", "read", nil, true},
		{"no employee record, no member grant", memberNoRecord, "slip", "read", mine, false},
		{"no role in the org, no member grant", memberNoRole, "slip", "read", store.Record{"employee": "USR-009", "released": true}, false},
		{"system bypasses", memberSystem, "slip", "write", mine, true},
		{"cashier creates own kasbon", memberKasir, "kasbon", "create", kasbon, true},
		{"cashier cannot create a kasbon for another", memberGudang, "kasbon", "create", kasbon, false},
		{"cashier cannot submit own kasbon", memberKasir, "kasbon", "submit", kasbon, false},
		{"cashier cannot cancel own kasbon", memberKasir, "kasbon", "cancel", kasbon, false},
	} {
		got := e.Authorize(ctx, c.a, c.model, c.action, c.rec) == nil
		if got != c.want {
			t.Errorf("%s: Authorize(%s %s) = %v, want %v", c.name, c.model, c.action, got, c.want)
		}
	}
	if e.Unconditional(memberKasir, "slip", "read") {
		t.Error("a member grant must never read every record (Unconditional)")
	}
}

// TestMemberGrantFailClosed: a member grant the validator refuses (no if: on user.name, or a decider's action) grants
// nothing at run time either.
func TestMemberGrantFailClosed(t *testing.T) {
	reg := memberRegistry(t)
	m := reg.Model("slip")
	perms := meta.AsList(m.Raw["permissions"])
	m.Raw["permissions"] = append(perms,
		map[string]any{"role": "member", "write": true},
		map[string]any{"role": "member", "write": true, "if": "released"},
		map[string]any{"role": "member", "cancel": true, "if": "employee == user.name"})
	e := New(nil, reg)
	mine := store.Record{"name": "SLP-1", "employee": "USR-002", "released": true, "docstatus": int64(1)}
	for _, act := range []string{"write", "cancel"} {
		if e.Can(context.Background(), memberKasir, "slip", act, mine) {
			t.Errorf("member grant gave %s", act)
		}
	}
}

// TestMemberWorkAndDashboards: screen ajukan (roles [member]) is offered to every member; its home counts only for a
// member whose own role has none (contract 0074 §3); dashboard saya (roles [member]) opens for members only.
func TestMemberWorkAndDashboards(t *testing.T) {
	e := New(nil, memberRegistry(t))
	home := func(a Actor) string {
		for _, w := range e.Work(a) {
			if w.Home {
				return w.Screen
			}
		}
		return ""
	}
	has := func(a Actor, s string) bool { return e.ScreenAllowed(a, s) != nil }
	if got := home(memberKasir); got != "jual" {
		t.Errorf("cashier home = %q, want jual (own role first)", got)
	}
	if !has(memberKasir, "ajukan") {
		t.Error("cashier must reach ajukan through member")
	}
	if got := home(memberGudang); got != "ajukan" {
		t.Errorf("warehouse home = %q, want ajukan (member home)", got)
	}
	if has(memberGudang, "jual") {
		t.Error("warehouse staff must not reach jual")
	}
	if has(memberNoRole, "ajukan") || has(memberNoRecord, "ajukan") {
		t.Error("a person without a role or an employee record is not a member")
	}
	d := e.Reg().Dashboard("saya")
	if !e.DashboardAllowed(memberGudang, d) || !e.DashboardAllowed(memberKeuangan, d) {
		t.Error("dashboard saya must open for every member")
	}
	if e.DashboardAllowed(memberNoRole, d) {
		t.Error("dashboard saya must not open for a non-member")
	}
}

// TestMemberGrantListAndDashboard (database): a list and the saya dashboard show a member only their own released
// slips; finance lists every slip through its role.
func TestMemberGrantListAndDashboard(t *testing.T) {
	url := testdb.New(t)
	ctx := context.Background()
	reg := memberRegistry(t)
	db, err := pg.NewPool(ctx, url, 4)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if _, err := store.Migrate(ctx, db, reg, false, "test"); err != nil {
		t.Fatal(err)
	}
	e := New(db, reg)
	e.Now = FixedNow
	if _, err := e.Seed(ctx, Actor{Org: "o", ID: "seed", System: true}, map[string][]map[string]any{"slip": {
		{"name": "SLP-1", "employee": "USR-002", "amount": 5000000, "released": true},
		{"name": "SLP-2", "employee": "USR-002", "amount": 5100000, "released": false},
		{"name": "SLP-3", "employee": "USR-003", "amount": 4000000, "released": true},
	}}); err != nil {
		t.Fatal(err)
	}
	names := func(a Actor) []string {
		rows, _, err := e.List(ctx, a, "slip", ListOpts{Sort: "name"})
		if err != nil {
			t.Fatalf("list as %s: %v", a.ID, err)
		}
		var out []string
		for _, r := range rows {
			out = append(out, r["name"].(string))
		}
		return out
	}
	if got := fmt.Sprint(names(memberKasir)); got != "[SLP-1]" {
		t.Errorf("cashier lists %s, want [SLP-1]", got)
	}
	if got := fmt.Sprint(names(memberGudang)); got != "[SLP-3]" {
		t.Errorf("warehouse staff lists %s, want [SLP-3]", got)
	}
	if got := fmt.Sprint(names(memberKeuangan)); got != "[SLP-1 SLP-2 SLP-3]" {
		t.Errorf("finance lists %s, want all three", got)
	}
	out, _, err := e.Dashboard(ctx, memberKasir, "saya", 0)
	if err != nil {
		t.Fatal(err)
	}
	if s := fmt.Sprint(out); !strings.Contains(s, "SLP-1") || strings.Contains(s, "SLP-2") || strings.Contains(s, "SLP-3") {
		t.Errorf("saya for the cashier = %s, want only SLP-1", s)
	}
}
