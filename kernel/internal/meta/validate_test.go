package meta

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/schema"
)

const (
	appsDir = "../../../../rangka-apps/apps"
	siteDir = "../../../../rangka-apps/sites/kopi-senja"
)

func TestRealAppsValid(t *testing.T) {
	reg, rep := Validate(appsDir, siteDir, nil, schema.Embedded())
	var buf bytes.Buffer
	if n := rep.Print(&buf); n != 0 || len(rep.All()) != 0 {
		t.Fatalf("expected no problems:\n%s", buf.String())
	}
	if got := strings.Join(reg.AppOrder(), ","); got != "core,crm,sales" {
		t.Errorf("installed apps = %s", got)
	}
	if reg.Model("deal") == nil || reg.Model("sales_invoice_item") == nil || len(reg.Flows) != 2 {
		t.Errorf("registry incomplete: %d models %d flows", len(reg.Models), len(reg.Flows))
	}
}

// golden compares the validator with a golden file made by rangka_validate.py (and with a live run of the
// Python tool when python3 + jsonschema are available). It runs from the kernel root, like make compare-python.
func golden(t *testing.T, goldenFile string, o Options) {
	t.Helper()
	wd, _ := os.Getwd()
	os.Chdir("../..")
	defer os.Chdir(wd)
	_, rep := ValidateOpts(o, nil, schema.Embedded())
	var buf bytes.Buffer
	rep.Print(&buf)
	want, err := os.ReadFile(goldenFile)
	if err != nil {
		t.Fatal(err)
	}
	if buf.String() != string(want) {
		t.Errorf("output differs from %s:\n--- got\n%s\n--- want\n%s", goldenFile, buf.String(), want)
	}
	args := []string{"../tools/rangka_validate.py", o.AppsDir}
	if o.SiteDir != "" {
		args = append(args, "--site", o.SiteDir)
	}
	if o.ConnectorsDir != "" {
		args = append(args, "--connectors", o.ConnectorsDir)
	}
	out, err := exec.Command("python3", args...).Output()
	if len(out) == 0 {
		t.Logf("python reference not run: %v", err)
		return
	}
	if buf.String() != string(out) {
		t.Errorf("output differs from live python:\n--- got\n%s\n--- want\n%s", buf.String(), out)
	}
}

func TestBrokenAppsMatchPython(t *testing.T) {
	golden(t, "testdata/broken-apps.expected.txt", Options{AppsDir: "testdata/broken-apps"})
}

func TestBrokenSiteMatchPython(t *testing.T) {
	golden(t, "testdata/broken-site.expected.txt", Options{AppsDir: "testdata/broken-apps", SiteDir: "testdata/broken-site", ConnectorsDir: "testdata/broken-connectors"})
}

// Overlay fields are visible through links: sales_invoice's overlay rule reads customer.x_customer_group,
// which customer's overlay adds (testdata/overlay-site).
func TestOverlayFieldsThroughLinks(t *testing.T) {
	reg, rep := ValidateOpts(Options{AppsDir: appsDir, SiteDir: "../../testdata/overlay-site"}, nil, schema.Embedded())
	if len(rep.All()) != 0 {
		t.Fatalf("problems: %v", rep.All())
	}
	if reg.Model("customer").Field("x_customer_group") == nil || reg.Model("sales_invoice").Field("x_po_number") == nil {
		t.Fatal("overlays not merged")
	}
}

func TestOverlayMerge(t *testing.T) {
	dir := t.TempDir()
	exec.Command("cp", "-r", siteDir, dir+"/site").Run()
	os.MkdirAll(dir+"/site/custom", 0o755)
	os.WriteFile(dir+"/site/custom/sales_invoice.rk", []byte(`# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.4/overlay.json
model: sales_invoice
fields:
  - {name: x_po_number, type: data, label: {id: Nomor PO}}
rules:
  - {name: po_needed, on: submit, check: "x_po_number != ''", message: {id: Isi nomor PO}}
views:
  form: {add: [{field: x_po_number, after: outlet}]}
  list: {add: [{field: x_po_number, after: customer}]}
`), 0o644)
	reg, rep := ValidateOpts(Options{AppsDir: appsDir, SiteDir: dir + "/site"}, nil, schema.Embedded())
	if rep.Errors() > 0 {
		t.Fatalf("errors: %v", rep.All())
	}
	m := reg.Model("sales_invoice")
	if m.Field("x_po_number") == nil || m.Overlay != "custom/sales_invoice.rk" || len(m.Rules) != 3 {
		t.Fatalf("overlay not merged: %v %v", m.Overlay, len(m.Rules))
	}
	cols := strList(asMap(asMap(m.Raw["views"])["list"])["columns"])
	if strings.Join(cols, ",") != "name,customer,x_po_number,posting_date,status,grand_total" {
		t.Errorf("list columns = %v", cols)
	}
	first := asMap(asList(asMap(asMap(m.Raw["views"])["form"])["layout"])[0])
	if got := strList(asList(first["columns"])[0]); strings.Join(got, ",") != "customer,outlet,x_po_number" {
		t.Errorf("form = %v", got)
	}
	if reg.App["sales"].Doc.Text == "" || strings.Contains(m.Doc.Text, "x_po_number") {
		t.Error("app file must not change")
	}
}

func TestSuggest(t *testing.T) {
	if got := suggest("customr", []string{"customer", "deal", "lead"}); got != `; did you mean "customer"?` {
		t.Errorf("got %q", got)
	}
	if got := suggest("zzz", []string{"customer"}); got != "" {
		t.Errorf("got %q", got)
	}
}
