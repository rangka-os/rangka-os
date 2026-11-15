package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/engine"
)

// Contract 0033 §3 and §4: a company connector runs only after a test of its current content (apply refuses flows
// that use it, calls and polls are refused or skipped), and a money-touching connector writes only after the owner
// confirmed its writes.

// gatedConnector is x_test with a poll trigger.
const gatedConnector = companyConnector + `triggers:
  - name: new_items
    via: poll
    every: 5m
    action: list_items
    for_each: "items[]"
    emit: {item_id: item.id}
`

// moneyConnector is x_test flagged money: true.
var moneyConnector = strings.Replace(companyConnector, "version: 0.1.0\n", "version: 0.1.0\nmoney: true\n", 1)

// syncFlow makes x_test money-touching without the flag: it calls x_test and writes the deal (which holds currency
// fields) it was started for.
const syncFlow = `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/flow.json
name: x_test_sync
label: {id: Kirim peluang ke sistem lain}
on: deal.updated
steps:
  - {call: x_test.create_item, with: {name: doc.deal_name}}
  - {update: doc, set: {notes: "'terkirim'"}}
`

func flowOp(path, text string) map[string]any {
	return map[string]any{"op": "create", "path": path, "content": text}
}

// gatedCall queues one call with five attempts, so a retry would show.
func gatedCall(t *testing.T, e *env, action string, input map[string]any) int64 {
	t.Helper()
	id, err := e.s.Queue.Enqueue(context.Background(), nil, "call", "kopi-senja", map[string]any{"connector": "x_test", "action": action, "input": input},
		engine.EnqueueOpts{MaxAttempts: 5})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestApplyRefusesUntestedConnector(t *testing.T) {
	e, _ := companyEnvWith(t, true, gatedConnector, nil)
	onTrigger := `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/flow.json
name: x_omilo_sync
label: {id: Barang baru}
on: x_test.new_items
steps:
  - {create: activity, set: {subject: "'barang baru'", activity_type: "'note'"}}
`
	callStep := `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/flow.json
name: x_omilo_pull
label: {id: Ambil barang}
on: deal.updated
steps:
  - if: doc.stage == 'won'
    then:
      - {call: x_test.list_items}
`
	a := proposeApproved(e, "Alur dari pemicu", flowOp("flows/x_omilo_sync.rk", onTrigger))
	b := proposeApproved(e, "Alur dengan panggilan", flowOp("flows/x_omilo_pull.rk", callStep))
	for _, id := range []string{a, b} {
		r := e.do("POST", "/api/v1/changesets/"+id+"/apply", nil)
		if r.Status != 409 || errCode(r) != "connector_untested" {
			t.Fatalf("apply %s while untested: %d %s", id, r.Status, r.Body)
		}
		cs, _ := get(r.JSON, "error", "connectors").([]any)
		if len(cs) != 1 || cs[0] != "x_test" {
			t.Fatalf("detail: %s", r.Body)
		}
	}
	if _, err := os.Stat(filepath.Join(e.site, "flows", "x_omilo_sync.rk")); err == nil {
		t.Fatal("the refused apply wrote the flow")
	}
	// tested: both apply
	e.must(200, "POST", testPath, map[string]any{"action": "list_items"})
	for _, id := range []string{a, b} {
		if ap := e.must(200, "POST", "/api/v1/changesets/"+id+"/apply", nil).JSON; ap["status"] != "applied" {
			t.Fatalf("apply %s after the test: %v", id, ap)
		}
	}
	// a change set that only adds a company connector applies: it starts needs_auth and runs nothing yet
	omilo := strings.Replace(companyConnector, "name: x_test", "name: x_omilo", 1)
	c := proposeApproved(e, "Penghubung Omilo", flowOp("connectors/x_omilo.rk", omilo))
	if ap := e.must(200, "POST", "/api/v1/changesets/"+c+"/apply", nil).JSON; ap["status"] != "applied" {
		t.Fatalf("apply connector only: %v", ap)
	}
	if g := e.must(200, "GET", "/api/v1/connectors/x_omilo/test", nil); g.JSON["state"] != "needs_auth" {
		t.Fatalf("new connector state: %s", g.Body)
	}
}

func TestCallUntestedAfterEdit(t *testing.T) {
	e, v := companyEnv(t, true)
	e.must(200, "POST", testPath, map[string]any{"action": "list_items"})
	if st, errs := waitJob(t, e, gatedCall(t, e, "list_items", nil)); st != "completed" {
		t.Fatalf("call after the test: %s %s", st, errs)
	}
	sent := len(v.sent())
	// an edit of the file resets the test
	rel := filepath.Join("connectors", "x_test", "connector.rk")
	writeSite(e, rel, readSite(e, rel)+"\n# diubah\n")
	st, errs := waitJob(t, e, gatedCall(t, e, "list_items", nil))
	if st != "discarded" || !strings.Contains(errs, "connector_untested") || strings.Count(errs, `"attempt"`) != 1 {
		t.Fatalf("call after an edit: %s %s", st, errs)
	}
	if len(v.sent()) != sent {
		t.Fatal("an untested connector sent a request")
	}
	if !strings.Contains(dump(t, e, "select coalesce(data::text, '') from rk_audit where kind = 'connector.error'"), "connector_untested") {
		t.Fatal("connector.error audit without the code")
	}
}

// writesRefusedUntilConfirmed is the §4 send-time rule for a money-touching x_test.
func writesRefusedUntilConfirmed(t *testing.T, e *env, v *vendor) {
	t.Helper()
	e.must(200, "POST", testPath, map[string]any{"action": "list_items"})
	sent := len(v.sent())
	// reads go through
	if st, errs := waitJob(t, e, gatedCall(t, e, "list_items", nil)); st != "completed" {
		t.Fatalf("read before confirm: %s %s", st, errs)
	}
	st, errs := waitJob(t, e, gatedCall(t, e, "create_item", map[string]any{"name": "Kopi"}))
	if st != "discarded" || !strings.Contains(errs, "connector_writes_unconfirmed") || strings.Count(errs, `"attempt"`) != 1 {
		t.Fatalf("write before confirm: %s %s", st, errs)
	}
	for _, r := range v.sent()[sent:] {
		if r.Method != "GET" {
			t.Fatalf("a write was sent before the confirmation: %s %s", r.Method, r.URL)
		}
	}
	e.must(200, "POST", "/api/v1/connectors/x_test/confirm-writes", nil)
	if st, errs := waitJob(t, e, gatedCall(t, e, "create_item", map[string]any{"name": "Kopi"})); st != "completed" {
		t.Fatalf("write after confirm: %s %s", st, errs)
	}
	all := v.sent()
	if last := all[len(all)-1]; last.Method != "POST" {
		t.Fatalf("last request: %s", last.Method)
	}
}

func TestWriteRefusedUnconfirmed(t *testing.T) {
	e, v := companyEnvWith(t, true, moneyConnector, nil)
	writesRefusedUntilConfirmed(t, e, v)
}

func TestWriteRefusedUnconfirmedUnflagged(t *testing.T) {
	e, v := companyEnvWith(t, true, companyConnector, map[string]string{"flows/x_test_sync.rk": syncFlow})
	if !e.s.Engine.Reg().MoneyTouching("x_test") {
		t.Fatal("setup: the flow does not make x_test money-touching")
	}
	writesRefusedUntilConfirmed(t, e, v)
}

func TestWriteNonMoneyTested(t *testing.T) {
	e, v := companyEnv(t, true)
	e.must(200, "POST", testPath, map[string]any{"action": "list_items"})
	if st, errs := waitJob(t, e, gatedCall(t, e, "create_item", map[string]any{"name": "Kopi"})); st != "completed" {
		t.Fatalf("non-money write: %s %s", st, errs)
	}
	all := v.sent()
	if last := all[len(all)-1]; last.Method != "POST" {
		t.Fatalf("last request: %s", last.Method)
	}
}

func TestPollSkipsUntested(t *testing.T) {
	e, v := companyEnvWith(t, true, gatedConnector, nil)
	ctx := context.Background()
	res, err := e.s.Connectors.Poll(ctx, "kopi-senja", "x_test", "new_items")
	if err != nil || res.Skipped != "untested" || len(v.sent()) != 0 {
		t.Fatalf("poll while untested: %+v %v, %d requests", res, err, len(v.sent()))
	}
	e.must(200, "POST", testPath, map[string]any{"action": "list_items"})
	sent := len(v.sent())
	res, err = e.s.Connectors.Poll(ctx, "kopi-senja", "x_test", "new_items")
	if err != nil || res.Skipped != "" || len(v.sent()) != sent+1 {
		t.Fatalf("poll after the test: %+v %v, %d requests", res, err, len(v.sent())-sent)
	}
}

// The test run is audited as connector.test without the answer; its connector.call row carries no output either.
func TestConnectorTestCallAuditHasNoBody(t *testing.T) {
	e, _ := companyEnv(t, true)
	e.must(200, "POST", testPath, map[string]any{"action": "list_items"})
	a := dump(t, e, "select kind, summary, coalesce(data::text, '') from rk_audit where kind like 'connector.%'")
	if strings.Contains(a, "Barang") {
		t.Fatalf("the test answer reached the audit: %s", a)
	}
}
