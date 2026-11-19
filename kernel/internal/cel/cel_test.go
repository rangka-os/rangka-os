package cel

import (
	"reflect"
	"testing"
)

func env(vars map[string]any) *Env {
	return &Env{
		Vars:  func(n string) (any, bool) { v, ok := vars[n]; return v, ok },
		Today: func() string { return "2026-10-30" },
		Option: func(f, a string) (any, error) {
			if f == "stage" && a == "probability" {
				return int64(50), nil
			}
			return nil, nil
		},
		Apps: func(n string) bool { return n == "sales" },
	}
}

func TestEval(t *testing.T) {
	cust := &Link{Model: "customer", Name: "CUST-1", Load: func() (map[string]any, error) {
		return map[string]any{"payment_days": int64(14), "outstanding": 100.0, "credit_limit": 1000.0}, nil
	}}
	vars := map[string]any{
		"npwp": "", "stage": "proposal", "value": 12500000.0, "probability": 50.0, "posting_date": "2026-10-20",
		"customer": cust, "deal": "", "lead": "LEAD-1", "done": false, "grand_total": 500.0, "qty": 2.0, "rate": 1000.0, "discount": 10.0,
		"items": []any{map[string]any{"amount": 100.0}, map[string]any{"amount": 250.5}},
		"user":  map[string]any{"name": "USR-002", "roles": []any{"sales_rep"}}, "owner": &Link{Model: "user", Name: "USR-002"},
	}
	cases := []struct {
		src  string
		want any
	}{
		{"npwp == '' || size(npwp) == 16", true},
		{"npwp == null", true},
		{"stage == null", false},
		{"npwp != null", false},
		{"null == null", true},
		{"deal == null && lead != null", true},
		{"stage != 'won' && stage != 'lost'", true},
		{"round(value * probability / 100)", 6250000.0},
		{"posting_date + duration(customer.payment_days)", "2026-11-03"},
		{"posting_date + duration('7d')", "2026-10-27"},
		{"posting_date - duration('1d')", "2026-10-19"},
		{"customer.outstanding + grand_total <= customer.credit_limit", true},
		{"items.map(i, i.amount).sum()", 350.5},
		{"sum(items.map(i, i.amount))", 350.5},
		{"round(qty * rate * (1 - discount / 100))", 1800.0},
		{"deal != '' || lead != ''", true},
		{"!done", true},
		{"option('stage', 'probability')", int64(50)},
		{"app_installed('sales')", true},
		{"app_installed('stock')", false},
		{"truncate('hello world', 5)", "hello"},
		{"'abc'.contains('b')", true},
		{"stage in ['proposal', 'won']", true},
		{"owner == user.name", true},
		{"posting_date <= today()", true},
		{"size(items) > 1 ? 'many' : 'one'", "many"},
		{"items.exists(i, i.amount > 200)", true},
		{"items.all(i, i.amount > 200)", false},
		{"1 == 1.0", true},
		{"7 / 2", int64(3)},
		{"7.0 / 2", 3.5},
		{"round(2.5)", 3.0},
		{"customer.payment_days", int64(14)},
		{"deal.anything", ""},
	}
	for _, c := range cases {
		got, err := Eval(c.src, env(vars))
		if err != nil {
			t.Errorf("%s: %v", c.src, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s = %#v, want %#v", c.src, got, c.want)
		}
	}
	// error absorption: false && error → false
	if v, err := Eval("false && nope", env(vars)); err != nil || v != false {
		t.Errorf("false && nope = %v %v", v, err)
	}
	if _, err := Eval("'a' + 1", env(vars)); err == nil {
		t.Error("'a' + 1 should fail")
	}
	if _, err := Parse("(a"); err == nil {
		t.Error("parse error expected")
	}
}

func TestIdents(t *testing.T) {
	got := Idents("items.map(i, i.amount).sum() + customer.payment_days + size(npwp) == 'x.y'")
	want := []Ref{{"ref", "items", ""}, {"ref", "customer", "payment_days"}, {"fn", "size", ""}, {"ref", "npwp", ""}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestInterpolate(t *testing.T) {
	e := env(map[string]any{"due": []any{1, 2, 3}})
	if got := Interpolate("Ada {size(due)} aktivitas", e); got != "Ada 3 aktivitas" {
		t.Errorf("got %q", got)
	}
}

// Every comprehension macro binds its first argument; Idents skips it and LoopRefs reports the member reads
// on it with the identifier the macro ranges over.
func TestIdentsMacroVars(t *testing.T) {
	src := "items.all(l, l.qty > 0) && items.exists(e, e.rate > 1) && items.exists_one(o, o.discount > 0) && " +
		"items.filter(f, f.rate > 0).map(g, g.amount).sum() > 0 && [1, 2].all(n, n > 0) && 'x.all(q, q)' != ''"
	want := []Ref{{"ref", "items", ""}, {"ref", "items", ""}, {"ref", "items", ""}, {"ref", "items", ""}}
	if got := Idents(src); !reflect.DeepEqual(got, want) {
		t.Errorf("Idents: got %v want %v", got, want)
	}
	wantLoops := []LoopRef{{"l", "items", "qty"}, {"e", "items", "rate"}, {"o", "items", "discount"}, {"f", "items", "rate"}}
	if got := LoopRefs(src); !reflect.DeepEqual(got, wantLoops) {
		t.Errorf("LoopRefs: got %v want %v", got, wantLoops)
	}
	// the evaluator runs every macro the validator accepts
	e := env(map[string]any{"items": []any{map[string]any{"qty": 2.0}, map[string]any{"qty": 0.0}}})
	for expr, want := range map[string]any{
		"items.all(l, l.qty > 0)":              false,
		"items.exists(l, l.qty > 1)":           true,
		"items.exists_one(l, l.qty == 0)":      true,
		"size(items.filter(l, l.qty > 0))":     int64(1),
		"items.map(l, l.qty).sum()":            2.0,
		"items.map(l, l.qty > 0, l.qty).sum()": 2.0,
	} {
		got, err := Eval(expr, e)
		if err != nil {
			t.Errorf("%s: %v", expr, err)
			continue
		}
		if Unwrap(got) != want {
			t.Errorf("%s = %v (%T), want %v", expr, got, got, want)
		}
	}
}

func TestJoin(t *testing.T) {
	e := env(map[string]any{
		"names": []any{"Ani", "Budi"},
		"mixed": []any{"a", int64(2), 1.5, true},
		"none":  nil,
		"rows":  []any{map[string]any{"n": "x"}, map[string]any{"n": "y"}},
	})
	for expr, want := range map[string]string{
		"names.join(', ')":           "Ani, Budi",
		"names.join()":               "AniBudi",
		"mixed.join('/')":            "a/2/1.5/true",
		"none.join(', ')":            "",
		"[].join(', ')":              "",
		"rows.map(r, r.n).join('-')": "x-y",
	} {
		got, err := Eval(expr, e)
		if err != nil {
			t.Errorf("%s: %v", expr, err)
			continue
		}
		if got != want {
			t.Errorf("%s = %q, want %q", expr, got, want)
		}
	}
	for _, expr := range []string{"'abc'.join(',')", "names.join(1)", "names.join(',', ';')"} {
		if _, err := Eval(expr, e); err == nil {
			t.Errorf("%s: expected an error", expr)
		}
	}
}

func TestMethodNames(t *testing.T) {
	got := MethodNames(`items.map(l, l.qty).sum() + size(x) > 0 && name.lower() != 'a.upper()' && n . trim () != "b.fmt()" && items.map(l, l.uom).join(', ') != ''`)
	want := []string{"map", "sum", "lower", "trim", "join"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}
