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

// cloudEnv starts kopi-senja (config.rk plan tumbuh) with the Cloud package plans in features.rk.
func cloudEnv(t *testing.T) *env {
	t.Helper()
	e := newEnv(t, false, func(apps, site string) {
		f := filepath.Join(site, "features.rk")
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte(strings.TrimRight(string(b), "\n")+"\n"+cloudPlans), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	e.login()
	return e
}

// A business on a Cloud package (contract 0028 + 0040 §9, card t_56c86525): `rangka plan set cloud_juragan` limits
// outlets and people and shapes the traffic, the apps stay those of config.rk's plan, a package change applies at
// once, and so does a change set that edits the package's limits in features.rk. No restart anywhere.
func TestCloudPackagePlan(t *testing.T) {
	e := cloudEnv(t)
	ctx := context.Background()
	setPlan(e, "cloud_juragan")

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

	// a change set that edits the package in features.rk applies at once: cloud_pro down to 4 outlets
	cs := e.must(201, "POST", "/api/v1/changesets", map[string]any{"title": "Batas paket Pro", "ops": []any{map[string]any{
		"op": "patch", "path": "features.rk", "patch": []any{map[string]any{"op": "replace", "path": "/plans/3/limits/outlets", "value": 4}}}}}).JSON
	id, _ := cs["id"].(string)
	if id == "" {
		t.Fatalf("propose: %v", cs)
	}
	e.must(200, "POST", "/api/v1/changesets/"+id+"/validate", nil)
	e.must(200, "POST", "/api/v1/changesets/"+id+"/preview", nil)
	e.must(200, "POST", "/api/v1/changesets/"+id+"/approve", map[string]any{"note": "uji"})
	e.must(200, "POST", "/api/v1/changesets/"+id+"/apply", nil)
	refreshPlan(e)
	rep = e.must(200, "GET", "/api/v1/plan", nil).JSON
	if get(rep, "plan", "name") != "cloud_pro" || get(rep, "usage", "outlets", "limit") != float64(4) || get(rep, "usage", "outlets", "used") != float64(4) {
		t.Fatalf("after the change set %v", get(rep, "usage", "outlets"))
	}
	r = e.must(403, "POST", "/api/v1/r/outlet", map[string]any{"outlet_name": "Kopi Senja · Bekasi"})
	if errCode(r) != "plan_limit" || !strings.Contains(errMsg(r), "Paket Pro berlaku untuk 4 outlet") {
		t.Fatalf("fifth outlet after the change set: %s", r.Body)
	}
	// back to the company's own plan (Cloud stops managing it): config.rk's tumbuh again
	if err := plan.Clear(ctx, e.s.DB, e.s.Engine.Reg(), "test"); err != nil {
		t.Fatal(err)
	}
	refreshPlan(e)
	if get(e.must(200, "GET", "/api/v1/plan", nil).JSON, "plan", "name") != "tumbuh" {
		t.Fatal("clear did not go back to config.rk's plan")
	}
}
