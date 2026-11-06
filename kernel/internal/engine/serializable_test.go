package engine

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// Contract 0025: two units of work that each check "is there one already?" and then write one must not both
// pass (write skew). The barrier makes both read before either writes, on the first attempt only.
func TestRunSerializesWriteSkew(t *testing.T) {
	e, a := setup(t)
	ctx := context.Background()
	if _, err := e.DB.Exec(ctx, `create table rk_test_skew (k text not null, who text not null)`); err != nil {
		t.Fatal(err)
	}
	var ready sync.WaitGroup
	ready.Add(2)
	gate := make(chan struct{})
	var mu sync.Mutex
	attempts := map[string]int{}
	work := func(who string) error {
		return e.run(ctx, a, func(o *op) error {
			mu.Lock()
			attempts[who]++
			first := attempts[who] == 1
			mu.Unlock()
			res, err := o.tx.Query(ctx, `select count(*) from rk_test_skew where k = 'only'`)
			if err != nil {
				return err
			}
			n := toInt(res.Rows[0][0])
			if first {
				ready.Done()
				<-gate
			}
			if n > 0 {
				return nil // someone already holds it
			}
			_, err = o.tx.Exec(ctx, `insert into rk_test_skew (k, who) values ('only', $1)`, who)
			return err
		})
	}
	errs := make(chan error, 2)
	for _, who := range []string{"a", "b"} {
		go func(who string) { errs <- work(who) }(who)
	}
	ready.Wait()
	close(gate)
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	res, err := e.DB.Query(ctx, `select count(*) from rk_test_skew`)
	if err != nil {
		t.Fatal(err)
	}
	if n := toInt(res.Rows[0][0]); n != 1 {
		t.Fatalf("both units of work wrote (%d rows): write skew not prevented", n)
	}
	if attempts["a"]+attempts["b"] < 3 {
		t.Fatalf("the loser must run again from current data: attempts %v", attempts)
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
