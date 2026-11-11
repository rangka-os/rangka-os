package engine

import (
	"context"
	"testing"
)

// A read grant's if: or a clock compute that loads a link and meets a database error passes the error on (a client
// filter then fails) instead of reading the linked record as unreadable or the field as zero (contract 0010 §5).
func TestLinkLoadDatabaseErrorPassesOn(t *testing.T) {
	e, a := approvalSetupFiles(t, memoFiles, "")
	ctx := context.Background()
	dead, cancel := context.WithCancel(ctx)
	cancel()
	links := func() map[string]map[string]any { return map[string]map[string]any{} }

	inv := e.Reg().Models["sales_invoice"]
	rec := map[string]any{"name": "X", "customer": "CUST-0012"}
	if ok, err := e.recordAllowedErr(dead, e.DB, a, inv, decision{conds: []string{"customer.customer_name != ''"}}, rec, links()); ok || err == nil {
		t.Fatalf("grant if: on a failed link load = %v, %v; want an error", ok, err)
	}
	if ok, err := e.recordAllowedErr(ctx, e.DB, a, inv, decision{conds: []string{"[1][3] == 1"}}, rec, links()); ok || err != nil {
		t.Fatalf("grant if: that cannot be evaluated = %v, %v; want false without error", ok, err)
	}
	if ok, err := e.recordAllowedErr(ctx, e.DB, a, inv, decision{conds: []string{"customer.customer_name != ''"}}, rec, links()); !ok || err != nil {
		t.Fatalf("grant if: on a live link = %v, %v; want true", ok, err)
	}

	mc := e.Reg().Models["memo_clock"]
	if err := e.refreshClockErr(dead, a, mc, map[string]any{"name": "Y", "customer": "CUST-0012"}, true, links()); err == nil {
		t.Fatal("clock compute on a failed link load: want an error")
	}
	live := map[string]any{"name": "Y", "customer": "CUST-0012"}
	if err := e.refreshClockErr(ctx, a, mc, live, true, links()); err != nil || str(live["city_today"]) == "" {
		t.Fatalf("clock compute on a live link = %v (city_today %q)", err, live["city_today"])
	}
}
