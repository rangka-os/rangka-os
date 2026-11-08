package meta

import (
	"bytes"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/schema"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

// onlyDeprecations reports whether the report has nothing but the 0.4 layout warnings (contract 0001).
func onlyDeprecations(rep *Report) bool {
	for _, it := range rep.All() {
		if it.Code != "deprecated_layout" && it.Code != "deprecated_template" {
			return false
		}
	}
	return true
}

// withoutDeprecations is the report without the 0.4 layout warnings.
func withoutDeprecations(rep *Report) []Item {
	var out []Item
	for _, it := range rep.All() {
		if it.Code != "deprecated_layout" && it.Code != "deprecated_template" {
			out = append(out, it)
		}
	}
	return out
}

func TestRealAppsValid(t *testing.T) {
	reg, rep := Validate(testdb.Catalogue("apps"), testdb.Catalogue("sites/kopi-senja"), nil, schema.Embedded())
	var buf bytes.Buffer
	if n := rep.Print(&buf); n != 0 || !onlyDeprecations(rep) {
		t.Fatalf("expected no problems but the deprecation warnings:\n%s", buf.String())
	}
	if reg.App["crm"].Kind != KindLegacy {
		t.Errorf("crm kind = %s", reg.App["crm"].Kind)
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

func TestBrokenModulesAppsMatchPython(t *testing.T) {
	golden(t, "testdata/broken-modules-apps.expected.txt", Options{AppsDir: "testdata/broken-modules-apps/apps", SiteDir: "testdata/broken-modules-apps/site"})
}

// Print formats and letterheads (contract 0008): every check except template_error, which is kernel only.
func TestBrokenPrintMatchPython(t *testing.T) {
	golden(t, "testdata/broken-print.expected.txt", Options{AppsDir: "testdata/broken-print-apps/apps", SiteDir: "testdata/broken-print-site"})
}

// The contract 0001 layout: modules/ next to apps/, an app that names its modules, a site installing the app.
func TestModulesApps(t *testing.T) {
	reg, rep := ValidateOpts(Options{AppsDir: "../../testdata/modules-apps/apps", SiteDir: "../../testdata/modules-apps/site"}, nil, schema.Embedded())
	var buf bytes.Buffer
	if rep.Print(&buf); len(rep.All()) != 0 {
		t.Fatalf("expected no problems:\n%s", buf.String())
	}
	if got := strings.Join(reg.AppOrder(), ","); got != "contacts,pipeline,crm" {
		t.Errorf("installed = %s", got)
	}
	kinds := map[string]string{"contacts": KindModule, "pipeline": KindModule, "crm": KindApp}
	for n, k := range kinds {
		if reg.App[n] == nil || reg.App[n].Kind != k {
			t.Errorf("%s: kind %v want %s", n, reg.App[n], k)
		}
	}
	if m := reg.Model("deal"); m == nil || m.App != "pipeline" {
		t.Fatalf("deal = %+v", m)
	}
	for _, k := range []string{"modules/pipeline/module.rk", "modules/pipeline/models/deal.rk", "modules/pipeline/flows/deal_follow_up.rk",
		"modules/contacts/roles/sales_rep.rk", "apps/crm/app.rk", "apps/crm/dashboards/crm_overview.rk"} {
		if reg.Docs[k] == nil {
			t.Errorf("no doc %s", k)
		}
	}
	if reg.Dashboard("crm_overview") == nil {
		t.Error("app dashboard not installed")
	}
}

// Overlay fields are visible through links: sales_invoice's overlay rule reads customer.x_customer_group,
// which customer's overlay adds (testdata/overlay-site).
func TestOverlayFieldsThroughLinks(t *testing.T) {
	reg, rep := ValidateOpts(Options{AppsDir: testdb.Catalogue("apps"), SiteDir: "../../testdata/overlay-site"}, nil, schema.Embedded())
	if !onlyDeprecations(rep) {
		t.Fatalf("problems: %v", rep.All())
	}
	if reg.Model("customer").Field("x_customer_group") == nil || reg.Model("sales_invoice").Field("x_po_number") == nil {
		t.Fatal("overlays not merged")
	}
}

func TestOverlayMerge(t *testing.T) {
	dir := t.TempDir()
	exec.Command("cp", "-r", testdb.Catalogue("sites/kopi-senja"), dir+"/site").Run()
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
	reg, rep := ValidateOpts(Options{AppsDir: testdb.Catalogue("apps"), SiteDir: dir + "/site"}, nil, schema.Embedded())
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

// approvals: key is parsed, ApprovalsFor filters by verb in declaration order (0007 §1).
func TestApprovalsParsed(t *testing.T) {
	dir := t.TempDir()
	if out, err := exec.Command("cp", "-r", testdb.Catalogue("apps"), dir+"/apps").CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	f := dir + "/apps/sales/models/sales_invoice.rk"
	b := mustRead(t, f)
	b = append(b, []byte(`approvals:
  - name: manager
    on: submit
    when: "grand_total > 10000000"
    approvers: [sales_manager]
    label: {id: Persetujuan manajer}
`)...)
	mustWrite(t, f, b)
	reg, rep := ValidateOpts(Options{AppsDir: dir + "/apps"}, nil, schema.Embedded())
	if rep.Errors() > 0 {
		t.Fatalf("errors: %v", rep.All())
	}
	m := reg.Models["sales_invoice"]
	got := m.ApprovalsFor("submit")
	if len(got) != 1 || strings.Join(got[0].Approvers, ",") != "sales_manager" || got[0].When != "grand_total > 10000000" ||
		got[0].Name != "manager" || got[0].On != "submit" || got[0].Label.Text() != "Persetujuan manajer" {
		t.Fatalf("ApprovalsFor(submit) = %+v", got)
	}
	if len(m.ApprovalsFor("cancel")) != 0 {
		t.Error("cancel must have no policies")
	}
}

// A company overlay adds policies after the app's own (ruling R1) and they are checked like the app's.
func TestApprovalsOverlay(t *testing.T) {
	dir := t.TempDir()
	if out, err := exec.Command("cp", "-r", testdb.Catalogue("apps"), dir+"/apps").CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	if out, err := exec.Command("cp", "-r", testdb.Catalogue("sites/kopi-senja"), dir+"/site").CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	f := dir + "/apps/sales/models/sales_invoice.rk"
	b := mustRead(t, f)
	mustWrite(t, f, append(b, []byte("approvals:\n  - {name: own, on: submit, approvers: [sales_manager]}\n")...))
	os.MkdirAll(dir+"/site/custom", 0o755)
	mustWrite(t, dir+"/site/custom/sales_invoice.rk", []byte(`# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.4/overlay.json
model: sales_invoice
approvals:
  - {name: big, on: submit, when: "grand_total > 50000000", approvers: [sales_manager]}
`))
	reg, rep := ValidateOpts(Options{AppsDir: dir + "/apps", SiteDir: dir + "/site"}, nil, schema.Embedded())
	if rep.Errors() > 0 {
		t.Fatalf("errors: %v", rep.All())
	}
	got := reg.Models["sales_invoice"].ApprovalsFor("submit")
	if len(got) != 2 || got[0].Name != "own" || got[1].Name != "big" || got[1].When != "grand_total > 50000000" {
		t.Fatalf("overlay approvals = %+v", got)
	}
	mustWrite(t, dir+"/site/custom/sales_invoice.rk", []byte(`model: sales_invoice
approvals:
  - {name: bad, on: post, when: "nope > 1", approvers: [sales_manager]}
`))
	_, rep = ValidateOpts(Options{AppsDir: dir + "/apps", SiteDir: dir + "/site"}, nil, schema.Embedded())
	var buf bytes.Buffer
	rep.Print(&buf)
	for _, want := range []string{"approvals/0/on", "unknown_verb", "approvals/0/when"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("missing %q in:\n%s", want, buf.String())
		}
	}
}

// Flow approve steps: schema, and parentheses in when (0007 §4).
func TestFlowApproveStep(t *testing.T) {
	dir := t.TempDir()
	exec.Command("cp", "-r", testdb.Catalogue("apps"), dir+"/apps").Run()
	flow := func(step string) string {
		mustWrite(t, dir+"/apps/crm/flows/morning_follow_up.rk", []byte("name: morning_follow_up\nschedule: 0 8 * * 1-5\nsteps:\n  - "+step+"\n"))
		_, rep := ValidateOpts(Options{AppsDir: dir + "/apps"}, nil, schema.Embedded())
		var buf bytes.Buffer
		rep.Print(&buf)
		return buf.String()
	}
	if out := flow(`{approve: {approvers: [sales_manager], when: "size(x) > 0", label: {id: Setujui}}, if: "true", as: gate}`); strings.Contains(out, "approve") {
		t.Errorf("valid approve step reported:\n%s", out)
	}
	if out := flow(`{approve: {approvers: [sales_manager], when: "size(x > 0"}}`); !strings.Contains(out, "steps/0/approve/when") || !strings.Contains(out, "cel_syntax") {
		t.Errorf("missing parens error:\n%s", out)
	}
	if out := flow(`{approve: {approvers: []}}`); !strings.Contains(out, "schema") {
		t.Errorf("empty approvers accepted:\n%s", out)
	}
}

func mustRead(t *testing.T, f string) []byte {
	t.Helper()
	b, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func mustWrite(t *testing.T, f string, b []byte) {
	t.Helper()
	if err := os.WriteFile(f, b, 0o644); err != nil {
		t.Fatal(err)
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

// An org listed in orgs: without letterheads/<id>.rk is a warning (its documents lose address, bank and
// signatory); one with its own letterhead is not.
func TestLetterheadMissingWarning(t *testing.T) {
	codes := func(site string) []string {
		_, rep := ValidateOpts(Options{AppsDir: "../../testdata/broken-print-apps/apps", SiteDir: site}, nil, schema.Embedded())
		var out []string
		for _, it := range rep.All() {
			if it.Code == "letterhead_missing" {
				out = append(out, it.Level+" "+it.Where+" "+it.Message)
			}
		}
		return out
	}
	want := []string{`warn orgs/pt_c org "pt_c" has no letterheads/pt_c.rk; its documents print without address, bank or signatory`}
	if got := codes("../../testdata/broken-print-site"); !reflect.DeepEqual(got, want) {
		t.Errorf("broken-print-site: %q", got)
	}
	if got := codes(overlaySite); got != nil {
		t.Errorf("overlay-site (pt_alpha has letterheads/pt_alpha.rk): %q", got)
	}
}

// Declared stock moves (contract 0021): every stock: check, plus the 0.5 schema of the block.
func TestBrokenStockMatchPython(t *testing.T) {
	golden(t, "testdata/broken-stock.expected.txt", Options{AppsDir: "testdata/broken-stock-apps/apps", SiteDir: "testdata/broken-stock-apps/site"})
}

func TestStockApps(t *testing.T) {
	_, rep := ValidateOpts(Options{AppsDir: "../../testdata/stock-apps/apps", SiteDir: "../../testdata/stock-apps/site"}, nil, schema.Embedded())
	if rep.Errors() != 0 {
		var buf bytes.Buffer
		rep.Print(&buf)
		t.Fatalf("declared stock documents do not validate:\n%s", buf.String())
	}
}

// Contract 0036: the catalogue keys (summary, category, icon) and config.connectors. The valid set has no
// problems; the broken one has the warnings and errors rangka_validate.py prints.
func TestCatalogueKeys(t *testing.T) {
	_, rep := ValidateOpts(Options{AppsDir: "../../testdata/catalogue-keys/apps", SiteDir: "../../testdata/catalogue-keys/site", ConnectorsDir: "../../testdata/catalogue-keys/connectors"}, nil, schema.Embedded())
	if n := len(rep.All()); n != 0 {
		t.Fatalf("problems: %+v", rep.All())
	}
}

func TestBrokenCatalogueKeysMatchPython(t *testing.T) {
	golden(t, "testdata/broken-catalogue-keys.expected.txt", Options{AppsDir: "testdata/broken-catalogue-keys/apps", SiteDir: "testdata/broken-catalogue-keys/site", ConnectorsDir: "testdata/broken-catalogue-keys/connectors"})
}
