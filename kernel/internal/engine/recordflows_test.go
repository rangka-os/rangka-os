package engine

import (
	"context"
	"testing"
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
