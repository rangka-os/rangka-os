package meta

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/schema"
)

const moneyHead = "# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/"

// moneyApp writes app "billing" with models bill (header) and bill_line (child) into a temp dir.
// billFields and lineFields replace the default field lists when not empty; quoteFields, when given, adds a
// second header model quote.
func moneyApp(t *testing.T, billFields, lineFields string, quoteFields ...string) string {
	t.Helper()
	if billFields == "" {
		billFields = `  - {name: currency, type: data}
  - {name: rate, type: float, label: {id: Kurs, en: Rate}}
  - {name: total, type: money, currency_field: currency, label: {id: Total, en: Total}}
  - {name: items, type: table, of: bill_line}
`
	}
	if lineFields == "" {
		lineFields = `  - {name: amount, type: money, currency_field: currency}
`
	}
	dir := t.TempDir()
	files := map[string]string{
		"billing/app.rk":         moneyHead + "app.json\nname: billing\nversion: 1.0.0\ntitle: {id: Tagihan}\n",
		"billing/models/bill.rk": moneyHead + "model.json\nname: bill\nlabel: {id: Tagihan}\nfields:\n" + billFields,
		"billing/models/bill_line.rk": moneyHead + "model.json\nname: bill_line\nis_child: true\nlabel: {id: Baris}\nfields:\n" +
			lineFields,
	}
	if len(quoteFields) > 0 {
		files["billing/models/quote.rk"] = moneyHead + "model.json\nname: quote\nlabel: {id: Penawaran}\nfields:\n" + quoteFields[0]
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
	return dir
}

func TestMoneyTwins(t *testing.T) {
	reg, rep := Validate(moneyApp(t, "", ""), "", nil, schema.Embedded())
	if !onlyDeprecations(rep) { // the fixture is a 0.4 app (contract 0001)
		t.Fatalf("problems: %v", rep.All())
	}
	tw := reg.Models["bill"].Field("total_idr")
	if tw == nil {
		t.Fatal("bill has no total_idr")
	}
	if tw.Type != "currency" || !tw.ReadOnly || tw.MoneyOf() != "total" {
		t.Errorf("twin = %+v", tw)
	}
	if tw.Label["id"] != "Total (IDR)" || tw.Label["en"] != "Total (IDR)" {
		t.Errorf("twin label = %v", tw.Label)
	}
	total := reg.Models["bill"].Field("total")
	if total.CurrencyField() != "currency" || total.RateField() != "rate" || total.MoneyOf() != "" {
		t.Errorf("total: currency %q rate %q money_of %q", total.CurrencyField(), total.RateField(), total.MoneyOf())
	}
	if f := reg.Models["bill_line"].Field("amount_idr"); f == nil || f.MoneyOf() != "amount" || f.Label != nil {
		t.Errorf("bill_line amount_idr = %+v", f)
	}
	if strings.Contains(reg.Models["bill"].Doc.Text, "total_idr") {
		t.Error("the model file must not change")
	}
	if n := len(asList(asMap(reg.Models["bill"].Doc.Data)["fields"])); n != 4 {
		t.Errorf("doc.Data has %d fields, want the 4 written", n)
	}
}

func TestWithTwins(t *testing.T) {
	in := []any{
		map[string]any{"name": "total", "type": "money", "currency_field": "currency"},
		map[string]any{"name": "fee", "type": "money", "currency_field": "currency"},
		map[string]any{"name": "fee_idr", "type": "currency"},
		"junk",
	}
	out := WithTwins(in)
	if len(in) != 4 || len(out) != 5 {
		t.Fatalf("in %d out %d", len(in), len(out))
	}
	tw := asMap(out[4])
	if tw["name"] != "total_idr" || tw["type"] != "currency" || tw["read_only"] != true || tw["money_of"] != "total" {
		t.Errorf("twin = %v", tw)
	}
	if _, ok := tw["label"]; ok {
		t.Error("no label expected")
	}
	if TwinName("x") != "x_idr" {
		t.Error("TwinName")
	}
}

func TestMoneyValidator(t *testing.T) {
	line := "  - {name: amount, type: money, currency_field: currency}\n"
	cases := []struct {
		name, bill, line, code, where, msg string
		quote                              []string
	}{
		{"no currency_field", `  - {name: currency, type: data}
  - {name: rate, type: float}
  - {name: total, type: money}
  - {name: items, type: table, of: bill_line}
`, line, "schema", "fields/2", "'currency_field' is a required property", nil},
		{"unknown currency_field", `  - {name: currency, type: data}
  - {name: rate, type: float}
  - {name: total, type: money, currency_field: nope}
  - {name: items, type: table, of: bill_line}
`, line, "bad_currency_field", "fields/2/currency_field", `"nope" is not a field of bill`, nil},
		{"date currency_field", `  - {name: currency, type: data}
  - {name: rate, type: float}
  - {name: posting_date, type: date}
  - {name: total, type: money, currency_field: posting_date}
  - {name: items, type: table, of: bill_line}
`, line, "bad_currency_field", "fields/3/currency_field", `currency field "posting_date" must be select, data or link; it is date`, nil},
		{"no rate", `  - {name: currency, type: data}
  - {name: total, type: money, currency_field: currency}
`, "  - {name: qty, type: int}\n", "missing_rate_field", "fields/1", `money fields need a rate field "rate" (float, currency or int) on bill`, nil},
		{"child without parent rate", `  - {name: currency, type: data}
  - {name: items, type: table, of: bill_line}
`, line, "missing_rate_field", "fields/0", `money fields need a rate field "rate" (float, currency or int) on bill`, nil},
		{"child rate is not the document rate", `  - {name: currency, type: data}
  - {name: items, type: table, of: bill_line}
`, `  - {name: rate, type: currency}
  - {name: amount, type: money, currency_field: currency}
`, "missing_rate_field", "fields/1", `money fields need a rate field "rate" (float, currency or int) on bill`, nil},
		{"two parents, one without rate", `  - {name: currency, type: data}
  - {name: rate, type: float}
  - {name: items, type: table, of: bill_line}
`, line, "missing_rate_field", "fields/0", `money fields need a rate field "rate" (float, currency or int) on quote`,
			[]string{"  - {name: currency, type: data}\n  - {name: lines, type: table, of: bill_line}\n"}},
		{"twin clash", `  - {name: currency, type: data}
  - {name: rate, type: float}
  - {name: total, type: money, currency_field: currency}
  - {name: total_idr, type: currency}
`, "  - {name: qty, type: int}\n", "twin_clash", "fields/3", `"total_idr" is generated for money field "total"; rename it`, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, rep := Validate(moneyApp(t, c.bill, c.line, c.quote...), "", nil, schema.Embedded())
			all := withoutDeprecations(rep) // the fixture is a 0.4 app (contract 0001)
			if len(all) != 1 {
				t.Fatalf("want 1 problem, got %v", all)
			}
			p := all[0]
			if p.Code != c.code || p.Where != c.where || p.Message != c.msg {
				t.Errorf("got %s %s %q", p.Code, p.Where, p.Message)
			}
		})
	}
}
