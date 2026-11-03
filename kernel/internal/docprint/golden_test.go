package docprint

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata/")

const (
	goldenRoot      = "../../testdata/print-apps"
	goldenApps      = goldenRoot + "/apps"
	goldenSite      = "../../testdata/print-site"
	referenceLayout = "../../../templates/print"
)

func goldenRegistry(t *testing.T) *meta.Registry {
	t.Helper()
	apps, _ := filepath.Abs(goldenApps)
	site, _ := filepath.Abs(goldenSite)
	return meta.Load(apps, site, nil)
}

// goldenInput builds the Input of a fixture format the way the print endpoint does: format, model and child
// models from the registry, the site letterhead and an explicit org.
func goldenInput(t *testing.T, reg *meta.Registry, format string, doc map[string]any, pkp bool) Input {
	t.Helper()
	f := reg.Print(format)
	if f == nil {
		t.Fatalf("no print format %q in the fixtures", format)
	}
	m := reg.Model(f.Model)
	if m == nil {
		t.Fatalf("no model %q in the fixtures", f.Model)
	}
	children := map[string]*meta.Model{}
	for _, fd := range m.Fields {
		if fd.Type == "table" {
			children[fd.Of] = reg.Model(fd.Of)
		}
	}
	jkt, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		t.Fatal(err)
	}
	return Input{
		Format:     f,
		Model:      m,
		Doc:        doc,
		Links:      map[string]map[string]any{},
		Related:    map[string][]map[string]any{},
		Letterhead: reg.Letterhead(""),
		Org:        map[string]any{"legal_name": "PT Contoh Jasa Teknik", "npwp": "00.000.000.0-000.000", "pkp": pkp},
		Printed:    time.Date(2026, 10, 30, 10, 0, 0, 0, jkt),
		Children:   children,
	}
}

var goldenCustomer = map[string]any{
	"name":          "CUST-0001",
	"customer_name": "PT Pelanggan Contoh",
	"address":       "Jl. Pelanggan No. 10\nSurabaya 60111",
	"npwp":          "00.000.000.0-000.000",
	"city":          "Surabaya",
}

// goldenLines are the two service lines of the quotation and the invoice. Numbers arrive as the
// record API gives them: floats for qty and percent, decimal strings for money.
func goldenLines() []any {
	return []any{
		map[string]any{"item": "JASA-NDT-UT", "description": "Inspeksi NDT ultrasonik", "qty": float64(3), "uom": "hari",
			"rate": "4500000", "discount": float64(0), "amount": "13500000"},
		map[string]any{"item": "JASA-MOB", "description": "Mobilisasi tim", "qty": float64(1), "uom": "lot",
			"rate": "2500000", "discount": float64(10), "amount": "2250000"},
	}
}

func quotationDoc() map[string]any {
	return map[string]any{
		"name": "QUO/CJT/26/0007", "revision": int64(1), "quotation_date": "2026-10-30", "valid_until": "2026-11-29",
		"customer": "CUST-0001", "contact_person": "Bapak Contoh", "subject": "Penawaran jasa inspeksi NDT",
		"items": goldenLines(), "subtotal": "15750000", "discount_amount": "0", "dpp": "14437500", "ppn": "1732500",
		"grand_total": "17482500", "payment_terms": "50% uang muka, 50% setelah laporan diserahkan",
		"delivery_terms": "3 hari kerja setelah PO diterima", "notes": "Harga belum termasuk akomodasi tim di luar Surabaya.",
	}
}

func invoiceDoc() map[string]any {
	return map[string]any{
		"name": "INV/CJT/26/0012", "posting_date": "2026-10-30", "due_date": "2026-11-29", "customer": "CUST-0001",
		"customer_po": "PO-PC-2026-0456", "quotation": "QUO/CJT/26/0007", "items": goldenLines(),
		"subtotal": "15750000", "discount_amount": "0", "down_payment": "0", "dpp": "14437500", "ppn": "1732500",
		"grand_total": "17482500", "currency": "IDR", "notes": "",
	}
}

func purchaseOrderDoc() map[string]any {
	return map[string]any{
		"name": "PO/CJT/26/0003", "order_date": "2026-10-30", "delivery_date": "2026-11-10", "vendor": "VND-0001",
		"ship_to": "PT Contoh Jasa Teknik\nGudang Cikarang\nJl. Contoh Industri No. 5, Bekasi 17530",
		"items": []any{
			map[string]any{"item": "BRG-0012", "description": "Couplant gel ultrasonik 1 liter", "qty": float64(10), "uom": "botol",
				"rate": "185000", "amount": "1850000"},
			map[string]any{"item": "BRG-0031", "description": "Kabel probe ultrasonik 2 m", "qty": float64(2), "uom": "pcs",
				"rate": "1250000", "amount": "2500000"},
		},
		"subtotal": "4350000", "ppn": "478500", "grand_total": "4828500",
		"payment_terms": "30 hari setelah barang diterima", "notes": "",
	}
}

// statementInvoices: one paid, one 10 days overdue, one 45 days overdue (partly paid) at 30 Okt 2026.
func statementInvoices() []map[string]any {
	return []map[string]any{
		{"name": "INV/CJT/26/0004", "posting_date": "2026-08-01", "due_date": "2026-08-31", "grand_total": "5550000", "paid_amount": "5550000"},
		{"name": "INV/CJT/26/0008", "posting_date": "2026-08-16", "due_date": "2026-09-15", "grand_total": "8880000", "paid_amount": "3000000"},
		{"name": "INV/CJT/26/0010", "posting_date": "2026-09-20", "due_date": "2026-10-20", "grand_total": "17482500", "paid_amount": "0"},
	}
}

func goldenCases(t *testing.T, reg *meta.Registry) map[string]Input {
	vendor := map[string]any{"name": "VND-0001", "vendor_name": "PT Pemasok Contoh",
		"address": "Jl. Pemasok No. 7\nBekasi 17530", "npwp": "00.000.000.0-000.000"}
	q := goldenInput(t, reg, "quotation", quotationDoc(), true)
	q.Links["customer"] = goldenCustomer
	inv := goldenInput(t, reg, "sales_invoice", invoiceDoc(), true)
	inv.Links["customer"] = goldenCustomer
	po := goldenInput(t, reg, "purchase_order", purchaseOrderDoc(), true)
	po.Links["vendor"] = vendor
	st := goldenInput(t, reg, "statement", goldenCustomer, true)
	st.Related["invoices"] = statementInvoices()
	return map[string]Input{"quotation": q, "sales_invoice": inv, "purchase_order": po, "statement": st}
}

func TestGolden(t *testing.T) {
	reg := goldenRegistry(t)
	for name, in := range goldenCases(t, reg) {
		t.Run(name, func(t *testing.T) {
			out, err := Render(in)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			path := filepath.Join("testdata", name+".golden.html")
			if *update {
				if err := os.MkdirAll("testdata", 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, out, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v (run with -update to create it)", err)
			}
			if !bytes.Equal(out, want) {
				t.Errorf("%s differs from %s (run go test -run Golden -update and review the diff)", name, path)
			}
		})
	}
}

func renderGolden(t *testing.T, in Input) string {
	t.Helper()
	out, err := Render(in)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return string(out)
}

func TestGoldenContent(t *testing.T) {
	reg := goldenRegistry(t)
	cases := goldenCases(t, reg)

	inv := renderGolden(t, cases["sales_invoice"])
	mustContain(t, inv, "Rp 17.482.500", "tujuh belas juta empat ratus delapan puluh dua ribu lima ratus rupiah",
		"Jatuh tempo", "29 Nov 2026", "No. PO pelanggan", "PO-PC-2026-0456", "PPN 12%", "DPP nilai lain (11/12)",
		"Bila pembayaran dipotong PPh Pasal 23, mohon kirimkan bukti potongnya kepada kami.",
		"Pembayaran ke rekening:", "Bank Contoh", "Faktur pajak (e-Faktur) diterbitkan terpisah.")
	if strings.Contains(inv, "Uang muka") {
		t.Error("invoice shows an Uang muka row for a zero down payment")
	}

	q := renderGolden(t, cases["quotation"])
	mustContain(t, q, "Rev.01", "Surat penawaran harga", "Kepada Yth.", "PT Pelanggan Contoh",
		"Dengan hormat, bersama ini kami sampaikan penawaran harga sebagai berikut:", "<em>Terbilang:",
		"Syarat dan ketentuan", "29 Nov 2026", "Hormat kami,", "Rp 4.500.000", "10%")

	po := renderGolden(t, cases["purchase_order"])
	mustContain(t, po, "Pesanan pembelian", "Kepada (pemasok)", "PT Pemasok Contoh", "Kirim ke", "Gudang Cikarang",
		"Tanggal kirim", "10 Nov 2026", "Syarat pembayaran", "Disetujui oleh", "Rp 4.828.500")

	st := renderGolden(t, cases["statement"])
	mustContain(t, st, "Laporan tagihan", "PT Pelanggan Contoh", "Belum jatuh tempo", "&gt; 90 hari",
		"Rp 5.880.000", "Rp 17.482.500", "Rp 23.362.500", ">45<", ">10<", "Lunas")
}

// A cut related list (more open invoices than the limit) says so on the statement; a complete one does not.
func TestGoldenStatementTruncated(t *testing.T) {
	reg := goldenRegistry(t)
	const note = "Daftar dipotong: hanya 3 tagihan terlama yang ditampilkan."
	in := goldenCases(t, reg)["statement"]
	if out := renderGolden(t, in); strings.Contains(out, "Daftar dipotong") {
		t.Errorf("complete list says it was cut:\n%s", out)
	}
	in.RelatedTruncated = map[string]bool{"invoices": true}
	mustContain(t, renderGolden(t, in), note)
}

func TestGoldenInvoiceWithoutPKP(t *testing.T) {
	reg := goldenRegistry(t)
	in := goldenCases(t, reg)["sales_invoice"]
	in.Org = map[string]any{"legal_name": "PT Contoh Jasa Teknik", "npwp": "00.000.000.0-000.000", "pkp": false}
	out := renderGolden(t, in)
	if strings.Contains(out, "PPN") {
		t.Errorf("invoice of a non-PKP org mentions PPN:\n%s", out)
	}
	mustContain(t, out, "Rp 17.482.500", "PPh Pasal 23")
}

func TestGoldenInvoiceDownPayment(t *testing.T) {
	reg := goldenRegistry(t)
	in := goldenCases(t, reg)["sales_invoice"]
	in.Doc = invoiceDoc()
	in.Doc["down_payment"] = "5000000"
	mustContain(t, renderGolden(t, in), "Uang muka", "-Rp 5.000.000")
}

// The terbilang line follows the same rule as money: a lower-case or empty currency is IDR, another code skips it.
func TestGoldenInvoiceTerbilangCurrency(t *testing.T) {
	reg := goldenRegistry(t)
	for _, cur := range []string{"idr", "Idr", ""} {
		in := goldenCases(t, reg)["sales_invoice"]
		in.Doc = invoiceDoc()
		in.Doc["currency"] = cur
		mustContain(t, renderGolden(t, in), "Terbilang:", "tujuh belas juta empat ratus delapan puluh dua ribu lima ratus rupiah", "Rp 17.482.500")
	}
	in := goldenCases(t, reg)["sales_invoice"]
	in.Doc = invoiceDoc()
	in.Doc["currency"] = "usd"
	if out := renderGolden(t, in); strings.Contains(out, "Terbilang:") || !strings.Contains(out, "USD ") {
		t.Errorf("usd invoice: terbilang present or currency not shown:\n%s", out)
	}
}

// The revision arrives as int64 from the database and as float64 from JSON; both print as Rev.NN.
func TestGoldenQuotationRevision(t *testing.T) {
	reg := goldenRegistry(t)
	for _, c := range []struct {
		rev  any
		want string
	}{{int64(1), "Rev.01"}, {float64(1), "Rev.01"}, {float64(0), "Rev.00"}, {int64(12), "Rev.12"}, {"3", "Rev.03"}} {
		in := goldenCases(t, reg)["quotation"]
		in.Doc = quotationDoc()
		in.Doc["revision"] = c.rev
		out := renderGolden(t, in)
		mustContain(t, out, c.want)
		if strings.Contains(out, "%!") {
			t.Errorf("revision %#v: bad format verb in output", c.rev)
		}
	}
}

// Missing links and empty records render without errors: every nested access is guarded.
func TestGoldenEmptyRecords(t *testing.T) {
	reg := goldenRegistry(t)
	for _, name := range []string{"quotation", "sales_invoice", "purchase_order", "statement"} {
		in := goldenInput(t, reg, name, map[string]any{"name": "X-1"}, true)
		in.Links = map[string]map[string]any{"customer": nil, "vendor": nil}
		renderGolden(t, in)
	}
}

// The fixtures' templates are byte-identical copies of the reference layouts in templates/print/.
func TestFixtureTemplatesMatchReference(t *testing.T) {
	var copies []string
	for _, root := range []string{goldenRoot, goldenSite} {
		err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() && filepath.Ext(p) == ".html" && filepath.Base(filepath.Dir(p)) == "print" {
				copies = append(copies, p)
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(copies) != 5 {
		t.Fatalf("found %d fixture templates, want 5: %v", len(copies), copies)
	}
	for _, p := range copies {
		got, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		want, err := os.ReadFile(filepath.Join(referenceLayout, filepath.Base(p)))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s differs from templates/print/%s", p, filepath.Base(p))
		}
	}
}

// The PPN rate and DPP factor come from the record (ppn_rate, else tax_rate; dpp_ratio), else from .Tax (the
// org's merged config tax), else the defaults 12 and 11/12. The layouts never hard-code either.
func TestGoldenTaxLabels(t *testing.T) {
	reg := goldenRegistry(t)
	render := func(name string, tax map[string]any, set map[string]any) string {
		in := goldenCases(t, reg)[name]
		in.Doc = map[string]any{"quotation": quotationDoc, "sales_invoice": invoiceDoc, "purchase_order": purchaseOrderDoc}[name].(func() map[string]any)()
		for k, v := range set {
			in.Doc[k] = v
		}
		in.Tax = tax
		return renderGolden(t, in)
	}
	for _, name := range []string{"quotation", "sales_invoice"} {
		t.Run(name, func(t *testing.T) {
			// no .Tax: the defaults
			mustContain(t, render(name, nil, nil), "<th>PPN 12%</th>", "<th>DPP nilai lain (11/12)</th>")
			// config 11 with DPP at full value
			out := render(name, map[string]any{"ppn_rate": 11, "dpp_ratio": "1/1", "pkp": true}, nil)
			mustContain(t, out, "<th>PPN 11%</th>", "<th>DPP</th>")
			if strings.Contains(out, "11/12") || strings.Contains(out, "DPP nilai lain") {
				t.Errorf("DPP 1/1 still printed as nilai lain:\n%s", out)
			}
			mustContain(t, render(name, map[string]any{"ppn_rate": float64(11), "dpp_ratio": 1}, nil), "<th>PPN 11%</th>", "<th>DPP</th>")
			// only some keys set: the rest take the defaults
			mustContain(t, render(name, map[string]any{"ppn_rate": 11}, nil), "<th>PPN 11%</th>", "<th>DPP nilai lain (11/12)</th>")
			// the record's own rate and factor win over config
			mustContain(t, render(name, map[string]any{"ppn_rate": 11, "dpp_ratio": "1/1"}, map[string]any{"ppn_rate": "12"}), "<th>PPN 12%</th>")
			mustContain(t, render(name, map[string]any{"ppn_rate": 11}, map[string]any{"tax_rate": "12"}), "<th>PPN 12%</th>")
			mustContain(t, render(name, map[string]any{"ppn_rate": 12}, map[string]any{"tax_rate": "11", "ppn_rate": "11.5"}), "<th>PPN 11,5%</th>")
			mustContain(t, render(name, map[string]any{"ppn_rate": 11}, map[string]any{"ppn_rate": "", "tax_rate": ""}), "<th>PPN 11%</th>")
			mustContain(t, render(name, map[string]any{"dpp_ratio": "1/1"}, map[string]any{"dpp_ratio": "11/12"}), "<th>DPP nilai lain (11/12)</th>")
			mustContain(t, render(name, nil, map[string]any{"dpp_ratio": "1"}), "<th>DPP</th>")
		})
	}
	t.Run("purchase_order", func(t *testing.T) {
		mustContain(t, render("purchase_order", nil, nil), "<th>PPN 12%</th>")
		mustContain(t, render("purchase_order", map[string]any{"ppn_rate": 11}, nil), "<th>PPN 11%</th>")
		mustContain(t, render("purchase_order", map[string]any{"ppn_rate": 11}, map[string]any{"ppn_rate": "12"}), "<th>PPN 12%</th>")
	})
}

// No reference layout hard-codes a tax rate or DPP factor.
func TestReferenceLayoutsHaveNoTaxLiterals(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(referenceLayout, "*.html"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no layouts in %s: %v", referenceLayout, err)
	}
	for _, p := range files {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, lit := range []string{"12%", "11/12"} {
			if strings.Contains(string(b), lit) {
				t.Errorf("%s hard-codes %q", filepath.Base(p), lit)
			}
		}
	}
}
