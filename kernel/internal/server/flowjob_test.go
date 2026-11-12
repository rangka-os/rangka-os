package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/jobs"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

const ledgerModel = "# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/model.json\n" +
	"name: crew_ledger\nnaming: CL-{#####}\nlabel: {id: Buku kru}\nappend_only: true\nfields:\n" +
	"  - {name: amount, type: currency, label: {id: Jumlah}}\n"

const ledgerFlow = "# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/flow.json\n" +
	"name: crew_ledger_later\nlabel: {id: Catat nanti}\nsteps:\n" +
	"  - create: crew_ledger\n    set: {amount: \"event.amount\"}\n"

// Contract 0026 through the job worker itself: a queued flow job marked manual (a run started by hand that waited)
// may not create ledger rows when the worker resumes it; the same job without the mark does.
func TestRunFlowJobKeepsManual(t *testing.T) {
	e := newEnv(t, false, func(apps, site string) {
		for p, c := range map[string]string{
			filepath.Join(apps, "crm", "models", "crew_ledger.rk"):      ledgerModel,
			filepath.Join(apps, "crm", "flows", "crew_ledger_later.rk"): ledgerFlow,
		} {
			if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	})
	e.login()
	ctx := context.Background()
	job := func(manual bool) *jobs.Job {
		return &jobs.Job{Org: "kopi-senja", Args: map[string]any{"flow": "crew_ledger_later", "event": map[string]any{"amount": 5},
			"resume": int64(0), "manual": manual}}
	}
	if _, err := e.s.runFlowJob(ctx, job(true)); err == nil || !strings.Contains(err.Error(), "append_only") && !strings.Contains(err.Error(), "dicatat otomatis") {
		t.Fatalf("manual job = %v, want append_only", err)
	}
	if _, err := e.s.runFlowJob(ctx, job(false)); err != nil {
		t.Fatalf("kernel job: %v", err)
	}
	if n := len(rowsOf(e.must(200, "GET", "/api/v1/r/crew_ledger", nil))); n != 1 {
		t.Fatalf("ledger rows = %d, want 1", n)
	}
}

// A flow job that fails for good releases the idempotency key its run holds (audit flowkey-release N1, card
// t_aa8d76cd): the run's pending marker goes, and the key with no documents left is removed.
func TestRunFlowJobDeadReleasesKey(t *testing.T) {
	flow := "# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/flow.json\n" +
		"name: crew_ledger_key\nlabel: {id: Catat nanti sekali}\nidempotency: event.ref\nsteps:\n" +
		"  - wait: 1m\n  - create: crew_ledger\n    set: {amount: \"event.amount\"}\n"
	e := newEnv(t, false, func(apps, site string) {
		for p, c := range map[string]string{
			filepath.Join(apps, "crm", "models", "crew_ledger.rk"):    ledgerModel,
			filepath.Join(apps, "crm", "flows", "crew_ledger_key.rk"): flow,
		} {
			if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	})
	e.login()
	ctx := context.Background()
	db := e.s.Engine.DB
	if _, _, err := store.ClaimFlowKey(ctx, db, "kopi-senja", "crew_ledger_key", "s:R-9"); err != nil {
		t.Fatal(err)
	}
	if err := store.AddFlowKeyDocs(ctx, db, "kopi-senja", "crew_ledger_key", "s:R-9", nil, true, true, "flow:pending:tok9"); err != nil {
		t.Fatal(err)
	}
	keys := func() int {
		res, err := db.Query(ctx, "select 1 from rk_flow_keys where org_id = 'kopi-senja' and flow = 'crew_ledger_key'")
		if err != nil {
			t.Fatal(err)
		}
		return len(res.Rows)
	}
	j := &jobs.Job{Org: "kopi-senja", Attempt: 1, MaxAttempts: 2, Args: map[string]any{"flow": "crew_ledger_key", "event": map[string]any{"amount": 5, "ref": "R-9"},
		"resume": int64(1), "manual": true, "flow_key": map[string]any{"key": "s:R-9", "token": "tok9"}}}
	if _, err := e.s.runFlowJob(ctx, j); err == nil {
		t.Fatal("manual job must fail (append_only)")
	}
	if keys() != 1 {
		t.Fatal("a job that will be retried released its key")
	}
	j.Attempt = 2
	if _, err := e.s.runFlowJob(ctx, j); err == nil {
		t.Fatal("manual job must fail (append_only)")
	}
	if keys() != 0 {
		t.Fatal("the key of a dead-lettered run is still held")
	}
}
