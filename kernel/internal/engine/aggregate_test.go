package engine

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/rk"
)

func TestParseAggFns(t *testing.T) {
	fns, err := ParseAggFns("count,sum:grand_total, avg:grand_total")
	if err != nil {
		t.Fatal(err)
	}
	want := []AggFn{{"count", ""}, {"sum", "grand_total"}, {"avg", "grand_total"}}
	if fmt.Sprint(fns) != fmt.Sprint(want) {
		t.Fatalf("fns = %v, want %v", fns, want)
	}
	for _, bad := range []string{"sum", "median:x", "count:x", "sum:", ",", ""} {
		if _, err := ParseAggFns(bad); !isBadRequest(err) {
			t.Errorf("ParseAggFns(%q) = %v, want 400", bad, err)
		}
	}
}

func isBadRequest(err error) bool {
	var se *StatusError
	return errors.As(err, &se) && se.Status == 400
}

func TestAggregateCountEqualsList(t *testing.T) {
	e, a := setup(t)
	ctx := context.Background()
	// nosuch.x fails to evaluate on every row: like List, Aggregate skips those rows instead of failing
	for _, opts := range []ListOpts{{}, {Filter: "docstatus == 1"}, {Q: "kopi"}, {Filter: "customer.payment_days >= 14"}, {Filter: "nosuch.x == 1"}} {
		_, total, err := e.List(ctx, a, "sales_invoice", opts)
		if err != nil {
			t.Fatal(err)
		}
		res, err := e.Aggregate(ctx, a, "sales_invoice", opts, "", []AggFn{{Kind: "count"}})
		if err != nil {
			t.Fatal(err)
		}
		if res.Count != int64(total) || res.Values["count"] != int64(total) {
			t.Errorf("%+v: aggregate count %d (values %v), list total %d", opts, res.Count, res.Values, total)
		}
	}
}

func TestAggregateSumByGroup(t *testing.T) {
	e, a := setup(t)
	res, err := e.Aggregate(context.Background(), a, "sales_invoice", ListOpts{}, "status", []AggFn{{Kind: "sum", Field: "grand_total"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Groups) < 2 || res.Other != nil {
		t.Fatalf("groups = %+v, other = %+v", res.Groups, res.Other)
	}
	var sum float64
	var n int64
	for _, g := range res.Groups {
		sum += g.Values["sum:grand_total"].(float64)
		n += g.Count
	}
	if total := res.Values["sum:grand_total"].(float64); total == 0 || sum != total || n != res.Count {
		t.Fatalf("group sums %v (count %d), total %v (count %d)", sum, n, total, res.Count)
	}
	for i := 1; i < len(res.Groups); i++ {
		if res.Groups[i].Count > res.Groups[i-1].Count {
			t.Fatalf("groups not sorted by count: %+v", res.Groups)
		}
	}
}

func TestAggregateEmptyGroup(t *testing.T) {
	e, a := setup(t)
	ctx := context.Background()
	if _, err := e.Create(ctx, a, "lead", map[string]any{"lead_name": "Tanpa email", "phone": "081200000001"}); err != nil {
		t.Fatal(err)
	}
	res, err := e.Aggregate(ctx, a, "lead", ListOpts{}, "email", []AggFn{{Kind: "count"}})
	if err != nil {
		t.Fatal(err)
	}
	empty := 0
	for _, g := range res.Groups {
		if g.Key == nil {
			empty++
			if g.Label != "(kosong)" || g.Count == 0 {
				t.Errorf("empty group = %+v", g)
			}
		}
	}
	if empty != 1 {
		t.Fatalf("%d empty groups, want 1: %+v", empty, res.Groups)
	}
}

func TestAggregateGroupCap(t *testing.T) {
	e, a := setup(t)
	ctx := context.Background()
	for i := 0; i < 60; i++ {
		if _, err := e.Create(ctx, a, "lead", map[string]any{"lead_name": fmt.Sprintf("Lead %d", i), "phone": fmt.Sprintf("0813000000%02d", i), "city": fmt.Sprintf("Kota %02d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	res, err := e.Aggregate(ctx, a, "lead", ListOpts{}, "city", []AggFn{{Kind: "count"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Groups) != 50 || res.Other == nil {
		t.Fatalf("groups = %d, other = %+v", len(res.Groups), res.Other)
	}
	var shown int64
	for _, g := range res.Groups {
		shown += g.Count
	}
	if res.Other.Count != res.Count-shown || res.Other.Count == 0 || res.Other.Values["count"] != res.Other.Count {
		t.Fatalf("other = %+v, count %d, shown %d", res.Other, res.Count, shown)
	}
}

func TestAggregateRejects(t *testing.T) {
	e, a := setup(t)
	ctx := context.Background()
	cases := []struct {
		model, group string
		fns          []AggFn
		opts         ListOpts
	}{
		{"sales_invoice", "nope", []AggFn{{Kind: "count"}}, ListOpts{}},
		{"sales_invoice", "", []AggFn{{Kind: "sum", Field: "customer"}}, ListOpts{}},
		{"sales_invoice", "", []AggFn{{Kind: "max", Field: "nope"}}, ListOpts{}},
		{"sales_invoice", "items", []AggFn{{Kind: "count"}}, ListOpts{}},
		{"sales_invoice", "", []AggFn{{Kind: "count"}}, ListOpts{Filter: "docstatus =="}},
		{"sales_invoice_item", "", []AggFn{{Kind: "count"}}, ListOpts{}},
	}
	for _, c := range cases {
		if _, err := e.Aggregate(ctx, a, c.model, c.opts, c.group, c.fns); !isBadRequest(err) {
			t.Errorf("%s group=%q fns=%v filter=%q: err = %v, want 400", c.model, c.group, c.fns, c.opts.Filter, err)
		}
	}
	var se *StatusError
	if _, err := e.Aggregate(ctx, a, "nope", ListOpts{}, "", []AggFn{{Kind: "count"}}); !errors.As(err, &se) || se.Status != 404 {
		t.Errorf("unknown model: %v, want 404", err)
	}
	// system and computed columns may be grouped
	for _, g := range []string{"status", "docstatus", "created_by"} {
		if _, err := e.Aggregate(ctx, a, "sales_invoice", ListOpts{}, g, []AggFn{{Kind: "count"}}); err != nil {
			t.Errorf("group %s: %v", g, err)
		}
	}
}

// TestAggregateLinkLabels: a link group's label is the linked record's title.
func TestAggregateLinkLabels(t *testing.T) {
	e, a := setup(t)
	res, err := e.Aggregate(context.Background(), a, "sales_invoice", ListOpts{}, "customer", []AggFn{{Kind: "count"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range res.Groups {
		if g.Key == nil {
			continue
		}
		if g.Label == "" || g.Label == g.Key {
			t.Errorf("group %v label %q, want the customer's title", g.Key, g.Label)
		}
	}
}

// TestAggregateMatchesListUnderRowScope: an outlet-scoped actor aggregates only the rows its list shows.
func TestAggregateMatchesListUnderRowScope(t *testing.T) {
	e, owner := setup(t)
	ctx := context.Background()
	e.Reg().Roles["outlet_staff"] = &rk.Doc{Data: map[string]any{"name": "outlet_staff", "scope": map[string]any{"field": "outlet", "from": "user.outlets"}}}
	m := e.Reg().Models["sales_invoice"]
	m.Raw["permissions"] = []any{map[string]any{"role": "outlet_staff", "read": true}, map[string]any{"role": "owner", "read": true}}
	e.permCache.Delete(m)
	staff := Actor{Org: owner.Org, ID: "staff@example.test", UserName: "USR-901", Roles: []string{"outlet_staff"}, Outlets: []string{"OUT-02"}}

	_, staffTotal, err := e.List(ctx, staff, "sales_invoice", ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.Aggregate(ctx, staff, "sales_invoice", ListOpts{}, "outlet", []AggFn{{Kind: "count"}})
	if err != nil {
		t.Fatal(err)
	}
	ownerRes, err := e.Aggregate(ctx, owner, "sales_invoice", ListOpts{}, "outlet", []AggFn{{Kind: "count"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Count != int64(staffTotal) || staffTotal == 0 || res.Count >= ownerRes.Count {
		t.Fatalf("staff aggregate %d, staff list %d, owner aggregate %d", res.Count, staffTotal, ownerRes.Count)
	}
	if len(res.Groups) != 1 || res.Groups[0].Key != "OUT-02" {
		t.Fatalf("staff groups = %+v", res.Groups)
	}
}

// TestListFilterEqualsNull: the group-by view opens the "(kosong)" group with `field == null`; it matches the rows
// whose field is empty (missing or ""), the same rows Aggregate puts in its null-key group.
func TestListFilterEqualsNull(t *testing.T) {
	e, a := setup(t)
	ctx := context.Background()
	if _, err := e.Create(ctx, a, "lead", map[string]any{"lead_name": "Tanpa kota", "phone": "081200000002"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Create(ctx, a, "lead", map[string]any{"lead_name": "Kota kosong", "phone": "081200000003", "city": ""}); err != nil {
		t.Fatal(err)
	}
	rows, total, err := e.List(ctx, a, "lead", ListOpts{Filter: "city == null", Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if c, _ := r["city"].(string); c != "" {
			t.Errorf("city == null matched %v (city %q)", r["name"], c)
		}
	}
	res, err := e.Aggregate(ctx, a, "lead", ListOpts{}, "city", []AggFn{{Kind: "count"}})
	if err != nil {
		t.Fatal(err)
	}
	var empty int64
	for _, g := range res.Groups {
		if g.Key == nil {
			empty = g.Count
		}
	}
	if total < 2 || int64(total) != empty {
		t.Fatalf("city == null: %d rows, the empty group has %d", total, empty)
	}
	// and != null is the rest
	_, rest, err := e.List(ctx, a, "lead", ListOpts{Filter: "city != null"})
	if err != nil {
		t.Fatal(err)
	}
	if int64(rest+total) != res.Count {
		t.Fatalf("city != null %d + city == null %d != %d", rest, total, res.Count)
	}
}
