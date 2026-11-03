package imports

import (
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
)

func TestConvert(t *testing.T) {
	cases := []struct {
		typ, col, cell string
		want           any
		err            string
	}{
		{"currency", "Gross Sales", "1.250.000", int64(1250000), ""},
		{"currency", "Gross Sales", "Rp 1.250.000", int64(1250000), ""},
		{"currency", "Gross Sales", "IDR1,250,000", int64(1250000), ""},
		{"currency", "Gross Sales", "1,250,000", int64(1250000), ""},
		{"currency", "Gross Sales", "1250000", int64(1250000), ""},
		{"currency", "Gross Sales", "50000.0", int64(50000), ""},
		{"currency", "Gross Sales", "(5.000)", int64(-5000), ""},
		{"currency", "Gross Sales", "-5.000", int64(-5000), ""},
		{"currency", "Gross Sales", "12,5", nil, `Kolom "Gross Sales" harus rupiah bulat, isinya "12,5"`},
		{"int", "Quantity", "3", int64(3), ""},
		{"int", "Quantity", "2,5", nil, `Kolom "Quantity" harus bilangan bulat, isinya "2,5"`},
		{"int", "Quantity", "dua", nil, `Kolom "Quantity" harus angka, isinya "dua"`},
		{"float", "Berat", "12,5", "12.5", ""},
		{"float", "Berat", "12.5", "12.5", ""},
		{"float", "Berat", "1.250,75", "1250.75", ""},
		{"percent", "Pajak", "10", "10", ""},
		{"float", "Berat", "-0,5", "-0.5", ""},
		{"date", "Tanggal", "02/11/2026", "2026-11-02", ""},
		{"date", "Tanggal", "2/11/2026", "2026-11-02", ""},
		{"date", "Tanggal", "2026-11-02", "2026-11-02", ""},
		{"date", "Tanggal", "46328", "2026-11-02", ""},
		{"date", "Tanggal", "11-02-26", nil, `Kolom "Tanggal" harus tanggal (dd/mm/yyyy), isinya "11-02-26"`},
		{"date", "Tanggal", "31/02/2026", nil, `Kolom "Tanggal" harus tanggal (dd/mm/yyyy), isinya "31/02/2026"`},
		{"datetime", "Waktu", "02/11/2026 08:14", "2026-11-02T08:14:00", ""},
		{"datetime", "Waktu", "2026-11-02 08:14:05", "2026-11-02T08:14:05", ""},
		{"datetime", "Waktu", "2026-11-02", "2026-11-02T00:00:00", ""},
		{"datetime", "Waktu", "kemarin", nil, `Kolom "Waktu" harus tanggal (dd/mm/yyyy), isinya "kemarin"`},
		{"date", "Tanggal", "46328.34375", "2026-11-02", ""},
		{"date", "Tanggal", "46328.0", "2026-11-02", ""},
		{"datetime", "Waktu", "46328.34375", "2026-11-02T08:15:00", ""},
		{"datetime", "Waktu", "46328", "2026-11-02T00:00:00", ""},
		{"datetime", "Waktu", "46328.99999999", "2026-11-03T00:00:00", ""},
		{"datetime", "Waktu", "2026-11-02T25:99", nil, `Kolom "Waktu" harus tanggal (dd/mm/yyyy), isinya "2026-11-02T25:99"`},
		{"float", "Berat", "0.500", "0.5", ""},
		{"currency", "Gross Sales", "0.500", nil, `Kolom "Gross Sales" harus rupiah bulat, isinya "0.500"`},
		{"currency", "Gross Sales", "0,5", nil, `Kolom "Gross Sales" harus rupiah bulat, isinya "0,5"`},
		{"currency", "Gross Sales", "1.25E+06", int64(1250000), ""},
		{"currency", "Gross Sales", "5E+4", int64(50000), ""},
		{"float", "Berat", "1.5E-3", "0.0015", ""},
		{"check", "Aktif", "Ya", true, ""},
		{"check", "Aktif", "TIDAK", false, ""},
		{"check", "Aktif", "1", true, ""},
		{"check", "Aktif", "mungkin", nil, `Kolom "Aktif" harus ya atau tidak, isinya "mungkin"`},
		{"data", "Item", "  Tahu  ", "Tahu", ""},
		{"select", "Tipe", "Dine In", "Dine In", ""},
		{"int", "Quantity", "", nil, ""},
		{"date", "Tanggal", "   ", nil, ""},
	}
	for _, c := range cases {
		got, msg := Convert(&meta.Field{Name: "x", Type: c.typ}, c.col, c.cell)
		if msg != c.err || got != c.want {
			t.Errorf("%s %q: got (%#v, %q), want (%#v, %q)", c.typ, c.cell, got, msg, c.want, c.err)
		}
	}
}

func TestParseNumber(t *testing.T) {
	for _, c := range []struct {
		in, i, f string
		ok       bool
	}{
		{"1.250.000,50", "1250000", "50", true},
		{"1,250.5", "1250", "5", true},
		{"-12,5", "-12", "5", true},
		{"abc", "", "", false},
		{"1.2.3", "", "", false},
	} {
		i, f, ok := parseNumber(c.in)
		if i != c.i || f != c.f || ok != c.ok {
			t.Errorf("%q: got (%q,%q,%v)", c.in, i, f, ok)
		}
	}
}

func TestExcelSerial(t *testing.T) {
	if got := excelDate(46328); got != "2026-11-02" {
		t.Fatal(got)
	}
}
