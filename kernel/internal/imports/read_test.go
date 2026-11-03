package imports

import (
	"bytes"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/xuri/excelize/v2"
)

func TestReadMokaSample(t *testing.T) {
	data, err := os.ReadFile("testdata/moka-item-details.csv")
	if err != nil {
		t.Fatal(err)
	}
	tb, err := Read(data, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(tb.Headers) != 20 || len(tb.Rows) != 8 {
		t.Fatalf("headers=%d rows=%d", len(tb.Headers), len(tb.Rows))
	}
	if tb.Rows[0].Num != 2 {
		t.Errorf("Rows[0].Num = %d", tb.Rows[0].Num)
	}
	if got := tb.Rows[2].Cells["Discounts"]; got != "4800" {
		t.Errorf("Discounts = %q", got)
	}
	if got := tb.Rows[3].Cells["Modifiers"]; got != "Extra pedas" {
		t.Errorf("Modifiers = %q", got)
	}
}

// mokaXLSX writes the rows of a Moka CSV as an XLSX file the way Excel keeps them: dates as serial day
// numbers and amounts as numbers.
func mokaXLSX(t *testing.T, csvData []byte) []byte {
	t.Helper()
	tb, err := Read(csvData, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	numeric := map[string]bool{"Quantity": true, "Gross Sales": true, "Discounts": true, "Refunds": true,
		"Net Sales": true, "Tax": true, "Gratuity": true}
	f := excelize.NewFile()
	for j, h := range tb.Headers {
		cell, _ := excelize.CoordinatesToCellName(j+1, 1)
		f.SetCellStr("Sheet1", cell, h)
	}
	for i, r := range tb.Rows {
		for j, h := range tb.Headers {
			cell, _ := excelize.CoordinatesToCellName(j+1, i+2)
			v := r.Cells[h]
			switch {
			case h == "Date":
				d, err := time.Parse("02/01/2006", v)
				if err != nil {
					t.Fatal(err)
				}
				f.SetCellInt("Sheet1", cell, int64(d.Sub(time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)).Hours()/24))
			case numeric[h]:
				n, err := strconv.ParseInt(v, 10, 64)
				if err != nil {
					t.Fatal(err)
				}
				f.SetCellInt("Sheet1", cell, n)
			case v != "":
				f.SetCellStr("Sheet1", cell, v)
			}
		}
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestReadSemicolonBOM(t *testing.T) {
	data := []byte("\xef\xbb\xbfNama;Harga\nTahu;5000\n")
	tb, err := Read(data, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(tb.Headers) != 2 || tb.Headers[0] != "Nama" || tb.Rows[0].Cells["Harga"] != "5000" {
		t.Fatalf("%+v", tb)
	}
}

func TestReadHeaderRowAndBlanks(t *testing.T) {
	data := []byte("Laporan\nPeriode\n Nama , Harga \n\n,\nTahu, 5000\nTempe\n")
	tb, err := Read(data, "", 3)
	if err != nil {
		t.Fatal(err)
	}
	if tb.HeaderRow != 3 || tb.Headers[0] != "Nama" || tb.Headers[1] != "Harga" {
		t.Fatalf("%+v", tb)
	}
	if len(tb.Rows) != 2 || tb.Rows[0].Num != 6 || tb.Rows[0].Cells["Harga"] != "5000" {
		t.Fatalf("%+v", tb.Rows)
	}
	if tb.Rows[1].Cells["Harga"] != "" {
		t.Errorf("missing cell should be empty: %+v", tb.Rows[1])
	}
}

func TestReadXLSX(t *testing.T) {
	f := excelize.NewFile()
	if _, err := f.NewSheet("Item Details"); err != nil {
		t.Fatal(err)
	}
	f.DeleteSheet("Sheet1")
	f.SetCellValue("Item Details", "A1", "Date")
	f.SetCellValue("Item Details", "B1", "Gross Sales")
	f.SetCellInt("Item Details", "A2", 46328)
	f.SetCellInt("Item Details", "B2", 50000)
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatal(err)
	}
	tb, err := Read(buf.Bytes(), "Item Details", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(tb.Rows) != 1 || tb.Rows[0].Cells["Gross Sales"] != "50000" || tb.Rows[0].Cells["Date"] != "46328" {
		t.Fatalf("%+v", tb)
	}
	// Default sheet = first one.
	tb2, err := Read(buf.Bytes(), "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(tb2.Rows) != 1 {
		t.Fatalf("%+v", tb2)
	}
}

func TestReadCSVDelimiterFromHeaderLine(t *testing.T) {
	data := []byte("Laporan, bulan ini, Moka\nNama;Harga\nTahu;5000\n")
	tb, err := Read(data, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(tb.Headers) != 2 || tb.Rows[0].Cells["Harga"] != "5000" {
		t.Fatalf("%+v", tb)
	}
}

func TestReadXLSXDateCells(t *testing.T) {
	f := excelize.NewFile()
	f.SetCellValue("Sheet1", "A1", "Date")
	f.SetCellValue("Sheet1", "A2", time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC))
	f.SetCellInt("Sheet1", "A3", 46328)
	style, err := f.NewStyle(&excelize.Style{NumFmt: 14})
	if err != nil {
		t.Fatal(err)
	}
	f.SetCellStyle("Sheet1", "A3", "A3", style)
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatal(err)
	}
	tb, err := Read(buf.Bytes(), "", 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range tb.Rows {
		v, msg := Convert(&meta.Field{Type: "date"}, "Date", r.Cells["Date"])
		if msg != "" || v != "2026-11-02" {
			t.Errorf("row %d: raw %q -> %v %q", r.Num, r.Cells["Date"], v, msg)
		}
	}
	if len(tb.Rows) != 2 {
		t.Fatalf("rows: %+v", tb.Rows)
	}
}

func TestReadLimits(t *testing.T) {
	if _, err := Read(make([]byte, MaxBytes+1), "", 1); !errors.Is(err, ErrTooBig) {
		t.Errorf("want ErrTooBig, got %v", err)
	}
	if _, err := Read([]byte("PK\x03\x04garbage"), "", 1); !errors.Is(err, ErrUnreadable) {
		t.Errorf("want ErrUnreadable, got %v", err)
	}
	var b bytes.Buffer
	b.WriteString("a\n")
	for i := 0; i < MaxRows+1; i++ {
		b.WriteString("x\n")
	}
	if _, err := Read(b.Bytes(), "", 1); !errors.Is(err, ErrTooManyRows) {
		t.Errorf("want ErrTooManyRows, got %v", err)
	}
}
