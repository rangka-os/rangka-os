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

// Review fix 1: a company connector lacking any required key sends nothing, even an action that uses none of the
// missing keys; an edit that adds a required key does not let the new content run.
func TestCallNeedsEveryRequiredKey(t *testing.T) {
	e, v := companyEnvWith(t, true, gatedConnector, nil)
	e.must(200, "POST", testPath, map[string]any{"action": "list_items"})
	sent := len(v.sent())
	rel := filepath.Join("connectors", "x_test", "connector.rk")
	edited := strings.Replace(readSite(e, rel), "    - {name: access_token, label: {id: Access token}, secret: true}\n",
		"    - {name: access_token, label: {id: Access token}, secret: true}\n    - {name: signing_key, label: {id: Kunci tanda tangan}, secret: true}\n", 1)
	writeSite(e, rel, edited)
	if g := e.must(200, "GET", testPath, nil); g.JSON["state"] != "needs_auth" {
		t.Fatalf("setup: state %s", g.Body)
	}
	for _, act := range []string{"list_items", "create_item"} {
		st, errs := waitJob(t, e, gatedCall(t, e, act, map[string]any{"name": "Kopi"}))
		if st != "discarded" || !strings.Contains(errs, "connector_needs_auth") || strings.Count(errs, `"attempt"`) != 1 {
			t.Fatalf("%s with a key missing: %s %s", act, st, errs)
		}
	}
	if res, err := e.s.Connectors.Poll(context.Background(), "kopi-senja", "x_test", "new_items"); err != nil || res.Skipped != "needs_auth" {
		t.Fatalf("poll with a key missing: %+v %v", res, err)
	}
	// tested content whose second key is removed afterwards: refused too
	e.must(204, "PUT", "/api/v1/connectors/x_test/auth/signing_key", map[string]any{"value": "kunci-0033-zzzz"})
	e.must(200, "POST", testPath, map[string]any{"action": "list_items"})
	e.must(204, "DELETE", "/api/v1/connectors/x_test/auth/signing_key", nil)
	st, errs := waitJob(t, e, gatedCall(t, e, "list_items", nil))
	if st != "discarded" || !strings.Contains(errs, "connector_needs_auth") {
		t.Fatalf("tested content without a key: %s %s", st, errs)
	}
	if n := len(v.sent()) - sent; n != 1 { // only the second test went out
		t.Fatalf("%d requests sent, want 1 (the test)", n)
	}
}

const oauthCompany = `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/connector.json
name: x_test
label: {id: Uji OAuth perusahaan}
summary: {id: Penghubung perusahaan dengan OAuth2.}
version: 0.1.0
hosts: [api.example.com, auth.example.com]
experimental:
  source: {kind: owner_file}
auth:
  type: oauth2
  token_url: https://TOKEN_HOST/token
  fields:
    - {name: client_id, label: {id: Client ID}}
    - {name: client_secret, label: {id: Client secret}, secret: true}
    - {name: refresh_token, label: {id: Refresh token}, secret: true}
    - {name: access_token, label: {id: Access token}, secret: true, required: false}
    - {name: expires_at, label: {id: Expires at}, required: false}
actions:
  - name: list_items
    label: {id: Daftar barang}
    request:
      method: GET
      url: https://api.example.com/items
      headers: {Authorization: 'Bearer {auth.access_token}'}
`

func oauthCompanyEnv(t *testing.T, tokenHost string) (*env, *vendor) {
	t.Helper()
	e, v := companyEnvWith(t, false, strings.Replace(oauthCompany, "TOKEN_HOST", tokenHost, 1), nil)
	for k, val := range map[string]string{"client_id": "cid", "client_secret": "csecret-0033-zzzz", "refresh_token": "rtok-0033-zzzz",
		"access_token": "atok-0033-zzzz", "expires_at": "2000-01-01T00:00:00Z"} {
		e.must(204, "PUT", "/api/v1/connectors/x_test/auth/"+k, map[string]any{"value": val})
	}
	return e, v
}

// Review fix 2: an untested company connector does not refresh its OAuth2 token (the refresh posts the client secret
// to the file's token_url), and a refresh goes only to a declared host.
func TestOAuthRefreshWaitsForTest(t *testing.T) {
	e, v := oauthCompanyEnv(t, "auth.example.com")
	st, errs := waitJob(t, e, gatedCall(t, e, "list_items", nil))
	if st != "discarded" || !strings.Contains(errs, "connector_untested") || len(v.sent()) != 0 {
		t.Fatalf("untested oauth call: %s %s, %d requests", st, errs, len(v.sent()))
	}
}

func TestOAuthRefreshDeclaredHostOnly(t *testing.T) {
	e, v := oauthCompanyEnv(t, "collect.evil.example")
	r := e.must(200, "POST", testPath, map[string]any{"action": "list_items"})
	if r.JSON["ok"] != false || !strings.Contains(string(r.Body), "not in hosts") {
		t.Fatalf("test with a foreign token_url: %s", r.Body)
	}
	for _, q := range v.sent() {
		if q.URL.Hostname() == "collect.evil.example" {
			t.Fatal("the refresh went to an undeclared host")
		}
	}
	if len(v.sent()) != 0 {
		t.Fatalf("%d requests sent", len(v.sent()))
	}
}

// Review fix 3 and the fix wave (item 1): a change set that edits a company connector and adds a flow using it is
// reported at validate (connector_untested), not only at apply: the apply resets the test, so it could never apply.
func TestApplyRefusesEditedConnectorFlow(t *testing.T) {
	e, _ := companyEnvWith(t, true, gatedConnector, nil)
	e.must(200, "POST", testPath, map[string]any{"action": "list_items"})
	flow := `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/flow.json
name: x_omilo_pull
label: {id: Ambil barang}
on: deal.updated
steps:
  - {call: x_test.list_items}
`
	cs := e.must(201, "POST", "/api/v1/changesets", map[string]any{"title": "Ubah penghubung dan alur", "ops": []any{
		map[string]any{"op": "replace", "path": "connectors/x_test.rk", "content": gatedConnector + "# diubah\n"},
		flowOp("flows/x_omilo_pull.rk", flow)}}).JSON
	v := e.must(200, "POST", "/api/v1/changesets/"+cs["id"].(string)+"/validate", nil).JSON
	if v["status"] != "invalid" || get(v, "validation", "errors", 0, "code") != "connector_untested" ||
		get(v, "validation", "errors", 0, "file") != "flows/x_omilo_pull.rk" {
		t.Fatalf("validate edited connector + flow: %v", v)
	}
	// the edit alone applies
	only := proposeApproved(e, "Ubah penghubung", map[string]any{"op": "replace", "path": "connectors/x_test.rk", "content": gatedConnector + "# diubah\n"})
	if ap := e.must(200, "POST", "/api/v1/changesets/"+only+"/apply", nil).JSON; ap["status"] != "applied" {
		t.Fatalf("connector edit alone: %v", ap)
	}
}

// Fix wave item 1: the 0062 §8 flow, a new company connector and the flows that use it in one change set, is invalid
// at validate with a message that asks for two change sets; split, the connector applies and the flows wait for the
// owner's test.
func TestValidateRefusesNewConnectorWithFlows(t *testing.T) {
	e, _ := companyEnvWith(t, true, gatedConnector, nil)
	omilo := strings.Replace(gatedConnector, "name: x_test", "name: x_omilo", 1)
	flow := `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/flow.json
name: x_omilo_sync
label: {id: Barang baru}
on: x_omilo.new_items
steps:
  - {create: activity, set: {subject: "'barang baru'", activity_type: "'note'"}}
`
	cs := e.must(201, "POST", "/api/v1/changesets", map[string]any{"title": "Omilo sekaligus", "ops": []any{
		flowOp("connectors/x_omilo.rk", omilo), flowOp("flows/x_omilo_sync.rk", flow)}}).JSON
	id := cs["id"].(string)
	v := e.must(200, "POST", "/api/v1/changesets/"+id+"/validate", nil).JSON
	errs, _ := get(v, "validation", "errors").([]any)
	if v["status"] != "invalid" || len(errs) != 1 || get(v, "validation", "errors", 0, "code") != "connector_untested" {
		t.Fatalf("validate connector + flow: %v", v)
	}
	msg, _ := get(v, "validation", "errors", 0, "message").(string)
	if !strings.Contains(msg, "x_omilo") || !strings.Contains(msg, "dua change set") {
		t.Fatalf("message: %q", msg)
	}
	// preview refuses it too
	if r := e.do("POST", "/api/v1/changesets/"+id+"/preview", nil); r.Status != 409 {
		t.Fatalf("preview: %d %s", r.Status, r.Body)
	}
	// split: (1) the connector applies; (2) the flows validate and approve, and apply once the test passed
	c := proposeApproved(e, "Penghubung Omilo", flowOp("connectors/x_omilo.rk", omilo))
	e.must(200, "POST", "/api/v1/changesets/"+c+"/apply", nil)
	f := proposeApproved(e, "Alur Omilo", flowOp("flows/x_omilo_sync.rk", flow))
	// fix wave item 4: the refusal follows the state. No keys yet: connector_needs_auth, saying the keys are missing.
	r := e.do("POST", "/api/v1/changesets/"+f+"/apply", nil)
	if msg, _ := get(r.JSON, "error", "message").(string); r.Status != 409 || errCode(r) != "connector_needs_auth" || !strings.Contains(msg, "belum diisi") {
		t.Fatalf("flows before the keys: %d %s", r.Status, r.Body)
	}
	e.must(204, "PUT", "/api/v1/connectors/x_omilo/auth/access_token", map[string]any{"value": companyTestSecret})
	// keys set, never tested: "belum diuji", not "sejak terakhir diubah"
	r = e.do("POST", "/api/v1/changesets/"+f+"/apply", nil)
	if msg, _ := get(r.JSON, "error", "message").(string); r.Status != 409 || errCode(r) != "connector_untested" ||
		!strings.Contains(msg, "belum diuji") || strings.Contains(msg, "sejak terakhir diubah") {
		t.Fatalf("flows before the test: %d %s", r.Status, r.Body)
	}
	e.must(200, "POST", "/api/v1/connectors/x_omilo/test", map[string]any{"action": "list_items"})
	if ap := e.must(200, "POST", "/api/v1/changesets/"+f+"/apply", nil).JSON; ap["status"] != "applied" {
		t.Fatalf("flows after the test: %v", ap)
	}
}

// Fix wave item 2: the preview of a change set that drafts a company connector lists its unset keys under
// preview.requires, though no flow uses it yet (the first of the two change sets).
func TestPreviewRequiresDraftedConnectorKeys(t *testing.T) {
	e, _ := companyEnv(t, true)
	omilo := strings.Replace(companyConnector, "name: x_test", "name: x_omilo", 1)
	cs := e.must(201, "POST", "/api/v1/changesets", map[string]any{"title": "Penghubung Omilo", "ops": []any{flowOp("connectors/x_omilo.rk", omilo)}}).JSON
	id := cs["id"].(string)
	e.must(200, "POST", "/api/v1/changesets/"+id+"/validate", nil)
	pv := e.must(200, "POST", "/api/v1/changesets/"+id+"/preview", nil)
	reqs, _ := get(pv.JSON, "preview", "requires").([]any)
	if len(reqs) != 1 || get(pv.JSON, "preview", "requires", 0, "connector") != "x_omilo" ||
		get(pv.JSON, "preview", "requires", 0, "field") != "access_token" || get(pv.JSON, "preview", "requires", 0, "secret") != true {
		t.Fatalf("requires: %s", pv.Body)
	}
	// x_test is live and its key is set: not listed
	if strings.Contains(string(pv.Body), `"connector":"x_test"`) {
		t.Fatalf("a live connector is listed: %s", pv.Body)
	}
}

// hookConnector is x_test with an unsigned webhook trigger.
const hookConnector = companyConnector + `triggers:
  - name: changed
    via: webhook
    emit: {item_id: body.id}
`

const hookFlow = `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/flow.json
name: x_test_hook
label: {id: Barang berubah}
on: x_test.changed
steps:
  - {create: activity, set: {subject: "'barang berubah'", activity_type: "'note'"}}
`

// Fix wave item 6: a webhook for a company connector whose current content is not tested starts no flow: it is
// audited with connector_untested and answered 401, as the needs_auth refusals are. Tested, it dispatches; an edit
// stops it again.
func TestWebhookRefusesUntested(t *testing.T) {
	e, _ := companyEnvWith(t, true, hookConnector, map[string]string{"flows/x_test_hook.rk": hookFlow})
	const path = "/hooks/kopi-senja/x_test/changed"
	events := func() string {
		return dump(t, e, "select count(*) from rk_audit where kind = 'connector.event'")
	}
	before := events()
	if r := hook(t, e, path, []byte(`{"id": 1}`), ""); r.StatusCode != 401 {
		t.Fatalf("untested hook: %d", r.StatusCode)
	}
	if events() != before {
		t.Fatal("an untested connector recorded an event")
	}
	if !strings.Contains(dump(t, e, "select coalesce(data::text, '') from rk_audit where kind = 'connector.error'"), "connector_untested") {
		t.Fatal("no connector.error audit with connector_untested")
	}
	e.must(200, "POST", testPath, map[string]any{"action": "list_items"})
	if r := hook(t, e, path, []byte(`{"id": 2}`), ""); r.StatusCode != 200 {
		t.Fatalf("tested hook: %d", r.StatusCode)
	}
	if events() == before {
		t.Fatal("the tested hook recorded no event")
	}
	after := events()
	rel := filepath.Join("connectors", "x_test", "connector.rk")
	writeSite(e, rel, readSite(e, rel)+"\n# diubah\n")
	if r := hook(t, e, path, []byte(`{"id": 3}`), ""); r.StatusCode != 401 || events() != after {
		t.Fatalf("hook after an edit: %d", r.StatusCode)
	}
}

// Fix wave item 7: the ruang coba simulates calls, so a test there would pass on fake data. POST test is refused
// (409 sandbox_no_test) and nothing is saved.
func TestConnectorTestRefusedInSandbox(t *testing.T) {
	e, v := companyEnv(t, true)
	e.must(201, "POST", "/api/v1/sandbox", map[string]any{"fixtures": demoFixtures(t)})
	e.must(200, "POST", "/auth/org", map[string]any{"org": sb})
	r := e.do("POST", testPath, map[string]any{"action": "list_items"})
	if msg, _ := get(r.JSON, "error", "message").(string); r.Status != 409 || errCode(r) != "sandbox_no_test" || !strings.Contains(msg, "ruang coba") {
		t.Fatalf("test in the sandbox: %d %s", r.Status, r.Body)
	}
	if n := dump(t, e, "select count(*) from rk_connector_checks where org_id = '"+sb+"'"); strings.TrimSpace(n) != "0" {
		t.Fatalf("a check row was saved: %s", n)
	}
	if len(v.sent()) != 0 {
		t.Fatal("the sandbox test sent a request")
	}
}
