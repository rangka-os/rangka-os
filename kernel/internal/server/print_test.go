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
  - {role: sales_rep, read: true}
`

// printEnv installs the reference invoice format in the sales app, a site letterhead and print permissions
// on sales_invoice, runs extra on the copied apps and site, then signs in as the seeded owner.
func printEnv(t *testing.T, extra ...func(apps, site string)) *env {
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
		for _, x := range extra {
			x(apps, site)
		}
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
		r.Header.Get("Content-Disposition") != `inline; filename="`+name+`.pdf"; filename*=UTF-8''`+name+`.pdf` || r.Header.Get("Cache-Control") != "no-store" {
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
	// the rep can see the record (read) but not print it: S1's 403, not a 404
	e.must(200, "GET", "/api/v1/r/sales_invoice/"+name, nil)
	// the same 403 whatever format is asked for, so a denied caller cannot probe which formats exist
	for _, q := range []string{"", "?format=sales_invoice", "?format=nope", "?as=pdf"} {
		r := e.do("GET", "/api/v1/r/sales_invoice/"+name+"/print"+q, nil)
		if r.Status != 403 || code(r) != "permission_denied" || get(r.JSON, "error", "model") != "sales_invoice" ||
			get(r.JSON, "error", "action") != "print" || strings.Contains(string(r.Body), "<html") {
			t.Fatalf("rep print%s: %d %s", q, r.Status, r.Body)
		}
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

const statementRK = `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/print.json
name: statement
model: customer
label: {id: Laporan tagihan}
template: statement.html
related:
  invoices:
    model: sales_invoice
    match: {customer: name}
    sort: name
    limit: 3
`

// statementEnv adds a site format on customer listing the customer's invoices as "<li>name|customer</li>".
func statementEnv(t *testing.T) *env {
	return printEnv(t, func(apps, site string) {
		dir := filepath.Join(site, "print")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for p, s := range map[string]string{"statement.rk": statementRK,
			"statement.html": `<ul>{{range .Related.invoices}}<li>{{.name}}|{{.customer}}</li>{{end}}</ul>{{if .Truncated.invoices}}<p>TRUNCATED</p>{{end}}`} {
			if err := os.WriteFile(filepath.Join(dir, p), []byte(s), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	})
}

func TestPrintRelatedMatchBeforeLimit(t *testing.T) {
	e := statementEnv(t)
	// other customers' invoices come first in the related sort order (name) and outnumber the limit
	var others []string
	for i := 0; i < 4; i++ {
		others = append(others, e.submittedInvoice("CUST-0012"))
	}
	c := e.must(201, "POST", "/api/v1/r/customer", map[string]any{"customer_name": "PT Pelanggan Contoh"}).JSON["name"].(string)
	var mine []string
	for i := 0; i < 4; i++ {
		mine = append(mine, e.submittedInvoice(c))
	}
	body := string(e.must(200, "GET", "/api/v1/r/customer/"+c+"/print?format=statement", nil).Body)
	if got := strings.Count(body, "<li>"); got != 3 || !strings.Contains(body, "TRUNCATED") {
		t.Fatalf("want 3 rows (the related limit), marked truncated, got %d:\n%s", got, body)
	}
	for _, n := range mine[:3] {
		if !strings.Contains(body, "<li>"+n+"|"+c+"</li>") {
			t.Errorf("missing %s:\n%s", n, body)
		}
	}
	for _, n := range append(others, mine[3]) {
		if strings.Contains(body, n+"|") {
			t.Errorf("unexpected %s:\n%s", n, body)
		}
	}
	// a customer without invoices gets an empty list
	c2 := e.must(201, "POST", "/api/v1/r/customer", map[string]any{"customer_name": "PT Kosong Contoh"}).JSON["name"].(string)
	if body := string(e.must(200, "GET", "/api/v1/r/customer/"+c2+"/print?format=statement", nil).Body); strings.Contains(body, "<li>") {
		t.Errorf("empty statement has rows:\n%s", body)
	}
}

func TestPrintOtherOrg(t *testing.T) {
	e := statementEnv(t)
	ctx := context.Background()
	if _, err := e.s.DB.Exec(ctx, `insert into r_customer (org_id, name, customer_name) values ('other', 'CUST-9999', 'Rahasia Org Lain')`); err != nil {
		t.Fatal(err)
	}
	// another org's record is not found, as for GET /r/{model}/{name}
	if r := e.do("GET", "/api/v1/r/customer/CUST-9999/print?format=statement", nil); r.Status != 404 || code(r) != "not_found" {
		t.Errorf("other org's customer: %d %s", r.Status, r.Body)
	}
	// a related list holds the caller's org's rows only, even when another org's row matches
	if _, err := e.s.DB.Exec(ctx, `insert into r_sales_invoice (org_id, name, customer, docstatus) values ('other', 'INV-LAIN-0001', 'CUST-0012', 1)`); err != nil {
		t.Fatal(err)
	}
	if body := string(e.must(200, "GET", "/api/v1/r/customer/CUST-0012/print?format=statement", nil).Body); strings.Contains(body, "INV-LAIN-0001") {
		t.Errorf("another org's invoice in the related list:\n%s", body)
	}
	// a link pointing at another org's record is left out of .Links: the template prints the raw value
	name := e.submittedInvoice("CUST-0012")
	if _, err := e.s.DB.Exec(ctx, `update r_sales_invoice set customer = 'CUST-9999' where org_id = 'kopi-senja' and name = $1`, name); err != nil {
		t.Fatal(err)
	}
	body := string(e.must(200, "GET", "/api/v1/r/sales_invoice/"+name+"/print", nil).Body)
	if strings.Contains(body, "Rahasia Org Lain") || !strings.Contains(body, "CUST-9999") {
		t.Fatalf("other org's link rendered:\n%s", body)
	}
}

// orgProbeEnv adds a site format on customer printing .Org, and appends cfg to the site's config.rk
// (Kopi Senja's has tax: {pkp: true} and no org:).
func orgProbeEnv(t *testing.T, cfg string) *env {
	return printEnv(t, func(apps, site string) {
		dir := filepath.Join(site, "print")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		files := map[string]string{
			filepath.Join(dir, "org_probe.rk"): "# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/print.json\n" +
				"name: org_probe\nmodel: customer\ntemplate: org_probe.html\n",
			filepath.Join(dir, "org_probe.html"): `<p>{{.Org.name}}|{{.Org.code}}|{{.Org.legal_name}}|{{.Org.pkp}}</p>`,
		}
		b, err := os.ReadFile(filepath.Join(site, "config.rk"))
		if err != nil {
			t.Fatal(err)
		}
		files[filepath.Join(site, "config.rk")] = string(b) + cfg
		for p, s := range files {
			if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	})
}

// .Org.pkp is always the merged tax.pkp (contract 0010 §8): a PKP firm's invoice prints its DPP and PPN rows;
// an org: pkp key is ignored (an org's own tax: {pkp: false} is api TestPrintOrg and TestPrintAndCELAgreeOnTax).
func TestPrintOrgPKP(t *testing.T) {
	for _, tc := range []struct {
		name, cfg, probe string
		ppn              bool
	}{
		{"tax.pkp", "", "<p>|||true</p>", true},
		{"org.pkp ignored", "org: {legal_name: PT Contoh Jasa Teknik, pkp: false}\n", "<p>||PT Contoh Jasa Teknik|true</p>", true},
		{"org without pkp", "org: {legal_name: PT Contoh Jasa Teknik}\n", "<p>||PT Contoh Jasa Teknik|true</p>", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := orgProbeEnv(t, tc.cfg)
			if body := string(e.must(200, "GET", "/api/v1/r/customer/CUST-0012/print?format=org_probe", nil).Body); !strings.Contains(body, tc.probe) {
				t.Errorf("want %s in:\n%s", tc.probe, body)
			}
			name := e.submittedInvoice("CUST-0012")
			body := string(e.must(200, "GET", "/api/v1/r/sales_invoice/"+name+"/print", nil).Body)
			if got := strings.Contains(body, "DPP nilai lain") && strings.Contains(body, "PPN 12%"); got != tc.ppn {
				t.Errorf("DPP/PPN rows printed = %v, want %v:\n%s", got, tc.ppn, body)
			}
		})
	}
}

// The PPN rate comes from the record's tax_rate, else the org's config tax.ppn_rate; dpp_ratio left out of the
// config gives the default factor. Rates reach the layout as the engine reads them (float64), so a stored 0 is
// a 0% rate, not a missing one.
func TestPrintTaxFromConfig(t *testing.T) {
	e := printEnv(t, func(apps, site string) {
		p := filepath.Join(site, "config.rk")
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		const site12 = "tax: {ppn_rate: 12, dpp_ratio: 11/12, pkp: true}\n"
		if !strings.Contains(string(b), site12) {
			t.Fatalf("site config lacks %q", site12)
		}
		cfg := strings.Replace(string(b), site12, "tax: {ppn_rate: 11, pkp: true}\n", 1)
		// a site format on customer, a model with no rate or factor field: only the config can answer
		dir := filepath.Join(site, "print")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for p, s := range map[string]string{
			p: cfg,
			filepath.Join(dir, "tax_probe.rk"): "# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/print.json\n" +
				"name: tax_probe\nmodel: customer\ntemplate: tax_probe.html\n",
			filepath.Join(dir, "tax_probe.html"): `<p>PPN {{number (taxrate .Doc .Tax)}}%|{{dppfactor .Doc .Tax}}</p>`,
		} {
			if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	})
	if body := string(e.must(200, "GET", "/api/v1/r/customer/CUST-0012/print?format=tax_probe", nil).Body); !strings.Contains(body, "<p>PPN 11%|11/12</p>") {
		t.Errorf("config fallback: want <p>PPN 11%%|11/12</p> in:\n%s", body)
	}
	// the catalogue invoice stores tax_rate 12 by default: that record prints its own rate under config 11
	own := e.submittedInvoice("CUST-0012")
	if body := string(e.must(200, "GET", "/api/v1/r/sales_invoice/"+own+"/print", nil).Body); !strings.Contains(body, "<th>PPN 12%</th>") {
		t.Errorf("record tax_rate 12 under config 11:\n%s", body)
	}
	// a zero-rated invoice prints PPN 0%, never the config rate
	zero := e.submittedInvoice("CUST-0012")
	if _, err := e.s.DB.Exec(context.Background(), `update r_sales_invoice set tax_rate = 0 where name = $1`, zero); err != nil {
		t.Fatal(err)
	}
	body := string(e.must(200, "GET", "/api/v1/r/sales_invoice/"+zero+"/print", nil).Body)
	for _, want := range []string{"<th>PPN 0%</th>", "<th>DPP nilai lain (11/12)</th>"} {
		if !strings.Contains(body, want) {
			t.Errorf("record tax_rate 0 lacks %q:\n%s", want, body)
		}
	}
}

// Exactly the limit is not truncated.
func TestPrintRelatedAtLimitNotTruncated(t *testing.T) {
	e := statementEnv(t)
	c := e.must(201, "POST", "/api/v1/r/customer", map[string]any{"customer_name": "PT Pelanggan Contoh"}).JSON["name"].(string)
	for i := 0; i < 3; i++ {
		e.submittedInvoice(c)
	}
	body := string(e.must(200, "GET", "/api/v1/r/customer/"+c+"/print?format=statement", nil).Body)
	if strings.Count(body, "<li>") != 3 || strings.Contains(body, "TRUNCATED") {
		t.Fatalf("3 rows at limit 3 must not be marked truncated:\n%s", body)
	}
}

// The reference statement (kernel/testdata/print-apps) lists open invoices only, so the limit never hides
// what the customer still owes behind paid history.
func TestPrintReferenceStatementOpenOnly(t *testing.T) {
	e := printEnv(t, func(apps, site string) {
		dir := filepath.Join(site, "print")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, f := range []string{"statement.rk", "statement.html"} {
			b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "print-apps", "modules", "core", "print", f))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, f), b, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	})
	c := e.must(201, "POST", "/api/v1/r/customer", map[string]any{"customer_name": "PT Pelanggan Contoh"}).JSON["name"].(string)
	paid, open := e.submittedInvoice(c), e.submittedInvoice(c)
	if _, err := e.s.DB.Exec(context.Background(), `update r_sales_invoice set paid_amount = grand_total where name = $1`, paid); err != nil {
		t.Fatal(err)
	}
	body := string(e.must(200, "GET", "/api/v1/r/customer/"+c+"/print?format=statement", nil).Body)
	if !strings.Contains(body, open) || strings.Contains(body, paid) || strings.Contains(body, "Daftar dipotong") {
		t.Fatalf("want open %s only, not paid %s, not truncated:\n%s", open, paid, body)
	}
}

// HEAD answers with the headers of the page but writes no audit entry and starts no browser.
func TestPrintHead(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	fake := filepath.Join(t.TempDir(), "chromium")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\ntouch "+marker+"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RANGKA_CHROMIUM", fake)
	e := printEnv(t)
	name := e.submittedInvoice("CUST-0012")
	for as, ct := range map[string]string{"html": "text/html; charset=utf-8", "pdf": "application/pdf"} {
		r := e.do("HEAD", "/api/v1/r/sales_invoice/"+name+"/print?as="+as, nil)
		if r.Status != 200 || r.Header.Get("Content-Type") != ct || len(r.Body) != 0 {
			t.Errorf("HEAD as=%s: %d %v", as, r.Status, r.Header)
		}
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("HEAD as=pdf started the browser")
	}
	res, err := e.s.DB.Query(context.Background(), "select count(*) from rk_audit where kind = 'record.print'")
	if err != nil {
		t.Fatal(err)
	}
	if n := res.Rows[0][0]; n != int64(0) {
		t.Errorf("HEAD was audited: %v", n)
	}
}
