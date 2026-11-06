package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
	"github.com/rangka-os/rangka-os/kernel/internal/plan"
)

// planEnv starts kopi-senja on plan tumbuh with small limits: 3 outlets (the fixtures have 2) and 2 AI credits.
func planEnv(t *testing.T) *env {
	t.Helper()
	e := newEnv(t, false, func(apps, site string) {
		f := filepath.Join(site, "features.rk")
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		s := strings.Replace(string(b), "limits: {users: 15, outlets: 5, storage_mb: 10240, ai_credits_month: 2000}",
			"limits: {users: 15, outlets: 3, storage_mb: 10240, ai_credits_month: 2}", 1)
		if err := os.WriteFile(f, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	e.login()
	return e
}

func errCode(r *resp) string { s, _ := get(r.JSON, "error", "code").(string); return s }
func errMsg(r *resp) string  { s, _ := get(r.JSON, "error", "message").(string); return s }

func setPlan(e *env, name string) {
	e.t.Helper()
	if _, err := plan.Set(context.Background(), e.s.DB, e.s.Engine.Reg(), name, "test", "uji"); err != nil {
		e.t.Fatal(err)
	}
	refreshPlan(e)
}

// refreshPlan measures the plan now (the read-only check reads a snapshot refreshed in the background).
func refreshPlan(e *env) {
	e.t.Helper()
	if err := e.s.Engine.Plan.Refresh(context.Background(), e.s.DB, e.s.Engine.Reg(), e.s.Engine.Now()); err != nil {
		e.t.Fatal(err)
	}
}

func TestPlanOutletLimit(t *testing.T) {
	e := planEnv(t)
	rep := e.must(200, "GET", "/api/v1/plan", nil).JSON
	if get(rep, "plan", "name") != "tumbuh" || get(rep, "plan", "source") != "config" || get(rep, "usage", "outlets", "used") != float64(2) ||
		get(rep, "usage", "outlets", "limit") != float64(3) || get(rep, "usage", "outlets", "state") != "ok" {
		t.Fatalf("plan report %v", rep)
	}
	// the third outlet fills the plan: near/full is reported, the fourth is refused
	e.must(201, "POST", "/api/v1/r/outlet", map[string]any{"outlet_name": "Kopi Senja · Depok"})
	rep = e.must(200, "GET", "/api/v1/plan", nil).JSON
	if get(rep, "usage", "outlets", "state") != "full" || len(rep["notices"].([]any)) != 1 {
		t.Fatalf("after the third outlet %v", rep)
	}
	r := e.must(403, "POST", "/api/v1/r/outlet", map[string]any{"outlet_name": "Kopi Senja · Bogor"})
	if errCode(r) != "plan_limit" || !strings.Contains(errMsg(r), "Paket Tumbuh berlaku untuk 3 outlet") {
		t.Fatalf("fourth outlet: %s", r.Body)
	}
	// an import is the same create path
	if n := len(e.must(200, "GET", "/api/v1/r/outlet", nil).JSON["data"].([]any)); n != 3 {
		t.Fatalf("outlets after the refusal: %d", n)
	}
}

func TestPlanNear(t *testing.T) {
	e := newEnv(t, false, nil) // tumbuh: 5 outlets, 2 in the fixtures
	e.login()
	e.must(201, "POST", "/api/v1/r/outlet", map[string]any{"outlet_name": "Depok"})
	rep := e.must(200, "GET", "/api/v1/plan", nil).JSON
	if get(rep, "usage", "outlets", "state") != "ok" {
		t.Fatalf("3 of 5 %v", rep["usage"])
	}
	e.must(201, "POST", "/api/v1/r/outlet", map[string]any{"outlet_name": "Bogor"})
	rep = e.must(200, "GET", "/api/v1/plan", nil).JSON
	n, _ := rep["notices"].([]any)
	if get(rep, "usage", "outlets", "state") != "near" || len(n) != 1 || !strings.Contains(get(n[0], "message").(string), "4 dari 5 outlet") {
		t.Fatalf("4 of 5 %v", rep)
	}
	if len(rep["plans"].([]any)) != 2 || get(rep, "plans", 0, "limits", "outlets") != float64(1) || get(rep, "plans", 1, "label") != "Tumbuh" {
		t.Fatalf("plans %v", rep["plans"])
	}
}

func TestPlanDowngradeIsReadOnly(t *testing.T) {
	e := planEnv(t)
	setPlan(e, "mulai") // 1 outlet: OUT-02 (the newer) becomes read-only, nothing is deleted
	rep := e.must(200, "GET", "/api/v1/plan", nil).JSON
	ro, _ := get(rep, "read_only", "outlets").([]any)
	if len(ro) != 1 || ro[0] != "kopi-senja/OUT-02" {
		t.Fatalf("read-only outlets %v", rep["read_only"])
	}
	e.must(200, "GET", "/api/v1/r/outlet/OUT-02", nil)
	e.must(200, "GET", "/api/v1/r/sales_invoice/INV-2026-00044", nil)
	r := e.must(403, "PATCH", "/api/v1/r/outlet/OUT-02", map[string]any{"outlet_name": "Bintaro baru"})
	if errCode(r) != "plan_read_only" || !strings.Contains(errMsg(r), "mode baca") {
		t.Fatalf("patch read-only outlet: %s", r.Body)
	}
	r = e.must(403, "POST", "/api/v1/r/sales_invoice/INV-2026-00044/cancel", nil)
	if errCode(r) != "plan_read_only" {
		t.Fatalf("cancel an invoice of a read-only outlet: %s", r.Body)
	}
	e.must(200, "PATCH", "/api/v1/r/outlet/OUT-01", map[string]any{"outlet_name": "Kemang"})
	if errCode(e.must(403, "POST", "/api/v1/r/outlet", map[string]any{"outlet_name": "X"})) != "plan_limit" {
		t.Fatal("a new outlet on mulai with 2 outlets")
	}
	// the upgrade gives write back
	setPlan(e, "tumbuh")
	e.must(200, "PATCH", "/api/v1/r/outlet/OUT-02", map[string]any{"outlet_name": "Bintaro baru"})
	if n := len(e.must(200, "GET", "/api/v1/r/outlet", nil).JSON["data"].([]any)); n != 2 {
		t.Fatalf("outlets after downgrade and upgrade: %d", n)
	}
	// clear: config.rk plan again
	if err := plan.Clear(context.Background(), e.s.DB, e.s.Engine.Reg(), "test"); err != nil {
		t.Fatal(err)
	}
	if get(e.must(200, "GET", "/api/v1/plan", nil).JSON, "plan", "source") != "config" {
		t.Fatal("clear did not go back to config.rk")
	}
}

func TestPlanPeople(t *testing.T) {
	e := planEnv(t)
	ctx := context.Background()
	setPlan(e, "mulai") // 3 people
	for _, m := range []string{"a@kopisenja.id", "b@kopisenja.id"} {
		if err := plan.CheckNewPerson(ctx, e.s.DB, m); err != nil {
			t.Fatalf("%s: %v", m, err)
		}
		if _, err := auth.EnsureUser(ctx, e.s.DB, m, m, "rahasia-123", "kopi-senja", "sales_manager", ""); err != nil {
			t.Fatal(err)
		}
	}
	err := plan.CheckNewPerson(ctx, e.s.DB, "c@kopisenja.id")
	var pe *plan.Error
	if err == nil || !asPlanError(err, &pe) || pe.Code != "plan_limit" || pe.Resource != "users" {
		t.Fatalf("fourth person: %v", err)
	}
	if err := plan.CheckNewPerson(ctx, e.s.DB, "B@kopisenja.id"); err != nil {
		t.Fatalf("an existing member is not a new person: %v", err)
	}
	// a fourth person added before the downgrade (here: behind the check's back) is read-only; the owner never is
	if _, err := auth.EnsureUser(ctx, e.s.DB, "c@kopisenja.id", "C", "rahasia-123", "kopi-senja", "sales_manager", ""); err != nil {
		t.Fatal(err)
	}
	refreshPlan(e)
	rep := e.must(200, "GET", "/api/v1/plan", nil).JSON
	if ro, _ := get(rep, "read_only", "users").([]any); len(ro) != 1 || ro[0] != "c@kopisenja.id" || get(rep, "usage", "users", "state") != "over" {
		t.Fatalf("people report %v", rep)
	}
	c := &env{t: t, s: e.s, ts: e.ts, apps: e.apps, site: e.site, jar: newJar()}
	c.must(200, "POST", "/auth/login", map[string]any{"email": "c@kopisenja.id", "password": "rahasia-123"})
	c.must(200, "GET", "/api/v1/r/deal", nil)
	r := c.must(403, "POST", "/api/v1/r/lead", map[string]any{"lead_name": "Calon", "email": "calon@contoh.id"})
	if errCode(r) != "plan_read_only" {
		t.Fatalf("read-only person writes: %s", r.Body)
	}
	e.must(200, "PATCH", "/api/v1/r/outlet/OUT-01", map[string]any{"outlet_name": "Kemang"}) // the owner is never read-only
}

func TestPlanAICredits(t *testing.T) {
	e := planEnv(t)
	fail := true
	side := fakeSidecar(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		if fail {
			http.Error(w, "boom", 500)
			return
		}
		writeEvent(w, "text", `{"delta":"Ya."}`)
		writeEvent(w, "done", `{}`)
	})
	withAI(e, side.URL, nil)
	// a chat that never reached the assistant gives its credit back
	if evs := chat(e, ask("Halo")); names(evs) != "error" {
		t.Fatalf("failing sidecar %+v", evs)
	}
	if v := get(e.must(200, "GET", "/api/v1/plan", nil).JSON, "usage", "ai_credits_month", "used"); v != float64(0) {
		t.Fatalf("credits after a failed chat: %v", v)
	}
	fail = false
	for i := 0; i < 2; i++ {
		if evs := chat(e, ask("Halo")); names(evs) != "text,done" {
			t.Fatalf("chat %d: %+v", i, evs)
		}
	}
	status, out, err := chatRaw(e, ask("Halo"))
	if err != nil || status != 403 || !strings.Contains(out, `"plan_limit"`) || !strings.Contains(out, "Kredit Raka bulan ini sudah habis") {
		t.Fatalf("third chat over 2 credits = %d %v %s", status, err, out)
	}
	rep := e.must(200, "GET", "/api/v1/plan", nil).JSON
	if get(rep, "usage", "ai_credits_month", "used") != float64(2) || get(rep, "usage", "ai_credits_month", "state") != "full" {
		t.Fatalf("credits report %v", rep["usage"])
	}
}

func asPlanError(err error, pe **plan.Error) bool { return errors.As(err, pe) }

// audit F1: the plan checks never take a second connection inside a write's transaction, so many writes at once
// (more than the pool) still finish
func TestPlanParallelWrites(t *testing.T) {
	e := planEnv(t)
	setPlan(e, "tumbuh")
	e.s.Engine.Plan.Invalidate() // a refresh is due while the writes run
	errs := make(chan string, 28)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			b, _ := json.Marshal(map[string]any{"outlet_name": fmt.Sprintf("Kemang %d", i)})
			req, _ := http.NewRequest("PATCH", e.ts.URL+"/api/v1/r/outlet/OUT-01", bytes.NewReader(b))
			req.Header.Set("Content-Type", "application/json")
			res, err := (&http.Client{Jar: e.jar, Timeout: 10 * time.Second}).Do(req)
			if err != nil {
				errs <- err.Error()
				return
			}
			res.Body.Close()
			if res.StatusCode != 200 {
				errs <- fmt.Sprintf("patch %d", res.StatusCode)
			}
		}(i)
	}
	created := make(chan int, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			b, _ := json.Marshal(map[string]any{"outlet_name": fmt.Sprintf("Baru %d", i)})
			req, _ := http.NewRequest("POST", e.ts.URL+"/api/v1/r/outlet", bytes.NewReader(b))
			req.Header.Set("Content-Type", "application/json")
			res, err := (&http.Client{Jar: e.jar, Timeout: 10 * time.Second}).Do(req)
			if err != nil {
				errs <- err.Error()
				return
			}
			res.Body.Close()
			created <- res.StatusCode
		}(i)
	}
	wg.Wait()
	close(errs)
	close(created)
	for msg := range errs {
		t.Error(msg)
	}
	ok := 0
	for st := range created {
		if st == 201 {
			ok++
		} else if st != 403 {
			t.Errorf("create = %d", st)
		}
	}
	if ok != 1 { // 2 outlets, limit 3
		t.Fatalf("%d outlets created at once, want 1", ok)
	}
}

// audit F2: attachments follow the read-only mode of the record's outlet
func TestPlanAttachmentReadOnly(t *testing.T) {
	e := planEnv(t)
	setPlan(e, "mulai")
	r := e.upload("/api/v1/r/sales_invoice/INV-2026-00044/attachments", "bukti.pdf", []byte("%PDF-1.4 x\n"))
	if r.Status != 403 || errCode(r) != "plan_read_only" {
		t.Fatalf("attachment on a read-only outlet's invoice: %d %s", r.Status, r.Body)
	}
}
