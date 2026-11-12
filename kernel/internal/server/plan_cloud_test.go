package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
	"github.com/rangka-os/rangka-os/kernel/internal/plan"
)

// cloudPlans are the package plans rangka-cloud writes into a business's features.rk (provision.writePackagePlans:
// one flow-style cloud_<tier> plan per package, a label and limits only, rate when the router shapes too). They
// carry no apps: the apps follow config.rk's plan, which Cloud leaves alone.
const cloudPlans = `  - {name: cloud_juragan, label: {id: "Juragan"}, limits: {users: 4, outlets: 3, ai_credits_month: 50, rate: {rps: 2, burst: 2, max_wait_ms: 600, max_waiting: 10}}}
  - {name: cloud_pro, label: {id: "Pro"}, limits: {users: 10, outlets: 6, ai_credits_month: 500, rate: {rps: 50, burst: 100, max_wait_ms: 3000, max_waiting: 50}}}
`

// cloudEnv starts kopi-senja (config.rk plan tumbuh) with the Cloud package plans in features.rk; base is the
// site's own features.rk, which Cloud keeps when it writes its plans again.
func cloudEnv(t *testing.T) (e *env, base string) {
	t.Helper()
	e = newEnv(t, false, func(apps, site string) {
		f := filepath.Join(site, "features.rk")
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		base = strings.TrimRight(string(b), "\n") + "\n"
		if err := os.WriteFile(f, []byte(base+cloudPlans), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	e.login()
	return e, base
}

// A business on a Cloud package (contract 0028 + 0040 §9, card t_56c86525): `rangka plan set cloud_juragan` limits
// outlets and people and shapes the traffic, the apps stay those of config.rk's plan, a package change applies at
// once, and so does the operator's edit of the package's limits. While an operator set the plan the business may
// not change features.rk through a change set, and a person the plan made read-only decides no change set. On the
// company's own plan a change set to features.rk applies at once. No restart anywhere.
func TestCloudPackagePlan(t *testing.T) {
	e, base := cloudEnv(t)
	ctx := context.Background()
	// before Cloud manages the plan (an older business, or a package step that failed), the company applied a change
	// set to features.rk; reverting it once an operator set the plan is refused like a new one (audit F-5)
	early := e.must(201, "POST", "/api/v1/changesets", map[string]any{"title": "Batas Pro sendiri", "ops": []any{map[string]any{
		"op": "patch", "path": "features.rk", "patch": []any{map[string]any{"op": "replace", "path": "/plans/3/limits/ai_credits_month", "value": 600}}}}}).JSON
	earlyID, _ := early["id"].(string)
	e.must(200, "POST", "/api/v1/changesets/"+earlyID+"/validate", nil)
	e.must(200, "POST", "/api/v1/changesets/"+earlyID+"/preview", nil)
	e.must(200, "POST", "/api/v1/changesets/"+earlyID+"/approve", map[string]any{"note": "uji"})
	e.must(200, "POST", "/api/v1/changesets/"+earlyID+"/apply", nil)
	setPlan(e, "cloud_juragan")
	if r := e.must(403, "POST", "/api/v1/changesets/"+earlyID+"/revert", nil); errCode(r) != "plan_managed" {
		t.Fatalf("revert of a features.rk change set under an operator's plan: %s", r.Body)
	}

	rep := e.must(200, "GET", "/api/v1/plan", nil).JSON
	if get(rep, "plan", "name") != "cloud_juragan" || get(rep, "plan", "source") != "operator" || get(rep, "usage", "outlets", "limit") != float64(3) {
		t.Fatalf("plan report %v", rep)
	}
	// the apps are config.rk's (tumbuh), not the package's (it names none)
	if _, ok := get(e.must(200, "GET", "/api/v1/meta", nil).JSON, "models").(map[string]any)["sales_invoice"]; !ok {
		t.Fatal("sales_invoice gone on a Cloud package")
	}

	// outlets (0028): 2 in the fixtures, the third fills the package, the fourth is refused
	e.must(201, "POST", "/api/v1/r/outlet", map[string]any{"outlet_name": "Kopi Senja · Depok"})
	r := e.must(403, "POST", "/api/v1/r/outlet", map[string]any{"outlet_name": "Kopi Senja · Bogor"})
	if errCode(r) != "plan_limit" || !strings.Contains(errMsg(r), "Paket Juragan berlaku untuk 3 outlet") {
		t.Fatalf("fourth outlet on cloud_juragan: %s", r.Body)
	}
	// people (0028): 4 on the package; the owner is one of them
	added := 0
	var refused error
	for _, m := range []string{"a@kopisenja.id", "b@kopisenja.id", "c@kopisenja.id", "d@kopisenja.id", "e@kopisenja.id"} {
		if err := plan.CheckNewPerson(ctx, e.s.DB, m); err != nil {
			refused = err
			break
		}
		if _, err := auth.EnsureUser(ctx, e.s.DB, m, m, "rahasia-123", "kopi-senja", "sales_manager", ""); err != nil {
			t.Fatal(err)
		}
		added++
	}
	var pe *plan.Error
	if refused == nil || !asPlanError(refused, &pe) || pe.Code != "plan_limit" || pe.Resource != "users" {
		t.Fatalf("people past cloud_juragan's 4 (added %d): %v", added, refused)
	}

	// traffic (0040 §9): 2 a second, a burst of 2, at most 600 ms of waiting. Six requests at once: two pass, the
	// next waits (X-Rangka-Throttle-Ms), and those that would wait past 600 ms get 429 with Retry-After.
	time.Sleep(1100 * time.Millisecond)
	type got struct {
		status         int
		code, throttle string
		retry          string
	}
	out := make(chan got, 6)
	for i := 0; i < 6; i++ {
		go func() {
			r := e.do("GET", "/api/v1/meta", nil)
			code := ""
			if r.Status != 200 {
				code = errCode(r)
			}
			out <- got{r.Status, code, r.Header.Get("X-Rangka-Throttle-Ms"), r.Header.Get("Retry-After")}
		}()
	}
	ok, held, limited := 0, 0, 0
	for i := 0; i < 6; i++ {
		g := <-out
		switch {
		case g.status == 200 && g.throttle != "":
			held++
		case g.status == 200:
			ok++
		case g.status == 429 && g.code == "rate_limited" && g.retry != "":
			limited++
		default:
			t.Fatalf("unexpected answer under cloud_juragan's rate: %+v", g)
		}
	}
	if ok != 2 || held < 1 || limited < 1 {
		t.Fatalf("cloud_juragan's rate: %d served, %d held, %d refused", ok, held, limited)
	}

	// the package changes (Cloud runs rangka plan set again): limits and rate follow, no restart
	setPlan(e, "cloud_pro")
	time.Sleep(1100 * time.Millisecond)
	for i := 0; i < 10; i++ {
		e.must(200, "GET", "/api/v1/meta", nil)
	}
	e.must(201, "POST", "/api/v1/r/outlet", map[string]any{"outlet_name": "Kopi Senja · Bogor"})
	if err := plan.CheckNewPerson(ctx, e.s.DB, "f@kopisenja.id"); err != nil {
		t.Fatalf("a person on cloud_pro: %v", err)
	}

	// while an operator set the plan, the plans are the operator's: a change set may not touch features.rk, so the
	// business cannot raise its own package (audit cloud-package-plan F-1). The operator changes the file and runs
	// rangka plan set; here the file is rewritten and reloaded, then the plan set again, all without a restart.
	r = e.must(403, "POST", "/api/v1/changesets", map[string]any{"title": "Naikkan paket sendiri", "ops": []any{map[string]any{
		"op": "patch", "path": "features.rk", "patch": []any{map[string]any{"op": "replace", "path": "/plans/3/limits/outlets", "value": 99}}}}})
	if errCode(r) != "plan_managed" {
		t.Fatalf("a change set to features.rk under an operator's plan: %s", r.Body)
	}
	// Cloud writes its package plans again (provision.writePackagePlans keeps the company's own plans)
	if err := os.WriteFile(filepath.Join(e.site, "features.rk"), []byte(base+strings.Replace(cloudPlans, "users: 10, outlets: 6,", "users: 10, outlets: 4,", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := e.s.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	setPlan(e, "cloud_pro")
	rep = e.must(200, "GET", "/api/v1/plan", nil).JSON
	if get(rep, "plan", "name") != "cloud_pro" || get(rep, "usage", "outlets", "limit") != float64(4) || get(rep, "usage", "outlets", "used") != float64(4) {
		t.Fatalf("after the operator's change %v", get(rep, "usage", "outlets"))
	}
	r = e.must(403, "POST", "/api/v1/r/outlet", map[string]any{"outlet_name": "Kopi Senja · Bekasi"})
	if errCode(r) != "plan_limit" || !strings.Contains(errMsg(r), "Paket Pro berlaku untuk 4 outlet") {
		t.Fatalf("fifth outlet after the operator's change: %s", r.Body)
	}
	// rangka user add (no metadata) reads the limits Reload synced to rk_site_plan
	if st, err := plan.LoadStored(ctx, e.s.DB); err != nil || st == nil || st.Plan != "cloud_pro" || st.Limits["outlets"] != 4 {
		t.Fatalf("rk_site_plan after the operator's change: %+v %v", st, err)
	}
	// down to a smaller package: nothing is deleted, the newest outlet past 3 becomes read-only (0028 §4), and a
	// person past the people limit decides no change set either, not even one raising the limit (audit F-2)
	setPlan(e, "cloud_juragan")
	rep = e.must(200, "GET", "/api/v1/plan", nil).JSON
	if ro, _ := get(rep, "read_only", "outlets").([]any); get(rep, "usage", "outlets", "state") != "over" || len(ro) != 1 {
		t.Fatalf("cloud_juragan with 4 outlets: %v %v", get(rep, "usage", "outlets"), get(rep, "read_only"))
	}
	if _, err := auth.EnsureUser(ctx, e.s.DB, "f@kopisenja.id", "F", "rahasia-123", "kopi-senja", "sales_manager", ""); err != nil {
		t.Fatal(err)
	}
	refreshPlan(e)
	cs := e.must(201, "POST", "/api/v1/changesets", map[string]any{"title": "Warna merek", "ops": []any{map[string]any{"op": "patch", "path": "themes/kopi_senja.rk",
		"patch": []any{map[string]any{"op": "replace", "path": "/color/brand", "value": "#7a4520"}}}}}).JSON
	id, _ := cs["id"].(string)
	e.must(200, "POST", "/api/v1/changesets/"+id+"/validate", nil)
	e.must(200, "POST", "/api/v1/changesets/"+id+"/preview", nil)
	f2 := &env{t: t, s: e.s, ts: e.ts, apps: e.apps, site: e.site, jar: newJar()}
	f2.must(200, "POST", "/auth/login", map[string]any{"email": "f@kopisenja.id", "password": "rahasia-123"})
	if r := f2.must(403, "POST", "/api/v1/changesets/"+id+"/approve", nil); errCode(r) != "plan_read_only" {
		t.Fatalf("a read-only person approves: %s", r.Body)
	}
	if r := f2.must(403, "POST", "/api/v1/changesets/"+id+"/reject", map[string]any{"note": "x"}); errCode(r) != "plan_read_only" {
		t.Fatalf("a read-only person rejects: %s", r.Body)
	}
	// back to the company's own plan (Cloud stops managing it): config.rk's tumbuh again
	if err := plan.Clear(ctx, e.s.DB, e.s.Engine.Reg(), "test"); err != nil {
		t.Fatal(err)
	}
	refreshPlan(e)
	if get(e.must(200, "GET", "/api/v1/plan", nil).JSON, "plan", "name") != "tumbuh" {
		t.Fatal("clear did not go back to config.rk's plan")
	}
	// the company's own plan (no operator): a change set to features.rk applies at once, tumbuh up to 8 outlets
	cs = e.must(201, "POST", "/api/v1/changesets", map[string]any{"title": "Batas Tumbuh", "ops": []any{map[string]any{
		"op": "patch", "path": "features.rk", "patch": []any{map[string]any{"op": "replace", "path": "/plans/1/limits/outlets", "value": 8}}}}}).JSON
	id, _ = cs["id"].(string)
	e.must(200, "POST", "/api/v1/changesets/"+id+"/validate", nil)
	e.must(200, "POST", "/api/v1/changesets/"+id+"/preview", nil)
	e.must(200, "POST", "/api/v1/changesets/"+id+"/approve", map[string]any{"note": "uji"})
	e.must(200, "POST", "/api/v1/changesets/"+id+"/apply", nil)
	refreshPlan(e)
	if get(e.must(200, "GET", "/api/v1/plan", nil).JSON, "usage", "outlets", "limit") != float64(8) {
		t.Fatal("a change set to the company's own plan did not apply")
	}
}
