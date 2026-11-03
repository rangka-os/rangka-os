package server

import (
	"context"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
	"github.com/rangka-os/rangka-os/kernel/internal/docprint"
)

// repoTemplates is rangka-os/templates of this checkout (the reference print layouts).
const repoTemplates = "../../../templates"

const printFormatRK = `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/print.json
name: sales_invoice
model: sales_invoice
label: {id: Faktur, en: Invoice}
paper: A4
template: sales_invoice.html
default: true
`

const letterheadRK = `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/letterhead.json
legal_name: PT Contoh Jasa Teknik
address: |
  Jl. Contoh No. 1
  Jakarta 10110
email: halo@contoh.test
npwp: "00.000.000.0-000.000"
signatory: {name: Nama Penandatangan, title: Direktur}
`

const invoicePermissions = `permissions:
  - {role: owner, read: true, print: true}
  - {role: sales_manager, read: true, print: true}
`

// printEnv installs the reference invoice format in the sales app, a site letterhead and print permissions
// on sales_invoice, then signs in as the seeded owner.
func printEnv(t *testing.T) *env {
	t.Helper()
	e := newEnv(t, false, func(apps, site string) {
		dir := filepath.Join(apps, "sales", "print")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		tpl, err := os.ReadFile(filepath.Join(repoTemplates, "print", "sales_invoice.html"))
		if err != nil {
			t.Fatal(err)
		}
		write := func(p, s string) {
			if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		write(filepath.Join(dir, "sales_invoice.html"), string(tpl))
		write(filepath.Join(dir, "sales_invoice.rk"), printFormatRK)
		write(filepath.Join(site, "letterhead.rk"), letterheadRK)
		model := filepath.Join(apps, "sales", "models", "sales_invoice.rk")
		b, err := os.ReadFile(model)
		if err != nil {
			t.Fatal(err)
		}
		write(model, string(b)+invoicePermissions)
	})
	e.login()
	return e
}

// submittedInvoice creates and submits an invoice for customer and returns its name.
func (e *env) submittedInvoice(customer string) string {
	e.t.Helper()
	inv := e.must(201, "POST", "/api/v1/r/sales_invoice", map[string]any{"customer": customer, "outlet": "OUT-01",
		"items": []any{map[string]any{"item": "BRG-0044", "qty": 2}}}).JSON
	sub := e.must(200, "POST", "/api/v1/r/sales_invoice/"+inv["name"].(string)+"/submit", map[string]any{"force": true}).JSON
	return sub["name"].(string)
}

func TestPrintHTML(t *testing.T) {
	e := printEnv(t)
	name := e.submittedInvoice("CUST-0012")
	cust := e.must(200, "GET", "/api/v1/r/customer/CUST-0012", nil).JSON["customer_name"].(string)
	r := e.must(200, "GET", "/api/v1/r/sales_invoice/"+name+"/print", nil)
	body := string(r.Body)
	if ct := r.Header.Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("content type %q", ct)
	}
	if csp := r.Header.Get("Content-Security-Policy"); csp != "default-src 'none'; img-src data:; style-src 'unsafe-inline'" {
		t.Errorf("csp %q", csp)
	}
	if cc := r.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("cache control %q", cc)
	}
	for _, want := range []string{"<title>INV-", cust, "Rp ", "rupiah", "PT Contoh Jasa Teknik"} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q", want)
		}
	}
	// explicit format and as=html give the same page
	if r2 := e.must(200, "GET", "/api/v1/r/sales_invoice/"+name+"/print?format=sales_invoice&as=html", nil); string(r2.Body) != body {
		t.Error("explicit format/as=html differs from the default")
	}

	meta := e.must(200, "GET", "/api/v1/meta", nil).JSON
	prints, _ := get(meta, "models", "sales_invoice", "prints").([]any)
	if len(prints) != 1 || get(prints, 0, "name") != "sales_invoice" || get(prints, 0, "default") != true ||
		get(prints, 0, "paper") != "A4" || get(prints, 0, "label", "id") != "Faktur" {
		t.Errorf("meta prints: %v", prints)
	}
	if p, ok := get(meta, "models", "customer", "prints").([]any); !ok || len(p) != 0 {
		t.Errorf("customer prints = %v, want []", get(meta, "models", "customer", "prints"))
	}

	audit := e.must(200, "GET", "/api/v1/system/audit?kind=record.print", nil).JSON
	list, _ := audit["data"].([]any)
	if len(list) != 2 {
		t.Fatalf("audit: %v", audit)
	}
	res, err := e.s.DB.Query(context.Background(), "select target, actor, data->>'format', data->>'as' from rk_audit where kind = 'record.print' order by id")
	if err != nil {
		t.Fatal(err)
	}
	if row := res.Rows[0]; row[0] != "sales_invoice/"+name || row[1] != "ceo@saga.co.id" || row[2] != "sales_invoice" || row[3] != "html" {
		t.Errorf("audit row: %v", row)
	}
}

func TestPrintPDFUnavailable(t *testing.T) {
	t.Setenv("RANGKA_CHROMIUM", "off")
	e := printEnv(t)
	name := e.submittedInvoice("CUST-0012")
	r := e.do("GET", "/api/v1/r/sales_invoice/"+name+"/print?as=pdf", nil)
	if r.Status != 501 || code(r) != "pdf_unavailable" {
		t.Fatalf("pdf off: %d %s", r.Status, r.Body)
	}
	hint, _ := get(r.JSON, "error", "hint").(string)
	if !strings.HasSuffix(hint, "as=html") || !strings.HasPrefix(hint, "/api/v1/r/sales_invoice/"+name+"/print?format=sales_invoice") {
		t.Errorf("hint %q", hint)
	}
	if msg, _ := get(r.JSON, "error", "message").(string); !strings.HasPrefix(msg, "PDF belum tersedia di server ini.") {
		t.Errorf("message %q", msg)
	}
	if r := e.do("GET", "/api/v1/r/sales_invoice/"+name+"/print?as=docx", nil); r.Status != 400 {
		t.Errorf("as=docx: %d %s", r.Status, r.Body)
	}
}

func TestPrintPDF(t *testing.T) {
	if docprint.FindChromium() == "" {
		t.Skip("no Chromium on this machine")
	}
	e := printEnv(t)
	name := e.submittedInvoice("CUST-0012")
	r := e.must(200, "GET", "/api/v1/r/sales_invoice/"+name+"/print?as=pdf", nil)
	if !strings.HasPrefix(string(r.Body), "%PDF-") || r.Header.Get("Content-Type") != "application/pdf" ||
		r.Header.Get("Content-Disposition") != `inline; filename="`+name+`.pdf"` || r.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("pdf: %v %q", r.Header, r.Body[:min(len(r.Body), 64)])
	}
}

func TestPrintPermissionDenied(t *testing.T) {
	e := printEnv(t)
	name := e.submittedInvoice("CUST-0012")
	if _, err := auth.EnsureUser(context.Background(), e.s.DB, "rep@contoh.test", "Rep", "rahasia-123", "kopi-senja", "sales_rep", ""); err != nil {
		t.Fatal(err)
	}
	e.jar, _ = cookiejar.New(nil)
	e.must(200, "POST", "/auth/login", map[string]any{"email": "rep@contoh.test", "password": "rahasia-123"})
	r := e.do("GET", "/api/v1/r/sales_invoice/"+name+"/print", nil)
	if r.Status != 403 || code(r) != "permission_denied" || get(r.JSON, "error", "model") != "sales_invoice" ||
		get(r.JSON, "error", "action") != "print" || strings.Contains(string(r.Body), "<html") {
		t.Fatalf("rep print: %d %s", r.Status, r.Body)
	}
	res, err := e.s.DB.Query(context.Background(), "select count(*) from rk_audit where kind = 'record.print'")
	if err != nil {
		t.Fatal(err)
	}
	if n := res.Rows[0][0]; n != int64(0) {
		t.Errorf("denied print was audited: %v", n)
	}
}

func TestPrintUnknownFormat(t *testing.T) {
	e := printEnv(t)
	name := e.submittedInvoice("CUST-0012")
	if r := e.do("GET", "/api/v1/r/sales_invoice/"+name+"/print?format=nope", nil); r.Status != 404 || code(r) != "no_print_format" {
		t.Errorf("format=nope: %d %s", r.Status, r.Body)
	}
	if r := e.do("GET", "/api/v1/r/sales_invoice/INV-2099-99999/print", nil); r.Status != 404 || code(r) != "not_found" {
		t.Errorf("unknown record: %d %s", r.Status, r.Body)
	}
	if r := e.do("GET", "/api/v1/r/nope/X/print", nil); r.Status != 404 {
		t.Errorf("unknown model: %d %s", r.Status, r.Body)
	}
	// a model without formats
	if r := e.do("GET", "/api/v1/r/customer/CUST-0012/print", nil); r.Status != 404 || code(r) != "no_print_format" {
		t.Errorf("customer print: %d %s", r.Status, r.Body)
	}
	// a format of another model
	if r := e.do("GET", "/api/v1/r/customer/CUST-0012/print?format=sales_invoice", nil); r.Status != 404 || code(r) != "no_print_format" {
		t.Errorf("customer with invoice format: %d %s", r.Status, r.Body)
	}
}

func TestPrintEscapes(t *testing.T) {
	e := printEnv(t)
	c := e.must(201, "POST", "/api/v1/r/customer", map[string]any{"customer_name": "<script>alert(1)</script>"}).JSON
	name := e.submittedInvoice(c["name"].(string))
	body := string(e.must(200, "GET", "/api/v1/r/sales_invoice/"+name+"/print", nil).Body)
	if !strings.Contains(body, "&lt;script&gt;") || strings.Contains(body, "<script") {
		t.Fatalf("customer name not escaped:\n%s", body)
	}
}
