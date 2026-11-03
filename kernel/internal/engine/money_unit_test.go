package engine

import (
	"sort"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/money"
	"github.com/rangka-os/rangka-os/kernel/internal/schema"
)

func mdec(t *testing.T, s string) money.Decimal {
	t.Helper()
	d, err := money.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// TestCompareMoney: list sort by a money field is numeric, also against plain numbers.
func TestCompareMoney(t *testing.T) {
	vals := []any{mdec(t, "100"), mdec(t, "9"), mdec(t, "-5"), mdec(t, "9.5")}
	sort.SliceStable(vals, func(i, j int) bool { return compareValues(vals[i], vals[j]) < 0 })
	var got []string
	for _, v := range vals {
		got = append(got, v.(money.Decimal).String())
	}
	if want := "[-5 9 9.5 100]"; sliceStr(got) != want {
		t.Errorf("sorted = %v, want %s", got, want)
	}
	if c := compareValues(mdec(t, "9"), int64(10)); c != -1 {
		t.Errorf("9 vs int 10 = %d", c)
	}
	if c := compareValues(100.0, mdec(t, "9")); c != 1 {
		t.Errorf("float 100 vs 9 = %d", c)
	}
	if c := compareValues(mdec(t, "0.3"), 0.3); c != 0 {
		t.Errorf("0.3 vs float 0.3 = %d", c)
	}
}

func sliceStr(s []string) string {
	out := "["
	for i, x := range s {
		if i > 0 {
			out += " "
		}
		out += x
	}
	return out + "]"
}

// TestStatusMoney: the built-in invoice status reads money grand_total and paid_amount.
func TestStatusMoney(t *testing.T) {
	reg, rep := meta.Validate("../../testdata/money-apps", "", nil, schema.Embedded())
	if rep.Errors() > 0 {
		t.Fatalf("metadata errors: %v", rep.All())
	}
	m := reg.Models["bill"]
	for paid, want := range map[string]string{"100": "paid", "100.5": "paid", "40.25": "partly", "0": "submitted"} {
		rec := map[string]any{"docstatus": int64(1), "grand_total": mdec(t, "100"), "paid_amount": mdec(t, paid)}
		if got := Status(m, rec, "2026-11-03"); got != want {
			t.Errorf("paid %s: status = %q, want %q", paid, got, want)
		}
	}
}
