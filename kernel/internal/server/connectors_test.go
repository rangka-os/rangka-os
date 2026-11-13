package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
	"github.com/rangka-os/rangka-os/kernel/internal/connectors"
	"github.com/rangka-os/rangka-os/kernel/internal/engine"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
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

// sent returns a copy of the requests so far (the job worker appends from another goroutine).
func (v *vendor) sent() []*http.Request {
	v.mu.Lock()
	defer v.mu.Unlock()
	return append([]*http.Request{}, v.reqs...)
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

// stopScheduledPolls stops the scheduler's own polls of kopi-senja and waits for the poll jobs already queued, so a
// test's own Poll calls are the only ones (audit F4). SetPolls returns only after a tick in flight has finished
// enqueueing (jobs.Scheduler.pollMu), so no poll job appears after the look below.
func stopScheduledPolls(t *testing.T, e *env) {
	t.Helper()
	e.s.Sched.SetPolls("kopi-senja", nil)
	res, err := e.s.DB.Query(context.Background(), "select id from rk_jobs where kind = 'poll'")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res.Rows {
		waitJob(t, e, r[0].(int64))
	}
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

// A dead-lettered call is replayed after the owner sets the missing key: the replay is a new job rendered with the
// current keys, the dead-letter row stays, a second replay returns the first, and a sales_rep cannot replay.
func TestReplayDeadLetter(t *testing.T) {
	e, v := runtimeEnv(t)
	e.must(204, "DELETE", "/api/v1/connectors/whatsapp_business/auth/access_token", nil)
	id := callJob(t, e, "whatsapp_business", "send_template", map[string]any{"to": "6281234", "template": "halo", "idempotency_key": "r1"})
	if st, errs := waitJob(t, e, id); st != "discarded" || !strings.Contains(errs, "connector_needs_auth") {
		t.Fatalf("dead letter: %s %s", st, errs)
	}
	path := fmt.Sprintf("/api/v1/system/jobs/%d/replay", id)
	if _, err := auth.EnsureUser(context.Background(), e.s.DB, "rep@kopisenja.id", "Sales", "rahasia-123", "kopi-senja", "sales_rep", "USR-003"); err != nil {
		t.Fatal(err)
	}
	if r := loginAs(e, "rep@kopisenja.id", "rahasia-123").do("POST", path, nil); r.Status != 403 {
		t.Fatalf("sales_rep replay: %d %s", r.Status, r.Body)
	}
	e.must(204, "PUT", "/api/v1/connectors/whatsapp_business/auth/access_token", map[string]any{"value": waToken})
	r := e.must(202, "POST", path, nil)
	nid := int64(r.JSON["id"].(float64))
	if nid == id || r.JSON["replay_of"] != float64(id) {
		t.Fatalf("replay answer: %s", r.Body)
	}
	if st, errs := waitJob(t, e, nid); st != "completed" {
		t.Fatalf("replayed call: %s %s", st, errs)
	}
	if len(v.reqs) != 1 || v.reqs[0].Header.Get("Authorization") != "Bearer "+waToken {
		t.Fatalf("replay request: %d", len(v.reqs))
	}
	if again := e.must(202, "POST", path, nil); int64(again.JSON["id"].(float64)) != nid {
		t.Fatalf("second replay made another job: %s", again.Body)
	}
	if st, _ := waitJob(t, e, id); st != "discarded" {
		t.Fatalf("dead-letter row changed: %s", st)
	}
	if r := e.do("POST", fmt.Sprintf("/api/v1/system/jobs/%d/replay", nid), nil); r.Status != 409 || get(r.JSON, "error", "code") != "job_not_dead" {
		t.Fatalf("replay of a completed job: %d %s", r.Status, r.Body)
	}
	if r := e.do("POST", "/api/v1/system/jobs/999999/replay", nil); r.Status != 404 {
		t.Fatalf("replay of a missing job: %d", r.Status)
	}
	if !strings.Contains(dump(t, e, "select kind from rk_audit"), "job.replay") {
		t.Fatal("replay not audited")
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
	for i := 0; i < 200 && len(v.sent()) == 0; i++ {
		time.Sleep(25 * time.Millisecond)
	}
	if reqs := v.sent(); len(reqs) != 1 || reqs[0].Header.Get("Authorization") != "Bearer "+waToken {
		t.Fatalf("vendor requests: %d", len(reqs))
	}
	noLeak(t, "jobs", []byte(dump(t, e, "select args::text from rk_jobs where kind = 'call'")), waToken)
}

const pollerConnector = `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/connector.json
name: poller_test
label: {id: Poller test}
summary: {id: Penghubung uji yang mengambil pesanan baru secara berkala.}
version: 1.0.0
hosts: [api.example.com]
auth:
  type: token
  fields:
    - {name: api_key, label: {id: API key}, secret: true}
triggers:
  - name: new_orders
    via: poll
    every: 1m
    action: list_orders
    input: {since: cursor, limit: "50"}
    cursor: next_cursor
    for_each: "orders[]"
    emit: {order_id: item.id, customer: item.customer, phone: item.phone}
actions:
  - name: list_orders
    request:
      method: GET
      url: https://api.example.com/v1/orders
      headers: {Authorization: 'Bearer {auth.api_key}'}
      query: {since: '{input.since}', limit: '{input.limit}'}
    input:
      - {name: since, type: data}
      - {name: limit, type: int}
`

const pollFlow = `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.4/flow.json
name: order_to_lead
label: {id: Pesanan baru jadi lead}
on: poller_test.new_orders
steps:
  - create: lead
    set:
      lead_name: event.customer
      phone: event.phone
      source: "'whatsapp'"
      status: "'new'"
idempotency: event.order_id
`

// A via: poll trigger (contract 0002 §A.1): the scheduler queues one poll per slot for the trigger an installed flow
// listens to; the poll sends the action with the stored cursor, starts the flow per new item, drops items it saw
// before, stores the next cursor, and skips quietly while the connector has no key.
func TestPollTrigger(t *testing.T) {
	t.Setenv("RANGKA_MASTER_KEY", testKEK(t))
	t.Setenv("RANGKA_MASTER_KEY_FILE", "")
	e := newEnv(t, false, func(apps, site string) {
		p := filepath.Join(site, "system.rk")
		b, _ := os.ReadFile(p)
		os.WriteFile(p, []byte(strings.Replace(string(b), "secrets: {driver: env}", "secrets: {driver: db}", 1)), 0o644)
		dir := filepath.Join(filepath.Dir(apps), "rangka-connectors", "poller_test")
		os.MkdirAll(dir, 0o755)
		if err := os.WriteFile(filepath.Join(dir, "connector.rk"), []byte(pollerConnector), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(apps, "crm", "flows", "order_to_lead.rk"), []byte(pollFlow), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	v := &vendor{status: 200, answer: `{"orders":[{"id":"o1","customer":"Budi","phone":"628111222"}],"next_cursor":"c1"}`}
	e.s.Connectors.HTTP = &http.Client{Transport: v, Timeout: 5 * time.Second}
	e.login()
	ctx := context.Background()
	pollJobs := func() []int64 {
		res, err := e.s.DB.Query(ctx, "select id from rk_jobs where kind = 'poll' order by id")
		if err != nil {
			t.Fatal(err)
		}
		var out []int64
		for _, r := range res.Rows {
			out = append(out, r[0].(int64))
		}
		return out
	}
	// without its key the poll is skipped: completed, nothing sent, nothing dead-lettered
	e.s.Sched.Tick(ctx)
	ids := pollJobs()
	if len(ids) != 1 {
		t.Fatalf("poll jobs after the first tick: %v", ids)
	}
	if st, errs := waitJob(t, e, ids[0]); st != "completed" || len(v.reqs) != 0 {
		t.Fatalf("poll without a key: %s %s, %d requests", st, errs, len(v.reqs))
	}
	if !strings.Contains(dump(t, e, "select result::text from rk_jobs where kind = 'poll'"), "needs_auth") {
		t.Fatal("poll without a key not reported as skipped")
	}
	// the same slot is not queued twice
	e.s.Sched.Tick(ctx)
	if n := len(pollJobs()); n != 1 {
		t.Fatalf("same slot queued again: %d jobs", n)
	}
	e.must(204, "PUT", "/api/v1/connectors/poller_test/auth/api_key", map[string]any{"value": "poller-api-key-0002-zzzz"})
	res, err := e.s.Connectors.Poll(ctx, "kopi-senja", "poller_test", "new_orders")
	if err != nil || res.Events != 1 || !res.Cursor {
		t.Fatalf("first poll: %+v %v", res, err)
	}
	if len(v.reqs) != 1 || v.reqs[0].URL.Host != "api.example.com" || v.reqs[0].Header.Get("Authorization") != "Bearer poller-api-key-0002-zzzz" ||
		v.reqs[0].URL.Query().Get("since") != "" || v.reqs[0].URL.Query().Get("limit") != "50" {
		t.Fatalf("first request: %v", v.reqs[0].URL)
	}
	waitFor(t, func() bool { return leadsByPhone(t, e, "628111222") == 1 })
	if c := dump(t, e, "select cursor from rk_poll_cursors"); strings.TrimSpace(c) != "c1" {
		t.Fatalf("cursor: %q", c)
	}
	// the next poll sends the cursor; an item seen before starts nothing
	res, err = e.s.Connectors.Poll(ctx, "kopi-senja", "poller_test", "new_orders")
	if err != nil || res.Events != 0 || res.Dupes != 1 {
		t.Fatalf("second poll: %+v %v", res, err)
	}
	if v.reqs[1].URL.Query().Get("since") != "c1" {
		t.Fatalf("second request: %v", v.reqs[1].URL)
	}
	time.Sleep(200 * time.Millisecond)
	if n := leadsByPhone(t, e, "628111222"); n != 1 {
		t.Fatalf("leads after a duplicate: %d", n)
	}
	noLeak(t, "jobs and audit", []byte(dump(t, e, "select args::text, coalesce(errors::text, ''), coalesce(result::text, '') from rk_jobs")+
		dump(t, e, "select coalesce(data::text, '') from rk_audit")), "poller-api-key-0002-zzzz")
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if ok() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

const outletPoller = `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/connector.json
name: outlet_poller
label: {id: Outlet poller}
summary: {id: Penghubung uji yang mengambil transaksi per outlet.}
version: 1.0.0
hosts: [api.example.com]
auth:
  type: token
  fields:
    - {name: api_key, label: {id: API key}, secret: true}
triggers:
  - name: sales
    via: poll
    every: 15m
    action: latest
    each: {action: outlets, items: outlets, as: outlet, key: outlet.id}
    input: {outlet_id: outlet.id, since: "cursor != null ? cursor : '2026-11-01'"}
    cursor: "size(payments) > 0 ? max(payments.map(p, p.created_at)) : null"
    for_each: "payments[]"
    emit: {order_id: item.id, outlet: outlet.name, customer: item.customer, phone: item.phone}
actions:
  - name: outlets
    request:
      method: GET
      url: https://api.example.com/v1/outlets
      headers: {Authorization: 'Bearer {auth.api_key}'}
    output: {outlets: data}
  - name: latest
    request:
      method: GET
      url: https://api.example.com/v1/outlets/{input.outlet_id}/sales
      headers: {Authorization: 'Bearer {auth.api_key}'}
      query: {since: '{input.since}'}
    input:
      - {name: outlet_id, type: data, required: true}
      - {name: since, type: data, required: true}
    output: {payments: data.payments}
`

const outletSaleFlow = `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.4/flow.json
name: outlet_sale_to_lead
label: {id: Transaksi outlet jadi lead}
on: outlet_poller.sales
steps:
  - create: lead
    set:
      lead_name: event.customer
      phone: event.phone
      source: "'whatsapp'"
      status: "'new'"
idempotency: event.order_id
`

// outletVendor answers by path: two outlets, and per outlet the sales after since.
type outletVendor struct {
	mu   sync.Mutex
	reqs []*http.Request
}

func (o *outletVendor) RoundTrip(r *http.Request) (*http.Response, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.reqs = append(o.reqs, r)
	body := `{"data":[{"id":11,"name":"Senja Kemang"},{"id":12,"name":"Senja Depok"}]}`
	since := r.URL.Query().Get("since")
	switch r.URL.Path {
	case "/v1/outlets/11/sales":
		body = `{"data":{"payments":[{"id":"p1","customer":"Ani","phone":"62811000001","created_at":"2026-11-10T08:00:00Z"},{"id":"p2","customer":"Bayu","phone":"62811000002","created_at":"2026-11-10T09:00:00Z"}]}}`
		if since != "2026-11-01" {
			body = `{"data":{"payments":[]}}`
		}
	case "/v1/outlets/12/sales":
		body = `{"data":{"payments":[{"id":"p3","customer":"Citra","phone":"62811000003","created_at":"2026-11-10T07:30:00Z"}]}}`
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
}

// each: lists the outlets first, then polls each outlet with its own cursor; the outlet is visible to emit.
func TestPollEachOutlet(t *testing.T) {
	t.Setenv("RANGKA_MASTER_KEY", testKEK(t))
	t.Setenv("RANGKA_MASTER_KEY_FILE", "")
	e := newEnv(t, false, func(apps, site string) {
		p := filepath.Join(site, "system.rk")
		b, _ := os.ReadFile(p)
		os.WriteFile(p, []byte(strings.Replace(string(b), "secrets: {driver: env}", "secrets: {driver: db}", 1)), 0o644)
		dir := filepath.Join(filepath.Dir(apps), "rangka-connectors", "outlet_poller")
		os.MkdirAll(dir, 0o755)
		if err := os.WriteFile(filepath.Join(dir, "connector.rk"), []byte(outletPoller), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(apps, "crm", "flows", "outlet_sale_to_lead.rk"), []byte(outletSaleFlow), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	v := &outletVendor{}
	e.s.Connectors.HTTP = &http.Client{Transport: v, Timeout: 5 * time.Second}
	e.login()
	ctx := context.Background()
	// the scheduler polls this trigger every 15 minutes once the key is set: only this test's polls may run (the
	// first poll counted 4 requests or the second saw no duplicate when a scheduled poll ran in between)
	stopScheduledPolls(t, e)
	if res, err := e.s.Connectors.Poll(ctx, "kopi-senja", "outlet_poller", "sales"); err != nil || res.Skipped != "needs_auth" || len(v.reqs) != 0 {
		t.Fatalf("poll without a key: %+v %v, %d requests", res, err, len(v.reqs))
	}
	e.must(204, "PUT", "/api/v1/connectors/outlet_poller/auth/api_key", map[string]any{"value": "outlet-api-key-0002-zzzz"})
	res, err := e.s.Connectors.Poll(ctx, "kopi-senja", "outlet_poller", "sales")
	if err != nil || res.Items != 2 || res.Events != 3 || !res.Cursor {
		t.Fatalf("first poll: %+v %v", res, err)
	}
	if len(v.reqs) != 3 || v.reqs[0].URL.Path != "/v1/outlets" || v.reqs[1].URL.Query().Get("since") != "2026-11-01" {
		t.Fatalf("requests: %v", v.reqs)
	}
	cursors := dump(t, e, "select trigger || '=' || cursor from rk_poll_cursors order by trigger")
	if cursors != "sales@11=2026-11-10T09:00:00Z sales@12=2026-11-10T07:30:00Z " {
		t.Fatalf("cursors: %q", cursors)
	}
	waitFor(t, func() bool { return leadsByPhone(t, e, "62811000003") == 1 && leadsByPhone(t, e, "62811000002") == 1 })
	if !strings.Contains(dump(t, e, "select coalesce(data::text, '') from rk_audit where kind = 'connector.event'"), "Senja Depok") {
		t.Fatal("the outlet is not in the event")
	}
	// next poll: each outlet sends its own cursor; outlet 12 answers the same sale again, which starts nothing
	res, err = e.s.Connectors.Poll(ctx, "kopi-senja", "outlet_poller", "sales")
	if err != nil || res.Events != 0 || res.Dupes != 1 {
		t.Fatalf("second poll: %+v %v", res, err)
	}
	if got := v.reqs[4].URL.Query().Get("since"); got != "2026-11-10T09:00:00Z" {
		t.Fatalf("outlet 11 since: %q", got)
	}
	if got := v.reqs[5].URL.Query().Get("since"); got != "2026-11-10T07:30:00Z" {
		t.Fatalf("outlet 12 since: %q", got)
	}
}

const pagerConnector = `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/connector.json
name: pager_test
label: {id: Pager test}
summary: {id: Penghubung uji yang membaca transaksi per halaman.}
version: 1.0.0
hosts: [api.example.com]
auth:
  type: token
  fields:
    - {name: api_key, label: {id: API key}, secret: true}
triggers:
  - name: sales
    via: poll
    every: 5m
    action: latest
    input: {since: "cursor != null ? epoch(cursor) : 1794268800"}
    next: next
    cursor: "size(payments) > 0 ? timestamp(max(payments.map(p, epoch(p.created_at)))) : null"
    for_each: "payments[]"
    emit: {order_id: item.id, customer: item.customer, phone: item.phone}
  - name: paged
    via: poll
    every: 5m
    action: numbered
    input: {page: "page != null ? page : 1"}
    next: "has_more ? page_no + 1 : null"
    for_each: "payments[]"
    emit: {order_id: item.id, customer: item.customer, phone: item.phone}
  - name: endless
    via: poll
    every: 5m
    action: numbered
    input: {page: "page != null ? page : 1", mode: "'endless'"}
    next: "has_more ? page_no + 1 : null"
    cursor: page_no
    for_each: "payments[]"
  - name: stuck
    via: poll
    every: 5m
    action: numbered
    input: {page: "page != null ? page : 1", mode: "'stuck'"}
    next: "has_more ? page_no + 1 : null"
    for_each: "payments[]"
actions:
  - name: latest
    request:
      method: GET
      url: https://api.example.com/v3/sales
      headers: {Authorization: 'Bearer {auth.api_key}'}
      query: {since: '{input.since}', per_page: '2'}
    input:
      - {name: since, type: int, required: true}
    output: {payments: data.payments, next: data.next_url}
  - name: numbered
    request:
      method: GET
      url: https://api.example.com/v1/pages
      headers: {Authorization: 'Bearer {auth.api_key}'}
      query: {page: '{input.page}', mode: '{input.mode}'}
    input:
      - {name: page, type: int}
      - {name: mode, type: data}
    output: {payments: data.payments, has_more: data.has_more, page_no: data.page}
`

func pagerFlow(name, on string) string {
	return `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.4/flow.json
name: ` + name + `
label: {id: Transaksi jadi lead}
on: ` + on + `
steps:
  - create: lead
    set:
      lead_name: event.customer
      phone: event.phone
      source: "'whatsapp'"
      status: "'new'"
idempotency: event.order_id
`
}

// pagerVendor answers Moka-like pages: next_url links the next page (http://, which the kernel keeps on https), and a
// numbered API answers has_more.
type pagerVendor struct {
	mu      sync.Mutex
	reqs    []*http.Request
	offHost bool
}

func (o *pagerVendor) RoundTrip(r *http.Request) (*http.Response, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.reqs = append(o.reqs, r)
	body := `{}`
	switch r.URL.Path {
	case "/v3/sales":
		next := `"http://api.example.com/v3/sales?since=1794268800&per_page=2&cursor=b"`
		if o.offHost {
			next = `"https://evil.example.net/v3/sales?cursor=b"`
		}
		body = `{"data":{"payments":[{"id":"s3","customer":"Dewi","phone":"62812000003","created_at":"2026-11-10T10:00:00+07:00"},{"id":"s2","customer":"Eko","phone":"62812000002","created_at":"2026-11-10T09:00:00+07:00"}],"next_url":` + next + `}}`
		switch r.URL.Query().Get("cursor") {
		case "b":
			body = `{"data":{"payments":[{"id":"s1","customer":"Fajar","phone":"62812000001","created_at":"2026-11-10T08:00:00+07:00"}],"next_url":"/v3/sales?since=1794268800&per_page=2&cursor=c"}}`
		case "c":
			body = `{"data":{"payments":[],"next_url":null}}`
		}
		if r.URL.Query().Get("since") != "1794268800" {
			body = `{"data":{"payments":[],"next_url":null}}`
		}
	case "/v1/pages":
		switch r.URL.Query().Get("mode") {
		case "endless": // always one more page
			body = `{"data":{"payments":[],"has_more":true,"page":` + r.URL.Query().Get("page") + `}}`
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
		case "stuck": // says page 1 whatever was asked: the same next again
			body = `{"data":{"payments":[],"has_more":true,"page":1}}`
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
		}
		switch r.URL.Query().Get("page") {
		case "1":
			body = `{"data":{"payments":[{"id":"n1","customer":"Gita","phone":"62813000001"}],"has_more":true,"page":1}}`
		case "2":
			body = `{"data":{"payments":[{"id":"n2","customer":"Hadi","phone":"62813000002"}],"has_more":false,"page":2}}`
		}
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
}

// t_277ba9bf + t_1320981c: a poll follows next: (Moka's next_url, on the action's host, or a page number) until it is
// null; the cursor reads every page; epoch()/timestamp() keep the cursor a datetime and send since in seconds.
// t_346011ed: an event whose flow cannot be queued is not remembered, so the next poll brings it again.
func TestPollFollowsNextPage(t *testing.T) {
	t.Setenv("RANGKA_MASTER_KEY", testKEK(t))
	t.Setenv("RANGKA_MASTER_KEY_FILE", "")
	e := newEnv(t, false, func(apps, site string) {
		p := filepath.Join(site, "system.rk")
		b, _ := os.ReadFile(p)
		os.WriteFile(p, []byte(strings.Replace(string(b), "secrets: {driver: env}", "secrets: {driver: db}", 1)), 0o644)
		dir := filepath.Join(filepath.Dir(apps), "rangka-connectors", "pager_test")
		os.MkdirAll(dir, 0o755)
		if err := os.WriteFile(filepath.Join(dir, "connector.rk"), []byte(pagerConnector), 0o644); err != nil {
			t.Fatal(err)
		}
		for name, on := range map[string]string{"pager_sale_to_lead": "pager_test.sales", "pager_page_to_lead": "pager_test.paged"} {
			if err := os.WriteFile(filepath.Join(apps, "crm", "flows", name+".rk"), []byte(pagerFlow(name, on)), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	})
	v := &pagerVendor{}
	e.s.Connectors.HTTP = &http.Client{Transport: v, Timeout: 5 * time.Second}
	e.login()
	ctx := context.Background()
	stopScheduledPolls(t, e)
	e.must(204, "PUT", "/api/v1/connectors/pager_test/auth/api_key", map[string]any{"value": "pager-api-key-0002-zzzz"})

	// the flow cannot be queued: the poll fails and remembers nothing
	start := e.s.Connectors.StartFlow
	e.s.Connectors.StartFlow = func(context.Context, pg.Querier, string, string, map[string]any, string) error {
		return errors.New("queue down")
	}
	if _, err := e.s.Connectors.Poll(ctx, "kopi-senja", "pager_test", "sales"); err == nil {
		t.Fatal("poll with a failing queue: no error")
	}
	if n := strings.TrimSpace(dump(t, e, "select count(*) from rk_hook_events")); n != "0" {
		t.Fatalf("events remembered while their flow was not queued: %s", n)
	}
	if n := strings.TrimSpace(dump(t, e, "select count(*) from rk_audit where kind = 'connector.event'")); n != "0" {
		t.Fatalf("events audited while their flow was not queued: %s", n)
	}
	e.s.Connectors.StartFlow = start
	v.reqs = nil

	res, err := e.s.Connectors.Poll(ctx, "kopi-senja", "pager_test", "sales")
	if err != nil || res.Events != 3 || res.Pages != 3 || !res.Cursor || res.Truncated {
		t.Fatalf("paged poll: %+v %v", res, err)
	}
	if len(v.reqs) != 3 || v.reqs[0].URL.Query().Get("since") != "1794268800" || v.reqs[1].URL.Scheme != "https" ||
		v.reqs[1].URL.Query().Get("cursor") != "b" || v.reqs[2].URL.Query().Get("cursor") != "c" ||
		v.reqs[1].Header.Get("Authorization") != "Bearer pager-api-key-0002-zzzz" {
		t.Fatalf("requests: %v", v.reqs)
	}
	waitFor(t, func() bool { return leadsByPhone(t, e, "62812000001") == 1 && leadsByPhone(t, e, "62812000003") == 1 })
	// the cursor is the newest sale over all pages (page 1 here), as a UTC datetime
	if c := strings.TrimSpace(dump(t, e, "select cursor from rk_poll_cursors where trigger = 'sales'")); c != "2026-11-10T03:00:00Z" {
		t.Fatalf("cursor: %q", c)
	}
	// the next poll sends since = epoch(cursor)
	v.reqs = nil
	if res, err = e.s.Connectors.Poll(ctx, "kopi-senja", "pager_test", "sales"); err != nil || res.Events != 0 || res.Pages != 1 {
		t.Fatalf("second poll: %+v %v", res, err)
	}
	if got := v.reqs[0].URL.Query().Get("since"); got != "1794279600" {
		t.Fatalf("since from the cursor: %q", got)
	}

	// a page number: input: sees page
	v.reqs = nil
	if res, err = e.s.Connectors.Poll(ctx, "kopi-senja", "pager_test", "paged"); err != nil || res.Events != 2 || res.Pages != 2 {
		t.Fatalf("numbered poll: %+v %v", res, err)
	}
	if len(v.reqs) != 2 || v.reqs[0].URL.Query().Get("page") != "1" || v.reqs[1].URL.Query().Get("page") != "2" {
		t.Fatalf("numbered requests: %v", v.reqs)
	}

	// a vendor that always has one more page: 50 pages, then the poll stops and says so
	v.reqs = nil
	if res, err = e.s.Connectors.Poll(ctx, "kopi-senja", "pager_test", "endless"); err != nil || res.Pages != connectors.MaxPollPages || !res.Truncated || len(v.reqs) != connectors.MaxPollPages {
		t.Fatalf("endless poll: %+v %v, %d requests", res, err, len(v.reqs))
	}
	if n := strings.TrimSpace(dump(t, e, "select count(*) from rk_audit where data->>'code' = 'poll_pages_truncated'")); n != "1" {
		t.Fatalf("truncation audited %s times", n)
	}
	// pages not read must come again: the cursor of a cut poll does not move
	if c := strings.TrimSpace(dump(t, e, "select count(*) from rk_poll_cursors where trigger = 'endless'")); c != "0" {
		t.Fatalf("cursor moved after a cut poll: %s", c)
	}
	// a vendor that links the same next page again: read once
	v.reqs = nil
	if res, err = e.s.Connectors.Poll(ctx, "kopi-senja", "pager_test", "stuck"); err != nil || res.Pages != 2 || res.Truncated || len(v.reqs) != 2 {
		t.Fatalf("stuck poll: %+v %v, %d requests", res, err, len(v.reqs))
	}

	// a next_url on another host is refused (the key never leaves the declared host)
	if _, err := e.s.DB.Exec(ctx, "delete from rk_poll_cursors"); err != nil {
		t.Fatal(err)
	}
	v.offHost, v.reqs = true, nil
	if _, err := e.s.Connectors.Poll(ctx, "kopi-senja", "pager_test", "sales"); err == nil || !strings.Contains(err.Error(), "evil.example.net") {
		t.Fatalf("next_url on another host: %v", err)
	}
	for _, r := range v.reqs {
		if r.URL.Hostname() != "api.example.com" {
			t.Fatalf("request sent to %s", r.URL)
		}
	}
}
