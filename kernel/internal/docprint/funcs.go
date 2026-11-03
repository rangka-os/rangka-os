// Package docprint renders print formats (contract 0008) into HTML and PDF.
package docprint

import (
	"encoding/json"
	"fmt"
	"html/template"
	"math/big"
	"strconv"
	"strings"
	"time"
)

var (
	shortMonths = [12]string{"Jan", "Feb", "Mar", "Apr", "Mei", "Jun", "Jul", "Agu", "Sep", "Okt", "Nov", "Des"}
	longMonths  = [12]string{"Januari", "Februari", "Maret", "April", "Mei", "Juni", "Juli", "Agustus", "September", "Oktober", "November", "Desember"}
)

// baseFuncs returns the helpers that do not depend on the model. `label` and `option` are bound per
// render. `overdue` and `aging` are bound to asOf, the print time.
func baseFuncs(asOf time.Time) template.FuncMap {
	return template.FuncMap{
		"rupiah":    Rupiah,
		"money":     Money,
		"number":    Number,
		"date":      Date,
		"terbilang": Terbilang,
		"sub":       Sub,
		"sum":       Sum,
		"overdue":   func(d any) int { return Overdue(d, asOf) },
		"aging":     func(rows any) map[string]string { return Aging(rows, asOf) },
		"taxrate":   TaxRate,
		"dppfactor": DPPFactor,
		"upper": func(v any) string {
			if v == nil {
				return ""
			}
			return strings.ToUpper(fmt.Sprint(v))
		},
	}
}

// toRat converts an accepted numeric input to an exact rational. ok is false for nil, "" and
// anything that does not parse. Floats go through their shortest decimal form, never float arithmetic.
func toRat(v any) (*big.Rat, bool) {
	var s string
	switch x := v.(type) {
	case nil:
		return nil, false
	case int:
		return new(big.Rat).SetInt64(int64(x)), true
	case int64:
		return new(big.Rat).SetInt64(x), true
	case float64:
		s = strconv.FormatFloat(x, 'f', -1, 64)
	case json.Number:
		s = string(x)
	case string:
		s = x
	default:
		s = fmt.Sprint(v)
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, false
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil, false
	}
	return r, true
}

// orZero is toRat with missing or unparseable values counted as 0, for arithmetic.
func orZero(v any) *big.Rat {
	if r, ok := toRat(v); ok {
		return r
	}
	return new(big.Rat)
}

// scaled rounds |r|·10^decimals half away from zero and returns it as a non-negative integer,
// with the sign of the rounded result (a value that rounds to zero is not negative).
func scaled(r *big.Rat, decimals int) (n *big.Int, neg bool) {
	p := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	abs := new(big.Rat).Abs(r)
	abs.Mul(abs, new(big.Rat).SetInt(p))
	abs.Add(abs, big.NewRat(1, 2))
	n = new(big.Int).Quo(abs.Num(), abs.Denom())
	return n, r.Sign() < 0 && n.Sign() != 0
}

// group puts "." between thousands of a digit string.
func group(digits string) string {
	if len(digits) <= 3 {
		return digits
	}
	var b strings.Builder
	first := len(digits) % 3
	if first > 0 {
		b.WriteString(digits[:first])
	}
	for i := first; i < len(digits); i += 3 {
		if b.Len() > 0 {
			b.WriteByte('.')
		}
		b.WriteString(digits[i : i+3])
	}
	return b.String()
}

// fixed formats r with exactly decimals digits ("1.234,50"), without sign.
func fixed(r *big.Rat, decimals int) (text string, neg bool) {
	n, neg := scaled(r, decimals)
	s := n.String()
	if decimals == 0 {
		return group(s), neg
	}
	if len(s) <= decimals {
		s = strings.Repeat("0", decimals-len(s)+1) + s
	}
	return group(s[:len(s)-decimals]) + "," + s[len(s)-decimals:], neg
}

// Rupiah formats whole rupiah, "Rp 1.250.000" (negative "-Rp 1.250.000").
func Rupiah(v any) string {
	return Money(v, "IDR")
}

// Money formats an amount with a currency code: IDR is whole rupiah, anything else has two decimals. The code
// is trimmed and upper-cased; an empty code is IDR.
func Money(v any, code string) string {
	r, ok := toRat(v)
	if !ok {
		return ""
	}
	code = strings.ToUpper(strings.TrimSpace(code))
	decimals, prefix := 2, code+" "
	if code == "IDR" || code == "" {
		decimals, prefix = 0, "Rp "
	}
	text, neg := fixed(r, decimals)
	if neg {
		return "-" + prefix + text
	}
	return prefix + text
}

// Number formats with Indonesian separators. Without decimals the fraction is exact with trailing
// zeros dropped; with decimals it is rounded to that many digits.
func Number(v any, decimals ...int) string {
	r, ok := toRat(v)
	if !ok {
		return ""
	}
	if len(decimals) > 0 && decimals[0] >= 0 {
		text, neg := fixed(r, decimals[0])
		if neg {
			return "-" + text
		}
		return text
	}
	dec := trimDecimal(r)
	neg := strings.HasPrefix(dec, "-")
	dec = strings.TrimPrefix(dec, "-")
	whole, frac, _ := strings.Cut(dec, ".")
	out := group(whole)
	if frac != "" {
		out += "," + frac
	}
	if neg {
		out = "-" + out
	}
	return out
}

// trimDecimal renders r as a decimal string with trailing zeros dropped ("500.5", "0", "-2").
func trimDecimal(r *big.Rat) string {
	s := r.FloatString(12)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(s, "0")
		s = strings.TrimSuffix(s, ".")
	}
	if s == "-0" || s == "" {
		return "0"
	}
	return s
}

// Date formats an ISO date or date-time as "27 Okt 2026" (style "long": "27 Oktober 2026").
// The calendar date is the first 10 characters; no time zone conversion happens.
func Date(v any, style ...string) string {
	var t time.Time
	switch x := v.(type) {
	case nil:
		return ""
	case time.Time:
		t = x
	default:
		d, ok := parseDay(fmt.Sprint(v))
		if !ok {
			return ""
		}
		t = d
	}
	months := shortMonths
	if len(style) > 0 && style[0] == "long" {
		months = longMonths
	}
	return fmt.Sprintf("%d %s %d", t.Day(), months[t.Month()-1], t.Year())
}

// parseDay reads the leading YYYY-MM-DD of s as a calendar date (UTC midnight).
func parseDay(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if len(s) < 10 {
		return time.Time{}, false
	}
	t, err := time.Parse("2006-01-02", s[:10])
	return t, err == nil
}

var (
	ones = [...]string{"nol", "satu", "dua", "tiga", "empat", "lima", "enam", "tujuh", "delapan", "sembilan", "sepuluh", "sebelas"}
)

type scale struct {
	value int64
	name  string
}

var scales = [...]scale{{1_000_000_000_000, "triliun"}, {1_000_000_000, "miliar"}, {1_000_000, "juta"}, {1000, "ribu"}}

// spell writes n > 0 in Indonesian words.
func spell(n int64) string {
	switch {
	case n < 12:
		return ones[n]
	case n < 20:
		return ones[n-10] + " belas"
	case n < 100:
		return withRest(ones[n/10]+" puluh", n%10)
	case n < 200:
		return withRest("seratus", n-100)
	case n < 1000:
		return withRest(ones[n/100]+" ratus", n%100)
	case n < 2000:
		return withRest("seribu", n-1000)
	}
	for _, s := range scales {
		if n >= s.value {
			return withRest(spell(n/s.value)+" "+s.name, n%s.value)
		}
	}
	return ""
}

func withRest(head string, rest int64) string {
	if rest == 0 {
		return head
	}
	return head + " " + spell(rest)
}

// Terbilang spells an amount in Indonesian rupiah: "satu juta dua ratus lima puluh ribu rupiah".
// The amount is rounded to whole rupiah first, like Rupiah. Amounts beyond int64 give "".
func Terbilang(v any) string {
	r, ok := toRat(v)
	if !ok {
		return ""
	}
	n, neg := scaled(r, 0)
	if !n.IsInt64() {
		return ""
	}
	out := "nol"
	if n.Sign() > 0 {
		out = spell(n.Int64())
	}
	if neg {
		out = "minus " + out
	}
	return out + " rupiah"
}

// Sub returns a-b as an exact decimal string; missing operands count as 0.
func Sub(a, b any) string {
	return trimDecimal(new(big.Rat).Sub(orZero(a), orZero(b)))
}

// Sum adds field over a list of maps as an exact decimal string; missing values count as 0.
func Sum(list any, field string) string {
	total := new(big.Rat)
	for _, row := range rowsOf(list) {
		total.Add(total, orZero(row[field]))
	}
	return trimDecimal(total)
}

func rowsOf(list any) []map[string]any {
	switch x := list.(type) {
	case []map[string]any:
		return x
	case []any:
		rows := make([]map[string]any, 0, len(x))
		for _, e := range x {
			if m, ok := e.(map[string]any); ok {
				rows = append(rows, m)
			}
		}
		return rows
	}
	return nil
}

// Overdue returns the whole calendar days past d as of asOf (in asOf's own location), 0 when not due.
func Overdue(d any, asOf time.Time) int {
	if d == nil {
		return 0
	}
	due, ok := parseDay(fmt.Sprint(d))
	if !ok {
		return 0
	}
	y, m, day := asOf.Date()
	today := time.Date(y, m, day, 0, 0, 0, 0, time.UTC)
	days := int(today.Sub(due).Hours() / 24)
	if days < 0 {
		return 0
	}
	return days
}

// Aging sums the open amount (grand_total - paid_amount) of each row by age past due_date.
func Aging(rows any, asOf time.Time) map[string]string {
	keys := [...]string{"current", "d30", "d60", "d90", "over90"}
	sums := map[string]*big.Rat{}
	for _, k := range keys {
		sums[k] = new(big.Rat)
	}
	total := new(big.Rat)
	for _, row := range rowsOf(rows) {
		open := new(big.Rat).Sub(orZero(row["grand_total"]), orZero(row["paid_amount"]))
		days := Overdue(row["due_date"], asOf)
		k := "over90"
		switch {
		case days == 0:
			k = "current"
		case days <= 30:
			k = "d30"
		case days <= 60:
			k = "d60"
		case days <= 90:
			k = "d90"
		}
		sums[k].Add(sums[k], open)
		total.Add(total, open)
	}
	out := map[string]string{"total": trimDecimal(total)}
	for _, k := range keys {
		out[k] = trimDecimal(sums[k])
	}
	return out
}

// recordValue is m[key] when it is set: present and neither nil nor "". Any number, 0 included, is set.
func recordValue(m any, key string) (any, bool) {
	mm, _ := m.(map[string]any)
	v, ok := mm[key]
	if !ok || v == nil || v == "" {
		return nil, false
	}
	return v, true
}

// TaxRate is the PPN rate a document prints: the record's ppn_rate, else its tax_rate, else tax's ppn_rate
// (the org's config tax, defaults filled). A stored 0 is a rate, not a missing one.
func TaxRate(doc, tax any) any {
	for _, k := range []string{"ppn_rate", "tax_rate"} {
		if v, ok := recordValue(doc, k); ok {
			return v
		}
	}
	v, _ := recordValue(tax, "ppn_rate")
	return v
}

// dppValue is m's dpp_ratio when it is set. Unlike a rate, a numeric 0 is unset: no factor is 0, and the engine
// reads an empty numeric column as 0.
func dppValue(m any) (any, bool) {
	v, ok := recordValue(m, "dpp_ratio")
	if _, isText := v.(string); ok && !isText {
		if r, num := toRat(v); num && r.Sign() == 0 {
			return nil, false
		}
	}
	return v, ok
}

// DPPFactor is the DPP factor a document prints: the record's dpp_ratio, else tax's dpp_ratio, as written
// ("11/12"); a numeric 0 counts as unset. It is "" when the factor is exactly 1 (1, "1/1", "12/12", "1.0"):
// the DPP is the full value.
func DPPFactor(doc, tax any) string {
	v, ok := dppValue(doc)
	if !ok {
		v, _ = dppValue(tax)
	}
	if v == nil {
		return ""
	}
	s := strings.TrimSpace(fmt.Sprint(v))
	if r, ok := toRat(s); ok && r.Cmp(big.NewRat(1, 1)) == 0 {
		return ""
	}
	return s
}
