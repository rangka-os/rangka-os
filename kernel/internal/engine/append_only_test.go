package engine

import (
	"context"
	"errors"
	"testing"
)

// deal_log is an append_only ledger written by a flow on deal.updated (contract 0026).
var appendOnlyFiles = map[string]string{
	"crm/models/deal_log.rk": `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/model.json
name: deal_log
label: {id: Catatan peluang}
naming: DLOG-{#####}
append_only: true
fields:
  - {name: deal, type: link, to: deal, label: {id: Peluang}}
  - {name: value, type: currency, label: {id: Nilai}}
permissions:
  - {role: owner, read: true, create: true, write: true, delete: true}
`,
	"crm/flows/deal_log_on_update.rk": `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/flow.json
name: deal_log_on_update
label: {id: Catat peluang}
on: deal.updated
steps:
  - {find: deal, where: "name == event.name", as: d}
  - create: deal_log
    set: {deal: d.name, value: d.value}
    as: row
  - {find: deal_log, where: "name == row.name", as: again}
  - if: "again != null"
    update: again
    set: {value: "0"}
`,
}

func TestAppendOnlyLedger(t *testing.T) {
	files := map[string]string{"crm/models/deal_log.rk": appendOnlyFiles["crm/models/deal_log.rk"]}
	e, owner := approvalSetupFiles(t, files, "")
	ctx := context.Background()

	if _, err := e.Create(ctx, owner, "deal_log", map[string]any{"value": 5}); !isCode(err, "append_only") {
		t.Fatalf("owner create through the API = %v, want append_only", err)
	}
	if res, err := e.CreateMany(ctx, owner, "deal_log", []map[string]any{{"value": 5}}, ManyOpts{}); err == nil && (len(res) == 0 || res[0].Err == nil) {
		t.Fatal("an import must not write ledger rows")
	}
	can := e.Allowed(owner, "deal_log")
	if !can["read"] || can["create"] || can["write"] || can["delete"] || can["import"] {
		t.Fatalf("meta can for a ledger = %v", can)
	}

	// a hook-style write (not a client) creates the row; nobody may change or delete it afterwards
	var name string
	if err := e.run(ctx, owner, func(o *op) error {
		r, err := o.create("deal_log", map[string]any{"value": 7}, CreateOpts{authorize: true})
		if err == nil {
			name = str(r.Record["name"])
		}
		return err
	}); err != nil {
		t.Fatalf("flow/hook create of a ledger row: %v", err)
	}
	if _, err := e.Update(ctx, owner, "deal_log", name, map[string]any{"value": 0}); !isCode(err, "append_only") {
		t.Fatalf("owner update = %v, want append_only", err)
	}
	if err := e.Delete(ctx, owner, "deal_log", name); !isCode(err, "append_only") {
		t.Fatalf("owner delete = %v, want append_only", err)
	}
	got, err := e.Get(ctx, owner, "deal_log", name)
	if err != nil || str(got["value"]) == "0" {
		t.Fatalf("row changed: %v %v", got, err)
	}
}

// A flow may create ledger rows but not edit them: the update step fails and the whole write rolls back.
func TestAppendOnlyFlowCreatesButNeverEdits(t *testing.T) {
	e, owner := approvalSetupFiles(t, appendOnlyFiles, "")
	ctx := context.Background()
	res, err := e.Create(ctx, owner, "deal", map[string]any{"deal_name": "Uji", "customer": "CUST-0012", "value": 1000, "expected_close": "2026-12-01"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Update(ctx, owner, "deal", str(res.Record["name"]), map[string]any{"value": 2000}); !isCode(err, "append_only") {
		t.Fatalf("a flow editing a ledger row = %v, want append_only", err)
	}
	rows, _, err := e.List(ctx, owner, "deal_log", ListOpts{Limit: 10})
	if err != nil || len(rows) != 0 {
		t.Fatalf("the failed write must leave no row: %d %v", len(rows), err)
	}
}

// Without the editing step the flow writes its row as the person who updated the deal.
func TestAppendOnlyFlowWrites(t *testing.T) {
	flow := appendOnlyFiles["crm/flows/deal_log_on_update.rk"]
	flow = flow[:len(flow)-len(`  - {find: deal_log, where: "name == row.name", as: again}
  - if: "again != null"
    update: again
    set: {value: "0"}
`)]
	e, owner := approvalSetupFiles(t, map[string]string{"crm/models/deal_log.rk": appendOnlyFiles["crm/models/deal_log.rk"], "crm/flows/deal_log_on_update.rk": flow}, "")
	ctx := context.Background()
	res, err := e.Create(ctx, owner, "deal", map[string]any{"deal_name": "Uji", "customer": "CUST-0012", "value": 1000, "expected_close": "2026-12-01"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Update(ctx, owner, "deal", str(res.Record["name"]), map[string]any{"value": 2000}); err != nil {
		t.Fatal(err)
	}
	rows, _, err := e.List(ctx, owner, "deal_log", ListOpts{Limit: 10})
	if err != nil || len(rows) != 1 {
		t.Fatalf("flow must write one ledger row: %d %v", len(rows), err)
	}
}

func isCode(err error, code string) bool {
	var se *StatusError
	return errors.As(err, &se) && se.Code == code
}
