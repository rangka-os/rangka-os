package meta

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/schema"
)

const (
	printApps = "../../testdata/print-apps"
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
	want := Related{Model: "sales_invoice", Match: map[string]string{"customer": "name"}, Filter: "docstatus == 1", Sort: "posting_date", Limit: 500}
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

// config.org arrives with the schema 0.5 bootstrap (another stream); until then only this test's own config sets it.
func TestLetterheadFallback(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.rk"), []byte("# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.4/config.json\norg: {legal_name: PT Contoh Jasa Teknik}\n"), 0o644)
	reg := Load(printApps, dir, nil)
	if got := reg.Letterhead("x"); !reflect.DeepEqual(got, map[string]any{"legal_name": "PT Contoh Jasa Teknik"}) {
		t.Errorf("fallback = %v", got)
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
	if err := exec.Command("cp", "-r", printApps, dir+"/apps").Run(); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("cp", "-r", printSite, dir+"/site").Run(); err != nil {
		t.Fatal(err)
	}
	edit := func(path, old, new string) {
		t.Helper()
		b, err := os.ReadFile(path)
		if err != nil || !strings.Contains(string(b), old) {
			t.Fatalf("%s: %q not found (%v)", path, old, err)
		}
		os.WriteFile(path, []byte(strings.Replace(string(b), old, new, 1)), 0o644)
	}
	edit(dir+"/apps/sales/print/quotation.rk", "template: quotation.html", "template: ../../config.rk.html")
	edit(dir+"/apps/sales/print/purchase_order.rk", "template: purchase_order.html", "template: nope.html")
	edit(dir+"/site/print/sales_invoice.html", "{{.Doc.name}}", "{{ .Doc.x | nosuchfunc }}")
	edit(dir+"/apps/sales/print/sales_invoice.rk", "model: sales_invoice", "model: nope")
	edit(dir+"/apps/core/print/statement.rk", "match: {customer: name}", "match: {nope: name}")
	edit(dir+"/site/letterhead.rk", "logo: brand/logo.svg", "logo: brand/none.png")
	os.WriteFile(dir+"/site/print/a.rk", []byte(`# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/print.json
name: b
model: quotation
template: a.html
`), 0o644)
	os.WriteFile(dir+"/site/print/a.html", []byte("<p>{{.Doc.name}}</p>\n"), 0o644)

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
	exec.Command("cp", "-r", printApps, dir+"/apps").Run()
	os.WriteFile(dir+"/apps/sales/print/quotation.rk", []byte(`# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/print.json
name: quotation
model: quotation
template: quotation.html
paper: A3
`), 0o644)
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
