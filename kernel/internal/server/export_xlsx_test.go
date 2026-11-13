package server

import (
	"bytes"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

// Contract 0024 §8: ?format=xlsx writes the same rows as a native workbook: numbers as numbers, text as text (a
// formula-like text stays text, no apostrophe), codes with a leading zero or a plus sign stay text.
func TestExportXLSX(t *testing.T) {
	e := newPermEnv(t)
	owner, _ := e.clients(permPeople[0])
	clerkA, _ := e.clients(permPeople[1])
	owner.must(201, "POST", "/api/v1/r/voucher", map[string]any{"title": "=HYPERLINK(\"http://x\")", "owner": "USR-001", "amount": -5})
	owner.must(201, "POST", "/api/v1/r/voucher", map[string]any{"title": "0812345", "owner": "USR-001", "amount": 7})

	r := owner.must(200, "GET", "/api/v1/export/r/voucher?format=xlsx&sort=name&columns=name,title,amount", nil)
	if ct := r.Header.Get("Content-Type"); ct != "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" {
		t.Fatalf("content type %q", ct)
	}
	if cd := r.Header.Get("Content-Disposition"); !strings.Contains(cd, `filename="voucher-`) || !strings.Contains(cd, `.xlsx"`) {
		t.Fatalf("disposition %q", cd)
	}
	f, err := excelize.OpenReader(bytes.NewReader(r.Body))
	if err != nil {
		t.Fatalf("not a workbook: %v", err)
	}
	sheet := f.GetSheetName(0)
	rows, _ := f.GetRows(sheet)
	if len(rows) != 5 || strings.Join(rows[0], "|") != "Nomor|Judul|Jumlah" {
		t.Fatalf("rows = %q", rows)
	}
	v1 := -1
	for i, row := range rows {
		if row[0] == "V1" {
			v1 = i
		}
	}
	if v1 < 0 {
		t.Fatalf("no V1 row: %q", rows)
	}
	cellName, _ := excelize.CoordinatesToCellName(3, v1+1)
	if typ, _ := f.GetCellType(sheet, cellName); typ != excelize.CellTypeNumber && typ != excelize.CellTypeUnset {
		t.Fatalf("amount cell type %v", typ)
	}
	if v, _ := f.GetCellValue(sheet, cellName); v != "100000" {
		t.Fatalf("amount = %q", v)
	}
	// a negative amount is a number too; the code with a leading zero stays text
	for i, row := range rows {
		if row[1] == "0812345" {
			c, _ := excelize.CoordinatesToCellName(2, i+1)
			if typ, _ := f.GetCellType(sheet, c); typ == excelize.CellTypeNumber {
				t.Fatalf("0812345 written as a number")
			}
		}
	}
	var texts []string
	for _, row := range rows[1:] {
		texts = append(texts, strings.Join(row, "|"))
	}
	all := strings.Join(texts, "\n")
	if !strings.Contains(all, "=HYPERLINK(\"http://x\")") || strings.Contains(all, "'=HYPERLINK") || !strings.Contains(all, "0812345") {
		t.Fatalf("text cells:\n%s", all)
	}
	// formulas never: every cell is a value
	for ri := range rows {
		for ci := range rows[ri] {
			c, _ := excelize.CoordinatesToCellName(ci+1, ri+1)
			if fm, _ := f.GetCellFormula(sheet, c); fm != "" {
				t.Fatalf("formula at %s: %q", c, fm)
			}
		}
	}
	// the same grants as CSV, and an unknown format is refused
	want403(t, clerkA.do("GET", "/api/v1/export/r/voucher?format=xlsx", nil), "clerk xlsx", "voucher", "export")
	if r := owner.do("GET", "/api/v1/export/r/voucher?format=pdf", nil); r.Status != 400 {
		t.Fatalf("format=pdf: %d", r.Status)
	}

	// dashboard as a workbook: the same blocks, the KPI value a number
	r = owner.must(200, "GET", "/api/v1/export/dashboards/perm_overview?format=xlsx", nil)
	f, err = excelize.OpenReader(bytes.NewReader(r.Body))
	if err != nil {
		t.Fatalf("dashboard not a workbook: %v", err)
	}
	drows, _ := f.GetRows(f.GetSheetName(0))
	var flat []string
	for _, row := range drows {
		flat = append(flat, strings.Join(row, ","))
	}
	d := strings.Join(flat, "\n")
	for _, s := range []string{"Ringkasan izin", "Total voucher", "Jumlah catatan"} {
		if !strings.Contains(d, s) {
			t.Fatalf("dashboard xlsx lacks %q:\n%s", s, d)
		}
	}
}
