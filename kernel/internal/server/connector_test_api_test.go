package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Contract 0033 §3 and §4 through HTTP: test a company connector with one GET call, then confirm its writes.

const companyTestSecret = "company-test-token-0033-zzzz"

const companyConnector = `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/connector.json
name: x_test
label: {id: Uji perusahaan}
summary: {id: Penghubung perusahaan untuk uji endpoint.}
version: 0.1.0
hosts: [api.example.com]
experimental:
  source: {kind: owner_file}
auth:
  type: token
  fields:
    - {name: access_token, label: {id: Access token}, secret: true}
actions:
  - name: list_items
    label: {id: Daftar barang}
    request:
      method: GET
      url: https://api.example.com/items
      headers: {Authorization: 'Bearer {auth.access_token}'}
    output:
      items: response.items
  - name: create_item
    label: {id: Buat barang}
    request:
      method: POST
      url: https://api.example.com/items
      headers: {Authorization: 'Bearer {auth.access_token}'}
      body: {name: '{input.name}'}
    input:
      - {name: name, type: data, required: true}
`

// companyEnv is a kernel with the x_test company connector in the site, a fake vendor and the secret set only
// when withAuth.
func companyEnv(t *testing.T, withAuth bool) (*env, *vendor) {
	t.Helper()
	t.Setenv("RANGKA_MASTER_KEY", testKEK(t))
	t.Setenv("RANGKA_MASTER_KEY_FILE", "")
	e := newEnv(t, false, func(apps, site string) {
		p := filepath.Join(site, "system.rk")
		b, _ := os.ReadFile(p)
		os.WriteFile(p, []byte(strings.Replace(string(b), "secrets: {driver: env}", "secrets: {driver: db}", 1)), 0o644)
		dir := filepath.Join(site, "connectors", "x_test")
		os.MkdirAll(dir, 0o755)
		if err := os.WriteFile(filepath.Join(dir, "connector.rk"), []byte(companyConnector), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	items := make([]string, 30)
	for i := range items {
		items[i] = fmt.Sprintf(`{"id":%d,"name":"Barang %d"}`, i, i)
	}
	v := &vendor{status: 200, answer: `{"items":[` + strings.Join(items, ",") + `]}`}
	e.s.Connectors.HTTP = &http.Client{Transport: v, Timeout: 5 * time.Second}
	e.login()
	if withAuth {
		e.must(204, "PUT", "/api/v1/connectors/x_test/auth/access_token", map[string]any{"value": companyTestSecret})
	}
	return e, v
}

const testPath = "/api/v1/connectors/x_test/test"

func TestConnectorTestOK(t *testing.T) {
	e, v := companyEnv(t, true)
	r := e.must(200, "POST", testPath, map[string]any{"action": "list_items"})
	if r.JSON["ok"] != true || r.JSON["state"] != "tested" {
		t.Fatalf("answer: %s", r.Body)
	}
	items := get(r.JSON, "result", "items").([]any)
	if len(items) != 20 {
		t.Fatalf("rows: %d, want 20", len(items))
	}
	if len(v.sent()) != 1 || v.sent()[0].Header.Get("Authorization") != "Bearer "+companyTestSecret {
		t.Fatalf("vendor requests: %v", v.sent())
	}
	if strings.Contains(string(r.Body), companyTestSecret) {
		t.Fatal("answer leaks the secret")
	}
	// the audit row names the action and status, never the body
	a := e.must(200, "GET", "/api/v1/system/audit?kind=connector.test", nil)
	if !strings.Contains(string(a.Body), `"list_items"`) || strings.Contains(string(a.Body), "Barang") || strings.Contains(string(a.Body), companyTestSecret) {
		t.Fatalf("audit: %s", a.Body)
	}
	// GET reports the stored test
	g := e.must(200, "GET", testPath, nil)
	if g.JSON["state"] != "tested" || g.JSON["tested_by"] == "" || g.JSON["hash"] == "" || g.JSON["writes_confirmed_at"] != nil {
		t.Fatalf("GET test: %s", g.Body)
	}
	if wa := g.JSON["write_actions"].([]any); len(wa) != 1 || wa[0] != "create_item" {
		t.Fatalf("write_actions: %v", wa)
	}
	if len(get(g.JSON, "result", "items").([]any)) != 20 {
		t.Fatalf("stored result: %s", g.Body)
	}
}

func TestConnectorTestCutsTo4KB(t *testing.T) {
	e, v := companyEnv(t, true)
	big := make([]string, 30)
	for i := range big {
		big[i] = fmt.Sprintf(`{"id":%d,"note":"%s"}`, i, strings.Repeat("x", 400))
	}
	v.mu.Lock()
	v.answer = `{"items":[` + strings.Join(big, ",") + `]}`
	v.mu.Unlock()
	r := e.must(200, "POST", testPath, map[string]any{"action": "list_items"})
	b, _ := json.Marshal(r.JSON["result"])
	items := get(r.JSON, "result", "items").([]any)
	if len(b) > 4096 || len(items) == 0 || len(items) >= 20 {
		t.Fatalf("result %d bytes, %d rows", len(b), len(items))
	}
}

func TestConnectorTestRefusesPOST(t *testing.T) {
	e, v := companyEnv(t, true)
	r := e.do("POST", testPath, map[string]any{"action": "create_item", "input": map[string]any{"name": "x"}})
	if r.Status != 422 || errCode(r) != "test_get_only" || len(v.sent()) != 0 {
		t.Fatalf("POST action: %d %s", r.Status, r.Body)
	}
	if r := e.do("POST", "/api/v1/connectors/whatsapp_business/test", map[string]any{"action": "send_template"}); r.Status != 404 || errCode(r) != "not_company_connector" {
		t.Fatalf("catalogue connector: %d %s", r.Status, r.Body)
	}
}

func TestConnectorTestRefusesAI(t *testing.T) {
	e, v := companyEnv(t, true)
	ai := assistantEnv(t, e, 10*time.Minute)
	if r := ai.do("POST", testPath, map[string]any{"action": "list_items"}); r.Status != 403 || errCode(r) != "ai_forbidden" || len(v.sent()) != 0 {
		t.Fatalf("assistant POST test: %d %s", r.Status, r.Body)
	}
	// Raka reads the state to explain it
	e.must(200, "POST", testPath, map[string]any{"action": "list_items"})
	if r := ai.do("GET", testPath, nil); r.Status != 200 || r.JSON["state"] != "tested" {
		t.Fatalf("assistant GET test: %d %s", r.Status, r.Body)
	}
	rep := repEnv(t, e)
	if r := rep.do("POST", testPath, map[string]any{"action": "list_items"}); r.Status != 403 || errCode(r) != "forbidden" {
		t.Fatalf("sales_rep: %d %s", r.Status, r.Body)
	}
}

func TestConnectorTestNeedsAuth(t *testing.T) {
	e, v := companyEnv(t, false)
	r := e.do("POST", testPath, map[string]any{"action": "list_items"})
	if r.Status != 409 || errCode(r) != "connector_needs_auth" || len(v.sent()) != 0 {
		t.Fatalf("needs auth: %d %s", r.Status, r.Body)
	}
	if g := e.must(200, "GET", testPath, nil); g.JSON["state"] != "needs_auth" {
		t.Fatalf("GET: %s", g.Body)
	}
}

func TestConnectorTestFailedCallStaysUntested(t *testing.T) {
	e, v := companyEnv(t, true)
	v.mu.Lock()
	v.status, v.answer = 401, `{"error":"bad token `+companyTestSecret+`"}`
	v.mu.Unlock()
	r := e.must(200, "POST", testPath, map[string]any{"action": "list_items"})
	if r.JSON["ok"] != false || r.JSON["state"] != "untested" || strings.Contains(string(r.Body), companyTestSecret) {
		t.Fatalf("failed test: %s", r.Body)
	}
	e.must(409, "POST", "/api/v1/connectors/x_test/confirm-writes", nil)
}

func TestConfirmWritesOwnerOnly(t *testing.T) {
	e, _ := companyEnv(t, true)
	const confirm = "/api/v1/connectors/x_test/confirm-writes"
	// never tested: not_tested
	if r := e.do("POST", confirm, nil); r.Status != 409 || errCode(r) != "not_tested" {
		t.Fatalf("before test: %d %s", r.Status, r.Body)
	}
	e.must(200, "POST", testPath, map[string]any{"action": "list_items"})
	rep := repEnv(t, e)
	if r := rep.do("POST", confirm, nil); r.Status != 403 || errCode(r) != "owner_required" {
		t.Fatalf("sales_rep: %d %s", r.Status, r.Body)
	}
	r := e.must(200, "POST", confirm, nil)
	if r.JSON["writes_confirmed_at"] == nil {
		t.Fatalf("confirm: %s", r.Body)
	}
	if g := e.must(200, "GET", testPath, nil); g.JSON["writes_confirmed_at"] == nil {
		t.Fatalf("GET after confirm: %s", g.Body)
	}
	a := e.must(200, "GET", "/api/v1/system/audit?kind=connector.writes_confirmed", nil)
	if !strings.Contains(string(a.Body), "create_item") {
		t.Fatalf("audit: %s", a.Body)
	}
	// a new test of the same content keeps the confirmation (only a changed file clears it)
	e.must(200, "POST", testPath, map[string]any{"action": "list_items"})
	if g := e.must(200, "GET", testPath, nil); g.JSON["writes_confirmed_at"] == nil {
		t.Fatalf("confirmation lost on a re-test of the same file: %s", g.Body)
	}
}

func TestConfirmWritesAfter24h(t *testing.T) {
	e, _ := companyEnv(t, true)
	e.must(200, "POST", testPath, map[string]any{"action": "list_items"})
	if _, err := e.s.DB.Exec(context.Background(), `update rk_connector_checks set tested_at = now() - interval '25 hours' where connector = 'x_test'`); err != nil {
		t.Fatal(err)
	}
	if r := e.do("POST", "/api/v1/connectors/x_test/confirm-writes", nil); r.Status != 409 || errCode(r) != "not_tested" {
		t.Fatalf("after 25 h: %d %s", r.Status, r.Body)
	}
}

func TestConfirmWritesRefusesAI(t *testing.T) {
	e, _ := companyEnv(t, true)
	e.must(200, "POST", testPath, map[string]any{"action": "list_items"})
	ai := assistantEnv(t, e, 10*time.Minute)
	if r := ai.do("POST", "/api/v1/connectors/x_test/confirm-writes", nil); r.Status != 403 || errCode(r) != "ai_forbidden" {
		t.Fatalf("assistant confirm: %d %s", r.Status, r.Body)
	}
}

// A token carries its owner's roles, but testing and confirming are a person's act (contract 0033 §3, §4).
func TestConnectorTestAndConfirmRefuseAPIToken(t *testing.T) {
	e, v := companyEnv(t, true)
	e.must(200, "POST", testPath, map[string]any{"action": "list_items"})
	sent := len(v.sent())
	api := &env{t: t, s: e.s, ts: e.ts, token: e.must(201, "POST", "/api/v1/tokens", map[string]any{"name": "cli"}).JSON["token"].(string)}
	if r := api.do("POST", testPath, map[string]any{"action": "list_items"}); r.Status != 403 || errCode(r) != "forbidden" || len(v.sent()) != sent {
		t.Fatalf("token test: %d %s", r.Status, r.Body)
	}
	if r := api.do("POST", "/api/v1/connectors/x_test/confirm-writes", nil); r.Status != 403 || errCode(r) != "forbidden" {
		t.Fatalf("token confirm: %d %s", r.Status, r.Body)
	}
	api.must(200, "GET", testPath, nil)
}

// A failed re-test of unchanged content records the failure but keeps a tested connector tested and confirmed.
func TestConnectorTestFailedRetestKeepsState(t *testing.T) {
	e, v := companyEnv(t, true)
	e.must(200, "POST", testPath, map[string]any{"action": "list_items"})
	e.must(200, "POST", "/api/v1/connectors/x_test/confirm-writes", nil)
	v.mu.Lock()
	v.status, v.answer = 503, `{"error":"down"}`
	v.mu.Unlock()
	r := e.must(200, "POST", testPath, map[string]any{"action": "list_items"})
	if r.JSON["ok"] != false || r.JSON["state"] != "tested" {
		t.Fatalf("failed re-test: %s", r.Body)
	}
	g := e.must(200, "GET", testPath, nil)
	if g.JSON["state"] != "tested" || g.JSON["writes_confirmed_at"] == nil || get(g.JSON, "result", "error") != "call_failed" {
		t.Fatalf("GET after failed re-test: %s", g.Body)
	}
}
