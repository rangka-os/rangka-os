package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
	"github.com/rangka-os/rangka-os/kernel/internal/engine"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/plan"
	"github.com/rangka-os/rangka-os/kernel/internal/sandbox"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

// Contract 0052: the ruang coba, a sandbox org with demo data that sends nothing and changes no metadata.

const sb = "kopi-senja-coba"

// visitModel is a company model linking to another company model and to a catalogue model, so the generator
// has to order them and fill links from fixtures and from its own rows.
const visitModel = "# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/model.json\n" +
	"name: x_kunjungan\nnaming: KNJ-{####}\nlabel: {id: Kunjungan}\nfields:\n" +
	"  - {name: akun, type: link, to: x_akun, label: {id: Akun}, required: true}\n" +
	"  - {name: customer, type: link, to: customer, label: {id: Pelanggan}}\n" +
	"  - {name: tanggal, type: date, label: {id: Tanggal}, required: true}\n" +
	"  - {name: hasil, type: select, label: {id: Hasil}, options: [{value: baik, label: {id: Baik}}, {value: buruk, label: {id: Buruk}}]}\n"

func sandboxEnv(t *testing.T) *env {
	t.Helper()
	e := newEnv(t, false, func(apps, site string) {
		for f, body := range map[string]string{"x_akun.rk": accountModel, "x_kunjungan.rk": visitModel} {
			if err := os.WriteFile(filepath.Join(apps, "crm", "models", f), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	})
	e.login()
	return e
}

func demoFixtures(t *testing.T) map[string][]map[string]any {
	t.Helper()
	fx, err := engine.LoadFixtures(testdb.Catalogue("fixtures"))
	if err != nil {
		t.Fatal(err)
	}
	return fx
}

func (e *env) count(org, table string) int64 {
	e.t.Helper()
	res, err := e.s.DB.Query(pg.WithOrg(context.Background(), org), "select count(*) from "+pg.QuoteIdent(table)+" where org_id = $1", org)
	if err != nil {
		e.t.Fatal(err)
	}
	return res.Rows[0][0].(int64)
}

func TestSandboxLifecycle(t *testing.T) {
	e := sandboxEnv(t)
	fx := demoFixtures(t)
	if get(e.must(200, "GET", "/api/v1/sandbox", nil).JSON, "org") != nil {
		t.Fatal("a fresh site has no sandbox")
	}
	out := e.must(201, "POST", "/api/v1/sandbox", map[string]any{"fixtures": fx}).JSON
	if out["org"] != sb || get(out, "seeded", "created", "customer") == nil ||
		get(out, "seeded", "created", "x_akun") != 8.0 || get(out, "seeded", "created", "x_kunjungan") != 8.0 {
		t.Fatalf("create: %v", out)
	}
	if r := e.do("POST", "/api/v1/sandbox", map[string]any{"fixtures": fx}); r.Status != 409 || get(r.JSON, "error", "code") != "sandbox_exists" {
		t.Fatalf("second sandbox: %d %s", r.Status, r.Body)
	}
	if st := e.must(200, "GET", "/api/v1/sandbox", nil).JSON; st["org"] != sb || st["expires_at"] == nil {
		t.Fatalf("status: %v", st)
	}
	me := e.must(200, "GET", "/auth/me", nil).JSON
	if get(me, "orgs", 0, "id") != "kopi-senja" || get(me, "orgs", 0, "sandbox") != false ||
		get(me, "orgs", 1, "id") != sb || get(me, "orgs", 1, "sandbox") != true || get(me, "orgs", 1, "code") != "COBA" {
		t.Fatalf("me: %v", me["orgs"])
	}

	// a real record stays out of the sandbox; generated company rows link to fixtures and to each other
	e.must(201, "POST", "/api/v1/r/customer", map[string]any{"customer_name": "Hanya usaha asli"})
	e.must(200, "POST", "/auth/org", map[string]any{"org": sb})
	if l := e.must(200, "GET", "/api/v1/r/customer?limit=200", nil); strings.Contains(string(l.Body), "Hanya usaha asli") {
		t.Fatal("a real record is visible in the sandbox")
	}
	v := e.must(200, "GET", "/api/v1/r/x_kunjungan/KUNJUNGAN-001", nil).JSON
	if v["akun"] != "AKUN-001" || v["customer"] == nil || v["customer"] == "" || v["tanggal"] == nil {
		t.Fatalf("generated row: %v", v)
	}
	if r := e.do("POST", "/api/v1/sandbox/reset", nil); r.Status != 409 || get(r.JSON, "error", "code") != "sandbox_inside" {
		t.Fatalf("reset from inside: %d %s", r.Status, r.Body)
	}
	e.must(201, "POST", "/api/v1/r/customer", map[string]any{"customer_name": "Coba-coba"})
	e.must(200, "POST", "/auth/org", map[string]any{"org": "kopi-senja"})

	// reset: the demo comes back as it was, the try-out row is gone, the owner is still a member
	before := e.count(sb, "r_customer")
	e.must(200, "POST", "/api/v1/sandbox/reset", nil)
	if after := e.count(sb, "r_customer"); after != before-1 {
		t.Fatalf("reset: %d customers, want %d", after, before-1)
	}
	if n := e.count(sb, "rk_members"); n != 1 {
		t.Fatalf("reset dropped members: %d", n)
	}

	// delete: every row goes, the real org keeps its own
	real := e.count("kopi-senja", "r_customer")
	e.must(204, "DELETE", "/api/v1/sandbox", nil)
	if n := e.count(sb, "r_customer") + e.count(sb, "r_x_akun") + e.count(sb, "rk_members"); n != 0 {
		t.Fatalf("delete left %d rows", n)
	}
	if e.count("kopi-senja", "r_customer") != real {
		t.Fatal("delete touched the real org")
	}
	if res, _ := e.s.DB.Query(context.Background(), "select 1 from rk_orgs where id = $1", sb); len(res.Rows) != 0 {
		t.Fatal("sandbox org row survived")
	}
	e.must(404, "DELETE", "/api/v1/sandbox", nil)
	if res, _ := e.s.DB.Query(context.Background(), "select kind from rk_audit where org_id = 'kopi-senja' and kind like 'sandbox.%' order by id"); len(res.Rows) != 3 {
		t.Fatalf("audit in the real org: %v", res.Rows)
	}
}

func TestSandboxOwnerOnly(t *testing.T) {
	e := sandboxEnv(t)
	rep := repEnv(t, e)
	if r := rep.do("POST", "/api/v1/sandbox", map[string]any{}); r.Status != 403 || get(r.JSON, "error", "code") != "owner_required" {
		t.Fatalf("rep creates a sandbox: %d %s", r.Status, r.Body)
	}
}

var trailingNumber = regexp.MustCompile(`(\d+)$`)

func serial(t *testing.T, name string) int {
	t.Helper()
	m := trailingNumber.FindStringSubmatch(name)
	if m == nil {
		t.Fatalf("no number in %s", name)
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// A sandbox invoice never takes a number of the real series.
func TestSandboxNumbering(t *testing.T) {
	e := sandboxEnv(t)
	e.must(201, "POST", "/api/v1/sandbox", map[string]any{"fixtures": demoFixtures(t)})
	submit := func() string {
		name := e.newInvoice()
		return e.must(200, "POST", "/api/v1/r/sales_invoice/"+name+"/submit", map[string]any{}).JSON["name"].(string)
	}
	first := submit()
	e.must(200, "POST", "/auth/org", map[string]any{"org": sb})
	inSandbox := submit()
	e.must(200, "POST", "/auth/org", map[string]any{"org": "kopi-senja"})
	second := submit()
	if serial(t, second) != serial(t, first)+1 {
		t.Fatalf("the sandbox took a real number: %s, %s (sandbox %s)", first, second, inSandbox)
	}
	// the sandbox counts in its own series (names may repeat across orgs, contract 0010 §3; a pattern with {ORG}
	// shows COBA in them)
	if n := e.count(sb, "rk_number_series") + e.count(sb, "rk_naming"); n == 0 {
		t.Fatalf("the sandbox has no series of its own (invoice %s)", inSandbox)
	}
}

// Change sets are refused from the sandbox: metadata is shared with the real org.
func TestSandboxChangesetRefused(t *testing.T) {
	e := sandboxEnv(t)
	e.must(201, "POST", "/api/v1/sandbox", map[string]any{"fixtures": demoFixtures(t)})
	e.must(200, "POST", "/auth/org", map[string]any{"org": sb})
	body := map[string]any{"title": "Tambah wilayah", "ops": []any{map[string]any{"op": "create", "path": "custom/deal.rk",
		"content": "# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.4/overlay.json\nmodel: deal\nfields:\n  - {name: x_region, type: data}\n"}}}
	if r := e.do("POST", "/api/v1/changesets", body); r.Status != 409 || get(r.JSON, "error", "code") != "sandbox_readonly_meta" {
		t.Fatalf("propose from the sandbox: %d %s", r.Status, r.Body)
	}
	e.must(200, "POST", "/auth/org", map[string]any{"org": "kopi-senja"})
	e.must(201, "POST", "/api/v1/changesets", body)
}

// The rk_org cookie never signs anyone in to the sandbox; asking for it explicitly does.
func TestSandboxLoginLandsInRealOrg(t *testing.T) {
	e := sandboxEnv(t)
	e.must(201, "POST", "/api/v1/sandbox", map[string]any{"fixtures": demoFixtures(t)})
	e.must(200, "POST", "/auth/org", map[string]any{"org": sb}) // sets rk_org=kopi-senja-coba
	e.must(204, "POST", "/auth/logout", nil)
	e.login()
	if org := get(e.must(200, "GET", "/auth/me", nil).JSON, "org", "id"); org != "kopi-senja" {
		t.Fatalf("login with the sandbox cookie entered %v", org)
	}
	e.must(200, "POST", "/auth/login", map[string]any{"email": "ceo@saga.co.id", "password": "rangka-demo", "org": sb})
	if org := get(e.must(200, "GET", "/auth/me", nil).JSON, "org", "id"); org != sb {
		t.Fatalf("explicit sandbox login entered %v", org)
	}
	// a person who is a member of the sandbox only signs in there (staff in training)
	if _, err := auth.EnsureUser(context.Background(), e.s.DB, "latih@example.test", "Latih", "rangka-demo", sb, "sales_rep", ""); err != nil {
		t.Fatal(err)
	}
	e.must(204, "POST", "/auth/logout", nil)
	e.loginAs("latih@example.test")
	if org := get(e.must(200, "GET", "/auth/me", nil).JSON, "org", "id"); org != sb {
		t.Fatalf("trainee entered %v", org)
	}
}

// Members and outlets of the sandbox do not count against the plan.
func TestSandboxNotCounted(t *testing.T) {
	e := sandboxEnv(t)
	ctx := context.Background()
	before, err := plan.Measure(ctx, e.s.DB, e.s.Engine.Reg(), fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	e.must(201, "POST", "/api/v1/sandbox", map[string]any{"fixtures": demoFixtures(t)})
	if _, err := auth.EnsureUser(ctx, e.s.DB, "latih@example.test", "Latih", "rangka-demo", sb, "sales_rep", ""); err != nil {
		t.Fatal(err)
	}
	if err := plan.CheckNewPerson(ctx, e.s.DB, sb, "baru@example.test"); err != nil {
		t.Fatalf("a sandbox-only person was checked against the plan: %v", err)
	}
	after, err := plan.Measure(ctx, e.s.DB, e.s.Engine.Reg(), fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	if after.Users != before.Users || after.Outlets != before.Outlets {
		t.Fatalf("usage moved: before %+v after %+v", before, after)
	}
}

// A connector action from the sandbox is simulated: nothing reaches the vendor, and webhooks are not routed to it.
func TestSandboxConnectorSimulated(t *testing.T) {
	e, v := runtimeEnv(t)
	e.must(201, "POST", "/api/v1/sandbox", map[string]any{"fixtures": demoFixtures(t)})
	id, err := e.s.Queue.Enqueue(context.Background(), nil, "call", sb, map[string]any{"connector": "whatsapp_business", "action": "send_template",
		"input": map[string]any{"to": "6281234", "template": "halo", "idempotency_key": "coba-1"}}, engine.EnqueueOpts{MaxAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	if st, errs := waitJob(t, e, id); st != "completed" {
		t.Fatalf("simulated call: %s %s", st, errs)
	}
	if n := len(v.sent()); n != 0 {
		t.Fatalf("the sandbox sent %d requests", n)
	}
	all := dump(t, e, "select coalesce(result::text, '') from rk_jobs where org_id = '"+sb+"'") +
		dump(t, e, "select kind from rk_audit where org_id = '"+sb+"'")
	if !strings.Contains(all, `"simulated": true`) && !strings.Contains(all, `"simulated":true`) || !strings.Contains(all, "connector.simulated") {
		t.Fatalf("simulation not recorded: %s", all)
	}
	r, err := http.Post(e.ts.URL+"/hooks/"+sb+"/whatsapp_business/message", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != 404 {
		t.Fatalf("webhook into the sandbox: %d", r.StatusCode)
	}
}

// A sandbox nobody used for IdleDays is deleted.
func TestSandboxExpires(t *testing.T) {
	e := sandboxEnv(t)
	e.must(201, "POST", "/api/v1/sandbox", map[string]any{"fixtures": demoFixtures(t)})
	ctx := context.Background()
	if gone, err := sandbox.Expire(ctx, e.s.Engine, time.Now()); err != nil || len(gone) != 0 {
		t.Fatalf("fresh sandbox expired: %v %v", gone, err)
	}
	gone, err := sandbox.Expire(ctx, e.s.Engine, time.Now().Add((sandbox.IdleDays+1)*24*time.Hour))
	if err != nil || len(gone) != 1 || gone[0] != sb {
		t.Fatalf("expire: %v %v", gone, err)
	}
	if e.count(sb, "r_customer") != 0 {
		t.Fatal("expired sandbox kept rows")
	}
}
