package engine

import (
	"context"
	"math/big"
	"os"
	"path/filepath"
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

const unitHead = "# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/"

// TestGrandchildRate: a row of a row reads the rate from the document, not from its direct parent row (whose
// rate is a unit price) nor from itself.
func TestGrandchildRate(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"shop/app.rk":               unitHead + "app.json\nname: shop\nversion: 1.0.0\ntitle: {id: Toko}\n",
		"shop/models/order.rk":      unitHead + "model.json\nname: order\nlabel: {id: Pesanan}\nfields:\n  - {name: currency, type: data}\n  - {name: rate, type: float}\n  - {name: lines, type: table, of: order_line}\n",
		"shop/models/order_line.rk": unitHead + "model.json\nname: order_line\nis_child: true\nlabel: {id: Baris}\nfields:\n  - {name: rate, type: float}\n  - {name: parts, type: table, of: order_part}\n",
		"shop/models/order_part.rk": unitHead + "model.json\nname: order_part\nis_child: true\nlabel: {id: Bagian}\nfields:\n  - {name: currency, type: data}\n  - {name: rate, type: float}\n  - {name: price, type: money, currency_field: currency}\n",
	}
	for p, s := range files {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	reg, rep := meta.Validate(dir, "", nil, schema.Embedded())
	if rep.Errors() > 0 {
		t.Fatalf("metadata errors: %v", rep.All())
	}
	part := map[string]any{"currency": "USD", "rate": 5.0, "price": mdec(t, "2")}
	ln := map[string]any{"rate": 3.0, "parts": []any{part}}
	doc := map[string]any{"currency": "USD", "rate": 10.0, "lines": []any{ln}}
	e := New(nil, reg)
	ds := e.newScope(context.Background(), nil, Actor{}, reg.Models["order"], doc, nil)
	ls := e.newScope(context.Background(), nil, Actor{}, reg.Models["order_line"], ln, nil)
	ls.parent = ds
	ps := e.newScope(context.Background(), nil, Actor{}, reg.Models["order_part"], part, nil)
	ps.parent = ls
	f := reg.Models["order_part"].Field("price")
	if got := ps.currencyOf(f); got != "USD" {
		t.Errorf("currency = %q, want USD", got)
	}
	r, ok := ps.rateOf(f)
	if !ok || r.Cmp(big.NewRat(10, 1)) != 0 {
		t.Errorf("rate = %v %v, want 10 (the document's)", r, ok)
	}
	ds.settle()
	if got := part["price_idr"]; got != int64(20) {
		t.Errorf("price_idr = %#v, want 20", got)
	}
	// a missing document rate is reported on the document's field
	delete(doc, "rate")
	issues := ds.checkMoney("", "", "", map[string]bool{})
	if len(issues) != 1 || issues[0].Field != "rate" || issues[0].Code != "missing_rate" {
		t.Errorf("issues = %+v, want missing_rate on rate", issues)
	}
}
