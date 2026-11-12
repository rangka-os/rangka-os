package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// rateEnv starts kopi-senja on plan tumbuh with a small rate: 2 requests a second, a burst of 2, no waiting.
func rateEnv(t *testing.T, rate string) *env {
	t.Helper()
	e := newEnv(t, false, func(apps, site string) {
		f := filepath.Join(site, "features.rk")
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		s := strings.Replace(string(b), "limits: {users: 15, outlets: 5, storage_mb: 10240, ai_credits_month: 2000}",
			"limits: {users: 15, outlets: 5, storage_mb: 10240, ai_credits_month: 2000"+rate+"}", 1)
		if s == string(b) {
			t.Fatal("features.rk of kopi-senja changed: update the test")
		}
		if err := os.WriteFile(f, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	e.login()
	refreshPlan(e)
	return e
}

func TestPlanRateShapesTheTenant(t *testing.T) {
	e := rateEnv(t, ", rate: {rps: 2, burst: 2, max_wait_ms: 0, max_waiting: 0}")
	time.Sleep(1100 * time.Millisecond) // the sign-in used the per-client bucket; let the tenant's fill
	e.must(200, "GET", "/api/v1/meta", nil)
	e.must(200, "GET", "/api/v1/meta", nil)
	r := e.must(429, "GET", "/api/v1/meta", nil)
	if errCode(r) != "rate_limited" || errMsg(r) != "Sistem sedang ramai. Coba lagi sebentar." || r.Header.Get("Retry-After") != "1" {
		t.Fatalf("over the rate: %v %s", r.Header, r.Body)
	}
	// Not shaped: health, static web files, the poll endpoint.
	for i := 0; i < 5; i++ {
		if r := e.do("GET", "/healthz", nil); r.Status == 429 {
			t.Fatal("healthz shaped")
		}
		if r := e.do("GET", "/app/", nil); r.Status == 429 {
			t.Fatal("static web shaped")
		}
	}
	// Waiting: with room to wait, the next request is held, then served.
	time.Sleep(1100 * time.Millisecond)
	rep := e.must(200, "GET", "/api/v1/plan", nil).JSON
	if get(rep, "usage", "rate", "refused_today") != float64(1) || get(rep, "usage", "rate", "approximate") != true {
		t.Fatalf("usage.rate %v", get(rep, "usage", "rate"))
	}
	found := false
	for _, p := range rep["plans"].([]any) {
		if get(p, "name") == "tumbuh" {
			found = get(p, "limits", "rate", "rps") == float64(2) && get(p, "limits", "outlets") == float64(5)
		}
	}
	if !found {
		t.Fatalf("plans %v", rep["plans"])
	}
}

func TestPlanRateWaits(t *testing.T) {
	e := rateEnv(t, ", rate: {rps: 10, burst: 1, max_wait_ms: 1000, max_waiting: 5}")
	time.Sleep(200 * time.Millisecond)
	e.must(200, "GET", "/api/v1/meta", nil)
	r := e.must(200, "GET", "/api/v1/meta", nil)
	if r.Header.Get("X-Rangka-Throttle-Ms") == "" {
		t.Fatalf("the second request was not held: %v", r.Header)
	}
}

func TestNoRateNoShaping(t *testing.T) {
	e := planEnv(t)
	refreshPlan(e)
	for i := 0; i < 30; i++ {
		e.must(200, "GET", "/api/v1/meta", nil)
	}
}
