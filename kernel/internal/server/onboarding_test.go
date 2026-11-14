package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Contract 0053 §2–3: Mulai di sini over testdata/canvas (apps crm and jasa_proyek, no records seeded).

func stepStates(t *testing.T, ob any) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, s := range get(ob, "steps").([]any) {
		out[get(s, "stage").(string)] = get(s, "state").(string)
	}
	return out
}

func stepOf(ob any, stage string) any {
	for _, s := range get(ob, "steps").([]any) {
		if get(s, "stage") == stage {
			return s
		}
	}
	return nil
}

func TestOnboardingSteps(t *testing.T) {
	e, _ := canvasEnv(t, nil)
	ob := e.must(200, "GET", "/api/v1/onboarding", nil).JSON
	// rule 3: no site process, so the first declared app process by name (lead_to_cash before quote_to_cash)
	if get(ob, "process") != "lead_to_cash" || get(ob, "hidden") != false {
		t.Fatalf("process/hidden: %v", ob)
	}
	want := map[string]string{"lead_in": "open", "deal": "locked", "invoice": "locked", "paid": "locked"}
	if got := stepStates(t, ob); len(got) != 4 || got["lead_in"] != want["lead_in"] || got["deal"] != want["deal"] || got["invoice"] != want["invoice"] || got["paid"] != want["paid"] {
		t.Fatalf("states %v", got)
	}
	if get(ob, "current") != "lead_in" {
		t.Fatalf("current %v", get(ob, "current"))
	}
	deal := stepOf(ob, "deal")
	if n := get(deal, "needs").([]any); len(n) != 1 || n[0] != "customer" {
		t.Fatalf("deal needs %v", n)
	}
	if a, ok := get(deal, "answers").([]any); !ok || len(a) != 0 {
		t.Fatalf("answers must be [] when the stage has none: %v", get(deal, "answers"))
	}
	if _, has := deal.(map[string]any)["hint"]; has {
		t.Fatalf("hint must be absent when the stage has none: %v", deal)
	}
	if l := get(ob, "locks", "deal"); get(l, "reason") != "needs_data" || len(get(l, "needs").([]any)) != 1 {
		t.Fatalf("locks.deal %v", l)
	}
	// a model off the process is locked too (locks covers the menu, not only stages); customer itself is not
	if get(ob, "locks", "customer") != nil {
		t.Fatalf("customer has no required link and must not be locked: %v", get(ob, "locks"))
	}

	// a customer unlocks deal; a lead makes lead_in done with done_at
	e.must(201, "POST", "/api/v1/r/customer", map[string]any{"customer_name": "PT Satu"})
	e.must(201, "POST", "/api/v1/r/lead", map[string]any{"lead_name": "Bu Ani", "email": "ani@example.test"})
	ob = e.must(200, "GET", "/api/v1/onboarding", nil).JSON
	got := stepStates(t, ob)
	if got["lead_in"] != "done" || got["deal"] != "open" || got["paid"] != "open" {
		t.Fatalf("after data: %v", got)
	}
	if at, _ := get(stepOf(ob, "lead_in"), "done_at").(string); !strings.HasSuffix(at, "Z") {
		t.Fatalf("done_at %v", get(stepOf(ob, "lead_in"), "done_at"))
	}
	if get(ob, "current") != "deal" || get(ob, "locks", "deal") != nil {
		t.Fatalf("current %v locks %v", get(ob, "current"), get(ob, "locks"))
	}
	if _, has := stepOf(ob, "deal").(map[string]any)["done_at"]; has {
		t.Fatal("an open step has no done_at")
	}
}

// A stage when: is evaluated on the org's records, as metadata: only a matching record makes the step done.
func TestOnboardingStageWhen(t *testing.T) {
	e, _ := canvasEnv(t, func(root string) {
		dir := filepath.Join(root, "site", "processes")
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, "mulai.rk"), []byte("name: mulai\nstages:\n  - {name: semua, model: customer}\n  - {name: dua, model: customer, when: \"customer_name == 'PT Dua'\"}\n"), 0o644)
	})
	e.must(201, "POST", "/api/v1/r/customer", map[string]any{"customer_name": "PT Satu"})
	if got := stepStates(t, e.must(200, "GET", "/api/v1/onboarding", nil).JSON); got["semua"] != "done" || got["dua"] != "open" {
		t.Fatalf("states %v", got)
	}
	e.must(201, "POST", "/api/v1/r/customer", map[string]any{"customer_name": "PT Dua"})
	ob := e.must(200, "GET", "/api/v1/onboarding", nil).JSON
	if got := stepStates(t, ob); got["dua"] != "done" || get(ob, "current") != nil {
		t.Fatalf("states %v current %v", got, get(ob, "current"))
	}
}

func TestOnboardingSiteProcessHintAnswers(t *testing.T) {
	e, _ := canvasEnv(t, func(root string) {
		dir := filepath.Join(root, "site", "processes")
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, "mulai.rk"), []byte(`name: mulai
stages:
  - {name: pelanggan, label: {id: Tambah pelanggan pertama}, model: customer, hint: {id: Masukkan satu pelanggan.},
     answers: [{id: "Data pelanggan tercecer di WA"}]}
  - {name: peluang, model: deal}
`), 0o644)
	})
	ob := e.must(200, "GET", "/api/v1/onboarding", nil).JSON
	// rule 2: the only site process wins over the app processes
	if get(ob, "process") != "mulai" || get(ob, "current") != "pelanggan" {
		t.Fatalf("process %v current %v", get(ob, "process"), get(ob, "current"))
	}
	p := stepOf(ob, "pelanggan")
	if get(p, "hint", "id") != "Masukkan satu pelanggan." || get(p, "answers").([]any)[0].(map[string]any)["id"] != "Data pelanggan tercecer di WA" {
		t.Fatalf("hint/answers %v", p)
	}
	// a stage without a label takes the model's
	if get(stepOf(ob, "peluang"), "label", "id") == nil {
		t.Fatalf("label %v", stepOf(ob, "peluang"))
	}
}

func TestOnboardingConfigProcess(t *testing.T) {
	e, _ := canvasEnv(t, func(root string) {
		dir := filepath.Join(root, "site", "processes")
		os.MkdirAll(dir, 0o755)
		for _, n := range []string{"satu", "dua"} {
			os.WriteFile(filepath.Join(dir, n+".rk"), []byte("name: "+n+"\nstages:\n  - {name: s, model: customer}\n"), 0o644)
		}
		p := filepath.Join(root, "site", "config.rk")
		b, _ := os.ReadFile(p)
		os.WriteFile(p, append(b, []byte("onboarding: {process: satu}\n")...), 0o644)
	})
	if p := get(e.must(200, "GET", "/api/v1/onboarding", nil).JSON, "process"); p != "satu" {
		t.Fatalf("rule 1: %v", p)
	}
}

// A sales_rep without read on deal does not see that step; the hide flag is the owner's only.
func TestOnboardingRoleAndHide(t *testing.T) {
	e, _ := canvasEnv(t, noDealForReps)
	rep := canvasRep(t, e)
	ob := rep.must(200, "GET", "/api/v1/onboarding", nil).JSON
	if _, has := stepStates(t, ob)["deal"]; has {
		t.Fatalf("rep sees the deal step: %v", ob)
	}
	if get(ob, "locks", "deal") != nil {
		t.Fatalf("rep sees a lock on an unreadable model: %v", get(ob, "locks"))
	}
	if r := rep.must(403, "POST", "/api/v1/onboarding/hide", map[string]any{"hidden": true}); get(r.JSON, "error", "code") != "permission_denied" ||
		get(r.JSON, "error", "model") != "onboarding" || get(r.JSON, "error", "action") != "hide" {
		t.Fatalf("rep hide: %s", r.Body)
	}
	e.must(400, "POST", "/api/v1/onboarding/hide", map[string]any{})
	if r := e.must(200, "POST", "/api/v1/onboarding/hide", map[string]any{"hidden": true}); get(r.JSON, "hidden") != true {
		t.Fatalf("hide: %s", r.Body)
	}
	ob = rep.must(200, "GET", "/api/v1/onboarding", nil).JSON
	if get(ob, "hidden") != true || len(get(ob, "steps").([]any)) == 0 {
		t.Fatalf("hidden is per org and steps still come: %v", ob)
	}
	e.must(200, "POST", "/api/v1/onboarding/hide", map[string]any{"hidden": false})
	if get(e.must(200, "GET", "/api/v1/onboarding", nil).JSON, "hidden") != false {
		t.Fatal("show again")
	}
}

// Raka reads the steps over MCP (contract 0053 §6) and never hides them (§3).
func TestOnboardingMCPAndAssistant(t *testing.T) {
	e, _ := canvasEnv(t, nil)
	res := rpc(e, 21, "tools/call", map[string]any{"name": "rangka_onboarding", "arguments": map[string]any{}})
	if get(res, "structuredContent", "process") != "lead_to_cash" || get(res, "structuredContent", "current") != "lead_in" {
		t.Fatalf("MCP rangka_onboarding: %v", res)
	}
	ai := assistantEnv(t, e, 10*time.Minute)
	if got := ai.must(200, "GET", "/api/v1/onboarding", nil).JSON; get(got, "current") != "lead_in" {
		t.Fatalf("assistant read: %v", got)
	}
	if r := ai.must(403, "POST", "/api/v1/onboarding/hide", map[string]any{"hidden": true}); get(r.JSON, "error", "code") != "ai_forbidden" {
		t.Fatalf("assistant hide: %s", r.Body)
	}
	res = rpc(ai, 22, "tools/call", map[string]any{"name": "rangka_onboarding", "arguments": map[string]any{}})
	if get(res, "structuredContent", "current") != "lead_in" {
		t.Fatalf("assistant MCP rangka_onboarding: %v", res)
	}
}

// onbProcess writes a site process with the given stages (audit 0053 S5, S6).
func onbProcess(stages string) func(root string) {
	return func(root string) {
		dir := filepath.Join(root, "site", "processes")
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, "mulai.rk"), []byte("name: mulai\nstages:\n"+stages), 0o644)
	}
}

// A when that reads child rows without a dot (size(items)) loads them too (audit 0053 S5).
func TestOnboardingWhenReadsChildRows(t *testing.T) {
	e, _ := canvasEnv(t, onbProcess("  - {name: fn, model: customer_order, when: \"size(items) > 0\"}\n  - {name: me, model: customer_order, when: \"items.size() > 0\"}\n  - {name: none, model: customer_order, when: \"size(items) > 1\"}\n"))
	ctx := context.Background()
	for _, q := range []string{
		`insert into r_customer (org_id, name, customer_name) values ('kopi-senja','CUST-1','C1')`,
		`insert into r_customer_order (org_id, name, customer, po_number, po_date, subtotal) values ('kopi-senja','SO-1','CUST-1','PO1','2026-11-01', 100)`,
		`insert into r_customer_order_item (org_id, name, parent, parent_field, idx, item, qty, rate, amount) values ('kopi-senja','SOI-1','SO-1','items',1,'X',1,100,100)`,
	} {
		if _, err := e.s.DB.Exec(ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	got := stepStates(t, e.must(200, "GET", "/api/v1/onboarding", nil).JSON)
	if got["fn"] != "done" || got["me"] != "done" || got["none"] != "open" {
		t.Fatalf("states %v", got)
	}
}

// A when the flow find can push to SQL (status == 'x') filters in the query and stops at the oldest match: a big table
// with no match answers fast and done_at is the oldest matching record's (audit 0053 S6).
func TestOnboardingWhenPushedToSQL(t *testing.T) {
	e, _ := canvasEnv(t, onbProcess("  - {name: dua, model: customer, when: \"customer_name == 'PT Dua'\"}\n  - {name: tiga, model: customer, when: \"customer_name == 'PT Tiga'\"}\n"))
	ctx := context.Background()
	if _, err := e.s.DB.Exec(ctx, `insert into r_customer (org_id, name, customer_name, created_at)
		select 'kopi-senja', 'C-' || g, 'PT ' || g, timestamptz '2026-01-01' + g * interval '1 second' from generate_series(1, 20000) g`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.DB.Exec(ctx, `insert into r_customer (org_id, name, customer_name, created_at) values
		('kopi-senja','C-DUA-B','PT Dua', timestamptz '2026-03-01T00:00:00Z'), ('kopi-senja','C-DUA-A','PT Dua', timestamptz '2026-02-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	ob := e.must(200, "GET", "/api/v1/onboarding", nil).JSON
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("onboarding took %v over 20,000 customers", d)
	}
	if got := stepStates(t, ob); got["dua"] != "done" || got["tiga"] != "open" {
		t.Fatalf("states %v", got)
	}
	if at := get(stepOf(ob, "dua"), "done_at"); at != "2026-02-01T00:00:00Z" {
		t.Fatalf("done_at of the oldest match: %v", at)
	}
}
