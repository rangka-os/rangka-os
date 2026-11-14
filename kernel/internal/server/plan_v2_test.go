package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/plan"
)

// v2Plans are plans with the limits of contract 0057: one app, no file space, one PDF a month.
const v2Plans = `  - {name: satu_app, label: {id: "Satu App"}, limits: {apps: 1, files_mb: 0, pdf_month: 1, db_mb: 1000000}}
  - {name: dua_app, label: {id: "Dua App"}, limits: {apps: 2, files_mb: 100, pdf_month: 1000, db_mb: 1000000}}
`

func v2Env(t *testing.T) *env {
	t.Helper()
	e := newEnv(t, false, func(apps, site string) {
		f := filepath.Join(site, "features.rk")
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte(strings.TrimRight(string(b), "\n")+"\n"+v2Plans), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	e.login()
	return e
}

// Contract 0057: the apps limit makes the newest app read-only after a downgrade and refuses a change set that
// installs past it; a grant lifts it, a pool cap holds it with the pool message; files_mb refuses uploads;
// pdf_month slows PDFs; GET /api/v1/plan reports all of it.
func TestPlanLimitsV2(t *testing.T) {
	e := v2Env(t)
	ctx := context.Background()
	inv := e.newInvoice() // kopi-senja installs core, crm, sales: two apps count

	setPlan(e, "satu_app")
	rep := e.must(200, "GET", "/api/v1/plan", nil).JSON
	if get(rep, "usage", "apps", "used") != float64(2) || get(rep, "usage", "apps", "limit") != float64(1) || get(rep, "usage", "apps", "state") != "over" {
		t.Fatalf("apps usage: %v", get(rep, "usage", "apps"))
	}
	if ro, _ := get(rep, "read_only", "apps").([]any); len(ro) != 1 || ro[0] != "sales" {
		t.Fatalf("read_only apps: %v", get(rep, "read_only"))
	}
	for _, k := range []string{"db_mb", "files_mb", "pdf_month"} {
		if _, ok := get(rep, "usage", k).(map[string]any); !ok {
			t.Fatalf("usage %s missing: %v", k, rep["usage"])
		}
	}
	// writing on the newest app's records is read-only; reading is not
	r := e.must(403, "PATCH", "/api/v1/r/sales_invoice/"+inv, map[string]any{"remarks": "uji"})
	if errCode(r) != "plan_read_only" || !strings.Contains(errMsg(r), "berlaku untuk 1 app") {
		t.Fatalf("write on sales past the apps limit: %s", r.Body)
	}
	e.must(200, "GET", "/api/v1/r/sales_invoice/"+inv, nil)

	// a grant of one more app lifts it
	g, err := plan.AddGrant(ctx, e.s.DB, plan.Apps, 1, nil, "ADDON-1", "test", "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	refreshPlan(e)
	if s := get(e.must(200, "GET", "/api/v1/plan", nil).JSON, "usage", "apps"); get(s, "limit") != float64(2) || get(s, "plan_limit") != float64(1) || get(s, "state") != "full" {
		t.Fatalf("apps after grant: %v", s)
	}
	e.must(200, "PATCH", "/api/v1/r/sales_invoice/"+inv, map[string]any{"remarks": "uji"})

	// a pool cap holds it again, with the pool's message
	if err := plan.SetCap(ctx, e.s.DB, plan.Apps, 1, "pool", "test", time.Now()); err != nil {
		t.Fatal(err)
	}
	refreshPlan(e)
	if msg := get(e.must(200, "GET", "/api/v1/plan", nil).JSON, "usage", "apps", "message"); msg != "Kolam Juragan Grup sudah penuh. Tambah kapasitas atau kurangi pemakaian di bisnis lain." {
		t.Fatalf("pool message: %v", msg)
	}
	if err := plan.RemoveCap(ctx, e.s.DB, plan.Apps, "test"); err != nil {
		t.Fatal(err)
	}
	refreshPlan(e)

	// files_mb 0: an upload is refused, nothing stored
	up := e.upload("/api/v1/r/sales_invoice/"+inv+"/attachments", "po.pdf", pdfBytes)
	if up.Status != 403 || errCode(up) != "plan_limit" || !strings.Contains(errMsg(up), "Penyimpanan file") {
		t.Fatalf("upload with no file space: %d %s", up.Status, up.Body)
	}
	if err := plan.RevokeGrant(ctx, e.s.DB, g.ID, "test"); err != nil {
		t.Fatal(err)
	}
	refreshPlan(e)

	// a change set that installs more apps than the plan allows is invalid; keeping as many is fine
	cs := e.must(201, "POST", "/api/v1/changesets", map[string]any{"title": "Pasang lagi", "ops": []any{map[string]any{"op": "patch", "path": "config.rk",
		"patch": []any{map[string]any{"op": "replace", "path": "/apps", "value": []any{"core", "sales", "crm"}}}}}}).JSON
	if v := e.must(200, "POST", "/api/v1/changesets/"+cs["id"].(string)+"/validate", nil).JSON; v["status"] != "validated" {
		t.Fatalf("reordering the installed apps: %v", v)
	}
	if _, err := plan.Set(ctx, e.s.DB, e.s.Engine.Reg(), "satu_app", "test", ""); err != nil {
		t.Fatal(err)
	}
	if err := plan.SetCap(ctx, e.s.DB, plan.Apps, 0, "", "test", time.Now()); err != nil { // even core-only is "full"
		t.Fatal(err)
	}
	// with the current two apps counted as "before", a third would be needed to refuse: check the rule directly
	cur, err := plan.Resolve(ctx, e.s.DB, e.s.Engine.Reg())
	if err != nil {
		t.Fatal(err)
	}
	if pe := plan.CheckAppsChange(e.s.Engine.Reg(), cur, 2, 3); pe == nil || pe.Code != "plan_limit" {
		t.Fatalf("adding a third app: %+v", pe)
	}
}

// pdf_month: past the limit PDFs are slowed to 10 an hour (429 pdf_slowed with Retry-After), never refused for
// the month; the next hour gives 10 more.
func TestPDFSlowed(t *testing.T) {
	e := v2Env(t)
	ctx := context.Background()
	setPlan(e, "satu_app")
	reg := e.s.Engine.Reg()
	at := time.Date(2026, 11, 14, 10, 15, 0, 0, time.UTC)
	if err := plan.TakePDF(ctx, e.s.DB, reg, at); err != nil { // the month's one
		t.Fatal(err)
	}
	for i := 0; i < plan.PDFSlowPerHour; i++ {
		if err := plan.TakePDF(ctx, e.s.DB, reg, at); err != nil {
			t.Fatalf("slowed %d: %v", i, err)
		}
	}
	err := plan.TakePDF(ctx, e.s.DB, reg, at)
	pe, ok := err.(*plan.Error)
	if !ok || pe.Code != "pdf_slowed" || pe.RetryAfter <= 0 || !strings.Contains(pe.Msg, "10 per jam") {
		t.Fatalf("12th PDF in an hour: %v", err)
	}
	if err := plan.TakePDF(ctx, e.s.DB, reg, at.Add(time.Hour)); err != nil {
		t.Fatalf("next hour: %v", err)
	}
	if used, _ := plan.PDFUsed(ctx, e.s.DB, plan.Period(reg, at)); used != 12 {
		t.Fatalf("pdf used = %d, want 12 (the refused one not counted)", used)
	}
}
