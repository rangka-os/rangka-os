package imports

import (
	"strings"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
)

func TestBuildTemplateMoka(t *testing.T) {
	reg, imp := fixture(t)
	tp := BuildTemplate(reg, imp, time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC))
	var heads []string
	for _, c := range tp.Columns {
		heads = append(heads, c.Header)
	}
	// map columns first (the note is a formula: no column), then lines.map, then key and group columns not yet named
	want := "Date|SKU|Quantity|Gross Sales|Discounts|Net Sales|Refunds|Receipt Number|Item|Variant|Modifiers"
	if got := strings.Join(heads, "|"); got != want {
		t.Fatalf("headers:\n got %s\nwant %s", got, want)
	}
	col := func(h string) TemplateColumn {
		for _, c := range tp.Columns {
			if c.Header == h {
				return c
			}
		}
		t.Fatalf("no column %s", h)
		return TemplateColumn{}
	}
	if c := col("Date"); c.Example != "14/11/2026" || !strings.Contains(c.Note, "dd/mm/yyyy") || !strings.Contains(c.Note, "kunci") || !strings.Contains(c.Note, "jadi satu") {
		t.Fatalf("Date: %+v", c)
	}
	if c := col("Gross Sales"); c.Example != "15000" || !strings.Contains(c.Note, "rupiah bulat") {
		t.Fatalf("Gross Sales: %+v", c)
	}
	if c := col("SKU"); !strings.Contains(c.Note, "yang sudah ada") || !strings.Contains(c.Note, "sku") {
		t.Fatalf("SKU: %+v", c)
	}
	if c := col("Quantity"); c.Example != "1,5" {
		t.Fatalf("Quantity: %+v", c)
	}
	if tp.Sheet != "Data" || tp.HeaderRow != 1 || tp.Label != "Penjualan Moka (detail item)" {
		t.Fatalf("template: %+v", tp)
	}
	joined := strings.Join(tp.Notes, " ")
	for _, s := range []string{"Baris contoh", "outlet", "dilewati"} {
		if !strings.Contains(joined, s) {
			t.Fatalf("notes miss %q: %q", s, tp.Notes)
		}
	}
}

// The template reads back through the importer: every column the import needs is there once, and every example
// cell of a typed field converts without an error.
func TestTemplateReadsBack(t *testing.T) {
	reg, imp := fixture(t)
	tp := BuildTemplate(reg, imp, now)
	var head, ex []string
	for _, c := range tp.Columns {
		head = append(head, c.Header)
		ex = append(ex, c.Example)
	}
	tb, err := Read([]byte(strings.Join(head, ",")+"\n"+strings.Join(quoteAll(ex), ",")+"\n"), "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if errs := checkColumns(imp, tb); len(errs) != 0 {
		t.Fatalf("columns: %+v", errs)
	}
	model, lineModel := models(reg, imp)
	check := func(m *meta.Model, mp map[string]meta.MapValue) {
		for name, mv := range mp {
			f := m.Field(name)
			if mv.Column == "" || f == nil || f.Type == "link" {
				continue
			}
			if _, msg := Convert(f, mv.Column, tb.Rows[0].Cells[mv.Column]); msg != "" {
				t.Errorf("example of %s: %s", mv.Column, msg)
			}
		}
	}
	check(model, imp.Map)
	check(lineModel, imp.Lines.Map)
}

func TestTemplateHeaderRowAndSheet(t *testing.T) {
	reg, imp := fixture(t)
	cp := *imp
	cp.HeaderRow, cp.Sheet = 3, "Penjualan"
	tp := BuildTemplate(reg, &cp, now)
	if tp.HeaderRow != 3 || tp.Sheet != "Penjualan" {
		t.Fatalf("template: %+v", tp)
	}
	if !strings.Contains(strings.Join(tp.Notes, " "), "baris 3") {
		t.Fatalf("notes: %q", tp.Notes)
	}
}

func quoteAll(v []string) []string {
	out := make([]string, len(v))
	for i, s := range v {
		out[i] = `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	}
	return out
}
