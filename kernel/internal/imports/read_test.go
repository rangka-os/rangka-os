package imports

import (
	"bytes"
	"errors"
	"os"
	"testing"

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
	if len(tb.Headers) != 19 || len(tb.Rows) != 8 {
		t.Fatalf("headers=%d rows=%d", len(tb.Headers), len(tb.Rows))
	}
	if tb.Rows[0].Num != 2 {
		t.Errorf("Rows[0].Num = %d", tb.Rows[0].Num)
	}
	if got := tb.Rows[2].Cells["Discounts"]; got != "4800" {
		t.Errorf("Discounts = %q", got)
	}
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
