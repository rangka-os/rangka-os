package server

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/schema"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

const workspace = "../../../.."

var fixedNow = func() time.Time {
	loc, _ := time.LoadLocation("Asia/Jakarta")
	return time.Date(2026, 10, 30, 10, 0, 0, 0, loc)
}

type env struct {
	t     *testing.T
	s     *Server
	ts    *httptest.Server
	apps  string
	site  string
	jar   http.CookieJar
	token string // when set, requests send Authorization: Bearer token instead of the cookie
}

// newEnv copies the workspace apps, site and fixtures into a temp dir (change sets write files), seeds a
// fresh database and starts the kernel on an httptest server.
func newEnv(t *testing.T, devOpen bool, mutate func(apps, site string)) *env {
	t.Helper()
	url := testdb.New(t)
	auth.Iterations = 1000
	dir := t.TempDir()
	for _, p := range []string{"rangka-apps/apps", "rangka-apps/fixtures", "rangka-apps/sites", "rangka-connectors"} {
		dst := filepath.Join(dir, filepath.Base(p))
		if err := exec.Command("cp", "-r", filepath.Join(workspace, p), dst).Run(); err != nil {
			t.Fatal(err)
		}
	}
	apps, site := filepath.Join(dir, "apps"), filepath.Join(dir, "sites", "kopi-senja")
	if mutate != nil {
		mutate(apps, site)
	}
	cfg := Config{AppsDir: apps, SiteDir: site, ConnectorsDir: filepath.Join(dir, "rangka-connectors"), DeployTarget: "docker", DatabaseURL: url, Addr: ":0", DevOpen: devOpen, Schemas: schema.Embedded(), Version: "test",
		WebDir: filepath.Join(workspace, "rangka-os", "web"), TemplatesDir: filepath.Join(workspace, "rangka-os", "templates"), Now: fixedNow,
		Logf: func(string, ...any) {}}
	ctx, cancel := context.WithCancel(context.Background())
	reg, err := LoadRegistry(cfg)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pg.NewPool(ctx, url, 4)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Seed(ctx, pool, reg, SeedOptions{Org: "kopi-senja", FixturesDir: filepath.Join(dir, "fixtures"), Email: "ceo@saga.co.id",
		Password: "rangka-demo", UserRecord: "USR-001", Now: fixedNow}); err != nil {
		t.Fatal(err)
	}
	pool.Close()
	s, err := New(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	s.Start(ctx)
	ts := httptest.NewServer(s.Handler())
	jar, _ := cookiejar.New(nil)
	e := &env{t: t, s: s, ts: ts, apps: apps, site: site, jar: jar}
	t.Cleanup(func() {
		ts.Close()
		cancel()
		s.Wait()
		s.Close()
	})
	// wait for the NOTIFY listener
	for i := 0; i < 100 && !s.Hub.Listening(); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	return e
}

type resp struct {
	Status int
	Header http.Header
	Body   []byte
	JSON   map[string]any
}

func (e *env) do(method, path string, body any) *resp {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.ts.URL+path, rd)
	req.Header.Set("Content-Type", "application/json")
	if e.token != "" {
		req.Header.Set("Authorization", "Bearer "+e.token)
	}
	c := &http.Client{Jar: e.jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if e.token != "" {
		c.Jar = nil
	}
	res, err := c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	r := &resp{Status: res.StatusCode, Header: res.Header, Body: b}
	json.Unmarshal(b, &r.JSON)
	return r
}

func (e *env) must(status int, method, path string, body any) *resp {
	e.t.Helper()
	r := e.do(method, path, body)
	if r.Status != status {
		e.t.Fatalf("%s %s = %d, want %d: %s", method, path, r.Status, status, r.Body)
	}
	return r
}

func (e *env) login() {
	e.t.Helper()
	e.must(200, "POST", "/auth/login", map[string]any{"email": "ceo@saga.co.id", "password": "rangka-demo"})
}

func get(m any, path ...any) any {
	cur := m
	for _, p := range path {
		switch k := p.(type) {
		case string:
			cur = cur.(map[string]any)[k]
		case int:
			cur = cur.([]any)[k]
		}
	}
	return cur
}

// ---------- priority 3: REST, lifecycle, dashboards, actions

func TestREST(t *testing.T) {
	e := newEnv(t, false, nil)
	e.must(401, "GET", "/api/v1/meta", nil)
	e.login()
	meta := e.must(200, "GET", "/api/v1/meta", nil).JSON
	if len(meta["apps"].([]any)) != 3 || get(meta, "theme", "tokens", "light", "brand") != "#9a5b2e" || get(meta, "layout", "shell") != "top-nav" {
		t.Fatalf("meta: apps %v theme %v layout %v", meta["apps"], meta["theme"], meta["layout"])
	}
	if get(meta, "models", "deal", "app") != "crm" {
		t.Fatal("meta models")
	}
	// list with filter, sort, paging
	l := e.must(200, "GET", "/api/v1/r/deal?filter="+url.QueryEscape("stage != 'won' && stage != 'lost'")+"&sort=-value&limit=2", nil).JSON
	if l["total"] != 5.0 || get(l, "data", 0, "name") != "DEAL-00011" || len(l["data"].([]any)) != 2 {
		t.Fatalf("list: %v", l)
	}
	e.must(400, "GET", "/api/v1/r/deal?filter="+url.QueryEscape("stage =="), nil)
	e.must(404, "GET", "/api/v1/r/nope", nil)
	// create a draft invoice: defaults, link traversal, computed totals
	inv := e.must(201, "POST", "/api/v1/r/sales_invoice", map[string]any{"customer": "CUST-0012", "outlet": "OUT-01",
		"items": []any{map[string]any{"item": "BRG-0044", "qty": 2}}}).JSON
	name := inv["name"].(string)
	if !strings.HasPrefix(name, "DRAFT-") || inv["due_date"] != "2026-11-13" || inv["grand_total"] != 632700.0 || inv["status"] != "draft" {
		t.Fatalf("invoice: %v", inv)
	}
	// 422 shape
	bad := e.must(422, "POST", "/api/v1/r/sales_invoice", map[string]any{"outlet": "OUT-01"}).JSON
	if get(bad, "errors", 0, "field") != "customer" || get(bad, "errors", 0, "code") != "required" || bad["warnings"] == nil {
		t.Fatalf("422: %v", bad)
	}
	e.must(200, "PATCH", "/api/v1/r/sales_invoice/"+name, map[string]any{"notes": "Terima kasih"})
	sub := e.must(200, "POST", "/api/v1/r/sales_invoice/"+name+"/submit", map[string]any{}).JSON
	if sub["name"] != "INV-2026-00047" || sub["docstatus"] != 1.0 {
		t.Fatalf("submit: %v", sub)
	}
	e.must(409, "PATCH", "/api/v1/r/sales_invoice/INV-2026-00047", map[string]any{"notes": "x"})
	e.must(409, "DELETE", "/api/v1/r/sales_invoice/INV-2026-00047", nil)
	e.must(200, "POST", "/api/v1/r/sales_invoice/INV-2026-00047/cancel", nil)
	am := e.must(201, "POST", "/api/v1/r/sales_invoice/INV-2026-00047/amend", nil).JSON
	if am["amended_from"] != "INV-2026-00047" {
		t.Fatalf("amend: %v", am)
	}
	// warnings need force on submit
	big := e.must(201, "POST", "/api/v1/r/sales_invoice", map[string]any{"customer": "CUST-0058", "outlet": "OUT-01", "items": []any{map[string]any{"item": "BRG-0044", "qty": 10}}}).JSON
	w := e.must(422, "POST", "/api/v1/r/sales_invoice/"+big["name"].(string)+"/submit", map[string]any{}).JSON
	if w["needs_force"] != true || get(w, "warnings", 0, "rule") != "credit_limit" {
		t.Fatalf("warnings: %v", w)
	}
	e.must(200, "POST", "/api/v1/r/sales_invoice/"+big["name"].(string)+"/submit", map[string]any{"force": true})
	// non-submittable CRUD
	d := e.must(201, "POST", "/api/v1/r/deal", map[string]any{"deal_name": "Uji", "customer": "CUST-0012", "value": 1000000}).JSON
	if d["_warnings"] == nil || d["weighted_value"] != 100000.0 {
		t.Fatalf("deal: %v", d)
	}
	e.must(200, "PATCH", "/api/v1/r/deal/"+d["name"].(string), map[string]any{"stage": "proposal"})
	if got := e.must(200, "GET", "/api/v1/r/deal/"+d["name"].(string), nil).JSON; got["weighted_value"] != 500000.0 {
		t.Fatalf("recompute on patch: %v", got)
	}
	e.must(204, "DELETE", "/api/v1/r/deal/"+d["name"].(string), nil)
	e.must(404, "GET", "/api/v1/r/deal/"+d["name"].(string), nil)
	// action hook
	conv := e.must(200, "POST", "/api/v1/r/lead/LEAD-00031/actions/convert", map[string]any{}).JSON
	if get(conv, "open", "model") != "deal" || !strings.HasPrefix(conv["message"].(string), "Pelanggan CUST-0059 dan peluang DEAL-") {
		t.Fatalf("convert: %v", conv)
	}
	e.must(409, "POST", "/api/v1/r/lead/LEAD-00031/actions/convert", map[string]any{})
	// files
	f := e.must(200, "GET", "/api/v1/files/meta/apps/crm/models/deal.rk", nil).JSON
	if f["kind"] != "model" || !strings.Contains(f["text"].(string), "name: deal") {
		t.Fatalf("file: %v", f)
	}
	files := e.must(200, "GET", "/api/v1/files", nil).JSON["data"].([]any)
	if len(files) != 24 || len(e.must(200, "GET", "/api/v1/files/meta", nil).JSON["data"].([]any)) != 24 {
		t.Fatalf("files: %d", len(files))
	}
	for _, f := range files {
		if get(f, "path") == "apps/crm/models/deal.rk" && (get(f, "app") != "crm" || get(f, "layer") != 5.0 || get(f, "lines") != 143.0 || len(get(f, "sha").(string)) != 64) {
			t.Fatalf("file entry: %v", f)
		}
	}
	if f := e.must(200, "GET", "/api/v1/files/system.rk", nil).JSON; f["kind"] != "system" || f["layer"] != 2.0 || get(f, "parsed", "queue", "workers") != 4.0 {
		t.Fatalf("file: %v", f)
	}
	// v0.1 /meta: flows, roles, connectors, site with redacted system
	if get(meta, "flows", "whatsapp_to_lead", "app") != "crm" || get(meta, "roles", "sales_rep", "app") != "crm" ||
		get(meta, "connectors", "whatsapp_business", "secrets", 0) != "access_token" || get(meta, "connectors", "whatsapp_business", "triggers", 0) != "message_received" {
		t.Fatalf("meta flows/roles/connectors: %v %v", meta["roles"], meta["connectors"])
	}
	if get(meta, "site", "system", "db", "url") != "${DATABASE_URL}" || len(get(meta, "site", "themes").([]any)) != 1 || get(meta, "site", "config", "plan") != "tumbuh" {
		t.Fatalf("meta site: %v", meta["site"])
	}
	if get(meta, "theme", "overrides", "brand", "dark") != "#c9804b" || get(meta, "theme", "tokens", "dark", "brand") != "#c9804b" {
		t.Fatalf("dark derivation: %v", get(meta, "theme", "overrides"))
	}
	if got := strings.Join([]string{get(meta, "installed", 0).(string), get(meta, "installed", 2).(string)}, ","); got != "core,sales" {
		t.Fatalf("installed = %v", meta["installed"])
	}
	// orgs and members
	if o := e.must(200, "GET", "/api/v1/orgs", nil).JSON; get(o, "data", 0, "id") != "kopi-senja" || get(o, "data", 0, "role") != "owner" {
		t.Fatalf("orgs: %v", o)
	}
	if m := e.must(200, "GET", "/api/v1/orgs/kopi-senja/members", nil).JSON; get(m, "data", 0, "record") != "USR-001" || get(m, "data", 0, "roles", 0) != "owner" ||
		get(m, "data", 0, "name") != "Dede Kurniawan" || get(m, "data", 0, "status") != "active" || get(m, "data", 0, "last_seen_at") == nil {
		t.Fatalf("members: %v", m)
	}
	if o := e.must(200, "GET", "/api/v1/orgs", nil).JSON; get(o, "data", 0, "plan") != "tumbuh" || get(o, "data", 0, "usage", "users") != 1.0 ||
		get(o, "data", 0, "usage", "outlets") != 2.0 || get(o, "data", 0, "usage", "records").(float64) < 40 {
		t.Fatalf("org usage: %v", o)
	}
	// created_by is the creator; deal.owner stays the model's own field
	cd := e.must(201, "POST", "/api/v1/r/deal", map[string]any{"deal_name": "Milik Rina", "customer": "CUST-0012", "owner": "USR-002", "expected_close": "2026-12-01"}).JSON
	if cd["created_by"] != "ceo@saga.co.id" || cd["owner"] != "USR-002" {
		t.Fatalf("created_by/owner: %v %v", cd["created_by"], cd["owner"])
	}
	// the prototype and console are served at /
	if r := e.do("GET", "/prototype/", nil); r.Status != 200 || !bytes.Contains(r.Body, []byte("Rangka")) {
		t.Fatalf("static: %d", r.Status)
	}
}

func TestDashboardsOverHTTP(t *testing.T) {
	e := newEnv(t, false, nil)
	e.login()
	crm := e.must(200, "GET", "/api/v1/dashboards/crm_overview", nil)
	if v := get(crm.JSON, "widgets", 0, "value"); v != 51120000.0 {
		t.Errorf("pipeline = %v", v)
	}
	if v := get(crm.JSON, "widgets", 2, "delta"); v != "baru" {
		t.Errorf("won delta = %v", v)
	}
	if crm.Header.Get("X-Cache") != "miss" {
		t.Errorf("first call should miss the cache")
	}
	if r := e.must(200, "GET", "/api/v1/dashboards/crm_overview", nil); r.Header.Get("X-Cache") != "hit" {
		t.Errorf("crm_overview has cache: 5m; second call = %s", r.Header.Get("X-Cache"))
	}
	e.must(201, "POST", "/api/v1/r/deal", map[string]any{"deal_name": "Baru", "customer": "CUST-0031", "value": 1000000, "expected_close": "2026-12-01"})
	r := e.must(200, "GET", "/api/v1/dashboards/crm_overview", nil)
	if r.Header.Get("X-Cache") != "miss" || get(r.JSON, "widgets", 0, "value") != 52120000.0 {
		t.Errorf("after a write: cache %s pipeline %v", r.Header.Get("X-Cache"), get(r.JSON, "widgets", 0, "value"))
	}
	sales := e.must(200, "GET", "/api/v1/dashboards/sales_overview?period=90", nil).JSON
	if v := get(sales, "widgets", 1, "value"); v != 11203230.0 {
		t.Errorf("receivable = %v", v)
	}
	if v := get(sales, "widgets", 0, "value"); v != 35562180.0 {
		t.Errorf("revenue 90d = %v", v)
	}
	if rows := get(sales, "widgets", 5, "rows").([]any); get(rows[0], "status") != "overdue" {
		t.Errorf("unpaid first row status = %v", get(rows[0], "status"))
	}
	e.must(404, "GET", "/api/v1/dashboards/nope", nil)
}

// ---------- priority 4: realtime, queue, scheduler, status, audit

func readEvents(t *testing.T, body io.Reader, n int, timeout time.Duration) []map[string]any {
	t.Helper()
	ch := make(chan map[string]any, 16)
	go func() {
		sc := bufio.NewScanner(body)
		ev := ""
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				ev = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				var m map[string]any
				json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &m)
				m["_event"] = ev
				ch <- m
			}
		}
	}()
	var out []map[string]any
	deadline := time.After(timeout)
	for len(out) < n {
		select {
		case m := <-ch:
			out = append(out, m)
		case <-deadline:
			t.Fatalf("got %d of %d events: %v", len(out), n, out)
		}
	}
	return out
}

func TestCapabilities(t *testing.T) {
	e := newEnv(t, false, func(apps, site string) {
		p := filepath.Join(apps, "crm", "models", "lead.rk")
		b, _ := os.ReadFile(p)
		b = []byte(strings.Replace(string(b), "permissions:\n", `  - name: convert_later
    label: {id: Konversi di latar belakang}
    hook: crm.convert_lead
    queue: {retries: 1}
    if: status != 'lost' && converted_deal == ''
permissions:
`, 1))
		os.WriteFile(p, b, 0o644)
	})
	e.login()
	// SSE: subscribe to deal events, then write
	req, _ := http.NewRequest("GET", e.ts.URL+"/api/v1/events?models=deal", nil)
	for _, c := range e.jar.Cookies(req.URL) {
		req.AddCookie(c)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != 200 || res.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("events: %v %v", err, res)
	}
	defer res.Body.Close()
	time.Sleep(50 * time.Millisecond)
	e.must(201, "POST", "/api/v1/r/lead", map[string]any{"lead_name": "Tidak dikirim", "phone": "+62811"}) // filtered out
	d := e.must(201, "POST", "/api/v1/r/deal", map[string]any{"deal_name": "SSE", "customer": "CUST-0012", "expected_close": "2026-12-01"}).JSON
	evs := readEvents(t, res.Body, 1, 3*time.Second)
	if evs[0]["_event"] != "record" || evs[0]["model"] != "deal" || evs[0]["name"] != d["name"] || evs[0]["op"] != "created" {
		t.Fatalf("event: %v", evs[0])
	}
	// queued action → 202 + job → completed
	q := e.must(202, "POST", "/api/v1/r/lead/LEAD-00030/actions/convert_later", map[string]any{}).JSON
	jobID := int(q["job_id"].(float64))
	var job map[string]any
	for i := 0; i < 100; i++ {
		job = e.must(200, "GET", "/api/v1/system/jobs/"+itoa(jobID), nil).JSON
		if job["state"] == "completed" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if job["state"] != "completed" || !strings.HasPrefix(get(job, "result", "message").(string), "Pelanggan") {
		t.Fatalf("job: %v", job)
	}
	if lead := e.must(200, "GET", "/api/v1/r/lead/LEAD-00030", nil).JSON; lead["status"] != "qualified" {
		t.Fatalf("queued convert did not run: %v", lead)
	}
	// scheduler: morning_follow_up (0 8 * * 1-5) comes due at 08:00 Jakarta
	loc, _ := time.LoadLocation("Asia/Jakarta")
	e.s.Sched.SetNow(func() time.Time { return time.Date(2026, 10, 30, 7, 59, 0, 0, loc) })
	e.s.Sched.SetFlows(e.s.Engine.Reg(), "kopi-senja")
	e.s.Sched.Tick(context.Background())
	e.s.Sched.SetNow(func() time.Time { return time.Date(2026, 10, 30, 8, 0, 30, 0, loc) })
	e.s.Sched.Tick(context.Background())
	e.s.Sched.Tick(context.Background())
	var flowJobs []any
	for i := 0; i < 100; i++ {
		flowJobs = e.must(200, "GET", "/api/v1/system/jobs?state=completed", nil).JSON["data"].([]any)
		n := 0
		for _, j := range flowJobs {
			if get(j, "kind") == "flow" {
				n++
			}
		}
		if n > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	var fj map[string]any
	for _, j := range flowJobs {
		if get(j, "kind") == "flow" {
			if fj != nil {
				t.Fatal("the schedule ran twice")
			}
			fj = j.(map[string]any)
		}
	}
	if fj == nil {
		t.Fatal("flow job did not complete")
	}
	if txt := get(fj, "result", "notifications", 0, "text"); txt != "Ada 1 aktivitas yang harus di-follow-up hari ini" {
		t.Fatalf("flow notification = %v", txt)
	}
	if rcpt := get(fj, "result", "notifications", 0, "recipients", 0); rcpt != "USR-002" {
		t.Fatalf("recipients = %v", rcpt)
	}
	// status
	st := e.must(200, "GET", "/api/v1/system/status", nil).JSON
	if get(st, "capabilities", "db", "ok") != true || get(st, "capabilities", "realtime", "ok") != true ||
		get(st, "capabilities", "queue", "counts", "completed").(float64) < 2 || get(st, "capabilities", "scheduler", "schedules", 0, "name") != "morning_follow_up" ||
		get(st, "capabilities", "scheduler", "schedules", 0, "last_status") != "completed" || get(st, "capabilities", "realtime", "connections") != 1.0 ||
		get(st, "capabilities", "auth", "clients", 0, "id") != "omilo" || st["orgs"] != 1.0 || get(st, "deploy", "target") != "docker" ||
		st["schema_version"] != "0.4" || get(st, "connectors", 0, "name") != "whatsapp_business" {
		b, _ := json.MarshalIndent(st, "", " ")
		t.Fatalf("status:\n%s", b)
	}
	// audit
	au := e.must(200, "GET", "/api/v1/system/audit?limit=100", nil).JSON["data"].([]any)
	kinds := map[string]bool{}
	for _, a := range au {
		kinds[get(a, "kind").(string)] = true
	}
	for _, k := range []string{"record.created", "action.queued", "action.run", "flow.run", "notify.email", "auth.login"} {
		if !kinds[k] {
			t.Errorf("audit has no %s: %v", k, kinds)
		}
	}
}

// flow with an event: the WhatsApp message flow creates a lead and an activity
func TestEventFlow(t *testing.T) {
	e := newEnv(t, true, nil)
	r := e.must(200, "POST", "/api/v1/flows/whatsapp_to_lead/run", map[string]any{"event": map[string]any{
		"from": "+628129999", "profile_name": "Bu Ani", "text": "Halo, mau pesan kopi 5 kg", "message_id": "wamid.1"}}).JSON
	if len(r["created"].([]any)) != 2 {
		t.Fatalf("flow: %v", r)
	}
	leads := e.must(200, "GET", "/api/v1/r/lead?filter="+url.QueryEscape("phone == '+628129999'"), nil).JSON
	if leads["total"] != 1.0 || get(leads, "data", 0, "source") != "whatsapp" {
		t.Fatalf("lead: %v", leads)
	}
	acts := e.must(200, "GET", "/api/v1/r/activity?filter="+url.QueryEscape("lead == '"+get(leads, "data", 0, "name").(string)+"'"), nil).JSON
	if acts["total"] != 1.0 || get(acts, "data", 0, "done") != true || get(acts, "data", 0, "subject") != "Halo, mau pesan kopi 5 kg" {
		t.Fatalf("activity: %v", acts)
	}
	// the same sender again finds the lead and only logs an activity
	r = e.must(200, "POST", "/api/v1/flows/whatsapp_to_lead/run", map[string]any{"event": map[string]any{"from": "+628129999", "profile_name": "Bu Ani", "text": "Jadi ya", "message_id": "wamid.2"}}).JSON
	if len(r["created"].([]any)) != 1 {
		t.Fatalf("second message: %v", r)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// ---------- priority 5: auth, tokens, OIDC

func pkce() (verifier, challenge string) {
	verifier = "verifier-0123456789-abcdefghijklmnopqrstuvwxyz-ABCDEFG"
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

func TestAuth(t *testing.T) {
	e := newEnv(t, false, nil)
	e.must(401, "GET", "/api/v1/r/deal", nil)
	e.must(401, "POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
	if r := e.do("POST", "/auth/login", map[string]any{"email": "ceo@saga.co.id", "password": "salah"}); r.Status != 401 {
		t.Fatalf("bad password: %d", r.Status)
	}
	e.login()
	me := e.must(200, "GET", "/auth/me", nil).JSON
	if get(me, "user", "email") != "ceo@saga.co.id" || get(me, "user", "record") != "USR-001" || me["approver"] != true || me["via"] != "session" {
		t.Fatalf("me: %v", me)
	}
	tok := e.must(201, "POST", "/api/v1/tokens", map[string]any{"name": "cli"}).JSON["token"].(string)
	if !strings.HasPrefix(tok, "rk_") {
		t.Fatal(tok)
	}
	// OIDC: discovery, authorize with the session, token with PKCE, userinfo, JWKS
	disc := e.must(200, "GET", "/.well-known/openid-configuration", nil).JSON
	if disc["issuer"] != "http://localhost:8080" || get(disc, "code_challenge_methods_supported", 0) != "S256" {
		t.Fatalf("discovery: %v", disc)
	}
	jwks := e.must(200, "GET", "/oauth/jwks", nil).JSON
	if get(jwks, "keys", 0, "alg") != "RS256" {
		t.Fatalf("jwks: %v", jwks)
	}
	verifier, challenge := pkce()
	q := url.Values{"response_type": {"code"}, "client_id": {"omilo"}, "redirect_uri": {"http://localhost:3000/auth/callback"},
		"scope": {"openid profile email rangka.mcp"}, "state": {"xyz"}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}, "nonce": {"n1"}}
	r := e.must(302, "GET", "/oauth/authorize?"+q.Encode(), nil)
	loc, _ := url.Parse(r.Header.Get("Location"))
	code := loc.Query().Get("code")
	if code == "" || loc.Query().Get("state") != "xyz" || loc.Host != "localhost:3000" {
		t.Fatalf("authorize redirect: %s", r.Header.Get("Location"))
	}
	// without a session the authorize endpoint shows a sign-in form
	anon := &env{t: t, ts: e.ts}
	anon.jar, _ = cookiejar.New(nil)
	if r := anon.do("GET", "/oauth/authorize?"+q.Encode(), nil); r.Status != 200 || !bytes.Contains(r.Body, []byte(`name="password"`)) {
		t.Fatalf("login form: %d", r.Status)
	}
	// unknown redirect, missing PKCE
	bq := url.Values{}
	for k, v := range q {
		bq[k] = v
	}
	bq.Set("redirect_uri", "http://evil.example/cb")
	e.must(400, "GET", "/oauth/authorize?"+bq.Encode(), nil)
	bq.Set("redirect_uri", "http://localhost:3000/auth/callback")
	bq.Del("code_challenge")
	if r := e.must(302, "GET", "/oauth/authorize?"+bq.Encode(), nil); !strings.Contains(r.Header.Get("Location"), "error=invalid_request") {
		t.Fatalf("no PKCE: %s", r.Header.Get("Location"))
	}
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {"http://localhost:3000/auth/callback"}, "client_id": {"omilo"}, "code_verifier": {"wrong"}}
	tr, _ := http.PostForm(e.ts.URL+"/oauth/token", form)
	if tr.StatusCode != 400 {
		t.Fatalf("wrong verifier accepted: %d", tr.StatusCode)
	}
	// the code was consumed by the failed attempt (one use); get a new one
	r = e.must(302, "GET", "/oauth/authorize?"+q.Encode(), nil)
	loc, _ = url.Parse(r.Header.Get("Location"))
	form.Set("code", loc.Query().Get("code"))
	form.Set("code_verifier", verifier)
	tr, _ = http.PostForm(e.ts.URL+"/oauth/token", form)
	var tokRes map[string]any
	json.NewDecoder(tr.Body).Decode(&tokRes)
	if tr.StatusCode != 200 || tokRes["token_type"] != "Bearer" || tokRes["id_token"] == nil {
		t.Fatalf("token: %d %v", tr.StatusCode, tokRes)
	}
	access := tokRes["access_token"].(string)
	parts := strings.Split(tokRes["id_token"].(string), ".")
	cb, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var idc map[string]any
	json.Unmarshal(cb, &idc)
	if idc["nonce"] != "n1" || idc["email"] != "ceo@saga.co.id" || idc["aud"] != "omilo" {
		t.Fatalf("id_token claims: %v", idc)
	}
	bearer := &env{t: t, ts: e.ts, token: access}
	ui := bearer.must(200, "GET", "/oauth/userinfo", nil).JSON
	if ui["email"] != "ceo@saga.co.id" || ui["org"] != "kopi-senja" {
		t.Fatalf("userinfo: %v", ui)
	}
	// access tokens and API tokens work on /api and /mcp
	bearer.must(200, "GET", "/api/v1/r/deal?limit=1", nil)
	bearer.must(200, "POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
	api := &env{t: t, ts: e.ts, token: tok}
	if me := api.must(200, "GET", "/auth/me", nil).JSON; me["via"] != "token" || me["human"] != false {
		t.Fatalf("token me: %v", me)
	}
	api.must(200, "POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "ping"})
	(&env{t: t, ts: e.ts, token: "rk_nope"}).must(401, "GET", "/api/v1/r/deal", nil)
	(&env{t: t, ts: e.ts, token: access + "x"}).must(401, "GET", "/api/v1/r/deal", nil)
	// logout ends the session
	e.must(204, "POST", "/auth/logout", nil)
	e.must(401, "GET", "/api/v1/r/deal", nil)
}

func TestDevOpen(t *testing.T) {
	e := newEnv(t, true, nil)
	me := e.must(200, "GET", "/auth/me", nil).JSON
	if me["via"] != "dev" || get(me, "user", "email") != "ceo@saga.co.id" {
		t.Fatalf("dev-open me: %v", me)
	}
	e.must(200, "POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func TestFlowSteps(t *testing.T) {
	e := newEnv(t, false, func(apps, site string) {
		os.WriteFile(filepath.Join(apps, "crm", "flows", "steps_demo.rk"), []byte(`# yaml-language-server: $schema=https://rangka.dev/schema/0.4/flow.json
name: steps_demo
on: whatsapp_business.message_received
steps:
  - {find: lead, where: "phone == event.from", as: lead}
  - if: lead != null
    then:
      - {update: lead, set: {status: "'contacted'"}}
      - {call: whatsapp_business.send_template, with: {to: event.from, template: "'halo'"}, as: sent}
    else:
      - {notify: owner, channel: email, template: "Tidak ada lead untuk {event.from}"}
  - {wait: 10m}
  - {create: activity, set: {subject: "'Tindak lanjut'", lead: lead.name, done: "false"}, as: act}
`), 0o644)
	})
	e.login()
	r := e.must(200, "POST", "/api/v1/flows/steps_demo/run", map[string]any{"event": map[string]any{"from": "+6281234567890"}}).JSON
	if len(r["updated"].([]any)) != 1 || get(r, "calls", 0) != "whatsapp_business.send_template" || r["waiting_job"] == nil {
		t.Fatalf("flow: %v", r)
	}
	if l := e.must(200, "GET", "/api/v1/r/lead/LEAD-00031", nil).JSON; l["status"] != "contacted" {
		t.Fatalf("update step: %v", l["status"])
	}
	// the wait put the rest on the queue; let ten minutes pass
	jobID := strconv.Itoa(int(r["waiting_job"].(float64)))
	if _, err := e.s.DB.Exec(context.Background(), "update rk_jobs set scheduled_at = now() where id = "+jobID); err != nil {
		t.Fatal(err)
	}
	var job map[string]any
	for i := 0; i < 100; i++ {
		job = e.must(200, "GET", "/api/v1/system/jobs/"+jobID, nil).JSON
		if job["state"] == "completed" || job["state"] == "retryable" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if job["state"] != "completed" || !strings.HasPrefix(get(job, "result", "created", 0).(string), "activity/ACT-") || job["queue"] != "default" || job["org"] != "kopi-senja" {
		t.Fatalf("resumed job: %v", job)
	}
	st := e.must(200, "GET", "/api/v1/system/status", nil).JSON
	if c := get(st, "connectors", 0); get(c, "events_24h") != 1.0 || get(c, "calls_24h") != 1.0 || get(c, "ok") != true || get(c, "last_event_at") == nil {
		t.Fatalf("connector health: %v", c)
	}
	// no lead: the else branch notifies
	r = e.must(200, "POST", "/api/v1/flows/steps_demo/run", map[string]any{"event": map[string]any{"from": "+620000"}}).JSON
	if get(r, "notifications", 0, "text") != "Tidak ada lead untuk +620000" {
		t.Fatalf("else branch: %v", r)
	}
}

func TestConfigAppsMustBeInPlan(t *testing.T) {
	dir := t.TempDir()
	exec.Command("cp", "-r", filepath.Join(workspace, "rangka-apps/sites/kopi-senja"), dir+"/site").Run()
	p := dir + "/site/config.rk"
	b, _ := os.ReadFile(p)
	os.WriteFile(p, []byte(strings.Replace(string(b), "plan: tumbuh", "plan: mulai", 1)), 0o644)
	_, err := LoadRegistry(Config{AppsDir: filepath.Join(workspace, "rangka-apps/apps"), SiteDir: dir + "/site", Schemas: schema.Embedded()})
	if err == nil || !strings.Contains(err.Error(), `app_not_in_plan apps/2 plan "mulai" does not include "sales"`) {
		t.Fatalf("expected refusal, got %v", err)
	}
}

// A rule in one overlay reads another overlay's field through a link (customer.x_customer_group).
func TestOverlayFieldThroughLinkAtRuntime(t *testing.T) {
	e := newEnv(t, false, func(apps, site string) {
		os.MkdirAll(filepath.Join(site, "custom"), 0o755)
		for _, f := range []string{"customer.rk", "sales_invoice.rk"} {
			b, _ := os.ReadFile(filepath.Join("../../testdata/overlay-site/custom", f))
			os.WriteFile(filepath.Join(site, "custom", f), b, 0o644)
		}
	})
	e.login()
	if c := e.must(200, "PATCH", "/api/v1/r/customer/CUST-0012", map[string]any{"x_customer_group": "grosir"}).JSON; c["x_customer_group"] != "grosir" {
		t.Fatalf("customer: %v", c)
	}
	inv := map[string]any{"customer": "CUST-0012", "outlet": "OUT-01", "items": []any{map[string]any{"item": "BRG-0101", "qty": 1}}}
	bad := e.must(422, "POST", "/api/v1/r/sales_invoice", inv).JSON
	if get(bad, "errors", 0, "rule") != "po_for_wholesale" {
		t.Fatalf("rule: %v", bad)
	}
	inv["x_po_number"] = "PO-77"
	if ok := e.must(201, "POST", "/api/v1/r/sales_invoice", inv).JSON; ok["x_po_number"] != "PO-77" {
		t.Fatalf("invoice: %v", ok)
	}
	inv["customer"], inv["x_po_number"] = "CUST-0031", "" // not wholesale: no PO needed
	e.must(201, "POST", "/api/v1/r/sales_invoice", inv)
}
