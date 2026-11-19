package server

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

// Portal pages (contract 0073): customer links to one document.

func portalEnv(t *testing.T) *env {
	t.Helper()
	return newEnvFrom(t, func(dir string) (string, string) {
		src := filepath.Join(testdb.RepoRoot(), "kernel", "testdata", "portal")
		if err := exec.Command("cp", "-r", src+"/.", dir).Run(); err != nil {
			t.Fatal(err)
		}
		return filepath.Join(dir, "apps"), filepath.Join(dir, "site")
	})
}

// customer is a browser without credentials.
func (e *env) customer() *env { return &env{t: e.t, s: e.s, ts: e.ts, jar: newJar()} }

// portalInvoice creates and submits an invoice of 2 x 500.000 with a payment row; it returns its name.
func portalInvoice(e *env, paid float64, withPayment bool) string {
	e.t.Helper()
	cust := e.must(201, "POST", "/api/v1/r/pelanggan", map[string]any{"title": "Toko Maju"}).JSON["name"].(string)
	inv := e.must(201, "POST", "/api/v1/r/faktur", map[string]any{"customer": cust, "posting_date": "2026-10-30", "due_date": "2026-11-15",
		"grand_total": 1000000, "paid_amount": paid, "margin_note": "MARGIN-RAHASIA", "notes": "Terima kasih",
		"items": []any{map[string]any{"description": "Kopi susu literan", "qty": 2, "rate": 500000, "cost": 123456}}}).JSON["name"].(string)
	if withPayment {
		e.must(201, "POST", "/api/v1/r/cara_bayar", map[string]any{"faktur": inv, "bank": "BCA", "account_no": "1234567890", "holder": "PT Kopi",
			"reference": inv, "qris_payload": "00020101021126570011ID.CO.QRIS.WWW0215ID10203040506070303UMI5204581253033605802ID5909KOPI SENJA6007JAKARTA6304ABCD",
			"qris_merchant_name": "KOPI SENJA", "internal_code": "KODE-INTERNAL"})
	}
	return inv
}

func TestPortalLinkFlow(t *testing.T) {
	e := portalEnv(t)
	e.login()
	inv := portalInvoice(e, 0, true)

	// a draft is not shared
	e.must(409, "POST", "/api/v1/portal/tagihan/links", map[string]any{"record": inv})
	e.must(200, "POST", "/api/v1/r/faktur/"+inv+"/submit", nil)
	e.must(404, "POST", "/api/v1/portal/nope/links", map[string]any{"record": inv})
	e.must(400, "POST", "/api/v1/portal/tagihan/links", map[string]any{"record": inv, "days": 31})
	e.must(404, "POST", "/api/v1/portal/tagihan/links", map[string]any{"record": "FK-9999"})
	if pages := e.must(200, "GET", "/api/v1/portal", nil).JSON["data"].([]any); len(pages) != 1 || get(pages, 0, "name") != "tagihan" {
		t.Fatalf("pages = %v", pages)
	}
	link := e.must(201, "POST", "/api/v1/portal/tagihan/links", map[string]any{"record": inv}).JSON
	path := link["path"].(string)
	if !strings.HasPrefix(path, "/p/kopi-senja/tagihan/") || !strings.HasSuffix(link["url"].(string), path) {
		t.Fatalf("link = %v", link)
	}
	token := path[strings.LastIndex(path, "/")+1:]
	if len(token) != 43 {
		t.Fatalf("token %q", token)
	}
	if exp := link["expires_at"].(string); !strings.HasPrefix(exp, "2026-11-29") {
		t.Fatalf("expires_at = %s, want 30 days after 30 Oct", exp)
	}

	c := e.customer()
	shell := c.must(200, "GET", path, nil)
	for h, want := range map[string]string{"Referrer-Policy": "no-referrer", "Cache-Control": "no-store", "X-Robots-Tag": "noindex, nofollow"} {
		if got := shell.Header.Get(h); got != want {
			t.Errorf("%s = %q, want %q", h, got, want)
		}
	}
	if csp := shell.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") || !strings.Contains(string(shell.Body), "/portal/portal.js") {
		t.Fatalf("shell csp %q body %s", csp, shell.Body)
	}
	data := c.must(200, "GET", path+"/data", nil)
	body := string(data.Body)
	for _, leak := range []string{"MARGIN-RAHASIA", "margin_note", "123456", "cost", "KODE-INTERNAL", token} {
		if strings.Contains(body, leak) {
			t.Errorf("data leaks %q: %s", leak, body)
		}
	}
	d := data.JSON
	if get(d, "doc", "state") != "open" || get(d, "doc", "customer") != "Toko Maju" || get(d, "doc", "amount", "value") != float64(1000000) ||
		get(d, "doc", "due", "value") != "2026-11-15" || get(d, "org", "name") == "" || get(d, "print") != true {
		t.Fatalf("doc = %v", d)
	}
	if rows := get(d, "lines", "rows").([]any); len(rows) != 1 || get(rows, 0, "description") != "Kopi susu literan" || get(rows, 0, "amount") != float64(1000000) {
		t.Fatalf("lines = %v", get(d, "lines"))
	}
	if q, _ := get(d, "payment", "qris", "image").(string); !strings.HasPrefix(q, "data:image/png;base64,") || get(d, "payment", "qris", "label") != "KOPI SENJA" {
		t.Fatalf("qris = %v", get(d, "payment", "qris"))
	}
	if n := len(get(d, "payment", "fields").([]any)); n != 4 {
		t.Fatalf("payment fields = %v", get(d, "payment", "fields"))
	}
	pr := c.must(200, "GET", path+"/print", nil)
	if s := string(pr.Body); !strings.Contains(s, "Faktur "+inv) || strings.Contains(s, "MARGIN-RAHASIA") || strings.Contains(s, "123456") {
		t.Fatalf("print = %s", s)
	}
	if !strings.Contains(pr.Header.Get("Content-Security-Policy"), "default-src 'none'") {
		t.Fatalf("print csp = %q", pr.Header.Get("Content-Security-Policy"))
	}

	// every miss looks the same: one character changed, another org, another page, a malformed token
	other := token[:42] + map[bool]string{true: "B", false: "A"}[token[42] == 'A']
	for _, p := range []string{"/p/kopi-senja/tagihan/" + other, "/p/lain/tagihan/" + token, "/p/kopi-senja/lain/" + token, "/p/kopi-senja/tagihan/pendek"} {
		if r := c.do("GET", p+"/data", nil); r.Status != 404 || get(r.JSON, "error", "code") != "portal_link_unknown" {
			t.Fatalf("%s = %d %s", p, r.Status, r.Body)
		}
		c.must(404, "GET", p, nil)
	}
	// views are audited without the token; the link counts them
	if n := dbOne(e, "select count(*) from rk_audit where kind = 'portal.view' and actor = $1", "portal:"+link["id"].(string)).(int64); n != 2 {
		t.Fatalf("portal.view entries = %d", n)
	}
	if n := dbOne(e, "select count(*) from rk_audit where data::text like $1 or summary like $1", "%"+token+"%").(int64); n != 0 {
		t.Fatalf("token in audit")
	}
	links := e.must(200, "GET", "/api/v1/portal/tagihan/links?record="+inv, nil).JSON["data"].([]any)
	if len(links) != 1 || get(links, 0, "views") != float64(2) || strings.Contains(string(mustJSON(links)), token) {
		t.Fatalf("links = %v", links)
	}

	// revoked: 410 like an expired one
	if r := e.must(200, "POST", "/api/v1/portal/tagihan/links/revoke", map[string]any{"record": inv}).JSON; r["revoked"] != float64(1) {
		t.Fatalf("revoke = %v", r)
	}
	if r := c.do("GET", path+"/data", nil); r.Status != 410 || get(r.JSON, "error", "code") != "portal_link_expired" {
		t.Fatalf("revoked = %d %s", r.Status, r.Body)
	}
	c.must(410, "GET", path, nil)
	c.must(410, "GET", path+"/print", nil)

	// expired
	p2 := e.must(201, "POST", "/api/v1/portal/tagihan/links", map[string]any{"record": inv, "days": 1}).JSON
	c.must(200, "GET", p2["path"].(string)+"/data", nil)
	if _, err := e.s.DB.Exec(context.Background(), "update rk_portal_links set expires_at = $2 where id = $1", p2["id"], fixedNow().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	c.must(410, "GET", p2["path"].(string)+"/data", nil)
}

func TestPortalPaidAndCancelled(t *testing.T) {
	e := portalEnv(t)
	e.login()
	paid := portalInvoice(e, 1000000, true)
	e.must(200, "POST", "/api/v1/r/faktur/"+paid+"/submit", nil)
	c := e.customer()
	path := e.must(201, "POST", "/api/v1/portal/tagihan/links", map[string]any{"record": paid}).JSON["path"].(string)
	d := c.must(200, "GET", path+"/data", nil).JSON
	if get(d, "doc", "state") != "paid" || d["payment"] != nil {
		t.Fatalf("paid = %v", d)
	}

	open := portalInvoice(e, 0, true)
	e.must(200, "POST", "/api/v1/r/faktur/"+open+"/submit", nil)
	path = e.must(201, "POST", "/api/v1/portal/tagihan/links", map[string]any{"record": open}).JSON["path"].(string)
	e.must(200, "POST", "/api/v1/r/faktur/"+open+"/cancel", nil)
	// the link of a cancelled document stays readable, without payment or print, and names its amendment
	d = c.must(200, "GET", path+"/data", nil).JSON
	if get(d, "doc", "state") != "cancelled" || d["payment"] != nil || d["print"] != false || get(d, "doc", "replaced_by") != "" {
		t.Fatalf("cancelled = %v", d)
	}
	c.must(404, "GET", path+"/print", nil)
	e.must(409, "POST", "/api/v1/portal/tagihan/links", map[string]any{"record": open})
	amended := e.must(200, "POST", "/api/v1/r/faktur/"+open+"/amend", nil).JSON["name"].(string)
	if got := get(c.must(200, "GET", path+"/data", nil).JSON, "doc", "replaced_by"); got != amended {
		t.Fatalf("replaced_by = %v, want %s", got, amended)
	}
}

func TestPortalShareRoles(t *testing.T) {
	e := portalEnv(t)
	e.login()
	inv := portalInvoice(e, 0, false)
	e.must(200, "POST", "/api/v1/r/faktur/"+inv+"/submit", nil)
	ctx := context.Background()
	for email, role := range map[string]string{"kasir@toko.test": "kasir", "uang@toko.test": "keuangan"} {
		if _, err := auth.EnsureUser(ctx, e.s.DB, email, "Staf", "rahasia-123", "kopi-senja", role, ""); err != nil {
			t.Fatal(err)
		}
	}
	kasir := e.device()
	kasir.must(200, "POST", "/auth/login", map[string]any{"email": "kasir@toko.test", "password": "rahasia-123"})
	kasir.must(403, "POST", "/api/v1/portal/tagihan/links", map[string]any{"record": inv})
	kasir.must(403, "POST", "/api/v1/portal/tagihan/links/revoke", map[string]any{"record": inv})
	if pages := kasir.must(200, "GET", "/api/v1/portal", nil).JSON["data"].([]any); len(pages) != 0 {
		t.Fatalf("kasir pages = %v", pages)
	}
	uang := e.device()
	uang.must(200, "POST", "/auth/login", map[string]any{"email": "uang@toko.test", "password": "rahasia-123"})
	path := uang.must(201, "POST", "/api/v1/portal/tagihan/links", map[string]any{"record": inv}).JSON["path"].(string)
	// no payment row: the page has no payment block
	if d := e.customer().must(200, "GET", path+"/data", nil).JSON; d["payment"] != nil {
		t.Fatalf("payment = %v", d["payment"])
	}
	if n := dbOne(e, "select count(*) from rk_audit where kind = 'portal.link.create' and actor = 'uang@toko.test'").(int64); n != 1 {
		t.Fatalf("create audit = %d", n)
	}
}

func TestPortalRateLimit(t *testing.T) {
	e := portalEnv(t)
	c := e.customer()
	limited := 0
	for i := 0; i < 45; i++ {
		r := c.do("GET", "/p/kopi-senja/tagihan/"+strings.Repeat("A", 43)+"/data", nil)
		if r.Status == 429 {
			limited++
			if r.Header.Get("Retry-After") == "" {
				t.Fatal("429 without Retry-After")
			}
		}
	}
	if limited == 0 {
		t.Fatal("45 requests from one address were never limited")
	}
}
