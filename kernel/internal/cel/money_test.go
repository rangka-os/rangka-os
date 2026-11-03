package cel

import (
	"errors"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/money"
)

func mustDec(t *testing.T, s string) money.Decimal {
	t.Helper()
	d, err := money.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestDecimalArithmetic(t *testing.T) {
	vars := map[string]any{
		"a": mustDec(t, "1250.5"),
		"b": mustDec(t, "0.25"),
		"q": int64(3),
		"r": float64(0.11),
		"items": []any{
			map[string]any{"amount": mustDec(t, "0.25")},
			map[string]any{"amount": mustDec(t, "0.25")},
		},
	}
	dec := map[string]string{
		"a + b":                       "1250.75",
		"a - 1":                       "1249.5",
		"b * q":                       "0.75",
		"a * r":                       "137.555",
		"a / q":                       "416.8333",
		"sum(items.map(i, i.amount))": "0.5",
		"sum([b, 1])":                 "1.25",
		"round(a)":                    "1251",
		"-a":                          "-1250.5",
		"-(0 - b)":                    "0.25",
		"round(a * r, 2)":             "137.56",
	}
	for src, want := range dec {
		got, err := Eval(src, env(vars))
		if err != nil {
			t.Errorf("%s: %v", src, err)
			continue
		}
		d, ok := got.(money.Decimal)
		if !ok || d.String() != want {
			t.Errorf("%s = %#v, want decimal %s", src, got, want)
		}
	}
	if got, err := Eval("round(neg)", env(map[string]any{"neg": mustDec(t, "-2.5")})); err != nil || got.(money.Decimal).String() != "-3" {
		t.Errorf("round(-2.5) = %v, %v", got, err)
	}
	bools := map[string]bool{"a > 1000": true, "b == 0.25": true, "b < b": false}
	for src, want := range bools {
		if got, err := Eval(src, env(vars)); err != nil || got != want {
			t.Errorf("%s = %v, %v", src, got, err)
		}
	}
	if got, err := Eval("round(2.4)", env(vars)); err != nil || got != float64(2) {
		t.Errorf("round(2.4) = %#v, %v", got, err)
	}
	if got, err := Eval("round(2.345, 2)", env(vars)); err != nil || got != 2.35 {
		t.Errorf("round(2.345, 2) = %#v, %v", got, err)
	}
	errs := map[string]string{
		"a / 0":       "division by zero",
		"a / 0.0":     "division by zero",
		"a + 'x'":     "no such overload: decimal + string",
		"round(a, 5)": "round",
	}
	for src, want := range errs {
		if _, err := Eval(src, env(vars)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", src, err, want)
		}
	}
}

// TestDecimalConversions: int(decimal) truncates toward zero like int(double); double(decimal) converts.
func TestDecimalConversions(t *testing.T) {
	vars := map[string]any{"p": mustDec(t, "2.7"), "n": mustDec(t, "-2.7"), "h": mustDec(t, "1250.5")}
	want := map[string]any{"int(p)": int64(2), "int(n)": int64(-2), "double(h)": 1250.5, "double(n)": -2.7}
	for src, w := range want {
		got, err := Eval(src, env(vars))
		if err != nil || got != w {
			t.Errorf("%s = %#v, %v; want %#v", src, got, err, w)
		}
	}
}

// TestDecimalRangeError: decimal arithmetic out of numeric(20,4) keeps money.ErrRange as its cause.
func TestDecimalRangeError(t *testing.T) {
	vars := map[string]any{"big": mustDec(t, "9000000000000000"), "items": []any{mustDec(t, "9000000000000000"), mustDec(t, "9000000000000000")}}
	for _, src := range []string{"big * 2", "big + big", "sum(items)"} {
		_, err := Eval(src, env(vars))
		if err == nil || !errors.Is(err, money.ErrRange) {
			t.Errorf("%s: %v, want a money.ErrRange cause", src, err)
		}
	}
	if _, err := Eval("big / 0", env(vars)); err == nil || errors.Is(err, money.ErrRange) {
		t.Errorf("division by zero: %v, want an error that is not ErrRange", err)
	}
}
