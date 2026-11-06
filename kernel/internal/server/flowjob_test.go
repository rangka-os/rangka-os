package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/jobs"
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
