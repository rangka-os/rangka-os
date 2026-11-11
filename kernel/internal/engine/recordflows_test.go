package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// dealFlag is a flow on a record event: when the updated deal is at stage qualified it writes its notes.
func dealFlag(on string) map[string]string {
	return map[string]string{"crm/flows/deal_flag.rk": `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/flow.json
name: deal_flag
label: {id: Tandai peluang}
on: ` + on + `
steps:
  - {find: deal, where: "name == event.name", as: record}
  - if: "record.stage == 'qualified'"
    update: record
    set: {notes: "'menang'"}
`}
}

func TestRecordEventFlowRuns(t *testing.T) {
	e, a := approvalSetupFiles(t, dealFlag("deal.updated"), "")
	ctx := context.Background()
	res, err := e.Create(ctx, a, "deal", map[string]any{"deal_name": "Uji", "customer": "CUST-0012", "value": 1000000, "expected_close": "2026-12-01"})
	if err != nil {
		t.Fatal(err)
	}
	name := str(res.Record["name"])
	if str(res.Record["notes"]) != "" {
		t.Fatalf("created must not fire an updated flow: %v", res.Record["notes"])
	}
	res, err = e.Update(ctx, a, "deal", name, map[string]any{"contact_person": "Budi"})
	if err != nil || str(res.Record["notes"]) != "" {
		t.Fatalf("other update: %v %v", res, err)
	}
	res, err = e.Update(ctx, a, "deal", name, map[string]any{"stage": "qualified"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := e.Get(ctx, a, "deal", name)
	if err != nil || str(got["notes"]) != "menang" {
		t.Fatalf("flow did not write notes: %v %v", got, err)
	}
}

// A flow's own writes do not start flows again (the update step here would loop forever otherwise).
func TestRecordEventFlowNoReentry(t *testing.T) {
	e, a := approvalSetupFiles(t, dealFlag("deal.updated"), "")
	ctx := context.Background()
	res, err := e.Create(ctx, a, "deal", map[string]any{"deal_name": "Uji", "customer": "CUST-0012", "value": 1, "stage": "qualified", "expected_close": "2026-12-01"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Update(ctx, a, "deal", str(res.Record["name"]), map[string]any{"value": 2}); err != nil {
		t.Fatal(err)
	}
}

// A record flow's update shows in the result of the write that started it (the record is read again).
func TestRecordEventFlowResultIsCurrent(t *testing.T) {
	e, a := approvalSetupFiles(t, dealFlag("deal.updated"), "")
	ctx := context.Background()
	res, err := e.Create(ctx, a, "deal", map[string]any{"deal_name": "Uji", "customer": "CUST-0012", "value": 1, "expected_close": "2026-12-01"})
	if err != nil {
		t.Fatal(err)
	}
	res, err = e.Update(ctx, a, "deal", str(res.Record["name"]), map[string]any{"stage": "qualified"})
	if err != nil || str(res.Record["notes"]) != "menang" {
		t.Fatalf("update result must show the flow's write: %v %v", res, err)
	}
	created, err := approvalSetupFilesCreate(t, "deal.created")
	if err != nil || str(created["notes"]) != "menang" {
		t.Fatalf("create result must show the flow's write: %v %v", created, err)
	}
}

func approvalSetupFilesCreate(t *testing.T, on string) (map[string]any, error) {
	e, a := approvalSetupFiles(t, dealFlag(on), "")
	res, err := e.Create(context.Background(), a, "deal", map[string]any{"deal_name": "Uji", "customer": "CUST-0012", "value": 1, "stage": "qualified", "expected_close": "2026-12-01"})
	if err != nil {
		return nil, err
	}
	return res.Record, nil
}

// The event carries the record's fields at top level (flows written for 0.4 read event.<field>), plus model,
// name, op, record and before.
func TestRecordEventFlowPayload(t *testing.T) {
	p := setupPilot(t)
	m := p.masters()
	p.create("stock_move", map[string]any{"outlet": m.outA, "item": m.beras, "qty": -3, "balance_qty": -3, "negative": true})
	got, err := store.ListAudit(p.ctx, p.e.DB, store.AuditFilter{Orgs: []string{p.a.Org}, Kind: "notify."})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Target != "flow/stock_negative_alert" || !strings.Contains(got[0].Summary, m.beras) || !strings.Contains(got[0].Summary, "-3") {
		t.Fatalf("stock_negative_alert did not run with top-level fields: %+v", got)
	}
	p.create("stock_move", map[string]any{"outlet": m.outA, "item": m.beras, "qty": 1, "balance_qty": 1})
	if got, _ := store.ListAudit(p.ctx, p.e.DB, store.AuditFilter{Orgs: []string{p.a.Org}, Kind: "notify."}); len(got) != 1 {
		t.Fatalf("flow if: must skip a move that is not negative: %+v", got)
	}
}

// wonOnce is what Ubah writes for "when stage becomes qualified, set notes": the transition guard makes the
// flow fire once, so a later edit of notes sticks.
func wonOnce(on string) map[string]string {
	return map[string]string{"crm/flows/deal_won.rk": `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/flow.json
name: deal_won
on: ` + on + `
steps:
  - {find: deal, where: "name == event.name", as: record}
  - if: "record.stage == 'qualified' && (event.before == null || event.before.stage != 'qualified')"
    update: record
    set: {notes: "'menang'"}
`}
}

func TestRecordEventFlowTransitionFiresOnce(t *testing.T) {
	e, a := approvalSetupFiles(t, wonOnce("deal.updated"), "")
	ctx := context.Background()
	res, err := e.Create(ctx, a, "deal", map[string]any{"deal_name": "Uji", "customer": "CUST-0012", "value": 1, "expected_close": "2026-12-01"})
	if err != nil {
		t.Fatal(err)
	}
	name := str(res.Record["name"])
	if _, err := e.Update(ctx, a, "deal", name, map[string]any{"stage": "qualified"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.Get(ctx, a, "deal", name); str(got["notes"]) != "menang" {
		t.Fatalf("transition did not fire: %v", got["notes"])
	}
	if _, err := e.Update(ctx, a, "deal", name, map[string]any{"notes": "sudah diubah"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.Get(ctx, a, "deal", name); str(got["notes"]) != "sudah diubah" {
		t.Fatalf("second edit must stick, flow re-fired: %v", got["notes"])
	}
	// before is null on created: the guard still evaluates (and fires)
	e2, a2 := approvalSetupFiles(t, wonOnce("deal.created"), "")
	res, err = e2.Create(ctx, a2, "deal", map[string]any{"deal_name": "Uji", "customer": "CUST-0012", "value": 1, "stage": "qualified", "expected_close": "2026-12-01"})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := e2.Get(ctx, a2, "deal", str(res.Record["name"])); str(got["notes"]) != "menang" {
		t.Fatalf("created with before null must fire: %v", got["notes"])
	}
}

// A failing record flow aborts the write that started it: the error comes back and nothing is stored.
func TestRecordEventFlowFailureRollsBack(t *testing.T) {
	e, a := approvalSetupFiles(t, map[string]string{"crm/flows/deal_bad.rk": `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/flow.json
name: deal_bad
on: deal.created
steps:
  - {create: deal, set: {notes: "'tanpa nama'"}}
`}, "")
	ctx := context.Background()
	before, _, err := e.List(ctx, a, "deal", ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Create(ctx, a, "deal", map[string]any{"deal_name": "Gagal", "customer": "CUST-0012", "value": 1, "expected_close": "2026-12-01"}); err == nil || !strings.Contains(err.Error(), "deal_bad") {
		t.Fatalf("want the flow's error, got %v", err)
	}
	after, _, err := e.List(ctx, a, "deal", ListOpts{})
	if err != nil || len(after) != len(before) {
		t.Fatalf("the write must roll back: %d → %d (%v)", len(before), len(after), err)
	}
}

// Flows of an app that is not installed do not run on record events.
func TestRecordEventFlowUninstalledAppSkipped(t *testing.T) {
	e, a := approvalSetupFiles(t, dealFlag("deal.updated"), "")
	e.Reg().Flows["deal_flag"].App = "tidak_terpasang"
	ctx := context.Background()
	res, err := e.Create(ctx, a, "deal", map[string]any{"deal_name": "Uji", "customer": "CUST-0012", "value": 1, "expected_close": "2026-12-01"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Update(ctx, a, "deal", str(res.Record["name"]), map[string]any{"stage": "qualified"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.Get(ctx, a, "deal", str(res.Record["name"])); str(got["notes"]) != "" {
		t.Fatalf("flow of an uninstalled app ran: %v", got["notes"])
	}
}

// <model>.submitted fires on submit, with before = the draft as stored.
func TestRecordEventFlowSubmitted(t *testing.T) {
	e, a := approvalSetupFiles(t, map[string]string{"crm/flows/invoice_sent.rk": `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/flow.json
name: invoice_sent
on: sales_invoice.submitted
if: "event.before.docstatus == 0 && event.docstatus == 1"
steps:
  - notify: owner
    channel: in_app
    template: 'Faktur {event.name} diajukan ({event.op})'
`}, "")
	ctx := context.Background()
	name := newInvoice(t, e, a)
	res, err := e.Submit(ctx, a, "sales_invoice", name, true)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.ListAudit(ctx, e.DB, store.AuditFilter{Orgs: []string{a.Org}, Kind: "notify."})
	if err != nil {
		t.Fatal(err)
	}
	want := "Faktur " + str(res.Record["name"]) + " diajukan (submitted)"
	if len(got) != 1 || got[0].Summary != want {
		t.Fatalf("want %q, got %+v", want, got)
	}
}

// A flow on the record's own updated event that writes the record keeps the person's edit: the edit is stored
// before flows run and the flow's update loads the stored record (card t_cb89c0a4, payroll audit 7 Nov).
func TestRecordEventFlowKeepsUserEdit(t *testing.T) {
	e, a := approvalSetupFiles(t, dealFlag("deal.updated"), "")
	ctx := context.Background()
	res, err := e.Create(ctx, a, "deal", map[string]any{"deal_name": "Uji", "customer": "CUST-0012", "value": 1, "expected_close": "2026-12-01"})
	if err != nil {
		t.Fatal(err)
	}
	name := str(res.Record["name"])
	res, err = e.Update(ctx, a, "deal", name, map[string]any{"stage": "qualified", "contact_person": "Budi", "value": 7})
	if err != nil {
		t.Fatal(err)
	}
	got, err := e.Get(ctx, a, "deal", name)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []map[string]any{res.Record, got} {
		if str(r["notes"]) != "menang" || str(r["contact_person"]) != "Budi" || str(r["stage"]) != "qualified" || num(r["value"]) != 7 {
			t.Fatalf("record after a self-writing flow = notes %v contact %v stage %v value %v; want the edit kept",
				r["notes"], r["contact_person"], r["stage"], r["value"])
		}
	}
}
