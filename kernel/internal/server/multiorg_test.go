package server

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
	"github.com/rangka-os/rangka-os/kernel/internal/engine"
)

// twoOrgEnv adds a second org (pt_beta) next to kopi-senja: the seed user belongs to both,
// beta@example.test only to pt_beta.
func twoOrgEnv(t *testing.T) *env {
	t.Helper()
	e := newEnv(t, false, nil)
	ctx := context.Background()
	db := e.s.DB
	if err := auth.EnsureOrgCode(ctx, db, "pt_beta", "PT Beta", "BETA"); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.EnsureUser(ctx, db, "ceo@saga.co.id", "", "rangka-demo", "pt_beta", "owner", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.EnsureUser(ctx, db, "beta@example.test", "Beta", "rangka-demo", "pt_beta", "owner", ""); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) loginAs(email string) {
	e.t.Helper()
	e.must(200, "POST", "/auth/login", map[string]any{"email": email, "password": "rangka-demo"})
}

func TestAuthMeListsOrgs(t *testing.T) {
	e := twoOrgEnv(t)
	e.login()
	me := e.must(200, "GET", "/auth/me", nil).JSON
	if get(me, "org", "id") != "kopi-senja" || get(me, "orgs", 0, "id") != "kopi-senja" || get(me, "orgs", 1, "id") != "pt_beta" ||
		get(me, "orgs", 1, "code") != "BETA" || get(me, "orgs", 1, "name") != "PT Beta" || get(me, "orgs", 1, "roles", 0) != "owner" {
		t.Fatalf("me: %v", me)
	}
	if me["via"] != "session" || me["approver"] != true || get(me, "user", "outlets") == nil {
		t.Fatalf("me keeps old fields: %v", me)
	}
	if n := len(me["orgs"].([]any)); n != 2 {
		t.Fatalf("orgs: %d", n)
	}
}

func TestSwitchOrgIsolation(t *testing.T) {
	e := twoOrgEnv(t)
	e.login()
	c := e.must(201, "POST", "/api/v1/r/customer", map[string]any{"customer_name": "Hanya Kopi Senja"}).JSON
	name := c["name"].(string)
	r := e.must(200, "POST", "/auth/org", map[string]any{"org": "pt_beta"})
	if get(r.JSON, "org", "id") != "pt_beta" || len(r.JSON["orgs"].([]any)) != 2 {
		t.Fatalf("switch body: %v", r.JSON)
	}
	ck := r.Header.Get("Set-Cookie")
	for _, want := range []string{"rk_org=pt_beta", "Path=/", "Max-Age=31536000", "SameSite=Lax"} {
		if !strings.Contains(ck, want) {
			t.Fatalf("cookie %q lacks %s", ck, want)
		}
	}
	if strings.Contains(ck, "HttpOnly") {
		t.Fatalf("rk_org must be readable by the web app: %s", ck)
	}
	l := e.must(200, "GET", "/api/v1/r/customer?limit=200", nil)
	if strings.Contains(string(l.Body), "Hanya Kopi Senja") {
		t.Fatal("record of kopi-senja visible in pt_beta")
	}
	e.must(404, "GET", "/api/v1/r/customer/"+name, nil)
	if me := e.must(200, "GET", "/auth/me", nil).JSON; get(me, "org", "id") != "pt_beta" {
		t.Fatalf("session org: %v", me)
	}
	e.must(200, "POST", "/auth/org", map[string]any{"org": "kopi-senja"})
	l = e.must(200, "GET", "/api/v1/r/customer?limit=200", nil)
	if !strings.Contains(string(l.Body), "Hanya Kopi Senja") {
		t.Fatal("record missing after switching back")
	}
	e.must(200, "GET", "/api/v1/r/customer/"+name, nil)
}

func TestSwitchToForeignOrg403(t *testing.T) {
	e := twoOrgEnv(t)
	e.loginAs("beta@example.test")
	r := e.must(403, "POST", "/auth/org", map[string]any{"org": "kopi-senja"})
	if get(r.JSON, "error", "code") != "permission_denied" || get(r.JSON, "error", "model") != "org" || get(r.JSON, "error", "action") != "switch" {
		t.Fatalf("body: %s", r.Body)
	}
	if me := e.must(200, "GET", "/auth/me", nil).JSON; get(me, "org", "id") != "pt_beta" {
		t.Fatalf("org changed: %v", me)
	}
}

func TestLoginNoMembership(t *testing.T) {
	e := twoOrgEnv(t)
	ctx := context.Background()
	if _, err := e.s.DB.Query(ctx, "insert into rk_users (email, full_name, password_hash) values ('lone@example.test', 'Lone', $1)", mustHash(t)); err != nil {
		t.Fatal(err)
	}
	r := e.must(403, "POST", "/auth/login", map[string]any{"email": "lone@example.test", "password": "rangka-demo"})
	if get(r.JSON, "error", "code") != "no_membership" {
		t.Fatalf("body: %s", r.Body)
	}
}

func mustHash(t *testing.T) string {
	t.Helper()
	h, err := auth.HashPassword("rangka-demo")
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestTokenOrgFixed(t *testing.T) {
	e := twoOrgEnv(t)
	e.login()
	tok := e.must(201, "POST", "/api/v1/tokens", map[string]any{"name": "cli"}).JSON["token"].(string)
	e.token = tok
	r := e.must(400, "POST", "/auth/org", map[string]any{"org": "pt_beta"})
	if get(r.JSON, "error", "code") != "org_fixed" {
		t.Fatalf("body: %s", r.Body)
	}
}

func TestRemovedMemberLosesSession(t *testing.T) {
	e := twoOrgEnv(t)
	e.loginAs("beta@example.test")
	e.must(200, "GET", "/auth/me", nil)
	res, err := e.s.DB.Query(context.Background(), "select id from rk_users where email = 'beta@example.test'")
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.RemoveMember(context.Background(), e.s.DB, "pt_beta", res.Rows[0][0].(int64)); err != nil {
		t.Fatal(err)
	}
	e.must(401, "GET", "/auth/me", nil)
}

func TestOrgsListOnlyMine(t *testing.T) {
	e := twoOrgEnv(t)
	e.loginAs("beta@example.test")
	l := e.must(200, "GET", "/api/v1/orgs", nil).JSON
	d := l["data"].([]any)
	if len(d) != 1 || get(l, "data", 0, "id") != "pt_beta" || get(l, "data", 0, "code") != "BETA" || get(l, "data", 0, "current") != true || get(l, "data", 0, "role") != "owner" {
		t.Fatalf("orgs: %v", l)
	}
	if get(l, "data", 0, "usage") == nil {
		t.Fatalf("usage missing: %v", l)
	}
	e2 := twoOrgEnv(t)
	e2.login()
	if n := len(e2.must(200, "GET", "/api/v1/orgs", nil).JSON["data"].([]any)); n != 2 {
		t.Fatalf("ceo sees %d orgs", n)
	}
}

func TestMembersOfForeignOrg403(t *testing.T) {
	e := twoOrgEnv(t)
	e.loginAs("beta@example.test")
	r := e.must(403, "GET", "/api/v1/orgs/kopi-senja/members", nil)
	if get(r.JSON, "error", "code") != "permission_denied" || get(r.JSON, "error", "model") != "org" || get(r.JSON, "error", "action") != "read" {
		t.Fatalf("body: %s", r.Body)
	}
	e.must(200, "GET", "/api/v1/orgs/pt_beta/members", nil)
}

func TestDevOpenMeHasOrg(t *testing.T) {
	e := newEnv(t, true, nil)
	me := e.must(200, "GET", "/auth/me", nil).JSON
	if get(me, "org", "id") != "kopi-senja" || len(me["orgs"].([]any)) != 1 {
		t.Fatalf("me: %v", me)
	}
}

func TestSiteOrgsSynced(t *testing.T) {
	e := newEnv(t, false, func(apps, site string) {
		p := filepath.Join(site, "config.rk")
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		add := "\norgs:\n  pt_alpha:\n    name: PT Alpha\n    code: ALP\n"
		if err := os.WriteFile(p, append(b, add...), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	res, err := e.s.DB.Query(context.Background(), "select name, code from rk_orgs where id = 'pt_alpha'")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 || res.Rows[0][0] != "PT Alpha" || res.Rows[0][1] != "ALP" {
		t.Fatalf("rk_orgs: %v", res.Rows)
	}
}

// ---------- outlet row scope (contract 0010 §5)

const outletStaffRole = "# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/role.json\n" +
	"name: outlet_staff\nlabel: {id: Staf outlet}\nscope: {field: outlet, from: user.outlets}\n"

// scopedEnv adds role outlet_staff (scoped by outlet) and member staff@example.test working at the given
// outlets of kopi-senja. It returns the owner's env (logged in) and the staff member's env (logged in).
// more changes the temp apps and site copies further.
func scopedEnv(t *testing.T, outlets []string, more ...func(apps, site string)) (owner, staff *env) {
	t.Helper()
	e := newEnv(t, false, func(apps, site string) {
		dir := filepath.Join(apps, "core", "roles")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "outlet_staff.rk"), []byte(outletStaffRole), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, f := range more {
			f(apps, site)
		}
	})
	ctx := context.Background()
	id, err := auth.EnsureUser(ctx, e.s.DB, "staff@example.test", "Staf", "rangka-demo", "kopi-senja", "outlet_staff", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.SetOutlets(ctx, e.s.DB, "kopi-senja", id, outlets); err != nil {
		t.Fatal(err)
	}
	e.login()
	jar, _ := cookiejar.New(nil)
	staff = &env{t: t, s: e.s, ts: e.ts, apps: e.apps, site: e.site, jar: jar}
	staff.loginAs("staff@example.test")
	return e, staff
}

func rowsOf(r *resp) []any {
	d, _ := r.JSON["data"].([]any)
	return d
}

func assertDenied(t *testing.T, r *resp, model, action string) {
	t.Helper()
	if r.Status != 403 || get(r.JSON, "error", "code") != "permission_denied" || get(r.JSON, "error", "model") != model || get(r.JSON, "error", "action") != action {
		t.Fatalf("want 403 permission_denied %s/%s, got %d: %s", model, action, r.Status, r.Body)
	}
}

func TestOutletUserListsOnlyOwnOutlet(t *testing.T) {
	owner, staff := scopedEnv(t, []string{"OUT-01"})
	all := owner.must(200, "GET", "/api/v1/r/sales_invoice?limit=500", nil)
	own, other := 0, 0
	for _, r := range rowsOf(all) {
		if get(r, "outlet") == "OUT-01" {
			own++
		} else {
			other++
		}
	}
	if own == 0 || other == 0 {
		t.Fatalf("fixtures need invoices of both outlets: own %d other %d", own, other)
	}
	l := staff.must(200, "GET", "/api/v1/r/sales_invoice?limit=500", nil)
	for _, r := range rowsOf(l) {
		if get(r, "outlet") != "OUT-01" {
			t.Fatalf("staff sees %v of %v", get(r, "name"), get(r, "outlet"))
		}
	}
	if len(rowsOf(l)) != own || l.JSON["total"] != float64(own) {
		t.Fatalf("staff rows %d total %v, want %d", len(rowsOf(l)), l.JSON["total"], own)
	}
	// total counts visible rows only, also when paged
	if p := staff.must(200, "GET", "/api/v1/r/sales_invoice?limit=1", nil); p.JSON["total"] != float64(own) || len(rowsOf(p)) != 1 {
		t.Fatalf("paged: %v rows %d", p.JSON["total"], len(rowsOf(p)))
	}
	o := staff.must(200, "GET", "/api/v1/r/outlet", nil)
	if o.JSON["total"] != 1.0 || get(rowsOf(o)[0], "name") != "OUT-01" {
		t.Fatalf("outlets: %s", o.Body)
	}
	if c := staff.must(200, "GET", "/api/v1/r/customer?limit=500", nil); c.JSON["total"] != owner.must(200, "GET", "/api/v1/r/customer?limit=500", nil).JSON["total"] {
		t.Fatalf("customer is not scoped: %v", c.JSON["total"])
	}
}

func TestOutletUserGetOtherOutlet403(t *testing.T) {
	_, staff := scopedEnv(t, []string{"OUT-01"})
	assertDenied(t, staff.do("GET", "/api/v1/r/sales_invoice/INV-2026-00044", nil), "sales_invoice", "read")
	assertDenied(t, staff.do("GET", "/api/v1/r/outlet/OUT-02", nil), "outlet", "read")
	staff.must(200, "GET", "/api/v1/r/sales_invoice/INV-2026-00043", nil)
	staff.must(200, "GET", "/api/v1/r/outlet/OUT-01", nil)
	// writes and lifecycle on another outlet's record
	assertDenied(t, staff.do("PATCH", "/api/v1/r/sales_invoice/INV-2026-00044", map[string]any{"notes": "x"}), "sales_invoice", "update")
	assertDenied(t, staff.do("POST", "/api/v1/r/sales_invoice/INV-2026-00044/cancel", nil), "sales_invoice", "cancel")
	assertDenied(t, staff.do("DELETE", "/api/v1/r/sales_invoice/INV-2026-00044", nil), "sales_invoice", "delete")
}

func TestOutletUserCannotCreateForOtherOutlet(t *testing.T) {
	owner, staff := scopedEnv(t, []string{"OUT-01"})
	before := owner.must(200, "GET", "/api/v1/r/sales_invoice?limit=1", nil).JSON["total"]
	body := map[string]any{"customer": "CUST-0012", "outlet": "OUT-02", "items": []any{map[string]any{"item": "BRG-0044", "qty": 2}}}
	assertDenied(t, staff.do("POST", "/api/v1/r/sales_invoice", body), "sales_invoice", "create")
	if after := owner.must(200, "GET", "/api/v1/r/sales_invoice?limit=1", nil).JSON["total"]; after != before {
		t.Fatalf("invoices %v → %v", before, after)
	}
	body["outlet"] = "OUT-01"
	staff.must(201, "POST", "/api/v1/r/sales_invoice", body)
}

func TestOutletUserCannotRepointOutlet(t *testing.T) {
	_, staff := scopedEnv(t, []string{"OUT-01"})
	inv := staff.must(201, "POST", "/api/v1/r/sales_invoice", map[string]any{"customer": "CUST-0012", "outlet": "OUT-01",
		"items": []any{map[string]any{"item": "BRG-0044", "qty": 2}}}).JSON
	name := inv["name"].(string)
	assertDenied(t, staff.do("PATCH", "/api/v1/r/sales_invoice/"+name, map[string]any{"outlet": "OUT-02"}), "sales_invoice", "update")
	if r := staff.must(200, "GET", "/api/v1/r/sales_invoice/"+name, nil); r.JSON["outlet"] != "OUT-01" {
		t.Fatalf("outlet changed: %v", r.JSON["outlet"])
	}
	staff.must(200, "PATCH", "/api/v1/r/sales_invoice/"+name, map[string]any{"notes": "ok"})
}

func TestDashboardScopedAndCacheSeparate(t *testing.T) {
	owner, staff := scopedEnv(t, []string{"OUT-01"}, func(apps, _ string) {
		// sales_overview has no cache: in the demo apps; give it one so the cache key is exercised
		p := filepath.Join(apps, "sales", "dashboards", "sales_overview.rk")
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		b = []byte(strings.Replace(string(b), "default_period: 90\n", "default_period: 90\ncache: 5m\n", 1))
		if !strings.Contains(string(b), "cache: 5m") {
			t.Fatal("sales_overview: default_period line not found")
		}
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
	})
	// receivable over OUT-01 only, from the owner's unscoped list
	want := 0.0
	l := owner.must(200, "GET", "/api/v1/r/sales_invoice?limit=500&filter="+url.QueryEscape("docstatus == 1 && paid_amount < grand_total && outlet == 'OUT-01'"), nil)
	for _, r := range rowsOf(l) {
		want += get(r, "grand_total").(float64)
	}
	first := owner.must(200, "GET", "/api/v1/dashboards/sales_overview", nil)
	ownerVal := get(first.JSON, "widgets", 1, "value")
	if ownerVal != 11203230.0 || first.Header.Get("X-Cache") != "miss" {
		t.Fatalf("owner receivable %v cache %s", ownerVal, first.Header.Get("X-Cache"))
	}
	s := staff.must(200, "GET", "/api/v1/dashboards/sales_overview", nil)
	if s.Header.Get("X-Cache") != "miss" {
		t.Fatalf("staff got the owner's cached dashboard")
	}
	if v := get(s.JSON, "widgets", 1, "value"); v != want || v == ownerVal {
		t.Fatalf("staff receivable %v, want %v (owner %v)", v, want, ownerVal)
	}
	again := owner.must(200, "GET", "/api/v1/dashboards/sales_overview", nil)
	if get(again.JSON, "widgets", 1, "value") != ownerVal || again.Header.Get("X-Cache") != "hit" {
		t.Fatalf("owner second call %v cache %s", get(again.JSON, "widgets", 1, "value"), again.Header.Get("X-Cache"))
	}
}

func TestScopedMemberWithoutOutlets(t *testing.T) {
	_, staff := scopedEnv(t, []string{})
	if l := staff.must(200, "GET", "/api/v1/r/sales_invoice", nil); l.JSON["total"] != 0.0 || len(rowsOf(l)) != 0 {
		t.Fatalf("invoices: %s", l.Body)
	}
	if l := staff.must(200, "GET", "/api/v1/r/outlet", nil); l.JSON["total"] != 0.0 {
		t.Fatalf("outlets: %s", l.Body)
	}
	if l := staff.must(200, "GET", "/api/v1/r/customer", nil); l.JSON["total"] == 0.0 {
		t.Fatalf("customers: %s", l.Body)
	}
}

func TestMCPListScoped(t *testing.T) {
	_, staff := scopedEnv(t, []string{"OUT-01"})
	tok := staff.must(201, "POST", "/api/v1/tokens", map[string]any{"name": "agent"}).JSON["token"].(string)
	m := &env{t: t, ts: staff.ts, token: tok}
	q := call(m, "rangka_query", map[string]any{"model": "sales_invoice", "limit": 500})
	data, _ := q["data"].([]any)
	if len(data) == 0 || q["total"] != float64(len(data)) {
		t.Fatalf("query: total %v rows %d", q["total"], len(data))
	}
	for _, r := range data {
		if get(r, "outlet") != "OUT-01" {
			t.Fatalf("MCP shows %v of %v", get(r, "name"), get(r, "outlet"))
		}
	}
	res := rpc(m, 9, "tools/call", map[string]any{"name": "rangka_get_record", "arguments": map[string]any{"model": "sales_invoice", "name": "INV-2026-00044"}})
	if res["isError"] != true {
		t.Fatalf("get of another outlet's invoice: %v", res)
	}
}

func TestSSEScoped(t *testing.T) {
	owner, staff := scopedEnv(t, []string{"OUT-01"})
	req, _ := http.NewRequest("GET", staff.ts.URL+"/api/v1/events?models=sales_invoice", nil)
	for _, c := range staff.jar.Cookies(req.URL) {
		req.AddCookie(c)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("events: %v %v", err, res)
	}
	defer res.Body.Close()
	time.Sleep(50 * time.Millisecond)
	mk := func(outlet string) string {
		body := map[string]any{"customer": "CUST-0012", "outlet": outlet, "items": []any{map[string]any{"item": "BRG-0044", "qty": 1}}}
		return owner.must(201, "POST", "/api/v1/r/sales_invoice", body).JSON["name"].(string)
	}
	other := mk("OUT-02")
	own := mk("OUT-01")
	// events are ordered: if the out-of-scope one were delivered it would be the first to arrive
	evs := readEvents(t, res.Body, 1, 3*time.Second)
	if evs[0]["name"] == other {
		t.Fatalf("event of another outlet delivered: %v", evs[0])
	}
	if evs[0]["model"] != "sales_invoice" || evs[0]["name"] != own || evs[0]["op"] != "created" {
		t.Fatalf("event: %v", evs[0])
	}
	if _, leak := evs[0]["fields"]; leak {
		t.Fatalf("fields must not reach the client: %v", evs[0])
	}
}

// ---------- final review fixes: flow run, jobs, audit

func TestOutletUserCannotRunFlow(t *testing.T) {
	owner, staff := scopedEnv(t, []string{"OUT-01"})
	ev := map[string]any{"event": map[string]any{"from": "+628129999", "profile_name": "Bu Ani", "text": "Halo", "message_id": "wamid.s1"}}
	assertDenied(t, staff.do("POST", "/api/v1/flows/whatsapp_to_lead/run", ev), "flow", "run")
	owner.must(200, "POST", "/api/v1/flows/whatsapp_to_lead/run", ev)
}

// enqueueLater puts a job on the queue that the workers do not pick up during the test.
func enqueueLater(t *testing.T, e *env, org string, args map[string]any) string {
	t.Helper()
	id, err := e.s.Queue.Enqueue(context.Background(), nil, "action", org, args, engine.EnqueueOpts{RunAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	return itoa(int(id))
}

func TestJobOfOtherOrg404(t *testing.T) {
	e := twoOrgEnv(t)
	id := enqueueLater(t, e, "kopi-senja", map[string]any{"model": "customer", "name": "CUST-0001", "action": "x"})
	sys := enqueueLater(t, e, "", map[string]any{"flow": "system"})
	e.login()
	e.must(200, "GET", "/api/v1/system/jobs/"+id, nil)
	e.must(200, "POST", "/auth/org", map[string]any{"org": "pt_beta"})
	e.must(404, "GET", "/api/v1/system/jobs/"+id, nil)
	e.must(200, "GET", "/api/v1/system/jobs/"+sys, nil) // installation-wide jobs show in every org, like the list
	beta := &env{t: t, s: e.s, ts: e.ts, apps: e.apps, site: e.site}
	beta.jar, _ = cookiejar.New(nil)
	beta.loginAs("beta@example.test")
	beta.must(404, "GET", "/api/v1/system/jobs/"+id, nil)
}

func TestScopedMemberSeesOwnJobsOnly(t *testing.T) {
	owner, staff := scopedEnv(t, []string{"OUT-01"})
	mine := enqueueLater(t, owner, "kopi-senja", map[string]any{"model": "sales_invoice", "name": "INV-2026-00043", "action": "x",
		"actor": map[string]any{"id": "staff@example.test"}})
	theirs := enqueueLater(t, owner, "kopi-senja", map[string]any{"model": "sales_invoice", "name": "INV-2026-00044", "action": "x",
		"actor": map[string]any{"id": "ceo@saga.co.id"}})
	l := rowsOf(staff.must(200, "GET", "/api/v1/system/jobs?limit=500", nil))
	if len(l) != 1 || itoa(int(get(l, 0, "id").(float64))) != mine {
		t.Fatalf("staff jobs: %v", l)
	}
	staff.must(200, "GET", "/api/v1/system/jobs/"+mine, nil)
	staff.must(404, "GET", "/api/v1/system/jobs/"+theirs, nil)
	if n := len(rowsOf(owner.must(200, "GET", "/api/v1/system/jobs?limit=500", nil))); n < 2 {
		t.Fatalf("owner jobs: %d", n)
	}
	owner.must(200, "GET", "/api/v1/system/jobs/"+theirs, nil)
}

func TestAuditCurrentOrgOnly(t *testing.T) {
	e := twoOrgEnv(t)
	e.login()
	e.must(201, "POST", "/api/v1/r/customer", map[string]any{"customer_name": "Audit Kopi Senja"})
	e.must(200, "POST", "/auth/org", map[string]any{"org": "pt_beta"})
	e.must(201, "POST", "/api/v1/r/customer", map[string]any{"customer_name": "Audit Beta"})
	for _, x := range rowsOf(e.must(200, "GET", "/api/v1/system/audit?limit=500", nil)) {
		if o := get(x, "org"); o != "pt_beta" && o != "" {
			t.Fatalf("audit in pt_beta shows %v: %v", o, x)
		}
	}
	other := e.must(200, "GET", "/api/v1/system/audit?org=kopi-senja&limit=500", nil)
	if !strings.Contains(string(other.Body), "kopi-senja") {
		t.Fatalf("explicit org of a membership: %s", other.Body)
	}
	beta := &env{t: t, s: e.s, ts: e.ts, apps: e.apps, site: e.site}
	beta.jar, _ = cookiejar.New(nil)
	beta.loginAs("beta@example.test")
	assertDenied(t, beta.do("GET", "/api/v1/system/audit?org=kopi-senja", nil), "org", "read")
}

func TestAuditScopedLeavesOutScopedRecords(t *testing.T) {
	owner, staff := scopedEnv(t, []string{"OUT-01"})
	owner.must(201, "POST", "/api/v1/r/sales_invoice", map[string]any{"customer": "CUST-0012", "outlet": "OUT-02",
		"items": []any{map[string]any{"item": "BRG-0044", "qty": 2}}})
	owner.must(201, "POST", "/api/v1/r/customer", map[string]any{"customer_name": "Audit Pelanggan"})
	l := rowsOf(staff.must(200, "GET", "/api/v1/system/audit?limit=500", nil))
	sawCustomer := false
	for _, x := range l {
		k, _ := get(x, "kind").(string)
		tg, _ := get(x, "target").(string)
		if strings.HasPrefix(k, "record.") && (strings.HasPrefix(tg, "sales_invoice/") || strings.HasPrefix(tg, "outlet/")) {
			t.Fatalf("scoped member sees %s %s", k, tg)
		}
		if strings.HasPrefix(k, "record.") && strings.HasPrefix(tg, "customer/") {
			sawCustomer = true
		}
	}
	if !sawCustomer {
		t.Fatalf("unscoped model entries missing: %v", l)
	}
	if n := len(rowsOf(owner.must(200, "GET", "/api/v1/system/audit?kind=record.create&limit=500", nil))); n < 2 {
		t.Fatalf("owner audit: %d", n)
	}
}
