package cel

import (
	"math"
	"strings"
	"testing"
)

// int() of a double or money outside int's range is a CEL error, never a platform-dependent int64
// (audit changeset set_once, note 3).
func TestIntOutOfRange(t *testing.T) {
	// money holds at most 16 integer digits (money.InRange) and arithmetic past that is already an error, so
	// int(money) cannot leave int64 today; the check in int() keeps it so if that bound ever grows
	vars := map[string]any{
		"maxm": mustDec(t, "9999999999999999.9999"),
		"negm": mustDec(t, "-9999999999999999.9999"),
		"inf":  math.Inf(1),
	}
	for _, src := range []string{
		"int(1e19)", "int(-1e19)", "int(9223372036854775808.0)", "int(double('NaN'))", "int(double('Infinity'))",
		"int(inf)", "int(double('-Infinity'))",
	} {
		got, err := Eval(src, env(vars))
		if err == nil {
			t.Errorf("%s = %v, want an error", src, got)
			continue
		}
		if !strings.Contains(err.Error(), "out of range") {
			t.Errorf("%s: %v", src, err)
		}
	}
	ok := map[string]int64{
		"int(2.7)": 2, "int(-2.7)": -2, "int(9.2e18)": 9200000000000000000, "int(-9223372036854775808.0)": -9223372036854775808,
		"int(maxm)": 9999999999999999, "int(negm)": -9999999999999999, "int(-1e18)": -1000000000000000000,
	}
	for src, want := range ok {
		got, err := Eval(src, env(vars))
		if err != nil || got != want {
			t.Errorf("%s = %v, %v; want %d", src, got, err, want)
		}
	}
}
