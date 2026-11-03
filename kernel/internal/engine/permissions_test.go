package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/schema"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

// permAppsDir is the small permissions fixture (app perm: voucher with permissions:, note without).
const permAppsDir = "../../testdata/perm-apps"

const permOrg = "perm-test"

var (
	permClerkA  = Actor{Org: permOrg, ID: "a@example.test", UserName: "USR-002", Roles: []string{"clerk"}}
	permClerkB  = Actor{Org: permOrg, ID: "b@example.test", UserName: "USR-003", Roles: []string{"clerk"}}
	permManager = Actor{Org: permOrg, ID: "m@example.test", UserName: "USR-004", Roles: []string{"manager"}}
	permViewer  = Actor{Org: permOrg, ID: "v@example.test", UserName: "USR-005", Roles: []string{"viewer"}}
	permOwner   = Actor{Org: permOrg, ID: "o@example.test", UserName: "USR-001", Roles: []string{"owner"}}
	permNobody  = Actor{Org: permOrg, ID: "n@example.test", UserName: "USR-006"}
	permStray   = Actor{Org: permOrg, ID: "s@example.test", UserName: "USR-007", Roles: []string{"auditor"}}
	permSystem  = Actor{Org: permOrg, ID: "seed", System: true}
)

func loadPermRegistry(t *testing.T) *meta.Registry {
	t.Helper()
	reg, rep := meta.Validate(permAppsDir, "", nil, schema.Embedded())
	if rep.Errors() > 0 {
		t.Fatalf("metadata errors: %v", rep.All())
	}
	return reg
}

// permSetup migrates the perm fixture and seeds V1 (owner USR-002) and V2 (owner USR-003) as System.
func permSetup(t *testing.T) *Engine {
	t.Helper()
	url := testdb.New(t)
	ctx := context.Background()
	reg := loadPermRegistry(t)
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
	if _, err := e.Seed(ctx, permSystem, permFixtures()); err != nil {
		t.Fatal(err)
	}
	return e
}

func permFixtures() map[string][]map[string]any {
	return map[string][]map[string]any{
		"voucher": {
			{"name": "V1", "title": "Bensin", "owner": "USR-002", "amount": 100000},
			{"name": "V2", "title": "Parkir", "owner": "USR-003", "amount": 20000},
		},
		"note": {{"name": "N1", "body": "halo"}},
	}
}

func wantPermErr(t *testing.T, err error, model, action string) {
	t.Helper()
	var pe *PermissionError
	if !errors.As(err, &pe) {
		t.Fatalf("err = %v (%T), want *PermissionError{%s, %s}", err, err, model, action)
	}
	if pe.Model != model || pe.Action != action {
		t.Fatalf("PermissionError = %+v, want {%s %s}", *pe, model, action)
	}
	if want := "Kamu tidak punya izin untuk " + action + " " + model; pe.Error() != want {
		t.Fatalf("Error() = %q, want %q", pe.Error(), want)
	}
}

// TestAuthorizeMatrix needs no database: rec-level conditions only read the record and user.name.
func TestAuthorizeMatrix(t *testing.T) {
	e := New(nil, loadPermRegistry(t))
	ctx := context.Background()
	v1 := store.Record{"name": "V1", "owner": "USR-002", "docstatus": int64(0)}
	v2 := store.Record{"name": "V2", "owner": "USR-003", "docstatus": int64(0)}
	n1 := store.Record{"name": "N1", "body": "halo"}
	cases := []struct {
		name   string
		a      Actor
		model  string
		action string
		rec    store.Record
		want   bool
	}{
		{"owner deletes", permOwner, "voucher", "delete", v1, true},
		{"owner customizes", permOwner, "voucher", "customize", nil, true},
		{"system cancels", permSystem, "voucher", "cancel", v2, true},
		{"system without roles imports", Actor{System: true}, "voucher", "import", nil, true},
		{"manager reads other", permManager, "voucher", "read", v2, true},
		{"manager cancels", permManager, "voucher", "cancel", v1, true},
		{"manager exports", permManager, "voucher", "export", nil, true},
		{"manager cannot import", permManager, "voucher", "import", nil, false},
		{"clerk reads own", permClerkA, "voucher", "read", v1, true},
		{"clerk reads other", permClerkA, "voucher", "read", v2, false},
		{"clerk writes own", permClerkA, "voucher", "write", v1, true},
		{"clerk writes other", permClerkA, "voucher", "write", v2, false},
		{"clerk submits own", permClerkA, "voucher", "submit", v1, true},
		{"clerk cannot delete own", permClerkA, "voucher", "delete", v1, false},
		{"clerk cannot cancel own", permClerkA, "voucher", "cancel", v1, false},
		{"clerk model-level read is maybe", permClerkA, "voucher", "read", nil, true},
		{"clerk model-level delete", permClerkA, "voucher", "delete", nil, false},
		{"clerk B reads own", permClerkB, "voucher", "read", v2, true},
		{"viewer reads", permViewer, "voucher", "read", v2, true},
		{"viewer cannot write", permViewer, "voucher", "write", v1, false},
		{"viewer cannot create", permViewer, "voucher", "create", nil, false},
		{"no role on voucher", permNobody, "voucher", "read", v1, false},
		{"no role on note", permNobody, "note", "write", n1, true},
		{"no role on note delete", permNobody, "note", "delete", nil, true},
		{"unknown role", permStray, "voucher", "read", nil, false},
		{"union of roles", Actor{Org: permOrg, UserName: "USR-009", Roles: []string{"viewer", "clerk"}}, "voucher", "read", v2, true},
		{"child resolves to parent (clerk)", permClerkA, "voucher_line", "write", v1, true},
		{"child resolves to parent (viewer)", permViewer, "voucher_line", "write", nil, false},
		{"unknown action", permManager, "voucher", "approve", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := e.Authorize(ctx, c.a, c.model, c.action, c.rec)
			if got := err == nil; got != c.want {
				t.Fatalf("Authorize(%v, %s, %s) = %v, want allowed=%v", c.a.Roles, c.model, c.action, err, c.want)
			}
			if !c.want {
				model := c.model
				if model == "voucher_line" {
					model = "voucher"
				}
				wantPermErr(t, err, model, c.action)
			}
			if e.Can(ctx, c.a, c.model, c.action, c.rec) != c.want {
				t.Fatalf("Can disagrees with Authorize")
			}
		})
	}
	al := e.Allowed(permClerkA, "voucher")
	if len(al) != len(Actions) {
		t.Fatalf("Allowed has %d actions, want %d", len(al), len(Actions))
	}
	want := map[string]bool{"read": true, "create": true, "write": true, "submit": true, "print": true}
	for _, act := range Actions {
		if al[act] != want[act] {
			t.Errorf("Allowed(clerk)[%s] = %v, want %v", act, al[act], want[act])
		}
	}
	for act, ok := range e.Allowed(permNobody, "note") {
		if !ok {
			t.Errorf("Allowed(no role, note)[%s] = false", act)
		}
	}
}

func TestListFiltersByIf(t *testing.T) {
	e := permSetup(t)
	ctx := context.Background()
	rows, total, err := e.List(ctx, permClerkA, "voucher", ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(rows) != 1 || rows[0]["name"] != "V1" {
		t.Fatalf("clerk A list = %d rows %v, want only V1 with total 1", total, rows)
	}
	rows, total, err = e.List(ctx, permViewer, "voucher", ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(rows) != 2 {
		t.Fatalf("viewer list total = %d, want 2", total)
	}
	_, _, err = e.List(ctx, permNobody, "voucher", ListOpts{})
	wantPermErr(t, err, "voucher", "read")
	if _, total, err = e.List(ctx, permNobody, "note", ListOpts{}); err != nil || total != 1 {
		t.Fatalf("no-role note list = %d, %v", total, err)
	}
}

func TestGetOtherRecordDenied(t *testing.T) {
	e := permSetup(t)
	ctx := context.Background()
	if _, err := e.Get(ctx, permClerkA, "voucher", "V1"); err != nil {
		t.Fatalf("own record: %v", err)
	}
	_, err := e.Get(ctx, permClerkA, "voucher", "V2")
	wantPermErr(t, err, "voucher", "read")
	// write and delete check the stored record too
	_, err = e.Update(ctx, permClerkA, "voucher", "V2", map[string]any{"title": "x"})
	wantPermErr(t, err, "voucher", "write")
	err = e.Delete(ctx, permViewer, "voucher", "V1")
	wantPermErr(t, err, "voucher", "delete")
	if _, err := e.Update(ctx, permClerkA, "voucher", "V1", map[string]any{"title": "Bensin pagi"}); err != nil {
		t.Fatalf("clerk updates own: %v", err)
	}
}

func TestCreateChecksNewRecord(t *testing.T) {
	e := permSetup(t)
	ctx := context.Background()
	_, err := e.Create(ctx, permClerkA, "voucher", map[string]any{"title": "Tol", "owner": "USR-003", "amount": 5000})
	wantPermErr(t, err, "voucher", "create")
	res, err := e.Create(ctx, permClerkA, "voucher", map[string]any{"title": "Tol", "owner": "USR-002", "amount": 5000})
	if err != nil {
		t.Fatalf("create own: %v", err)
	}
	if res.Record["owner"] != "USR-002" {
		t.Fatalf("owner = %v", res.Record["owner"])
	}
	_, err = e.Create(ctx, permViewer, "voucher", map[string]any{"title": "Tol", "owner": "USR-005"})
	wantPermErr(t, err, "voucher", "create")
}

func TestSubmitCancel(t *testing.T) {
	e := permSetup(t)
	ctx := context.Background()
	_, err := e.Submit(ctx, permClerkB, "voucher", "V1", false)
	wantPermErr(t, err, "voucher", "submit")
	res, err := e.Submit(ctx, permClerkA, "voucher", "V1", false)
	if err != nil {
		t.Fatalf("clerk submits own: %v", err)
	}
	name := str(res.Record["name"])
	_, err = e.Cancel(ctx, permClerkA, "voucher", name)
	wantPermErr(t, err, "voucher", "cancel")
	if _, err := e.Cancel(ctx, permManager, "voucher", name); err != nil {
		t.Fatalf("manager cancels: %v", err)
	}
	_, err = e.Amend(ctx, permClerkA, "voucher", name)
	wantPermErr(t, err, "voucher", "amend")
	if _, err := e.Amend(ctx, permManager, "voucher", name); err != nil {
		t.Fatalf("manager amends: %v", err)
	}
}

func TestSeedAndFlowsBypass(t *testing.T) {
	e := permSetup(t) // seeded as Actor{System: true}
	ctx := context.Background()
	rows, total, err := e.List(ctx, Actor{Org: permOrg, ID: "flow:x", System: true}, "voucher", ListOpts{})
	if err != nil || total != 2 || len(rows) != 2 {
		t.Fatalf("system list = %d, %v", total, err)
	}
	if _, err := e.Create(ctx, Actor{Org: permOrg, ID: "flow:x", System: true}, "voucher", map[string]any{"title": "Flow", "owner": "USR-099"}); err != nil {
		t.Fatalf("system create: %v", err)
	}
	// seeding as a person needs create
	_, err = e.Seed(ctx, permViewer, map[string][]map[string]any{"voucher": {{"name": "V9", "title": "x", "owner": "USR-005"}}})
	wantPermErr(t, err, "voucher", "create")
}

func TestDashboardWidgetPermissions(t *testing.T) {
	e := permSetup(t)
	ctx := context.Background()
	widgets := func(a Actor) []any {
		t.Helper()
		d, _, err := e.Dashboard(ctx, a, "perm_overview", 0)
		if err != nil {
			t.Fatal(err)
		}
		return roundTrip(d).(map[string]any)["widgets"].([]any)
	}
	// no role: voucher widget denied, note widget computed
	w := widgets(permNobody)
	want := map[string]any{"code": "permission_denied", "message": "Kamu tidak punya izin untuk read voucher", "model": "voucher", "action": "read"}
	if got := w[0].(map[string]any)["error"]; !mapsEqual(got, want) {
		t.Fatalf("voucher widget error = %v, want %v", got, want)
	}
	if _, ok := w[0].(map[string]any)["value"]; ok {
		t.Fatalf("denied widget leaks its value")
	}
	if v := w[1].(map[string]any)["value"]; v != 1.0 {
		t.Fatalf("note count = %v", v)
	}
	// clerk A sees only own vouchers; the cache must not hand the owner's numbers to the clerk
	if v := widgets(permOwner)[0].(map[string]any)["value"]; v != 120000.0 {
		t.Fatalf("owner total = %v, want 120000", v)
	}
	if v := widgets(permClerkA)[0].(map[string]any)["value"]; v != 100000.0 {
		t.Fatalf("clerk A total = %v, want 100000", v)
	}
	if v := widgets(permClerkB)[0].(map[string]any)["value"]; v != 20000.0 {
		t.Fatalf("clerk B total = %v, want 20000", v)
	}
}

func mapsEqual(got any, want map[string]any) bool {
	g, ok := got.(map[string]any)
	if !ok || len(g) != len(want) {
		return false
	}
	for k, v := range want {
		if g[k] != v {
			return false
		}
	}
	return true
}

func TestWriteChecksPatchedRecord(t *testing.T) {
	e := permSetup(t)
	ctx := context.Background()
	_, err := e.Update(ctx, permClerkA, "voucher", "V1", map[string]any{"owner": "USR-003"})
	wantPermErr(t, err, "voucher", "write")
	v1, err := e.Get(ctx, permOwner, "voucher", "V1")
	if err != nil {
		t.Fatal(err)
	}
	if v1["owner"] != "USR-002" {
		t.Fatalf("V1 owner = %v after a denied patch, want USR-002", v1["owner"])
	}
	if _, err := e.Update(ctx, permClerkA, "voucher", "V1", map[string]any{"title": "Bensin sore"}); err != nil {
		t.Fatalf("clerk patches own title: %v", err)
	}
}

func TestFlowRunChecksSteps(t *testing.T) {
	e := permSetup(t)
	ctx := context.Background()
	count := func() int {
		t.Helper()
		_, total, err := e.List(ctx, permOwner, "voucher", ListOpts{})
		if err != nil {
			t.Fatal(err)
		}
		return total
	}
	_, err := e.RunFlow(ctx, permViewer, "make_voucher", nil)
	wantPermErr(t, err, "voucher", "create")
	if n := count(); n != 2 {
		t.Fatalf("vouchers after denied flow = %d, want 2", n)
	}
	// a person with no read grant is stopped at the find_all step
	_, err = e.RunFlow(ctx, permNobody, "make_voucher", nil)
	wantPermErr(t, err, "voucher", "read")
	if _, err := e.RunFlow(ctx, permManager, "make_voucher", nil); err != nil {
		t.Fatalf("manager runs flow: %v", err)
	}
	if _, err := e.RunFlow(ctx, Actor{Org: permOrg, ID: "flow:make_voucher", System: true}, "make_voucher", nil); err != nil {
		t.Fatalf("system runs flow: %v", err)
	}
	// clerk A: find_all sees only readable rows (V1), the created voucher is clerk A's own
	res, err := e.RunFlow(ctx, permClerkA, "make_voucher", nil)
	if err != nil {
		t.Fatalf("clerk A runs flow: %v", err)
	}
	made, err := e.Get(ctx, permOwner, "voucher", res.Created[0][len("voucher/"):])
	if err != nil {
		t.Fatal(err)
	}
	if made["owner"] != "USR-002" || made["amount"] != int64(1) {
		t.Fatalf("clerk flow voucher = owner %v amount %v (%T), want USR-002 and 1", made["owner"], made["amount"], made["amount"])
	}
	if n := count(); n != 5 {
		t.Fatalf("vouchers = %d, want 5", n)
	}
}

// recordingQueue keeps the jobs a flow puts on the queue.
type recordingQueue struct{ jobs []map[string]any }

func (q *recordingQueue) Enqueue(_ context.Context, _ pg.Querier, kind, org string, args map[string]any, _ EnqueueOpts) (int64, error) {
	q.jobs = append(q.jobs, map[string]any{"kind": kind, "org": org, "args": args})
	return int64(len(q.jobs)), nil
}

func TestFlowResumeKeepsActor(t *testing.T) {
	e := permSetup(t)
	q := &recordingQueue{}
	e.Queue = q
	ctx := context.Background()
	resume := func(starter Actor) error {
		t.Helper()
		res, err := e.RunFlow(ctx, starter, "wait_voucher", nil)
		if err != nil {
			t.Fatalf("start as %v: %v", starter.Roles, err)
		}
		if res.WaitingJob == 0 || len(res.Created) != 0 {
			t.Fatalf("flow did not wait: %+v", res)
		}
		j := q.jobs[len(q.jobs)-1]
		args := roundTrip(j["args"]).(map[string]any) // the queue stores JSON
		a := JobActor(j["org"].(string), args["actor"])
		if a.System != starter.System || a.UserName != starter.UserName || !equalStrings(a.Roles, starter.Roles) || a.Org != starter.Org {
			t.Fatalf("resumed actor = %+v, want %+v", a, starter)
		}
		_, err = e.RunFlowFrom(ctx, a, "wait_voucher", meta.AsMap(args["event"]), int(args["resume"].(float64)), meta.AsMap(args["vars"]))
		return err
	}
	wantPermErr(t, resume(permViewer), "voucher", "create")
	if err := resume(permSystem); err != nil {
		t.Fatalf("system resume: %v", err)
	}
	if err := resume(permManager); err != nil {
		t.Fatalf("manager resume: %v", err)
	}
	// jobs without an actor (schedules) resume as System
	if a := JobActor(permOrg, nil); !a.System {
		t.Fatalf("actor-less job = %+v, want System", a)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestListSearchHidesUnreadableLinkTitles: q matches a linked record's display title only when the actor may read
// that record, so searching cannot reveal names of customers the caller cannot see (model-level or row-level).
func TestListSearchHidesUnreadableLinkTitles(t *testing.T) {
	e, owner := setup(t)
	ctx := context.Background()
	set := func(model string, grants ...any) {
		e.Reg().Models[model].Raw["permissions"] = grants
		e.permCache.Delete(e.Reg().Models[model])
	}
	grant := func(role string, extra map[string]any) map[string]any {
		g := map[string]any{"role": role, "read": true}
		for k, v := range extra {
			g[k] = v
		}
		return g
	}
	all, total, err := e.List(ctx, owner, "sales_invoice", ListOpts{Limit: 1000})
	if err != nil || total == 0 {
		t.Fatalf("invoices: %d %v", total, err)
	}
	cust, err := store.Get(ctx, e.DB, e.Reg(), owner.Org, e.Reg().Models["customer"], str(all[0]["customer"]))
	if err != nil {
		t.Fatal(err)
	}
	title := e.Title(ctx, owner, "customer", cust, 0)
	if title == "" || title == str(cust["name"]) {
		t.Fatalf("customer title %q should differ from its key", title)
	}
	q := strings.ToLower(title)
	rep := Actor{Org: owner.Org, ID: "rep@example.test", UserName: "USR-900", Roles: []string{"rep"}}

	set("sales_invoice", grant("rep", nil), grant("owner", nil))
	set("customer", grant("owner", nil)) // rep has no grant on customer
	rows, n, err := e.List(ctx, rep, "sales_invoice", ListOpts{Q: q, Limit: 1000})
	if err != nil || n != 0 || len(rows) != 0 {
		t.Fatalf("rep without customer read: q=%q found %d invoices (%v), want 0", q, n, err)
	}
	if _, n, err = e.List(ctx, owner, "sales_invoice", ListOpts{Q: q, Limit: 1000}); err != nil || n == 0 {
		t.Fatalf("owner: q=%q found %d invoices (%v), want some", q, n, err)
	}

	// Row-level: rep may read only customers whose name is not this one.
	set("customer", grant("rep", map[string]any{"if": "name != '" + str(cust["name"]) + "'"}), grant("owner", nil))
	if _, n, err = e.List(ctx, rep, "sales_invoice", ListOpts{Q: q, Limit: 1000}); err != nil || n != 0 {
		t.Fatalf("rep with row scope excluding the customer: found %d invoices (%v), want 0", n, err)
	}
	// And when the row is readable the title matches again.
	set("customer", grant("rep", nil), grant("owner", nil))
	if _, n, err = e.List(ctx, rep, "sales_invoice", ListOpts{Q: q, Limit: 1000}); err != nil || n == 0 {
		t.Fatalf("rep with customer read: found %d invoices (%v), want some", n, err)
	}
}
