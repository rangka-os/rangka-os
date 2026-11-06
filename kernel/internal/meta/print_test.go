package meta

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/schema"
)

const (
	printRoot = "../../testdata/print-apps" // apps/ and modules/ side by side (contract 0001)
	printApps = printRoot + "/apps"
	printSite = "../../testdata/print-site"
)

func TestLoadPrints(t *testing.T) {
	reg := Load(printApps, printSite, nil)
	inv := reg.PrintsFor("sales_invoice")
	if len(inv) != 1 || inv[0].App != "" || inv[0].Label.Text() != "Faktur (kop situs)" {
		t.Fatalf("sales_invoice formats = %+v", inv)
	}
	if !filepath.IsAbs(inv[0].Template) || !strings.HasSuffix(inv[0].Template, filepath.Join("print-site", "print", "sales_invoice.html")) {
		t.Errorf("template = %s", inv[0].Template)
	}
	cust := reg.PrintsFor("customer")
	if len(cust) != 1 {
		t.Fatalf("customer formats = %+v", cust)
	}
	want := Related{Model: "sales_invoice", Match: map[string]string{"customer": "name"}, Filter: "docstatus == 1 && grand_total > paid_amount", Sort: "posting_date", Limit: 1000}
	if got := cust[0].Related["invoices"]; !reflect.DeepEqual(got, want) {
		t.Errorf("related = %+v", got)
	}
	q := reg.Print("quotation")
	if q == nil || q.Paper != "A4" || q.App != "sales" || !q.Default {
		t.Fatalf("quotation = %+v", q)
	}
	if reg.Print("nope") != nil {
		t.Error("unknown format found")
	}
}

func TestPrintsForOrderAndInstall(t *testing.T) {
	reg := Load(printApps, printSite, nil)
	extra := &Print{Name: "a_draft", App: "sales", Model: "quotation", Paper: "A4"}
	other := &Print{Name: "b_other", App: "crm", Model: "quotation", Paper: "A4"}
	reg.Prints[extra.Name], reg.Prints[other.Name] = extra, other
	var names []string
	for _, p := range reg.PrintsFor("quotation") {
		names = append(names, p.Name)
	}
	if strings.Join(names, ",") != "quotation,a_draft" {
		t.Errorf("order = %v (default first, uninstalled app left out)", names)
	}
	if reg.Print("b_other") != nil {
		t.Error("format of an app that is not installed")
	}
}

func TestRelatedLimit(t *testing.T) {
	if got := parseRelated(map[string]any{"model": "x"}).Limit; got != 200 {
		t.Errorf("default limit = %d", got)
	}
	if got := parseRelated(map[string]any{"model": "x", "limit": int64(5000)}).Limit; got != 1000 {
		t.Errorf("capped limit = %d", got)
	}
}

func TestLetterhead(t *testing.T) {
	reg := Load(printApps, printSite, nil)
	if got := reg.Letterhead("pt-kedua")["legal_name"]; got != "PT Contoh Kedua" {
		t.Errorf("per-org legal_name = %v", got)
	}
	lh := reg.Letterhead("other")
	if lh["legal_name"] != "PT Contoh Jasa Teknik" {
		t.Errorf("default legal_name = %v", lh["legal_name"])
	}
	logo, _ := lh["logo"].(string)
	if !filepath.IsAbs(logo) {
		t.Fatalf("logo not absolute: %q", logo)
	}
	if _, err := os.Stat(logo); err != nil {
		t.Errorf("logo: %v", err)
	}
	if reg.Site.Docs["letterhead.rk"] == nil || reg.Site.Docs["letterheads/pt-kedua.rk"] == nil || reg.Site.Letterheads[""] == nil {
		t.Error("letterheads missing from Site.Docs / Site.Letterheads")
	}
	if s, _ := asMap(reg.Site.Letterheads[""].Data)["logo"].(string); s != "brand/logo.svg" {
		t.Errorf("Letterhead changed the document: %q", s)
	}
}

// copyDir copies a directory tree.
func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func editFile(t *testing.T, path, old, new string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), old) {
		t.Fatalf("%s: %q not found (%v)", path, old, err)
	}
	writeFile(t, path, strings.Replace(string(b), old, new, 1))
}

const configHead = "# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/config.json\n"

func TestLetterheadFallback(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "config.rk"), configHead+"org: {legal_name: PT Contoh Jasa Teknik}\n")
	reg := validSite(t, dir)
	if got := reg.Letterhead("x"); !reflect.DeepEqual(got, map[string]any{"legal_name": "PT Contoh Jasa Teknik"}) {
		t.Errorf("fallback = %v", got)
	}
}

// validSite loads the print apps with the site in dir and fails the test on any validation error.
func validSite(t *testing.T, dir string) *Registry {
	t.Helper()
	reg, rep := Validate(printApps, dir, nil, schema.Embedded())
	var buf bytes.Buffer
	if n := rep.Print(&buf); n != 0 {
		t.Fatalf("site does not validate:\n%s", buf.String())
	}
	return reg
}

func TestLetterheadFromOrgs(t *testing.T) {
	dir := t.TempDir()
	cfg := configHead + `org: {legal_name: PT Lama, npwp: "99.999.999.9-999.999"}
orgs:
  pusat:
    name: Kantor pusat
    code: PST
    config:
      org: {legal_name: PT Contoh Jasa Teknik, npwp: "00.000.000.0-000.000", pkp: true}
  cabang: {name: PT Contoh Cabang}
  tanpa:
    name: PT Contoh Tanpa Nama Resmi
    config:
      org: {pkp: false}
  dua:
    name: PT Contoh Dua
    config:
      org: {legal_name: PT Contoh Dua Resmi}
`
	writeFile(t, filepath.Join(dir, "config.rk"), cfg)
	reg := validSite(t, dir)
	for org, want := range map[string]map[string]any{
		"pusat":  {"legal_name": "PT Contoh Jasa Teknik", "npwp": "00.000.000.0-000.000"},
		"cabang": {"legal_name": "PT Contoh Cabang"},                        // listed, no config.org: its own name, no site npwp
		"lain":   {"legal_name": "PT Lama", "npwp": "99.999.999.9-999.999"}, // not in orgs: the site's org:
		"tanpa":  {"legal_name": "PT Contoh Tanpa Nama Resmi"},              // config.org without legal_name: the org's name, no site npwp
		"dua":    {"legal_name": "PT Contoh Dua Resmi"},                     // config.org: {legal_name} inherits no npwp
	} {
		if got := reg.Letterhead(org); !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %v, want %v", org, got, want)
		}
	}

	// a letterhead file without legal_name / npwp takes them from the org's data; its own values win
	writeFile(t, filepath.Join(dir, "letterhead.rk"), "# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/letterhead.json\ntagline: Inspeksi\n")
	writeFile(t, filepath.Join(dir, "letterheads", "cabang.rk"), "# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/letterhead.json\nlegal_name: PT Kop Cabang\nnpwp: \"11.111.111.1-111.111\"\n")
	reg = validSite(t, dir)
	// an unlisted org uses letterhead.rk and the site's org:
	if got := reg.Letterhead("lain"); !reflect.DeepEqual(got, map[string]any{"tagline": "Inspeksi", "legal_name": "PT Lama", "npwp": "99.999.999.9-999.999"}) {
		t.Errorf("unlisted = %v", got)
	}
	// a listed org without its own letterheads/<org>.rk gets nothing from the site's letterhead.rk
	if got := reg.Letterhead("pusat"); !reflect.DeepEqual(got, map[string]any{"legal_name": "PT Contoh Jasa Teknik", "npwp": "00.000.000.0-000.000"}) {
		t.Errorf("listed without own file = %v", got)
	}
	if got := reg.Letterhead("cabang"); got["legal_name"] != "PT Kop Cabang" || got["npwp"] != "11.111.111.1-111.111" {
		t.Errorf("own values = %v", got)
	}
}

func TestLetterheadDeepCopy(t *testing.T) {
	reg := Load(printApps, printSite, nil)
	lh := reg.Letterhead("")
	asMap(asList(lh["bank"])[0])["bank"] = "changed"
	asMap(lh["signatory"])["name"] = "changed"
	again := reg.Letterhead("")
	if asMap(asList(again["bank"])[0])["bank"] != "Bank Contoh" || asMap(again["signatory"])["name"] != "Nama Penandatangan" {
		t.Errorf("Letterhead shares nested values with the document: %v", again)
	}
}

func TestLogoOutsideSite(t *testing.T) {
	for _, logo := range []string{"/etc/hosts", "../print-apps/modules/core/module.rk"} {
		dir := t.TempDir()
		copyDir(t, printSite, dir+"/site")
		copyDir(t, printRoot, dir+"/print-apps")
		editFile(t, dir+"/site/letterhead.rk", "logo: brand/logo.svg", "logo: "+logo)
		reg, rep := Validate(printApps, dir+"/site", nil, schema.Embedded())
		var n int
		for _, it := range rep.All() {
			if it.Code == "logo_missing" && strings.Contains(it.Message, "inside the site folder") {
				n++
			} else if it.Level == "error" {
				t.Errorf("%s: unexpected %v", logo, it)
			}
		}
		if n != 1 {
			t.Errorf("%s: logo_missing reported %d times: %v", logo, n, rep.All())
		}
		if _, has := reg.Letterhead("")["logo"]; has {
			t.Errorf("%s: Letterhead returned a logo outside the site", logo)
		}
	}
	dir := t.TempDir()
	copyDir(t, printSite, dir+"/site")
	editFile(t, dir+"/site/letterhead.rk", "logo: brand/logo.svg", "logo: brand/none.png")
	if _, has := Load(printApps, dir+"/site", nil).Letterhead("")["logo"]; has {
		t.Error("Letterhead returned a missing logo")
	}
}

func TestSamePrintNameInstalledWins(t *testing.T) {
	dir := t.TempDir()
	copyDir(t, printRoot, dir)
	writeFile(t, dir+"/modules/aa/module.rk", "# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/module.json\nname: aa\nversion: 1.0.0\n")
	writeFile(t, dir+"/modules/aa/print/quotation.rk", "# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/print.json\nname: quotation\nmodel: quotation\ntemplate: quotation.html\n")
	reg := Load(dir+"/apps", printSite, nil) // config.rk installs the jasa app (sales, core), not aa
	if p := reg.Print("quotation"); p == nil || p.App != "sales" {
		t.Fatalf("quotation = %+v", p)
	}
	if got := reg.PrintsFor("quotation"); len(got) != 1 || got[0].App != "sales" {
		t.Errorf("PrintsFor = %+v", got)
	}
}

func TestValidatePrintsClean(t *testing.T) {
	_, rep := Validate(printApps, printSite, nil, schema.Embedded())
	var buf bytes.Buffer
	if n := rep.Print(&buf); n != 0 {
		t.Fatalf("expected no errors:\n%s", buf.String())
	}
}

func TestValidatePrintsBroken(t *testing.T) {
	dir := t.TempDir()
	copyDir(t, printRoot, dir)
	copyDir(t, printSite, dir+"/site")
	editFile(t, dir+"/modules/sales/print/quotation.rk", "template: quotation.html", "template: ../../config.rk.html")
	editFile(t, dir+"/modules/sales/print/purchase_order.rk", "template: purchase_order.html", "template: nope.html")
	editFile(t, dir+"/site/print/sales_invoice.html", "{{.Doc.name}}", "{{ .Doc.x | nosuchfunc }}")
	editFile(t, dir+"/modules/sales/print/sales_invoice.rk", "model: sales_invoice", "model: nope")
	editFile(t, dir+"/modules/core/print/statement.rk", "match: {customer: name}", "match: {nope: name}")
	editFile(t, dir+"/site/letterhead.rk", "logo: brand/logo.svg", "logo: brand/none.png")
	writeFile(t, dir+"/site/print/a.rk", `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/print.json
name: b
model: quotation
template: a.html
`)
	writeFile(t, dir+"/site/print/a.html", "<p>{{.Doc.name}}</p>\n")

	_, rep := Validate(dir+"/apps", dir+"/site", nil, schema.Embedded())
	count := map[string]int{}
	for _, it := range rep.All() {
		count[it.Code]++
	}
	var buf bytes.Buffer
	rep.Print(&buf)
	for _, code := range []string{"template_outside", "template_missing", "template_error", "unknown_model", "unknown_field", "logo_missing", "name_mismatch"} {
		if count[code] != 1 {
			t.Errorf("%s reported %d times:\n%s", code, count[code], buf.String())
		}
	}
	if len(count) != 7 {
		t.Errorf("unexpected codes %v:\n%s", count, buf.String())
	}
}

func TestPrintSchemaErrors(t *testing.T) {
	dir := t.TempDir()
	copyDir(t, printRoot, dir)
	writeFile(t, dir+"/modules/sales/print/quotation.rk", `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/print.json
name: quotation
model: quotation
template: quotation.html
paper: A3
`)
	_, rep := Validate(dir+"/apps", "", nil, schema.Embedded())
	var n int
	for _, it := range rep.All() {
		if it.Code == "schema" && it.Where == "paper" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("paper schema error reported %d times: %v", n, rep.All())
	}
}

// Contract 0028: an XML format parses as text/template with the print helpers, require checks compile, and the
// template's extension follows output.
func TestValidateXMLPrints(t *testing.T) {
	dir := t.TempDir()
	copyDir(t, printRoot, dir)
	copyDir(t, printSite, dir+"/site")
	head := "# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/print.json\n"
	writeFile(t, dir+"/site/print/good_xml.rk", head+"name: good_xml\nmodel: customer\noutput: xml\ntemplate: good_xml.xml\nrelated:\n  invoices:\n    model: sales_invoice\n    match: {customer: name}\n    children: true\n    require:\n      - {check: \"docstatus == 1\", message: {id: Harus diajukan}}\n")
	writeFile(t, dir+"/site/print/good_xml.xml", `<A t="{{isodate .Printed}}">{{range .Related.invoices}}<B>{{dec (round (mul .grand_total 2) 2) 2}}{{digits .name}}</B>{{end}}</A>`)
	_, rep := Validate(dir+"/apps", dir+"/site", nil, schema.Embedded())
	var buf bytes.Buffer
	if n := rep.Print(&buf); n != 0 {
		t.Fatalf("good XML format:\n%s", buf.String())
	}
	writeFile(t, dir+"/site/print/bad_xml.rk", head+"name: bad_xml\nmodel: customer\noutput: xml\ntemplate: bad_xml.html\nrelated:\n  invoices:\n    model: sales_invoice\n    match: {customer: name}\n    require:\n      - {check: \"size(x > 0\", message: {id: Rusak}}\n")
	writeFile(t, dir+"/site/print/bad_xml.html", `<A>{{nosuch .}}</A>`)
	writeFile(t, dir+"/site/print/html_xml.rk", head+"name: html_xml\nmodel: customer\ntemplate: html_xml.xml\n")
	writeFile(t, dir+"/site/print/html_xml.xml", `<A/>`)
	_, rep = Validate(dir+"/apps", dir+"/site", nil, schema.Embedded())
	buf.Reset()
	rep.Print(&buf)
	out := buf.String()
	for _, want := range []string{"bad_xml.rk", "cel_syntax", "template_error", "html_xml.rk"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "schema") < 2 { // .html with output xml, .xml without it
		t.Errorf("template extension not checked against output:\n%s", out)
	}
}
