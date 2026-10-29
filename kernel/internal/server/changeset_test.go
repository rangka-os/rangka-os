package server

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
)

// addRegion is the organisation-layer way to add a field: an overlay in custom/.
func addRegion() map[string]any {
	return map[string]any{"title": "Tambah wilayah di peluang", "reason": "Tim ingin melihat pipeline per wilayah",
		"author": map[string]any{"kind": "ai", "id": "rangka-ai", "model": "claude"},
		"ops": []any{map[string]any{"op": "create", "path": "custom/deal.rk", "content": `# yaml-language-server: $schema=https://rangka.dev/schema/0.4/overlay.json
model: deal
fields:
  - {name: x_region, type: data, label: {id: Wilayah}, in_list: true}
views:
  form: {add: [{field: x_region, after: source}]}
`}}}
}

func TestChangeSets(t *testing.T) {
	e := newEnv(t, false, nil)
	e.login()
	tok := e.must(201, "POST", "/api/v1/tokens", map[string]any{"name": "ai"}).JSON["token"].(string)
	ai := &env{t: t, ts: e.ts, token: tok}

	// editing an app file is high risk; an overlay in custom/ is medium (organisation layer 4)
	if hi := ai.must(201, "POST", "/api/v1/changesets", map[string]any{"title": "Ubah file app", "ops": []any{map[string]any{"op": "patch", "path": "apps/crm/models/deal.rk",
		"patch": []any{map[string]any{"op": "replace", "path": "/label/id", "value": "Peluang usaha"}}}}}).JSON; hi["risk"] != "high" {
		t.Fatalf("app edit risk: %v", hi["risk"])
	}
	cs := ai.must(201, "POST", "/api/v1/changesets", addRegion()).JSON
	id := cs["id"].(string)
	if cs["status"] != "proposed" || cs["risk"] != "medium" || get(cs, "layers", 0) != 4.0 || get(cs, "author", "kind") != "ai" || cs["created_at"] == nil || cs["updated_at"] == nil {
		t.Fatalf("proposed: %v", cs)
	}
	if r := ai.do("POST", "/api/v1/changesets", map[string]any{"title": "x", "ops": []any{}}); r.Status != 422 {
		t.Fatalf("bad change set: %d %s", r.Status, r.Body)
	}
	if v := ai.must(200, "POST", "/api/v1/changesets/"+id+"/validate", nil).JSON; v["status"] != "validated" {
		t.Fatalf("validate: %v", v)
	}
	// approval needs the preview first
	e.must(409, "POST", "/api/v1/changesets/"+id+"/approve", nil)
	pv := ai.must(200, "POST", "/api/v1/changesets/"+id+"/preview", nil).JSON
	if pv["status"] != "previewed" || len(get(pv, "preview", "migrations").([]any)) != 0 || get(pv, "preview", "affected", "deal") != 7.0 ||
		get(pv, "preview", "files", 0, "path") != "custom/deal.rk" || get(pv, "preview", "files", 0, "before") != nil ||
		!strings.Contains(get(pv, "preview", "files", 0, "after").(string), "x_region") {
		t.Fatalf("preview: %v", pv["preview"])
	}
	overlay := filepath.Join(e.site, "custom", "deal.rk")
	if _, err := os.Stat(overlay); err == nil {
		t.Fatal("preview wrote the file")
	}
	if r := ai.do("POST", "/api/v1/changesets/"+id+"/approve", nil); r.Status != 403 {
		t.Fatalf("token approved: %d", r.Status)
	}
	ai.must(409, "POST", "/api/v1/changesets/"+id+"/apply", nil)
	req, _ := http.NewRequest("GET", e.ts.URL+"/api/v1/events", nil)
	for _, c := range e.jar.Cookies(req.URL) {
		req.AddCookie(c)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	time.Sleep(50 * time.Millisecond)
	e.must(200, "POST", "/api/v1/changesets/"+id+"/approve", map[string]any{"note": "ok"})
	ap := e.must(200, "POST", "/api/v1/changesets/"+id+"/apply", nil).JSON
	if ap["status"] != "applied" {
		t.Fatalf("apply: %v", ap)
	}
	evs := readEvents(t, res.Body, 2, 3*time.Second)
	if evs[0]["_event"] != "changeset" || evs[0]["status"] != "approved" || evs[1]["status"] != "applied" || evs[1]["id"] != id {
		t.Fatalf("events: %v", evs)
	}
	// the overlay file is in the site, the app file is untouched, the model is merged live
	if b, err := os.ReadFile(overlay); err != nil || !strings.Contains(string(b), "x_region") {
		t.Fatalf("overlay not written: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(e.apps, "crm", "models", "deal.rk")); strings.Contains(string(b), "x_region") {
		t.Fatal("app file changed")
	}
	m := e.must(200, "GET", "/api/v1/meta", nil).JSON
	if !strings.Contains(string(mustJSON(get(m, "models", "deal", "fields"))), `"x_region"`) || get(m, "models", "deal", "overlay") != "custom/deal.rk" {
		t.Fatal("meta not reloaded with the overlay")
	}
	d := e.must(201, "POST", "/api/v1/r/deal", map[string]any{"deal_name": "Jakarta", "customer": "CUST-0012", "x_region": "Jabodetabek", "expected_close": "2026-12-01"}).JSON
	if d["x_region"] != "Jabodetabek" {
		t.Fatalf("overlay field not stored: %v", d)
	}
	if got := e.must(200, "GET", "/api/v1/r/deal/"+d["name"].(string), nil).JSON; got["x_region"] != "Jabodetabek" {
		t.Fatalf("overlay field not read back: %v", got)
	}
	if f := e.must(200, "GET", "/api/v1/files/custom/deal.rk", nil).JSON; f["kind"] != "overlay" || f["layer"] != 4.0 {
		t.Fatalf("file: %v", f)
	}
	hs := []string{}
	for _, h := range e.must(200, "GET", "/api/v1/changesets/"+id, nil).JSON["history"].([]any) {
		hs = append(hs, get(h, "status").(string))
	}
	if strings.Join(hs, ",") != "proposed,validated,previewed,approved,applied" {
		t.Fatalf("history: %v", hs)
	}
	// revert removes the overlay; the stored values stay in the record's x column
	if rv := e.must(200, "POST", "/api/v1/changesets/"+id+"/revert", nil).JSON; rv["status"] != "reverted" {
		t.Fatalf("revert: %v", rv)
	}
	if _, err := os.Stat(overlay); err == nil {
		t.Fatal("revert left the overlay")
	}
	if m := e.must(200, "GET", "/api/v1/meta", nil).JSON; strings.Contains(string(mustJSON(get(m, "models", "deal", "fields"))), `"x_region"`) {
		t.Fatal("meta still has x_region after revert")
	}
	if res, err := e.s.DB.Query(context.Background(), "select x->>'x_region' from r_deal where name = $1", d["name"]); err != nil || res.Rows[0][0] != "Jabodetabek" {
		t.Fatalf("data must be kept after revert: %v %v", err, res)
	}
	audit := e.must(200, "GET", "/api/v1/system/audit?kind=changeset&actor=ceo@saga.co.id", nil).JSON["data"].([]any)
	if get(audit, 0, "kind") != "changeset.reverted" || get(audit, 0, "org") != "kopi-senja" {
		t.Fatalf("audit: %v", audit)
	}
	e.must(403, "GET", "/api/v1/system/audit?org=elsewhere", nil)

	// risk: themes low (6), system.rk high (2), a money field in an overlay high
	theme := e.must(201, "POST", "/api/v1/changesets", map[string]any{"title": "Warna merek lebih gelap", "ops": []any{map[string]any{"op": "patch", "path": "themes/kopi_senja.rk",
		"patch": []any{map[string]any{"op": "replace", "path": "/color/brand", "value": "#7a4520"}}}}}).JSON
	if theme["risk"] != "low" || get(theme, "layers", 0) != 6.0 {
		t.Fatalf("theme risk: %v %v", theme["risk"], theme["layers"])
	}
	sys := e.must(201, "POST", "/api/v1/changesets", map[string]any{"title": "Lebih banyak worker", "ops": []any{map[string]any{"op": "patch", "path": "system.rk",
		"patch": []any{map[string]any{"op": "replace", "path": "/queue/workers", "value": 8}}}}}).JSON
	if sys["risk"] != "high" || get(sys, "layers", 0) != 2.0 {
		t.Fatalf("system risk: %v %v", sys["risk"], sys["layers"])
	}
	money := e.must(201, "POST", "/api/v1/changesets", map[string]any{"title": "Diskon faktur", "ops": []any{map[string]any{"op": "create", "path": "custom/sales_invoice.rk",
		"content": "# yaml-language-server: $schema=https://rangka.dev/schema/0.4/overlay.json\nmodel: sales_invoice\nfields:\n  - {name: x_discount, type: currency}\n"}}}).JSON
	if money["risk"] != "high" {
		t.Fatalf("money field risk: %v", money["risk"])
	}
	// high risk: only the owner approves (a sales_manager is an approver for medium/low)
	if _, err := auth.EnsureUser(context.Background(), e.s.DB, "manager@kopisenja.id", "Manajer", "rahasia-123", "kopi-senja", "sales_manager", "USR-002"); err != nil {
		t.Fatal(err)
	}
	mgr := &env{t: t, ts: e.ts}
	mgr.jar, _ = cookiejar.New(nil)
	mgr.must(200, "POST", "/auth/login", map[string]any{"email": "manager@kopisenja.id", "password": "rahasia-123"})
	mid := money["id"].(string)
	e.must(200, "POST", "/api/v1/changesets/"+mid+"/preview", nil)
	if r := mgr.do("POST", "/api/v1/changesets/"+mid+"/approve", nil); r.Status != 403 || get(r.JSON, "error", "code") != "owner_required" {
		t.Fatalf("manager approved high risk: %d %s", r.Status, r.Body)
	}
	tid := theme["id"].(string)
	e.must(200, "POST", "/api/v1/changesets/"+tid+"/preview", nil)
	mgr.must(200, "POST", "/api/v1/changesets/"+tid+"/approve", nil)
	mgr.must(200, "POST", "/api/v1/changesets/"+tid+"/apply", nil)
	if m := e.must(200, "GET", "/api/v1/meta", nil).JSON; get(m, "theme", "tokens", "light", "brand") != "#7a4520" || get(m, "theme", "overrides", "brand", "dark_derived") != true {
		t.Fatalf("theme not live: %v", get(m, "theme", "overrides"))
	}
	// an invalid change: a link to a model that does not exist
	bad := e.must(201, "POST", "/api/v1/changesets", map[string]any{"title": "Overlay rusak", "ops": []any{map[string]any{"op": "create", "path": "custom/lead.rk",
		"content": "# yaml-language-server: $schema=https://rangka.dev/schema/0.4/overlay.json\nmodel: lead\nfields:\n  - {name: x_place, type: link, to: placee}\n"}}}).JSON
	bv := e.must(200, "POST", "/api/v1/changesets/"+bad["id"].(string)+"/validate", nil).JSON
	if bv["status"] != "invalid" || get(bv, "validation", "errors", 0, "code") != "unknown_model" || get(bv, "validation", "errors", 0, "file") != "custom/lead.rk" {
		t.Fatalf("invalid: %v", bv["validation"])
	}
	e.must(409, "POST", "/api/v1/changesets/"+bad["id"].(string)+"/approve", nil)
	e.must(200, "POST", "/api/v1/changesets/"+bad["id"].(string)+"/reject", map[string]any{"note": "salah"})
}

func TestAutoApplyLowRisk(t *testing.T) {
	e := newEnv(t, false, func(apps, site string) {
		p := filepath.Join(site, "config.rk")
		b, _ := os.ReadFile(p)
		os.WriteFile(p, []byte(strings.Replace(string(b), "auto_apply: never", "auto_apply: low_risk", 1)), 0o644)
	})
	e.login()
	tok := e.must(201, "POST", "/api/v1/tokens", map[string]any{"name": "ai"}).JSON["token"].(string)
	ai := &env{t: t, ts: e.ts, token: tok}
	cs := ai.must(201, "POST", "/api/v1/changesets", map[string]any{"title": "Warna grafik", "ops": []any{map[string]any{"op": "patch", "path": "themes/kopi_senja.rk",
		"patch": []any{map[string]any{"op": "replace", "path": "/color/chart-1", "value": "#0055cc"}}}}}).JSON
	if cs["risk"] != "low" {
		t.Fatalf("risk %v", cs["risk"])
	}
	ai.must(200, "POST", "/api/v1/changesets/"+cs["id"].(string)+"/validate", nil)
	ap := ai.must(200, "POST", "/api/v1/changesets/"+cs["id"].(string)+"/apply", nil).JSON
	if ap["status"] != "applied" {
		t.Fatalf("auto apply: %v", ap)
	}
	if m := e.must(200, "GET", "/api/v1/meta", nil).JSON; get(m, "theme", "tokens", "light", "chart-1") != "#0055cc" {
		t.Fatalf("theme: %v", get(m, "theme", "tokens", "light", "chart-1"))
	}
	// medium risk still needs approval
	cs2 := ai.must(201, "POST", "/api/v1/changesets", addRegion()).JSON
	ai.must(200, "POST", "/api/v1/changesets/"+cs2["id"].(string)+"/validate", nil)
	ai.must(409, "POST", "/api/v1/changesets/"+cs2["id"].(string)+"/apply", nil)
}
