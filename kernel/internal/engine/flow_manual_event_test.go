package engine

import (
	"context"
	"strings"
	"testing"
)

// A flow on <model>.<event> started by hand by a member who is not the owner gets the event rebuilt from the
// stored record, never the body (audit kernel-gelombang1 N3): a forged value does not reach the flow.
func TestManualModelEventFlowRebuildsEvent(t *testing.T) {
	flow := `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/flow.json
name: deal_echo
label: {id: Gema peluang}
on: deal.updated
steps:
  - notify: owner
    channel: email
    template: {id: 'nilai {event.value} nama {event.name} karangan {has(event.forged)}'}
`
	e, owner := approvalSetupFiles(t, map[string]string{"crm/flows/deal_echo.rk": flow}, "")
	ctx := context.Background()
	res, err := e.Create(ctx, owner, "deal", map[string]any{"deal_name": "Uji", "customer": "CUST-0012", "value": 1000, "expected_close": "2026-12-01"})
	if err != nil {
		t.Fatal(err)
	}
	name := str(res.Record["name"])
	mgr := Actor{Org: owner.Org, ID: "mgr@example.test", UserName: "USR-MGR", Roles: []string{"sales_manager"}, Human: true}
	out, err := e.RunFlow(ctx, mgr, "deal_echo", map[string]any{"name": name, "value": 999999999, "forged": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Notifications) != 1 {
		t.Fatalf("notifications: %+v", out.Notifications)
	}
	txt := out.Notifications[0].Text
	if strings.Contains(txt, "999999999") || strings.Contains(txt, "karangan true") || !strings.Contains(txt, "nama "+name) {
		t.Fatalf("forged event reached the flow: %q", txt)
	}
	if _, err := e.RunFlow(ctx, mgr, "deal_echo", map[string]any{"value": 5}); !isCode(err, "event_name") {
		t.Fatalf("run without event.name = %v, want event_name", err)
	}
	if _, err := e.RunFlow(ctx, mgr, "deal_echo", map[string]any{"name": "DEAL-TIDAK-ADA"}); err == nil {
		t.Fatal("run for a missing record must fail")
	}
	// a member who may not read the record cannot start the flow for it either
	rep := Actor{Org: owner.Org, ID: "rep@example.test", UserName: "USR-REP", Roles: []string{"sales_rep"}, Human: true}
	if _, err := e.RunFlow(ctx, rep, "deal_echo", map[string]any{"name": name}); err == nil {
		t.Fatal("sales_rep read another's deal through a flow run")
	}
	// the owner may still run it with a written event (tests, demos)
	out, err = e.RunFlow(ctx, owner, "deal_echo", map[string]any{"name": name, "value": 7})
	if err != nil || !strings.Contains(out.Notifications[0].Text, "nilai 7") {
		t.Fatalf("owner run: %v %+v", err, out)
	}
}
