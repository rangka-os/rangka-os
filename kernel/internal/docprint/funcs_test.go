package docprint

import (
	"encoding/json"
	"sort"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
)

func TestRupiah(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{1250000, "Rp 1.250.000"},
		{"1250000.00", "Rp 1.250.000"},
		{1250000.0, "Rp 1.250.000"},
		{int64(1250000), "Rp 1.250.000"},
		{json.Number("1250000"), "Rp 1.250.000"},
		{0, "Rp 0"},
		{"0.5", "Rp 1"},
		{"-0.5", "-Rp 1"},
		{"-0.4", "Rp 0"},
		{-1500, "-Rp 1.500"},
		{999, "Rp 999"},
		{nil, ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := Rupiah(c.in); got != c.want {
			t.Errorf("Rupiah(%#v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestMoney(t *testing.T) {
	cases := []struct {
		in   any
		code string
		want string
	}{
		{"1234.5", "USD", "USD 1.234,50"},
		{1250000, "IDR", "Rp 1.250.000"},
		{"-10", "USD", "-USD 10,00"},
		{nil, "USD", ""},
		{1250000, "idr", "Rp 1.250.000"},
		{5, "", "Rp 5"},
		{"7", " usd ", "USD 7,00"},
	}
	for _, c := range cases {
		if got := Money(c.in, c.code); got != c.want {
			t.Errorf("Money(%#v, %q) = %q, want %q", c.in, c.code, got, c.want)
		}
	}
}

func TestNumber(t *testing.T) {
	if got := Number(1.5); got != "1,5" {
		t.Errorf("got %q", got)
	}
	if got := Number(1000); got != "1.000" {
		t.Errorf("got %q", got)
	}
	if got := Number("2.50"); got != "2,5" {
		t.Errorf("got %q", got)
	}
	if got := Number(2, 2); got != "2,00" {
		t.Errorf("got %q", got)
	}
	if got := Number("1.005", 2); got != "1,01" {
		t.Errorf("got %q", got)
	}
	if got := Number(nil); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestDate(t *testing.T) {
	cases := []struct {
		in    any
		style []string
		want  string
	}{
		{"2026-10-27", nil, "27 Okt 2026"},
		{"2026-10-27T09:00:00+07:00", nil, "27 Okt 2026"},
		{"2026-08-01", []string{"long"}, "1 Agustus 2026"},
		{"", nil, ""},
		{nil, nil, ""},
		{"bukan tanggal", nil, ""},
		{"2026-05-03", nil, "3 Mei 2026"},
	}
	for _, c := range cases {
		if got := Date(c.in, c.style...); got != c.want {
			t.Errorf("Date(%#v, %v) = %q, want %q", c.in, c.style, got, c.want)
		}
	}
	months := "Jan Feb Mar Apr Mei Jun Jul Agu Sep Okt Nov Des"
	var got string
	for m := 1; m <= 12; m++ {
		if m > 1 {
			got += " "
		}
		got += Date(time.Date(2026, time.Month(m), 1, 0, 0, 0, 0, time.UTC))[2:5]
	}
	if got != months {
		t.Errorf("months = %q", got)
	}
}

func TestTerbilang(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{1250000, "satu juta dua ratus lima puluh ribu rupiah"},
		{0, "nol rupiah"},
		{11, "sebelas rupiah"},
		{100, "seratus rupiah"},
		{1000, "seribu rupiah"},
		{1100, "seribu seratus rupiah"},
		{2015, "dua ribu lima belas rupiah"},
		{1000000, "satu juta rupiah"},
		{int64(21000000000), "dua puluh satu miliar rupiah"},
		{int64(5000000000000), "lima triliun rupiah"},
		{-7, "minus tujuh rupiah"},
		{"1250000.40", "satu juta dua ratus lima puluh ribu rupiah"},
		{"0.4", "nol rupiah"},
		{"-0.4", "nol rupiah"},
		{12, "dua belas rupiah"},
		{111, "seratus sebelas rupiah"},
		{1999, "seribu sembilan ratus sembilan puluh sembilan rupiah"},
		{nil, ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := Terbilang(c.in); got != c.want {
			t.Errorf("Terbilang(%#v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSubSum(t *testing.T) {
	if got := Sub("1000.50", 500); got != "500.5" {
		t.Errorf("Sub = %q", got)
	}
	if got := Sub("10", "10"); got != "0" {
		t.Errorf("Sub zero = %q", got)
	}
	if got := Sub("10", nil); got != "10" {
		t.Errorf("Sub nil = %q", got)
	}
	rows := []any{map[string]any{"amount": 100}, map[string]any{"amount": "50.25"}}
	if got := Sum(rows, "amount"); got != "150.25" {
		t.Errorf("Sum = %q", got)
	}
	typed := []map[string]any{{"amount": 1}, {"amount": 2}, {}}
	if got := Sum(typed, "amount"); got != "3" {
		t.Errorf("Sum typed = %q", got)
	}
	if got := Sum(nil, "amount"); got != "0" {
		t.Errorf("Sum nil = %q", got)
	}
}

var asOf = time.Date(2026, 10, 30, 14, 0, 0, 0, time.FixedZone("WIB", 7*3600))

func TestOverdue(t *testing.T) {
	if got := Overdue("2026-10-01", asOf); got != 29 {
		t.Errorf("got %d", got)
	}
	if got := Overdue("2026-11-15", asOf); got != 0 {
		t.Errorf("got %d", got)
	}
	if got := Overdue("2026-10-30", asOf); got != 0 {
		t.Errorf("same day: got %d", got)
	}
	if got := Overdue("2026-10-29T23:00:00Z", asOf); got != 1 {
		t.Errorf("date-time: got %d", got)
	}
	if got := Overdue(nil, asOf); got != 0 {
		t.Errorf("nil: got %d", got)
	}
}

func TestAging(t *testing.T) {
	rows := []any{
		map[string]any{"due_date": "2026-11-15", "grand_total": 1000, "paid_amount": 0},
		map[string]any{"due_date": "2026-10-20", "grand_total": 500, "paid_amount": 100},
		map[string]any{"due_date": "2026-09-10", "grand_total": 300, "paid_amount": 0},
	}
	got := Aging(rows, asOf)
	want := map[string]string{"current": "1000", "d30": "400", "d60": "300", "d90": "0", "over90": "0", "total": "1700"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("aging[%s] = %q, want %q", k, got[k], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("keys = %v", got)
	}
	edge := []any{
		map[string]any{"due_date": "2026-09-30", "grand_total": 1, "paid_amount": nil}, // 30 days
		map[string]any{"due_date": "2026-09-29", "grand_total": 2},                     // 31
		map[string]any{"due_date": "2026-08-31", "grand_total": 4},                     // 60
		map[string]any{"due_date": "2026-08-30", "grand_total": 8},                     // 61
		map[string]any{"due_date": "2026-07-31", "grand_total": 16},                    // 91
	}
	got = Aging(edge, asOf)
	if got["d30"] != "1" || got["d60"] != "6" || got["d90"] != "8" || got["over90"] != "16" || got["total"] != "31" {
		t.Errorf("edge aging = %v", got)
	}
}

func TestBaseFuncsMatchMetaNames(t *testing.T) {
	fm := baseFuncs(asOf)
	var have []string
	for k := range fm {
		have = append(have, k)
	}
	have = append(have, "label", "option")
	want := append([]string(nil), meta.PrintFuncNames...)
	sort.Strings(have)
	sort.Strings(want)
	if len(have) != len(want) {
		t.Fatalf("baseFuncs+label+option = %v, meta.PrintFuncNames = %v", have, want)
	}
	for i := range have {
		if have[i] != want[i] {
			t.Fatalf("baseFuncs+label+option = %v, meta.PrintFuncNames = %v", have, want)
		}
	}
}

func TestBaseFuncsBoundToAsOf(t *testing.T) {
	fm := baseFuncs(asOf)
	if got := fm["overdue"].(func(any) int)("2026-10-01"); got != 29 {
		t.Errorf("overdue = %d", got)
	}
	if got := fm["upper"].(func(any) string)("rp"); got != "RP" {
		t.Errorf("upper = %q", got)
	}
	if got := fm["upper"].(func(any) string)(nil); got != "" {
		t.Errorf("upper nil = %q", got)
	}
}

// Rates reach the renderer as the engine gives them: float64 (numeric and float8 columns, NULL as 0).
func TestTaxRate(t *testing.T) {
	cfg := map[string]any{"ppn_rate": 12}
	for _, c := range []struct {
		doc  map[string]any
		tax  map[string]any
		want string
	}{
		{map[string]any{"tax_rate": float64(0)}, cfg, "0"}, // a stored 0% is set
		{map[string]any{"tax_rate": float64(11)}, cfg, "11"},
		{map[string]any{"ppn_rate": float64(11.5), "tax_rate": float64(12)}, cfg, "11,5"},
		{map[string]any{"ppn_rate": float64(0), "tax_rate": float64(12)}, cfg, "0"},
		{map[string]any{"ppn_rate": "", "tax_rate": nil}, cfg, "12"},
		{map[string]any{"tax_rate": "11"}, cfg, "11"},
		{map[string]any{}, map[string]any{"ppn_rate": float64(11)}, "11"},
		{nil, map[string]any{"ppn_rate": 11}, "11"},
	} {
		if got := Number(TaxRate(c.doc, c.tax)); got != c.want {
			t.Errorf("TaxRate(%v, %v) = %q, want %q", c.doc, c.tax, got, c.want)
		}
	}
}

func TestDPPFactor(t *testing.T) {
	cfg := map[string]any{"dpp_ratio": "11/12"}
	for _, c := range []struct {
		doc  map[string]any
		tax  map[string]any
		want string
	}{
		{nil, cfg, "11/12"},
		{map[string]any{"dpp_ratio": ""}, cfg, "11/12"},
		{map[string]any{"dpp_ratio": nil}, cfg, "11/12"},
		{map[string]any{"dpp_ratio": "1/1"}, cfg, ""},
		{map[string]any{"dpp_ratio": "11/12"}, map[string]any{"dpp_ratio": "1/1"}, "11/12"},
		{nil, map[string]any{"dpp_ratio": 1}, ""},
		{nil, map[string]any{"dpp_ratio": float64(1)}, ""},
		{nil, map[string]any{"dpp_ratio": "1"}, ""},
		{nil, map[string]any{"dpp_ratio": "1/1"}, ""},
		{nil, map[string]any{"dpp_ratio": "12/12"}, ""},
		{nil, map[string]any{"dpp_ratio": "1.0"}, ""},
		{nil, map[string]any{"dpp_ratio": " 11/12 "}, "11/12"},
		{nil, map[string]any{"dpp_ratio": "nilai lain"}, "nilai lain"}, // not a number: printed as written
		{nil, map[string]any{}, ""},
	} {
		if got := DPPFactor(c.doc, c.tax); got != c.want {
			t.Errorf("DPPFactor(%v, %v) = %q, want %q", c.doc, c.tax, got, c.want)
		}
	}
}
