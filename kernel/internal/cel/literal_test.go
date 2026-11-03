package cel

import (
	"math"
	"testing"
)

func TestLiteralRoundTrip(t *testing.T) {
	for _, v := range []any{"", "CUST-0001", `a"b`, `a\b`, `\"`, `" || true || "`, "x\" == x || \"", "baris\nbaru\ttab",
		"emoji 😀 é", "\x00\x7f", "r\"raw\"", "'single'", "`back`", int64(-42), 7, 1.5, -0.25, true, false} {
		src, ok := Literal(v)
		if !ok {
			t.Fatalf("Literal(%#v) not ok", v)
		}
		got, err := Eval(src, &Env{})
		if err != nil {
			t.Fatalf("Eval(%s) for %#v: %v", src, v, err)
		}
		if !Equal(got, v) {
			t.Errorf("Literal(%#v) = %s evaluates to %#v", v, src, got)
		}
		// the literal is one operand: comparing a field with it never turns into another expression
		eq, err := Eval("f == "+src, &Env{Vars: func(n string) (any, bool) { return "other", n == "f" }})
		if err != nil || eq != Equal("other", v) {
			t.Errorf("f == %s = %v, %v", src, eq, err)
		}
	}
}

func TestLiteralRejects(t *testing.T) {
	for _, v := range []any{nil, math.NaN(), math.Inf(1), map[string]any{}, []any{"a"}} {
		if src, ok := Literal(v); ok {
			t.Errorf("Literal(%#v) = %s, want not ok", v, src)
		}
	}
}
