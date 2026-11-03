package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/schema"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

const managerPolicy = `approvals:
  - name: manager
    on: submit
    when: "grand_total > 1000000"
    approvers: [sales_manager]
    label: {id: Persetujuan manajer}
`

const ownerBigPolicy = `  - name: owner_big
    on: submit
    when: "grand_total > 3000000"
    approvers: [owner]
`

var (
	mgr = Actor{Org: "kopi-senja", ID: "mgr@x", UserName: "USR-002", Roles: []string{"sales_manager"}, Human: true}
	rep = Actor{Org: "kopi-senja", ID: "rep@x", UserName: "USR-002", Roles: []string{"sales_rep"}, Human: true}
)

// approvalSetup is setup with approvals: appended to sales_invoice (and lead, when given) and the submit rule
// kota_diblokir added to sales_invoice.
func approvalSetup(t *testing.T, policies string, leadPolicies ...string) (*Engine, Actor) {
	t.Helper()
	return approvalSetupFiles(t, nil, policies, leadPolicies...)
}

// approvalSetupFiles is approvalSetup that also writes files (path relative to apps/ → content).
func approvalSetupFiles(t *testing.T, files map[string]string, policies string, leadPolicies ...string) (*Engine, Actor) {
	t.Helper()
	apps := filepath.Join(t.TempDir(), "apps")
	if err := os.CopyFS(apps, os.DirFS(appsDir)); err != nil {
		t.Fatal(err)
	}
	for rel, text := range files {
		if err := os.WriteFile(filepath.Join(apps, rel), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	appendTo := func(rel, text string, rule bool) {
		p := filepath.Join(apps, rel)
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		s := string(b)
		if rule {
			s = strings.Replace(s, "\nrules:\n", "\nrules:\n  - name: kota_diblokir\n    on: submit\n    check: \"customer.city != 'Blokir'\"\n    message: {id: Kota pelanggan diblokir}\n", 1)
		}
		if !strings.HasSuffix(s, "\n") {
			s += "\n"
		}
		if err := os.WriteFile(p, []byte(s+text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	appendTo("sales/models/sales_invoice.rk", policies, true)
	if len(leadPolicies) > 0 {
		appendTo("crm/models/lead.rk", strings.Join(leadPolicies, ""), false)
	}
	url := testdb.New(t)
	ctx := context.Background()
	reg, rp := meta.Validate(apps, siteDir, nil, schema.Embedded())
	if rp.Errors() > 0 {
		t.Fatalf("metadata errors: %v", rp.All())
	}
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
	a := Actor{Org: "kopi-senja", ID: "ceo@saga.co.id", UserName: "USR-001", Roles: []string{"owner"}, Human: true}
	fx, err := LoadFixtures(FixturesDir(appsDir))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Seed(ctx, a, fx); err != nil {
		t.Fatal(err)
	}
	return e, a
}

// newInvoice creates the 3.163.500 invoice (BRG-0044 x 10 for CUST-0058) as a.
func newInvoice(t *testing.T, e *Engine, a Actor) string {
	t.Helper()
	res, err := e.Create(context.Background(), a, "sales_invoice", map[string]any{
		"customer": "CUST-0058", "outlet": "OUT-01",
		"items": []any{map[string]any{"item": "BRG-0044", "qty": 10}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return res.Record["name"].(string)
}

func pendingFor(t *testing.T, e *Engine, a Actor, model, name string) []store.Approval {
	t.Helper()
	l, _, err := e.ListApprovals(context.Background(), a, store.ApprovalFilter{State: "pending", Model: model, Name: name})
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func approval(t *testing.T, e *Engine, id string) *store.Approval {
	t.Helper()
	ap, err := store.GetApproval(context.Background(), e.DB, "kopi-senja", id, false)
	if err != nil {
		t.Fatal(err)
	}
	return ap
}

func wantStatus(t *testing.T, err error, status int, code string) {
	t.Helper()
	var se *StatusError
	if !errors.As(err, &se) || se.Status != status || se.Code != code {
		t.Fatalf("err = %#v, want %d %s", err, status, code)
	}
}

func heldInvoice(t *testing.T, e *Engine) (string, *Result) {
	t.Helper()
	name := newInvoice(t, e, rep)
	res, err := e.Submit(context.Background(), rep, "sales_invoice", name, true)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Held {
		t.Fatalf("not held: %v", res.Record["name"])
	}
	return name, res
}

func TestSubmitHeldForApproval(t *testing.T) {
	e, _ := approvalSetup(t, managerPolicy)
	ctx := context.Background()
	name, res := heldInvoice(t, e)
	if len(res.Approvals) != 1 || res.Approvals[0].State != "pending" || res.Approvals[0].Policy != "manager" {
		t.Fatalf("approvals = %+v", res.Approvals)
	}
	if res.Record["docstatus"] != int64(0) || !strings.HasPrefix(str(res.Record["name"]), "DRAFT-") {
		t.Fatalf("record = %v %v", res.Record["name"], res.Record["docstatus"])
	}
	got, err := e.Get(ctx, rep, "sales_invoice", name)
	if err != nil || got["docstatus"] != int64(0) {
		t.Fatalf("stored = %v %v", got, err)
	}
	res2, err := e.Submit(ctx, rep, "sales_invoice", name, true)
	if err != nil || !res2.Held || len(res2.Approvals) != 1 || res2.Approvals[0].ID != res.Approvals[0].ID {
		t.Fatalf("second submit: %+v %v", res2, err)
	}
	if p := pendingFor(t, e, rep, "sales_invoice", name); len(p) != 1 {
		t.Fatalf("pending = %d", len(p))
	}
}

func TestApproveRunsVerbAsRequester(t *testing.T) {
	e, owner := approvalSetup(t, managerPolicy)
	ctx := context.Background()
	_, res := heldInvoice(t, e)
	d, err := e.Approve(ctx, mgr, res.Approvals[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if d.Record["docstatus"] != int64(1) || strings.HasPrefix(str(d.Record["name"]), "DRAFT-") {
		t.Fatalf("record = %v %v", d.Record["name"], d.Record["docstatus"])
	}
	if d.Approval.State != "approved" || d.Approval.DecidedBy != "mgr@x" {
		t.Fatalf("approval = %+v", d.Approval)
	}
	// the request follows the record's rename from DRAFT-… to its number
	if d.Approval.Name != d.Record["name"] {
		t.Fatalf("decision approval name = %v, record %v", d.Approval.Name, d.Record["name"])
	}
	if ap := approval(t, e, res.Approvals[0].ID); ap.State != "approved" || ap.DecidedBy != "mgr@x" || ap.DecidedAt == nil || ap.Name != d.Record["name"] {
		t.Fatalf("stored approval = %+v", ap)
	}
	au, err := store.ListAudit(ctx, e.DB, store.AuditFilter{Orgs: []string{owner.Org}, Kind: "record.submitted"})
	if err != nil || len(au) != 1 || au[0].Actor != "rep@x" {
		t.Fatalf("audit = %+v %v", au, err)
	}
	if au, _ := store.ListAudit(ctx, e.DB, store.AuditFilter{Orgs: []string{owner.Org}, Kind: "approval.approved"}); len(au) != 1 || au[0].Actor != "mgr@x" {
		t.Fatalf("approval audit = %+v", au)
	}
}

func TestNonApproverForbidden(t *testing.T) {
	e, _ := approvalSetup(t, managerPolicy)
	ctx := context.Background()
	_, res := heldInvoice(t, e)
	id := res.Approvals[0].ID
	_, err := e.Approve(ctx, rep, id)
	wantStatus(t, err, 403, "permission_denied")
	if !strings.Contains(err.Error(), "Persetujuan manajer") {
		t.Fatalf("message = %v", err)
	}
	bot := Actor{Org: "kopi-senja", ID: "bot@x", Roles: []string{"sales_manager"}}
	_, err = e.Approve(ctx, bot, id)
	wantStatus(t, err, 403, "human_required")
	_, err = e.Reject(ctx, bot, id, "x")
	wantStatus(t, err, 403, "human_required")
	if CanDecide(rep, &res.Approvals[0]) || !CanDecide(mgr, &res.Approvals[0]) || CanDecide(bot, &res.Approvals[0]) {
		t.Fatal("CanDecide")
	}
	_, err = e.Approve(ctx, mgr, "apr_nope")
	wantStatus(t, err, 404, "not_found")
	if approval(t, e, id).State != "pending" {
		t.Fatal("decided by a forbidden caller")
	}
}

func TestWhenFalseSubmitsDirectly(t *testing.T) {
	e, _ := approvalSetup(t, managerPolicy)
	ctx := context.Background()
	c, err := e.Create(ctx, rep, "sales_invoice", map[string]any{"customer": "CUST-0058", "outlet": "OUT-01",
		"items": []any{map[string]any{"item": "BRG-0101", "qty": 1}}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.Submit(ctx, rep, "sales_invoice", c.Record["name"].(string), true)
	if err != nil || res.Held || res.Record["docstatus"] != int64(1) {
		t.Fatalf("submit: %+v %v", res, err)
	}
	if l, n, _ := e.ListApprovals(ctx, rep, store.ApprovalFilter{}); n != 0 || len(l) != 0 {
		t.Fatalf("approvals = %d", n)
	}
}

func TestSelfApprovalByApproverRole(t *testing.T) {
	e, _ := approvalSetup(t, managerPolicy)
	ctx := context.Background()
	name := newInvoice(t, e, mgr)
	res, err := e.Submit(ctx, mgr, "sales_invoice", name, true)
	if err != nil || res.Held || res.Record["docstatus"] != int64(1) {
		t.Fatalf("submit: %+v %v", res, err)
	}
	if l, n, _ := e.ListApprovals(ctx, mgr, store.ApprovalFilter{Model: "sales_invoice", Name: name}); n != 0 {
		t.Fatalf("approvals still under the draft name: %+v", l)
	}
	l, n, _ := e.ListApprovals(ctx, mgr, store.ApprovalFilter{Model: "sales_invoice", Name: str(res.Record["name"])})
	if n != 1 || l[0].State != "approved" || l[0].DecidedBy != "mgr@x" || l[0].RequestedBy != "mgr@x" {
		t.Fatalf("approvals = %+v", l)
	}
}

func TestApprovalVoidedByEdit(t *testing.T) {
	e, _ := approvalSetup(t, managerPolicy)
	ctx := context.Background()
	name, res := heldInvoice(t, e)
	first := res.Approvals[0]
	if _, err := e.Update(ctx, rep, "sales_invoice", name, map[string]any{"items": []any{map[string]any{"item": "BRG-0044", "qty": 20}}}); err != nil {
		t.Fatal(err)
	}
	if ap := approval(t, e, first.ID); ap.State != "void" {
		t.Fatalf("state after edit = %s", ap.State)
	}
	if au, _ := store.ListAudit(ctx, e.DB, store.AuditFilter{Orgs: []string{"kopi-senja"}, Kind: "approval.void"}); len(au) != 1 {
		t.Fatalf("void audit = %+v", au)
	}
	_, err := e.Approve(ctx, mgr, first.ID)
	wantStatus(t, err, 409, "approval_void")
	res, err = e.Submit(ctx, rep, "sales_invoice", name, true)
	if err != nil || !res.Held || len(res.Approvals) != 1 {
		t.Fatalf("resubmit: %+v %v", res, err)
	}
	second := res.Approvals[0]
	if second.ID == first.ID || second.Snapshot == first.Snapshot || second.State != "pending" {
		t.Fatalf("second = %+v first = %+v", second, first)
	}
	d, err := e.Approve(ctx, mgr, second.ID)
	if err != nil || d.Record["docstatus"] != int64(1) {
		t.Fatalf("approve: %+v %v", d, err)
	}
}

func TestApprovalVoidedByDelete(t *testing.T) {
	e, _ := approvalSetup(t, managerPolicy)
	ctx := context.Background()
	name, res := heldInvoice(t, e)
	if err := e.Delete(ctx, rep, "sales_invoice", name); err != nil {
		t.Fatal(err)
	}
	if ap := approval(t, e, res.Approvals[0].ID); ap.State != "void" {
		t.Fatalf("state after delete = %s", ap.State)
	}
}

func TestApprovalSnapshotMismatch(t *testing.T) {
	e, _ := approvalSetup(t, managerPolicy)
	ctx := context.Background()
	name, res := heldInvoice(t, e)
	if _, err := e.DB.Exec(ctx, "update "+pg.QuoteIdent(store.TableName("sales_invoice"))+" set notes = 'x' where name = $1", name); err != nil {
		t.Fatal(err)
	}
	_, err := e.Approve(ctx, mgr, res.Approvals[0].ID)
	wantStatus(t, err, 409, "approval_void")
	if ap := approval(t, e, res.Approvals[0].ID); ap.State != "void" {
		t.Fatalf("state = %s", ap.State)
	}
	got, _ := e.Get(ctx, rep, "sales_invoice", name)
	if got["docstatus"] != int64(0) {
		t.Fatalf("docstatus = %v", got["docstatus"])
	}
}

func TestApproveRollsBackWhenVerbFails(t *testing.T) {
	e, owner := approvalSetup(t, managerPolicy)
	ctx := context.Background()
	name, res := heldInvoice(t, e)
	if _, err := e.Update(ctx, owner, "customer", "CUST-0058", map[string]any{"city": "Blokir"}); err != nil {
		t.Fatal(err)
	}
	if ap := approval(t, e, res.Approvals[0].ID); ap.State != "pending" {
		t.Fatalf("customer edit voided the invoice approval: %s", ap.State)
	}
	_, err := e.Approve(ctx, mgr, res.Approvals[0].ID)
	var ve *ValidationError
	if !errors.As(err, &ve) || len(ve.Errors) == 0 || ve.Errors[0].Rule != "kota_diblokir" {
		t.Fatalf("approve: %v", err)
	}
	if ap := approval(t, e, res.Approvals[0].ID); ap.State != "pending" {
		t.Fatalf("state = %s", ap.State)
	}
	got, _ := e.Get(ctx, rep, "sales_invoice", name)
	if got["docstatus"] != int64(0) {
		t.Fatalf("docstatus = %v", got["docstatus"])
	}
}

func TestTwoPoliciesBothNeeded(t *testing.T) {
	e, owner := approvalSetup(t, managerPolicy+ownerBigPolicy)
	ctx := context.Background()
	name, res := heldInvoice(t, e)
	if len(res.Approvals) != 2 {
		t.Fatalf("approvals = %+v", res.Approvals)
	}
	byPolicy := map[string]string{}
	for _, ap := range res.Approvals {
		byPolicy[ap.Policy] = ap.ID
	}
	d, err := e.Approve(ctx, mgr, byPolicy["manager"])
	if err != nil || d.Record != nil {
		t.Fatalf("first approve: %+v %v", d, err)
	}
	if got, _ := e.Get(ctx, rep, "sales_invoice", name); got["docstatus"] != int64(0) {
		t.Fatalf("submitted after one of two")
	}
	_, err = e.Approve(ctx, mgr, byPolicy["owner_big"])
	wantStatus(t, err, 403, "permission_denied")
	d, err = e.Approve(ctx, owner, byPolicy["owner_big"])
	if err != nil || d.Record["docstatus"] != int64(1) {
		t.Fatalf("second approve: %+v %v", d, err)
	}
	_, err = e.Approve(ctx, owner, byPolicy["manager"])
	wantStatus(t, err, 409, "not_pending")
}

func TestRejectVoidsSiblings(t *testing.T) {
	e, owner := approvalSetup(t, managerPolicy+ownerBigPolicy)
	ctx := context.Background()
	name, res := heldInvoice(t, e)
	byPolicy := map[string]string{}
	for _, ap := range res.Approvals {
		byPolicy[ap.Policy] = ap.ID
	}
	_, err := e.Reject(ctx, owner, byPolicy["owner_big"], " ")
	wantStatus(t, err, 400, "bad_request")
	d, err := e.Reject(ctx, owner, byPolicy["owner_big"], "Harga salah")
	if err != nil || d.Approval.State != "rejected" || d.Approval.Reason != "Harga salah" {
		t.Fatalf("reject: %+v %v", d, err)
	}
	if ap := approval(t, e, byPolicy["owner_big"]); ap.State != "rejected" || ap.Reason != "Harga salah" || ap.DecidedBy != owner.ID {
		t.Fatalf("rejected = %+v", ap)
	}
	if ap := approval(t, e, byPolicy["manager"]); ap.State != "void" {
		t.Fatalf("sibling = %s", ap.State)
	}
	if got, _ := e.Get(ctx, rep, "sales_invoice", name); got["docstatus"] != int64(0) {
		t.Fatal("record left draft")
	}
	_, err = e.Reject(ctx, owner, byPolicy["owner_big"], "lagi")
	wantStatus(t, err, 409, "not_pending")
}

func TestCancelAndActionHeld(t *testing.T) {
	e, owner := approvalSetup(t, "approvals:\n  - {name: batal, on: cancel, approvers: [owner]}\n",
		"approvals:\n  - {name: konversi, on: convert, approvers: [sales_manager]}\n")
	ctx := context.Background()
	name := newInvoice(t, e, rep)
	sub, err := e.Submit(ctx, rep, "sales_invoice", name, true)
	if err != nil || sub.Held {
		t.Fatalf("submit: %+v %v", sub, err)
	}
	inv := sub.Record["name"].(string)
	res, err := e.Cancel(ctx, rep, "sales_invoice", inv)
	if err != nil || !res.Held || len(res.Approvals) != 1 || res.Approvals[0].Verb != "cancel" {
		t.Fatalf("cancel: %+v %v", res, err)
	}
	if got, _ := e.Get(ctx, rep, "sales_invoice", inv); got["docstatus"] != int64(1) {
		t.Fatalf("docstatus = %v", got["docstatus"])
	}
	d, err := e.Approve(ctx, owner, res.Approvals[0].ID)
	if err != nil || d.Record["docstatus"] != int64(2) {
		t.Fatalf("approve cancel: %+v %v", d, err)
	}

	customers, _ := store.Count(ctx, e.DB, "kopi-senja", "customer")
	out, err := e.RunAction(ctx, rep, "lead", "LEAD-00031", "convert", nil, ActionOpts{})
	if err != nil || !out.Held || len(out.Approvals) != 1 || out.Record["name"] != "LEAD-00031" {
		t.Fatalf("action: %+v %v", out, err)
	}
	lead, _ := e.Get(ctx, rep, "lead", "LEAD-00031")
	if lead["status"] == "qualified" {
		t.Fatal("lead converted while held")
	}
	if n, _ := store.Count(ctx, e.DB, "kopi-senja", "customer"); n != customers {
		t.Fatalf("customers %d → %d", customers, n)
	}
	d, err = e.Approve(ctx, mgr, out.Approvals[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	open, _ := d.Action["open"].(map[string]any)
	if open["model"] != "deal" || !strings.HasPrefix(str(open["name"]), "DEAL-") {
		t.Fatalf("action result = %v", d.Action)
	}
	if lead, _ = e.Get(ctx, rep, "lead", "LEAD-00031"); lead["status"] != "qualified" {
		t.Fatalf("lead = %v", lead["status"])
	}
}

const convertPolicy = "approvals:\n  - {name: konversi, on: convert, approvers: [sales_manager]}\n"

// a real status field is part of the snapshot (only the derived status of submittable models is not a field).
func TestSnapshotIncludesRealStatus(t *testing.T) {
	e, _ := approvalSetup(t, "", convertPolicy)
	ctx := context.Background()
	m := e.Reg().Model("lead")
	lead, err := store.Get(ctx, e.DB, e.Reg(), "kopi-senja", m, "LEAD-00031")
	if err != nil {
		t.Fatal(err)
	}
	changed := cloneRecord(lead)
	changed["status"] = "x"
	if Snapshot(m, lead) == Snapshot(m, changed) {
		t.Fatal("status change kept the snapshot")
	}
	out, err := e.RunAction(ctx, rep, "lead", "LEAD-00031", "convert", nil, ActionOpts{})
	if err != nil || !out.Held {
		t.Fatalf("held: %+v %v", out, err)
	}
	if _, err := e.DB.Exec(ctx, "update "+pg.QuoteIdent(store.TableName("lead"))+" set status = 'x' where name = $1", "LEAD-00031"); err != nil {
		t.Fatal(err)
	}
	_, err = e.Approve(ctx, mgr, out.Approvals[0].ID)
	wantStatus(t, err, 409, "approval_void")
	if ap := approval(t, e, out.Approvals[0].ID); ap.State != "void" {
		t.Fatalf("state = %s", ap.State)
	}
}

// noopConvert replaces crm.convert_lead with a hook that leaves the lead unchanged.
func noopConvert(e *Engine) {
	e.Hooks["crm.convert_lead"] = func(h *HookCtx) (map[string]any, error) { return map[string]any{"ok": true}, nil }
}

func TestApprovalSingleUse(t *testing.T) {
	e, _ := approvalSetup(t, "", convertPolicy)
	noopConvert(e)
	ctx := context.Background()
	out, err := e.RunAction(ctx, rep, "lead", "LEAD-00031", "convert", nil, ActionOpts{})
	if err != nil || !out.Held {
		t.Fatalf("held: %+v %v", out, err)
	}
	first := out.Approvals[0].ID
	d, err := e.Approve(ctx, mgr, first)
	if err != nil || d.Action["ok"] != true {
		t.Fatalf("approve: %+v %v", d, err)
	}
	out, err = e.RunAction(ctx, rep, "lead", "LEAD-00031", "convert", nil, ActionOpts{})
	if err != nil || !out.Held || len(out.Approvals) != 1 || out.Approvals[0].ID == first || out.Approvals[0].State != "pending" {
		t.Fatalf("second run reused the approval: %+v %v", out, err)
	}
}

func TestApprovalBoundToArgs(t *testing.T) {
	e, _ := approvalSetup(t, "", convertPolicy)
	noopConvert(e)
	ctx := context.Background()
	a, err := e.RunAction(ctx, rep, "lead", "LEAD-00031", "convert", map[string]any{"note": "A"}, ActionOpts{})
	if err != nil || !a.Held {
		t.Fatalf("A: %+v %v", a, err)
	}
	b, err := e.RunAction(ctx, rep, "lead", "LEAD-00031", "convert", map[string]any{"note": "B"}, ActionOpts{})
	if err != nil || !b.Held || b.Approvals[0].ID == a.Approvals[0].ID {
		t.Fatalf("B reused A's request: %+v %v", b, err)
	}
	a2, err := e.RunAction(ctx, rep, "lead", "LEAD-00031", "convert", map[string]any{"note": "A"}, ActionOpts{})
	if err != nil || !a2.Held || a2.Approvals[0].ID != a.Approvals[0].ID {
		t.Fatalf("A again: %+v %v", a2, err)
	}
	// nil args and empty args are the same call
	n1, err := e.RunAction(ctx, rep, "lead", "LEAD-00031", "convert", nil, ActionOpts{})
	if err != nil || !n1.Held {
		t.Fatalf("nil args: %+v %v", n1, err)
	}
	n2, err := e.RunAction(ctx, rep, "lead", "LEAD-00031", "convert", map[string]any{}, ActionOpts{})
	if err != nil || !n2.Held || n2.Approvals[0].ID != n1.Approvals[0].ID {
		t.Fatalf("empty args made another request: %+v %v", n2, err)
	}
	if p := pendingFor(t, e, rep, "lead", "LEAD-00031"); len(p) != 3 {
		t.Fatalf("pending = %d", len(p))
	}
	// approving B runs B (with B's args); the requests of the other calls (A, no args) become void
	var got map[string]any
	e.Hooks["crm.convert_lead"] = func(h *HookCtx) (map[string]any, error) { got = h.Args; return map[string]any{}, nil }
	if _, err := e.Approve(ctx, mgr, b.Approvals[0].ID); err != nil || got["note"] != "B" {
		t.Fatalf("approve B: %v args %v", err, got)
	}
	for _, id := range []string{a.Approvals[0].ID, n1.Approvals[0].ID} {
		if ap := approval(t, e, id); ap.State != "void" {
			t.Fatalf("%s = %s", id, ap.State)
		}
	}
	if au, _ := store.ListAudit(ctx, e.DB, store.AuditFilter{Orgs: []string{"kopi-senja"}, Kind: "approval.void"}); len(au) != 2 {
		t.Fatalf("void audit = %d", len(au))
	}
}

// an approval of the current snapshot runs the verb; pending requests of an older snapshot are voided.
func TestApproveVoidsOlderSnapshot(t *testing.T) {
	e, _ := approvalSetup(t, managerPolicy)
	ctx := context.Background()
	name, res := heldInvoice(t, e)
	old := res.Approvals[0].ID
	if _, err := e.DB.Exec(ctx, "update "+pg.QuoteIdent(store.TableName("sales_invoice"))+" set notes = 'x' where name = $1", name); err != nil {
		t.Fatal(err)
	}
	res, err := e.Submit(ctx, rep, "sales_invoice", name, true)
	if err != nil || !res.Held || res.Approvals[0].ID == old {
		t.Fatalf("resubmit: %+v %v", res, err)
	}
	d, err := e.Approve(ctx, mgr, res.Approvals[0].ID)
	if err != nil || d.Record["docstatus"] != int64(1) {
		t.Fatalf("approve: %+v %v", d, err)
	}
	if ap := approval(t, e, old); ap.State != "void" {
		t.Fatalf("old = %s", ap.State)
	}
}

// when is evaluated on the stored record: the stored grand_total, not a value prepare would recompute.
func TestSubmitWhenUsesStoredRecord(t *testing.T) {
	e, _ := approvalSetup(t, managerPolicy)
	ctx := context.Background()
	name := newInvoice(t, e, rep)
	// stored grand_total below the threshold, though the items still compute to 3.163.500
	if _, err := e.DB.Exec(ctx, "update "+pg.QuoteIdent(store.TableName("sales_invoice"))+" set grand_total = 1 where name = $1", name); err != nil {
		t.Fatal(err)
	}
	res, err := e.Submit(ctx, rep, "sales_invoice", name, true)
	if err != nil || res.Held {
		t.Fatalf("submit: %+v %v", res, err)
	}
}

// approveFlow is a flow (no on:) that waits for a sales manager before it creates an activity for the lead.
func approveFlow(when string) map[string]string {
	return map[string]string{"crm/flows/approve_demo.rk": `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/flow.json
name: approve_demo
label: {id: Demo persetujuan}
steps:
  - {find: lead, as: lead}
  - approve: {approvers: [sales_manager], when: "` + when + `", label: {id: Setujui tindak lanjut}}
    as: ok
  - {create: activity, set: {subject: "'Disetujui'", lead: lead.name, done: "false"}}
`}
}

func TestFlowApproveStep(t *testing.T) {
	e, _ := approvalSetupFiles(t, approveFlow("lead != null"), "")
	ctx := context.Background()
	out, err := e.RunFlow(ctx, rep, "approve_demo", nil)
	if err != nil || out.WaitingApproval == "" || len(out.Created) != 0 {
		t.Fatalf("run: %+v %v", out, err)
	}
	if last := out.Steps[len(out.Steps)-1]; last != "2: approve ("+out.WaitingApproval+")" {
		t.Fatalf("steps = %v", out.Steps)
	}
	ap := approval(t, e, out.WaitingApproval)
	if ap.State != "pending" || ap.Model != "" || ap.Name != "" || ap.Verb != "flow:approve_demo" || ap.Policy != "Setujui tindak lanjut" ||
		toInt(ap.Args["resume"]) != 2 || ap.Args["as"] != "ok" || ap.RequestedBy != rep.ID {
		t.Fatalf("approval = %+v", ap)
	}
	if _, ok := meta.AsMap(ap.Args["vars"])["event"]; ok {
		t.Fatal("vars carry the event")
	}
	if _, err := e.Approve(ctx, rep, ap.ID); err == nil {
		t.Fatal("rep approved")
	}
	d, err := e.Approve(ctx, mgr, ap.ID)
	if err != nil || d.Flow == nil || len(d.Flow.Created) != 1 || !strings.HasPrefix(d.Flow.Created[0], "activity/ACT-") {
		t.Fatalf("approve: %+v %v", d, err)
	}
	act, err := e.Get(ctx, rep, "activity", strings.TrimPrefix(d.Flow.Created[0], "activity/"))
	if err != nil || act["subject"] != "Disetujui" || act["lead"] == nil || act["created_by"] != rep.ID {
		t.Fatalf("activity = %v %v", act, err)
	}
	if ap := approval(t, e, ap.ID); ap.State != "approved" || ap.DecidedBy != mgr.ID {
		t.Fatalf("after approve = %+v", ap)
	}
	_, err = e.Approve(ctx, mgr, ap.ID)
	wantStatus(t, err, 409, "not_pending")

	// rejected: no activity, audit flow.rejected
	acts, _ := store.Count(ctx, e.DB, "kopi-senja", "activity")
	out, err = e.RunFlow(ctx, rep, "approve_demo", nil)
	if err != nil || out.WaitingApproval == "" {
		t.Fatalf("second run: %+v %v", out, err)
	}
	if _, err := e.Reject(ctx, mgr, out.WaitingApproval, "Belum perlu"); err != nil {
		t.Fatal(err)
	}
	if n, _ := store.Count(ctx, e.DB, "kopi-senja", "activity"); n != acts {
		t.Fatalf("activities %d → %d", acts, n)
	}
	if au, _ := store.ListAudit(ctx, e.DB, store.AuditFilter{Orgs: []string{"kopi-senja"}, Kind: "flow.rejected"}); len(au) != 1 || au[0].Actor != mgr.ID {
		t.Fatalf("flow.rejected audits = %+v", au)
	}

	// when false: the step is skipped and the flow runs on
	e2, _ := approvalSetupFiles(t, approveFlow("false"), "")
	out, err = e2.RunFlow(ctx, rep, "approve_demo", nil)
	if err != nil || out.WaitingApproval != "" || len(out.Created) != 1 || out.Steps[1] != "2: skipped" {
		t.Fatalf("when false: %+v %v", out, err)
	}
	if l, n, err := e2.ListApprovals(ctx, rep, store.ApprovalFilter{}); err != nil || n != 0 {
		t.Fatalf("approvals = %v %d %v", l, n, err)
	}
}

// two approvers deciding the two policies of one invoice at once: the second to commit runs the verb.
func TestConcurrentApprovesRunVerb(t *testing.T) {
	e, owner := approvalSetup(t, managerPolicy+ownerBigPolicy)
	ctx := context.Background()
	for round := 0; round < 5; round++ {
		_, res := heldInvoice(t, e)
		byPolicy := map[string]string{}
		for _, ap := range res.Approvals {
			byPolicy[ap.Policy] = ap.ID
		}
		var wg sync.WaitGroup
		ds := make([]*Decision, 2)
		errs := make([]error, 2)
		for i, x := range []struct {
			a  Actor
			id string
		}{{mgr, byPolicy["manager"]}, {owner, byPolicy["owner_big"]}} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ds[i], errs[i] = e.Approve(ctx, x.a, x.id)
			}()
		}
		wg.Wait()
		ran := 0
		for i := range ds {
			if errs[i] != nil {
				t.Fatalf("round %d approve %d: %v", round, i, errs[i])
			}
			if ds[i].Record != nil {
				if ds[i].Record["docstatus"] != int64(1) {
					t.Fatalf("round %d: docstatus %v", round, ds[i].Record["docstatus"])
				}
				ran++
			}
		}
		if ran != 1 {
			t.Fatalf("round %d: verb ran %d times, want 1", round, ran)
		}
	}
}

// a double-clicked submit makes one pending request per policy.
func TestConcurrentSubmitsOneRequestPerPolicy(t *testing.T) {
	e, _ := approvalSetup(t, managerPolicy+ownerBigPolicy)
	ctx := context.Background()
	for round := 0; round < 5; round++ {
		name := newInvoice(t, e, rep)
		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i := range errs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, errs[i] = e.Submit(ctx, rep, "sales_invoice", name, true)
			}()
		}
		wg.Wait()
		for _, err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		p := pendingFor(t, e, rep, "sales_invoice", name)
		if len(p) != 2 || p[0].Policy == p[1].Policy {
			t.Fatalf("round %d: pending = %+v", round, p)
		}
	}
}

// directorPolicy matches convert only while the lead's owner (a user record) has role Direktur: data outside
// the lead's snapshot.
const directorPolicy = "  - {name: direktur, on: convert, when: \"owner.role == 'Direktur'\", approvers: [sales_manager]}\n"

func makeDirector(t *testing.T, e *Engine, a Actor) {
	t.Helper()
	if _, err := e.Update(context.Background(), a, "user", "USR-002", map[string]any{"role": "Direktur"}); err != nil {
		t.Fatal(err)
	}
}

// a request made by a token runs as a non-person: a policy that starts to match at approve time is not
// self-approved by the requester's role.
func TestNonHumanRequesterNeverSelfApproves(t *testing.T) {
	e, owner := approvalSetup(t, "", "approvals:\n  - {name: konversi, on: convert, approvers: [owner]}\n"+directorPolicy)
	noopConvert(e)
	ctx := context.Background()
	bot := Actor{Org: "kopi-senja", ID: "bot@x", Roles: []string{"sales_manager"}}
	out, err := e.RunAction(ctx, bot, "lead", "LEAD-00031", "convert", nil, ActionOpts{})
	if err != nil || !out.Held || len(out.Approvals) != 1 || out.Approvals[0].Policy != "konversi" {
		t.Fatalf("held: %+v %v", out, err)
	}
	if h, ok := out.Approvals[0].Requester["human"].(bool); !ok || h {
		t.Fatalf("requester = %v", out.Approvals[0].Requester)
	}
	makeDirector(t, e, owner)
	d, err := e.Approve(ctx, owner, out.Approvals[0].ID)
	if err != nil || d.Action != nil {
		t.Fatalf("approve ran the action: %+v %v", d, err)
	}
	p := pendingFor(t, e, owner, "lead", "LEAD-00031")
	if len(p) != 1 || p[0].Policy != "direktur" {
		t.Fatalf("pending = %+v", p)
	}
}

// an approval that let the verb run is used up: when another policy starts to match on the unchanged record,
// the next run asks the first policy again as well.
func TestUsedApprovalNotReused(t *testing.T) {
	e, owner := approvalSetup(t, "", convertPolicy+directorPolicy)
	noopConvert(e)
	ctx := context.Background()
	out, err := e.RunAction(ctx, rep, "lead", "LEAD-00031", "convert", nil, ActionOpts{})
	if err != nil || !out.Held || len(out.Approvals) != 1 {
		t.Fatalf("held: %+v %v", out, err)
	}
	first := out.Approvals[0].ID
	if d, err := e.Approve(ctx, mgr, first); err != nil || d.Action["ok"] != true {
		t.Fatalf("approve: %+v %v", d, err)
	}
	if ap := approval(t, e, first); ap.UsedAt == nil {
		t.Fatal("used_at not set after the action ran")
	}
	makeDirector(t, e, owner)
	out, err = e.RunAction(ctx, rep, "lead", "LEAD-00031", "convert", nil, ActionOpts{})
	if err != nil || !out.Held || len(out.Approvals) != 2 {
		t.Fatalf("second run: %+v %v", out, err)
	}
	for _, ap := range out.Approvals {
		if ap.ID == first || ap.State != "pending" {
			t.Fatalf("second run reused %s: %+v", first, out.Approvals)
		}
	}
}

// a self-approved run marks its approval used at once.
func TestSelfApprovedRunMarksUsed(t *testing.T) {
	e, _ := approvalSetup(t, managerPolicy)
	ctx := context.Background()
	name := newInvoice(t, e, mgr)
	res, err := e.Submit(ctx, mgr, "sales_invoice", name, true)
	if err != nil || res.Held {
		t.Fatalf("submit: %+v %v", res, err)
	}
	l, _, _ := e.ListApprovals(ctx, mgr, store.ApprovalFilter{Model: "sales_invoice", Name: str(res.Record["name"])})
	if len(l) != 1 || l[0].UsedAt == nil {
		t.Fatalf("approvals = %+v", l)
	}
}
