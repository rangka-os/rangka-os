package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/pg"
)

// Contract 0025: a unit of work that lost a race runs again from scratch; what it reports comes from the attempt
// that committed (audit 2026-11-06 F1: an import reported the first attempt's row errors after committing).
func TestRunRetryStartsFresh(t *testing.T) {
	e, a := setup(t)
	ctx := context.Background()
	calls := 0
	before := func(ctx context.Context, tx pg.Querier) error {
		calls++
		if calls == 1 { // lose the first attempt, after the inputs would have been checked
			_, err := tx.Exec(ctx, `do $$ begin raise exception 'lost' using errcode = '40001'; end $$`)
			return err
		}
		return nil
	}
	res, err := e.CreateMany(ctx, a, "customer", []map[string]any{{"customer_name": "Retry Satu"}, {"customer_name": "Retry Dua"}}, ManyOpts{Before: before})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(res) != 2 || res[0].Err != nil || res[1].Err != nil || res[0].Name == "" {
		t.Fatalf("retried import result: calls=%d %+v", calls, res)
	}
	rows, _, err := e.List(ctx, a, "customer", ListOpts{Q: "Retry", Limit: 10})
	if err != nil || len(rows) != 2 {
		t.Fatalf("committed once: %d rows %v", len(rows), err)
	}
}

// The same race through the public API: a record flow checks the other deals of the customer (find_all) and marks
// the deal "pemenang" only when none is marked yet. Concurrent updates of two deals must leave exactly one.
func TestFlowCheckThenWriteIsSerialized(t *testing.T) {
	e, a := approvalSetupFiles(t, map[string]string{"crm/flows/deal_winner.rk": `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/flow.json
name: deal_winner
label: {id: Satu pemenang per pelanggan}
on: deal.updated
if: "event.stage == 'qualified'"
steps:
  - {find: deal, where: "name == event.name", as: me}
  - {find_all: deal, where: "customer == me.customer && notes == 'pemenang'", as: winners}
  - if: "size(winners) == 0"
    update: me
    set: {notes: "'pemenang'"}
`}, "")
	ctx := context.Background()
	for round, cust := range []string{"CUST-0012", "CUST-0031", "CUST-0047", "CUST-0058"} {
		var names []string
		for i := 0; i < 3; i++ {
			res, err := e.Create(ctx, a, "deal", map[string]any{"deal_name": fmt.Sprintf("R%d-%d", round, i), "customer": cust, "value": 1000, "expected_close": "2026-12-01"})
			if err != nil {
				t.Fatal(err)
			}
			names = append(names, str(res.Record["name"]))
		}
		var wg sync.WaitGroup
		start := make(chan struct{})
		errs := make(chan error, len(names))
		for _, n := range names {
			wg.Add(1)
			go func(n string) {
				defer wg.Done()
				<-start
				_, err := e.Update(ctx, a, "deal", n, map[string]any{"stage": "qualified"})
				errs <- err
			}(n)
		}
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil && !strings.Contains(err.Error(), "bersamaan") {
				t.Fatal(err)
			}
		}
		winners := 0
		for _, n := range names {
			got, err := e.Get(ctx, a, "deal", n)
			if err != nil {
				t.Fatal(err)
			}
			if str(got["notes"]) == "pemenang" {
				winners++
			}
		}
		if winners != 1 {
			t.Fatalf("round %d (%s): %d deals marked pemenang, want exactly 1", round, cust, winners)
		}
	}
}

// A unit of work that keeps losing gives up after writeAttempts with 409 write_conflict, and a lost race that the
// unit itself wrapped (a flow step error) is still recognised.
func TestRunGivesUpWithWriteConflict(t *testing.T) {
	e, a := setup(t)
	ctx := context.Background()
	n := 0
	err := e.run(ctx, a, func(o *op) error {
		n++
		_, err := o.tx.Exec(ctx, `do $$ begin raise exception 'lost' using errcode = '40001'; end $$`)
		return fmt.Errorf("step 2 update deal: %w", err)
	})
	var se *StatusError
	if !errors.As(err, &se) || se.Status != 409 || se.Code != "write_conflict" {
		t.Fatalf("err = %v, want 409 write_conflict", err)
	}
	if n != writeAttempts {
		t.Fatalf("attempts = %d, want %d", n, writeAttempts)
	}
	// an ordinary error is not retried
	n = 0
	err = e.run(ctx, a, func(o *op) error { n++; return badRequest("no") })
	if err == nil || n != 1 {
		t.Fatalf("plain error retried: n=%d err=%v", n, err)
	}
}
