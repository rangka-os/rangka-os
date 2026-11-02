package money

import (
	"encoding/json"
	"math/big"
	"testing"
)

func TestParseAndString(t *testing.T) {
	ok := map[string]string{"1250.50": "1250.5", "1250000": "1250000", "-0.5000": "-0.5", " 7 ": "7", "0.0000": "0"}
	for in, want := range ok {
		d, err := Parse(in)
		if err != nil || d.String() != want {
			t.Errorf("Parse(%q) = %q, %v; want %q", in, d.String(), err, want)
		}
	}
	for _, in := range []string{"1.23456", "1e3", "+1", "", "abc", "12345678901234567", "1.", ".5", "-"} {
		if _, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) should fail", in)
		}
	}
	if _, err := Parse("9999999999999999.9999"); err != nil {
		t.Errorf("max range should parse: %v", err)
	}
}

func TestFromFloat(t *testing.T) {
	for in, want := range map[float64]string{0.1: "0.1", 15873.5: "15873.5"} {
		d, err := FromFloat(in)
		if err != nil || d.String() != want {
			t.Errorf("FromFloat(%v) = %q, %v", in, d.String(), err)
		}
	}
	if _, err := FromFloat(0.12345); err == nil {
		t.Error("0.12345 should fail")
	}
}

func mustParse(t *testing.T, s string) Decimal {
	t.Helper()
	d, err := Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestRound(t *testing.T) {
	cases := []struct {
		in     string
		places int
		want   string
	}{{"2.5", 0, "3"}, {"-2.5", 0, "-3"}, {"1.005", 2, "1.01"}, {"1.0049", 2, "1"}}
	for _, c := range cases {
		if got := mustParse(t, c.in).Round(c.places).String(); got != c.want {
			t.Errorf("%s.Round(%d) = %s; want %s", c.in, c.places, got, c.want)
		}
	}
}

func TestFromRat(t *testing.T) {
	for _, c := range []struct {
		n, d int64
		want string
	}{{1, 3, "0.3333"}, {2, 3, "0.6667"}, {-2, 3, "-0.6667"}} {
		d, err := FromRat(big.NewRat(c.n, c.d))
		if err != nil || d.String() != c.want {
			t.Errorf("FromRat(%d/%d) = %s, %v", c.n, c.d, d.String(), err)
		}
	}
	huge, _ := new(big.Rat).SetString("100000000000000000")
	if _, err := FromRat(huge); err == nil {
		t.Error("out of range should fail")
	}
}

func TestToIDR(t *testing.T) {
	cases := []struct {
		amt  string
		rate *big.Rat
		want int64
	}{
		{"1250.5", big.NewRat(31747, 2), 19849812},
		{"0.5", big.NewRat(3, 1), 2},
		{"-0.5", big.NewRat(3, 1), -2},
		{"0.25", big.NewRat(3, 1), 1},
		{"100", big.NewRat(1, 1), 100},
	}
	for _, c := range cases {
		got, err := ToIDR(mustParse(t, c.amt), c.rate)
		if err != nil || got != c.want {
			t.Errorf("ToIDR(%s, %s) = %d, %v; want %d", c.amt, c.rate, got, err, c.want)
		}
	}
	big1, _ := new(big.Rat).SetString("9999999999999999")
	if _, err := ToIDR(mustParse(t, "9999999999999999"), big1); err == nil {
		t.Error("int64 overflow should fail")
	}
}

func TestJSON(t *testing.T) {
	b, err := json.Marshal(map[string]any{"a": mustParse(t, "1250.5")})
	if err != nil || string(b) != `{"a":"1250.5"}` {
		t.Errorf("got %s, %v", b, err)
	}
}

func TestCodes(t *testing.T) {
	if !IsIDR("") || !IsIDR("IDR") || IsIDR("USD") {
		t.Error("IsIDR")
	}
	if !ValidCode("USD") || !ValidCode("") || ValidCode("usd") || ValidCode("US") || ValidCode("USDD") {
		t.Error("ValidCode")
	}
}

func TestMisc(t *testing.T) {
	var z Decimal
	if !z.IsZero() || z.String() != "0" || z.Sign() != 0 {
		t.Error("zero value")
	}
	a, b := mustParse(t, "1.5"), mustParse(t, "2.25")
	if a.Add(b).String() != "3.75" || a.Sub(b).String() != "-0.75" || a.Neg().String() != "-1.5" || a.Cmp(b) != -1 {
		t.Error("arith")
	}
	if !mustParse(t, "3.0").IsWhole() || a.IsWhole() {
		t.Error("IsWhole")
	}
	if !InRange(mustParse(t, "9999999999999999.9999")) {
		t.Error("InRange")
	}
	if InRange(FromInt(10000000000000000)) {
		t.Error("InRange over")
	}
	for _, v := range []any{a, int64(2), 3, 1.5, "2.5"} {
		if r, ok := Rat(v); !ok || r == nil {
			t.Errorf("Rat(%T)", v)
		}
	}
	if _, ok := Rat(true); ok {
		t.Error("Rat(bool)")
	}
	if r, _ := Rat(0.1); r.Cmp(big.NewRat(1, 10)) != 0 {
		t.Error("Rat(float) shortest form")
	}
}
