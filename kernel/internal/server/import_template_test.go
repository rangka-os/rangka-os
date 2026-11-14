package server

import (
	"bytes"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/rangka-os/rangka-os/kernel/internal/imports"
)

// Contract 0012 §7: GET /imports/{import}/template downloads the columns, one example row and (XLSX) a notes
// sheet; the downloaded file previews with no missing column.
func TestImportTemplate(t *testing.T) {
	e := newEnv(t, false, addFnbTest)
	e.login()

	r := e.must(200, "GET", "/api/v1/imports/moka_item_details/template", nil)
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Fatalf("default format: %q", ct)
	}
	if cd := r.Header.Get("Content-Disposition"); cd != `attachment; filename="template-moka_item_details.csv"` {
		t.Fatalf("disposition %q", cd)
	}
	tb, err := imports.Read(r.Body, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(strings.Join(tb.Headers, ","), "Date,SKU,Quantity") || len(tb.Rows) != 1 {
		t.Fatalf("csv: %q %d rows", tb.Headers, len(tb.Rows))
	}

	x := e.must(200, "GET", "/api/v1/imports/moka_item_details/template?format=xlsx", nil)
	f, err := excelize.OpenReader(bytes.NewReader(x.Body))
	if err != nil {
		t.Fatalf("not a workbook: %v", err)
	}
	if got := strings.Join(f.GetSheetList(), ","); got != "Data,Petunjuk" {
		t.Fatalf("sheets %q", got)
	}
	rows, _ := f.GetRows("Data")
	if len(rows) != 2 || rows[0][0] != "Date" || rows[1][2] != "1,5" {
		t.Fatalf("data rows %q", rows)
	}
	if typ, _ := f.GetCellType("Data", "D2"); typ == excelize.CellTypeFormula || typ == excelize.CellTypeNumber {
		t.Fatalf("example cell type %v: every cell is text", typ)
	}
	notes, _ := f.GetRows("Petunjuk")
	var all []string
	for _, n := range notes {
		all = append(all, strings.Join(n, " "))
	}
	if txt := strings.Join(all, "\n"); !strings.Contains(txt, "Kolom Isi Wajib Contoh") || !strings.Contains(txt, "Baris contoh") {
		t.Fatalf("notes sheet:\n%s", txt)
	}

	// the template itself previews with no column missing (only the example values may fail)
	outlet := e.must(201, "POST", "/api/v1/r/outlet", map[string]any{"outlet_name": "Outlet Dago"}).JSON["name"].(string)
	pv := e.uploadForm("/api/v1/imports/moka_item_details", "template.xlsx", x.Body, map[string]string{"outlet": outlet})
	if pv.Status != 200 {
		t.Fatalf("preview: %d %s", pv.Status, pv.Body)
	}
	for _, it := range pv.JSON["errors"].([]any) {
		if msg := it.(map[string]any)["message"].(string); strings.Contains(msg, "tidak ada di file") {
			t.Fatalf("template misses a column: %s", msg)
		}
	}

	if r := e.must(400, "GET", "/api/v1/imports/moka_item_details/template?format=pdf", nil); get(r.JSON, "error", "code") != "bad_request" {
		t.Fatalf("bad format: %s", r.Body)
	}
	e.must(404, "GET", "/api/v1/imports/nope/template", nil)
}
