package server

import (
	"context"
	"encoding/xml"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
)

// Contract 0029: print formats with output: xml.

const ledgerXMLRK = `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/print.json
name: ledger_xml
model: customer
label: {id: XML tagihan}
output: xml
template: ledger_xml.xml
related:
  invoices:
    model: sales_invoice
    match: {customer: name}
    filter: docstatus == 1
    sort: name
    children: true
    limit: 5
`

const ledgerXML = `<?xml version="1.0" encoding="utf-8"?>
<Ledger>
	<Name>{{.Doc.customer_name}}</Name>
	<Printed>{{isodate .Printed}}</Printed>
	<Calc>{{dec (round (div (mul 10 11) 12) 2) 2}}|{{dec 2.50 2}}|{{dec 15000 2}}|{{digits "01.234.567-8"}}|{{dec (add 1 2) 0}}|{{dec (div 1 0) 0}}|{{dec (round -2.5) 0}}</Calc>
	<ListOfInvoice>{{range .Related.invoices}}
		<Invoice><No>{{.name}}</No>{{range .items}}<Line><Item>{{.item}}</Item><Qty>{{dec .qty 2}}</Qty></Line>{{end}}</Invoice>{{end}}
	</ListOfInvoice>
</Ledger>
`

func xmlEnv(t *testing.T, rk, tpl string) *env {
	return printEnv(t, func(apps, site string) {
		dir := filepath.Join(site, "print")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for p, s := range map[string]string{"ledger_xml.rk": rk, "ledger_xml.xml": tpl, "statement.rk": statementRK,
			"statement.html": `<ul>{{range .Related.invoices}}<li>{{.name}}</li>{{end}}</ul>`} {
			if err := os.WriteFile(filepath.Join(dir, p), []byte(s), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	})
}

func TestPrintXML(t *testing.T) {
	e := xmlEnv(t, ledgerXMLRK, ledgerXML)
	c := e.must(201, "POST", "/api/v1/r/customer", map[string]any{"customer_name": `PT A&B <Sejahtera> "Jaya"`}).JSON["name"].(string)
	inv := e.submittedInvoice(c)
	r := e.must(200, "GET", "/api/v1/r/customer/"+c+"/print?format=ledger_xml", nil)
	body := string(r.Body)
	if ct := r.Header.Get("Content-Type"); ct != "application/xml; charset=utf-8" {
		t.Errorf("content type %q", ct)
	}
	if cd := r.Header.Get("Content-Disposition"); cd != `attachment; filename="ledger_xml-`+c+`.xml"` {
		t.Errorf("disposition %q", cd)
	}
	if cc := r.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("cache control %q", cc)
	}
	var doc struct {
		Name     string `xml:"Name"`
		Calc     string `xml:"Calc"`
		Invoices []struct {
			No    string `xml:"No"`
			Lines []struct {
				Item string `xml:"Item"`
				Qty  string `xml:"Qty"`
			} `xml:"Line"`
		} `xml:"ListOfInvoice>Invoice"`
	}
	if err := xml.Unmarshal(r.Body, &doc); err != nil {
		t.Fatalf("not XML: %v\n%s", err, body)
	}
	if doc.Name != `PT A&B <Sejahtera> "Jaya"` || strings.Contains(body, "<Sejahtera>") {
		t.Errorf("name not escaped: %q\n%s", doc.Name, body)
	}
	if doc.Calc != "9.17|2.5|15000|012345678|3|0|-3" {
		t.Errorf("helpers: %q", doc.Calc)
	}
	if len(doc.Invoices) != 1 || doc.Invoices[0].No != inv || len(doc.Invoices[0].Lines) != 1 ||
		doc.Invoices[0].Lines[0].Item != "BRG-0044" || doc.Invoices[0].Lines[0].Qty != "2" {
		t.Errorf("related with children: %+v\n%s", doc.Invoices, body)
	}
	if strings.Contains(body, "<!doctype") || strings.Contains(body, "<html") {
		t.Errorf("XML wrapped in the HTML shell:\n%s", body)
	}

	// meta lists both outputs; as=html/pdf on an XML format and as=xml on an HTML one are refused
	meta := e.must(200, "GET", "/api/v1/meta", nil).JSON
	for _, p := range get(meta, "models", "customer", "prints").([]any) {
		want := map[string]string{"ledger_xml": "xml", "statement": "html"}[get(p, "name").(string)]
		if get(p, "output") != want {
			t.Errorf("meta print %v: output %v, want %s", get(p, "name"), get(p, "output"), want)
		}
	}
	for _, q := range []string{"?format=ledger_xml&as=html", "?format=ledger_xml&as=pdf", "?format=statement&as=xml"} {
		if r := e.do("GET", "/api/v1/r/customer/"+c+"/print"+q, nil); r.Status != 400 || code(r) != "bad_request" {
			t.Errorf("%s: %d %s", q, r.Status, r.Body)
		}
	}
	if r := e.must(200, "GET", "/api/v1/r/customer/"+c+"/print?format=ledger_xml&as=xml", nil); string(r.Body) != body {
		t.Error("as=xml differs from the default")
	}

	res, err := e.s.DB.Query(context.Background(), "select data->>'format', data->>'as' from rk_audit where kind = 'record.print' order by id")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 2 || res.Rows[0][0] != "ledger_xml" || res.Rows[0][1] != "xml" {
		t.Errorf("audit: %v", res.Rows)
	}
	// HEAD renders and audits nothing
	if r := e.do("HEAD", "/api/v1/r/customer/"+c+"/print?format=ledger_xml", nil); r.Status != 200 || r.Header.Get("Content-Type") != "application/xml; charset=utf-8" {
		t.Errorf("HEAD: %d %v", r.Status, r.Header)
	}
	if res, _ := e.s.DB.Query(context.Background(), "select count(*) from rk_audit where kind = 'record.print'"); res.Rows[0][0] != int64(2) {
		t.Errorf("HEAD audited: %v", res.Rows)
	}
}

// A template cannot write markup from data, also through a variable or a helper result.
func TestPrintXMLEscapesEveryAction(t *testing.T) {
	tpl := `<R>{{$n := .Doc.customer_name}}<A>{{$n}}</A><B>{{upper .Doc.customer_name}}</B><C>{{printf "%s" .Doc.customer_name}}</C>{{with .Doc.customer_name}}<D>{{.}}</D>{{end}}</R>`
	e := xmlEnv(t, ledgerXMLRK, tpl)
	c := e.must(201, "POST", "/api/v1/r/customer", map[string]any{"customer_name": "</A><X>1</X><A>"}).JSON["name"].(string)
	r := e.must(200, "GET", "/api/v1/r/customer/"+c+"/print?format=ledger_xml", nil)
	if strings.Contains(string(r.Body), "<X>") {
		t.Fatalf("markup from data:\n%s", r.Body)
	}
	var doc struct {
		A, B, C, D string
	}
	if err := xml.Unmarshal(r.Body, &doc); err != nil || doc.A != "</A><X>1</X><A>" || doc.D != doc.A || doc.C != doc.A {
		t.Errorf("%v %+v\n%s", err, doc, r.Body)
	}
}

// A template that writes broken XML is refused, nothing is sent.
func TestPrintXMLMustBeWellFormed(t *testing.T) {
	for _, tpl := range []string{"<A><B></A>", "<A/><B/>", `<!DOCTYPE A [<!ENTITY x "y">]><A>&x;</A>`, "plain text", ""} {
		e := xmlEnv(t, ledgerXMLRK, tpl)
		c := e.must(201, "POST", "/api/v1/r/customer", map[string]any{"customer_name": "PT Rusak Contoh"}).JSON["name"].(string)
		r := e.do("GET", "/api/v1/r/customer/"+c+"/print?format=ledger_xml", nil)
		if r.Status != 500 || code(r) != "xml_invalid" {
			t.Errorf("%q: %d %s", tpl, r.Status, r.Body)
		}
		if res, _ := e.s.DB.Query(context.Background(), "select count(*) from rk_audit where kind = 'record.print'"); res.Rows[0][0] != int64(0) {
			t.Errorf("%q: broken file audited", tpl)
		}
	}
}

// require: every related row must pass; failures are listed, nothing rendered, nothing audited (also on HEAD).
func TestPrintRequire(t *testing.T) {
	rk := ledgerXMLRK + `    require:
      - check: "items.map(i, i.qty > 1 ? 0 : 1).sum() == 0"
        message: {id: 'Jumlah barang harus lebih dari 1'}
      - check: nope_field == 1
        message: {id: 'Cek yang error dianggap gagal'}
`
	e := xmlEnv(t, rk, ledgerXML)
	c := e.must(201, "POST", "/api/v1/r/customer", map[string]any{"customer_name": "PT Cek Contoh"}).JSON["name"].(string)
	inv := e.submittedInvoice(c)
	small := e.must(201, "POST", "/api/v1/r/sales_invoice", map[string]any{"customer": c, "outlet": "OUT-01",
		"items": []any{map[string]any{"item": "BRG-0044", "qty": 1}}}).JSON["name"].(string)
	small = e.must(200, "POST", "/api/v1/r/sales_invoice/"+small+"/submit", map[string]any{"force": true}).JSON["name"].(string)
	for _, method := range []string{"GET", "HEAD"} {
		r := e.do(method, "/api/v1/r/customer/"+c+"/print?format=ledger_xml", nil)
		if r.Status != 422 {
			t.Fatalf("%s: %d %s", method, r.Status, r.Body)
		}
		if method == "HEAD" {
			continue
		}
		if code(r) != "export_invalid" {
			t.Fatalf("code: %s", r.Body)
		}
		probs, _ := get(r.JSON, "error", "problems").([]any)
		want := []string{inv + "|Cek yang error dianggap gagal", small + "|Jumlah barang harus lebih dari 1", small + "|Cek yang error dianggap gagal"}
		var got []string
		for _, p := range probs {
			if get(p, "list") != "invoices" {
				t.Errorf("list: %v", p)
			}
			got = append(got, get(p, "name").(string)+"|"+get(p, "message").(string))
		}
		if strings.Join(got, ";") != strings.Join(want, ";") {
			t.Errorf("problems:\n got %v\nwant %v", got, want)
		}
	}
	if res, _ := e.s.DB.Query(context.Background(), "select count(*) from rk_audit where kind = 'record.print'"); res.Rows[0][0] != int64(0) {
		t.Error("refused export audited")
	}
}

// An XML file must hold every row: a related list cut at its limit is refused.
func TestPrintXMLTooLarge(t *testing.T) {
	e := xmlEnv(t, strings.Replace(ledgerXMLRK, "limit: 5", "limit: 2", 1), ledgerXML)
	c := e.must(201, "POST", "/api/v1/r/customer", map[string]any{"customer_name": "PT Besar Contoh"}).JSON["name"].(string)
	for i := 0; i < 2; i++ {
		e.submittedInvoice(c)
	}
	e.must(200, "GET", "/api/v1/r/customer/"+c+"/print?format=ledger_xml", nil)
	e.submittedInvoice(c)
	if r := e.do("GET", "/api/v1/r/customer/"+c+"/print?format=ledger_xml", nil); r.Status != 422 || code(r) != "export_too_large" {
		t.Errorf("3 rows, limit 2: %d %s", r.Status, r.Body)
	}
}

// An XML format is an export: print without export is refused.
func TestPrintXMLNeedsExport(t *testing.T) {
	rk := strings.NewReplacer("name: ledger_xml", "name: invoice_xml", "model: customer", "model: sales_invoice",
		"template: ledger_xml.xml", "template: invoice_xml.xml").Replace(ledgerXMLRK)
	rk = rk[:strings.Index(rk, "related:")]
	e := printEnv(t, func(apps, site string) {
		dir := filepath.Join(site, "print")
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, "invoice_xml.rk"), []byte(rk), 0o644)
		os.WriteFile(filepath.Join(dir, "invoice_xml.xml"), []byte("<I>{{.Doc.name}}</I>"), 0o644)
	})
	name := e.submittedInvoice("CUST-0012")
	if _, err := auth.EnsureUser(context.Background(), e.s.DB, "sm@contoh.test", "SM", "rahasia-123", "kopi-senja", "sales_manager", ""); err != nil {
		t.Fatal(err)
	}
	e.jar, _ = cookiejar.New(nil)
	e.must(200, "POST", "/auth/login", map[string]any{"email": "sm@contoh.test", "password": "rahasia-123"})
	e.must(200, "GET", "/api/v1/r/sales_invoice/"+name+"/print?format=sales_invoice", nil) // print is granted
	r := e.do("GET", "/api/v1/r/sales_invoice/"+name+"/print?format=invoice_xml", nil)
	if r.Status != 403 || code(r) != "permission_denied" || get(r.JSON, "error", "action") != "export" {
		t.Errorf("print without export: %d %s", r.Status, r.Body)
	}
}

// range picks related rows between two document fields (inclusive, as text: ISO dates); require checks see the
// printed document as doc.
func TestPrintRangeAndDocInRequire(t *testing.T) {
	rk := `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/print.json
name: period_xml
model: sales_invoice
output: xml
template: period_xml.xml
related:
  invoices:
    model: sales_invoice
    range: {posting_date: [posting_date, due_date]}
    filter: docstatus == 1
    sort: posting_date
    limit: 1000
    require:
      - check: customer == doc.customer
        message: {id: 'Pelanggan lain'}
`
	e := printEnv(t, func(apps, site string) {
		dir := filepath.Join(site, "print")
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, "period_xml.rk"), []byte(rk), 0o644)
		os.WriteFile(filepath.Join(dir, "period_xml.xml"), []byte(`<P>{{range .Related.invoices}}<I>{{.name}}|{{.posting_date}}</I>{{end}}</P>`), 0o644)
	})
	c := e.must(201, "POST", "/api/v1/r/customer", map[string]any{"customer_name": "PT Rentang Contoh"}).JSON["name"].(string)
	mk := func(cust, day string) string {
		inv := e.must(201, "POST", "/api/v1/r/sales_invoice", map[string]any{"customer": cust, "outlet": "OUT-01", "posting_date": day, "due_date": day,
			"items": []any{map[string]any{"item": "BRG-0044", "qty": 1}}}).JSON["name"].(string)
		return e.must(200, "POST", "/api/v1/r/sales_invoice/"+inv+"/submit", map[string]any{"force": true}).JSON["name"].(string)
	}
	// the printed document spans 2031-03-01..2031-03-31 (far from the fixtures' dates)
	doc := e.must(201, "POST", "/api/v1/r/sales_invoice", map[string]any{"customer": c, "outlet": "OUT-01", "posting_date": "2031-03-01", "due_date": "2031-03-31",
		"items": []any{map[string]any{"item": "BRG-0044", "qty": 1}}}).JSON["name"].(string)
	in1, in2 := mk(c, "2031-03-15"), mk(c, "2031-03-31")
	mk(c, "2031-04-01")
	mk(c, "2031-02-28")
	r := e.must(200, "GET", "/api/v1/r/sales_invoice/"+doc+"/print?format=period_xml", nil)
	if got, want := string(r.Body), "<P><I>"+in1+"|2031-03-15</I><I>"+in2+"|2031-03-31</I></P>"; got != want {
		t.Fatalf("range:\n got %s\nwant %s", got, want)
	}
	other := mk("CUST-0012", "2031-03-20")
	r = e.do("GET", "/api/v1/r/sales_invoice/"+doc+"/print?format=period_xml", nil)
	if r.Status != 422 || get(r.JSON, "error", "problems", 0, "name") != other || len(get(r.JSON, "error", "problems").([]any)) != 1 {
		t.Errorf("doc in require: %d %s", r.Status, r.Body)
	}
}

// A tax file holds every row or none: rows the caller loses to an if: of their grants (or a row scope) refuse the
// file with 422 export_partial instead of serving a file that looks complete (audit pajak 7 Nov #11).
func TestPrintXMLPartialRefused(t *testing.T) {
	// also with a filter that reads the child table (children: true; audit xml-followups #1)
	for _, rk := range []string{ledgerXMLRK, strings.Replace(ledgerXMLRK, "filter: docstatus == 1", "filter: docstatus == 1 && size(items) > 0", 1)} {
		partialRefused(t, rk)
	}
}

func partialRefused(t *testing.T, rk string) {
	t.Helper()
	e := printEnv(t, func(apps, site string) {
		dir := filepath.Join(site, "print")
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, "ledger_xml.rk"), []byte(rk), 0o644)
		os.WriteFile(filepath.Join(dir, "ledger_xml.xml"), []byte(ledgerXML), 0o644)
		inv := filepath.Join(apps, "sales", "models", "sales_invoice.rk")
		b, _ := os.ReadFile(inv)
		os.WriteFile(inv, []byte(strings.Replace(string(b), "  - {role: sales_manager, read: true, print: true}",
			"  - {role: sales_manager, read: true, print: true, export: true, if: \"outlet == 'OUT-01'\"}", 1)), 0o644)
		cust := filepath.Join(apps, "core", "models", "customer.rk")
		b, _ = os.ReadFile(cust)
		os.WriteFile(cust, []byte(string(b)+"permissions:\n  - {role: owner, read: true, create: true, write: true, print: true, export: true}\n"+
			"  - {role: sales_manager, read: true, print: true, export: true}\n"), 0o644)
	})
	c := e.must(201, "POST", "/api/v1/r/customer", map[string]any{"customer_name": "PT Sebagian Contoh"}).JSON["name"].(string)
	e.submittedInvoice(c)
	other := e.must(201, "POST", "/api/v1/r/sales_invoice", map[string]any{"customer": c, "outlet": "OUT-02",
		"items": []any{map[string]any{"item": "BRG-0044", "qty": 1}}}).JSON["name"].(string)
	e.must(200, "POST", "/api/v1/r/sales_invoice/"+other+"/submit", map[string]any{"force": true})
	url := "/api/v1/r/customer/" + c + "/print?format=ledger_xml"
	if r := e.must(200, "GET", url, nil); strings.Count(string(r.Body), "<Invoice>") != 2 {
		t.Fatalf("owner: both invoices: %s", r.Body)
	}
	if _, err := auth.EnsureUser(context.Background(), e.s.DB, "sm@contoh.test", "SM", "rahasia-123", "kopi-senja", "sales_manager", ""); err != nil {
		t.Fatal(err)
	}
	e.jar, _ = cookiejar.New(nil)
	e.must(200, "POST", "/auth/login", map[string]any{"email": "sm@contoh.test", "password": "rahasia-123"})
	for _, method := range []string{"GET", "HEAD"} {
		r := e.do(method, url, nil)
		if r.Status != 422 || (method == "GET" && (code(r) != "export_partial" || !strings.Contains(string(r.Body), "1 dokumen"))) {
			t.Errorf("%s, one of two invoices outside the grant: %d %s", method, r.Status, r.Body)
		}
	}
}
