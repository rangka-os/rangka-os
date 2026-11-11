package engine

import (
	"context"
	"encoding/json"
	"testing"
)

// memoOutletFiles: a submittable model whose submit waits for a sales manager and whose submit rule reads the
// person's outlets (user.outlets, contract 0010 §5), like a void that only the outlet's own staff may ask for.
var memoOutletFiles = map[string]string{
	"crm/models/memo_outlet.rk": memoHeader + `name: memo_outlet
label: {id: Memo outlet}
is_submittable: true
naming: MO-{#####}
fields:
  - {name: note, type: data, label: {id: Catatan}}
rules:
  - name: outlet_sendiri
    on: submit
    check: "'OUT-1' in user.outlets"
    message: {id: Hanya staf outlet OUT-1}
approvals:
  - {name: ajukan, on: submit, approvers: [sales_manager]}
`,
}

// An approved verb runs as the requester with the outlets they had when they asked, so rules, flows and row
// scope that read user.outlets see the same person (card t_64f5579a: a void approved by head office was
// refused as not the cashier's own).
func TestApprovedVerbKeepsRequesterOutlets(t *testing.T) {
	e, _ := approvalSetupFiles(t, memoOutletFiles, "")
	ctx := context.Background()
	staff := rep
	staff.Outlets = []string{"OUT-1"}
	res, err := e.Create(ctx, staff, "memo_outlet", map[string]any{"note": "void"})
	if err != nil {
		t.Fatal(err)
	}
	held, err := e.Submit(ctx, staff, "memo_outlet", str(res.Record["name"]), false)
	if err != nil || !held.Held {
		t.Fatalf("submit: %+v %v", held, err)
	}
	dec, err := e.Approve(ctx, mgr, held.Approvals[0].ID)
	if err != nil {
		t.Fatalf("approve ran the verb without the requester's outlets: %v", err)
	}
	if toInt(dec.Record["docstatus"]) != 1 {
		t.Fatalf("approved record = %v, want submitted", dec.Record)
	}
}

// A queued flow job runs as the person who started it, outlets included.
func TestJobActorKeepsOutlets(t *testing.T) {
	a := Actor{Org: "o", ID: "kasir@x", UserName: "USR-009", Roles: []string{"cashier"}, Outlets: []string{"OUT-1", "OUT-2"}}
	b, err := json.Marshal(a.JobArgs()) // job args are stored as JSON
	if err != nil {
		t.Fatal(err)
	}
	var args map[string]any
	if err := json.Unmarshal(b, &args); err != nil {
		t.Fatal(err)
	}
	got := JobActor("o", args)
	if len(got.Outlets) != 2 || got.Outlets[0] != "OUT-1" || got.Outlets[1] != "OUT-2" {
		t.Fatalf("JobActor outlets = %v, want [OUT-1 OUT-2]", got.Outlets)
	}
}
