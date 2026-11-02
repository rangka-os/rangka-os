package imports

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
)

type fakeLookup struct {
	skus map[string]string // sku -> item name
	dup  map[string]bool
}

func (f *fakeLookup) Find(model, field, value string) ([]string, error) {
	if model == "item" && field == "sku" {
		if f.dup[value] {
			return []string{"BRG-0001", "BRG-0002"}, nil
		}
		if n, ok := f.skus[value]; ok {
			return []string{n}, nil
		}
	}
	return nil, nil
}

func newFake() *fakeLookup {
	return &fakeLookup{skus: map[string]string{
		"TH-ORI-10": "BRG-0001", "TH-ORI-20": "BRG-0002", "TH-ISI-5": "BRG-0003",
		"MN-ETM-R": "BRG-0004", "MN-EJR-R": "BRG-0005",
	}}
}

func fixture(t *testing.T) (*meta.Registry, *meta.Import) {
	t.Helper()
	reg := meta.Load("testdata/apps", "", nil)
	imp := reg.Imports["moka_item_details"]
	if imp == nil {
		t.Fatalf("import moka_item_details not loaded: %v", reg.Imports)
	}
	return reg, imp
}

func mokaTable(t *testing.T) *Table {
	t.Helper()
	data, err := os.ReadFile("testdata/moka-item-details.csv")
	if err != nil {
		t.Fatal(err)
	}
	tb, err := Read(data, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	return tb
}

var now = time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)

func TestMapMoka(t *testing.T) {
	reg, imp := fixture(t)
	m := Map(reg, imp, mokaTable(t), map[string]any{"outlet": "OUT-01"}, newFake(), now)
	if len(m.Errors) != 0 {
		t.Fatalf("errors: %+v", m.Errors)
	}
	if len(m.Records) != 1 {
		t.Fatalf("records: %d", len(m.Records))
	}
	r := m.Records[0]
	if r.Input["date"] != "2026-11-02" || r.Input["outlet"] != "OUT-01" || r.Input["note"] != "Moka 02/11/2026" {
		t.Fatalf("parent: %+v", r.Input)
	}
	lines := r.Input["lines"].([]map[string]any)
	if len(lines) != 8 || len(r.Rows) != 8 || r.Rows[0] != 2 || r.Rows[7] != 9 {
		t.Fatalf("lines %d rows %v", len(lines), r.Rows)
	}
	l := lines[0]
	if l["item"] != "BRG-0001" || l["qty"] != "2" || l["gross"] != int64(50000) || l["discount"] != int64(0) || l["net"] != int64(50000) {
		t.Fatalf("line 1: %#v", l)
	}
	// Refund row: Net Sales 0 minus Refunds 25000.
	if lines[5]["net"] != int64(-25000) {
		t.Fatalf("line 6: %#v", lines[5])
	}
	if len(r.Keys) != 8 {
		t.Fatalf("keys: %d", len(r.Keys))
	}
	seen := map[string]bool{}
	for _, k := range r.Keys {
		if len(k) != 64 || seen[k] {
			t.Fatalf("keys not distinct hex sha256: %v", r.Keys)
		}
		seen[k] = true
	}
}

func TestMapUnknownLink(t *testing.T) {
	reg, imp := fixture(t)
	lk := newFake()
	delete(lk.skus, "MN-EJR-R")
	m := Map(reg, imp, mokaTable(t), map[string]any{"outlet": "OUT-01"}, lk, now)
	want := RowError{Row: 6, Column: "SKU", Message: `Barang dengan sku "MN-EJR-R" tidak ditemukan`}
	if len(m.Errors) != 1 || m.Errors[0] != want {
		t.Fatalf("errors: %+v", m.Errors)
	}
	if len(m.Records) != 1 {
		t.Fatalf("record with errors must still be returned")
	}
}

func TestMapAmbiguousLink(t *testing.T) {
	reg, imp := fixture(t)
	lk := newFake()
	lk.dup = map[string]bool{"MN-EJR-R": true}
	m := Map(reg, imp, mokaTable(t), nil, lk, now)
	want := RowError{Row: 6, Column: "SKU", Message: `Barang dengan sku "MN-EJR-R" lebih dari satu`}
	if len(m.Errors) != 1 || m.Errors[0] != want {
		t.Fatalf("errors: %+v", m.Errors)
	}
}

func TestMapMissingColumn(t *testing.T) {
	reg, imp := fixture(t)
	tb := mokaTable(t)
	var hs []string
	for _, h := range tb.Headers {
		if h != "Quantity" {
			hs = append(hs, h)
		}
	}
	tb.Headers = hs
	m := Map(reg, imp, tb, nil, newFake(), now)
	want := RowError{Row: 1, Column: "Quantity", Message: `Kolom "Quantity" tidak ada di file`}
	if len(m.Errors) != 1 || m.Errors[0] != want || len(m.Records) != 0 {
		t.Fatalf("got %+v / %d records", m.Errors, len(m.Records))
	}
}

func TestMapDuplicateHeader(t *testing.T) {
	reg, imp := fixture(t)
	tb := mokaTable(t)
	tb.Headers = append(tb.Headers, "SKU")
	m := Map(reg, imp, tb, nil, newFake(), now)
	want := RowError{Row: 1, Column: "SKU", Message: `Kolom "SKU" ada lebih dari satu di file`}
	if len(m.Errors) != 1 || m.Errors[0] != want || len(m.Records) != 0 {
		t.Fatalf("got %+v", m.Errors)
	}
}

func TestMapDuplicateRow(t *testing.T) {
	reg, imp := fixture(t)
	tb := mokaTable(t)
	dup := Row{Num: 10, Cells: map[string]string{}}
	for k, v := range tb.Rows[0].Cells {
		dup.Cells[k] = v
	}
	tb.Rows = append(tb.Rows, dup)
	m := Map(reg, imp, tb, nil, newFake(), now)
	want := RowError{Row: 10, Column: "Date", Message: "Baris ganda: sama dengan baris 2"}
	if len(m.Errors) != 1 || m.Errors[0] != want {
		t.Fatalf("errors: %+v", m.Errors)
	}
}

func TestMapBadCell(t *testing.T) {
	reg, imp := fixture(t)
	tb := mokaTable(t)
	tb.Rows[2].Cells["Gross Sales"] = "abc"
	m := Map(reg, imp, tb, nil, newFake(), now)
	if len(m.Errors) != 1 || m.Errors[0].Row != 4 || m.Errors[0].Column != "Gross Sales" || !strings.Contains(m.Errors[0].Message, "harus angka") {
		t.Fatalf("errors: %+v", m.Errors)
	}
}

func TestMapFormulaFailure(t *testing.T) {
	reg, imp := fixture(t)
	tb := mokaTable(t)
	tb.Rows[0].Cells["Refunds"] = "x"
	m := Map(reg, imp, tb, nil, newFake(), now)
	if len(m.Errors) != 1 || m.Errors[0].Row != 2 || !strings.HasPrefix(m.Errors[0].Message, "Rumus untuk net gagal: ") {
		t.Fatalf("errors: %+v", m.Errors)
	}
}

func TestMapNoGroupOneRecordPerRow(t *testing.T) {
	reg, imp := fixture(t)
	i2 := *imp
	i2.Group = nil
	m := Map(reg, &i2, mokaTable(t), nil, newFake(), now)
	if len(m.Records) != 8 || len(m.Errors) != 0 {
		t.Fatalf("records %d errors %+v", len(m.Records), m.Errors)
	}
}
