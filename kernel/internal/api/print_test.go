package api

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/cel"
	"github.com/rangka-os/rangka-os/kernel/internal/docprint"
	"github.com/rangka-os/rangka-os/kernel/internal/engine"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
)

func TestPrintOrg(t *testing.T) {
	dir := t.TempDir()
	cfg := `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/config.json
tax: {pkp: true}
org: {legal_name: PT Contoh Jasa Teknik, npwp: "00.000.000.0-000.000"}
orgs:
  pusat:
    name: Kantor pusat
    code: PST
    config:
      org: {legal_name: PT Contoh Pusat}
      tax: {pkp: false}
  abaikan:
    name: PT Abaikan
    config:
      org: {legal_name: PT Abaikan, pkp: false}
  cabang: {name: Cabang Contoh, code: CBG}
  bebas:
    name: Bebas PPN
    config:
      tax: {ppn_rate: 11}
`
	if err := os.WriteFile(filepath.Join(dir, "config.rk"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := meta.Load(t.TempDir(), dir, nil)
	for org, want := range map[string]map[string]any{
		"pusat":   {"name": "Kantor pusat", "code": "PST", "legal_name": "PT Contoh Pusat", "pkp": false}, // org: replaces the site's: no inherited npwp; pkp from its tax:
		"abaikan": {"name": "PT Abaikan", "code": "", "legal_name": "PT Abaikan", "pkp": true},            // an org: pkp key is ignored: pkp is always the merged tax.pkp
		"cabang":  {"name": "Cabang Contoh", "code": "CBG", "legal_name": "Cabang Contoh", "pkp": true},   // listed, no org:: its orgs name, no site identity, pkp from tax
		"bebas":   {"name": "Bebas PPN", "code": "", "legal_name": "Bebas PPN", "pkp": true},              // its tax: {ppn_rate: 11} merges over the site's, so tax.pkp stays
		"lain":    {"legal_name": "PT Contoh Jasa Teknik", "npwp": "00.000.000.0-000.000", "pkp": true},
	} {
		if got := printOrg(reg, org); !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %v, want %v", org, got, want)
		}
	}
	// printOrg copies: changing the result leaves the registry alone
	printOrg(reg, "lain")["legal_name"] = "X"
	if got := printOrg(reg, "lain")["legal_name"]; got != "PT Contoh Jasa Teknik" {
		t.Errorf("printOrg shares the config map: %v", got)
	}
}

// Print and CEL read the same TaxSettings: an org whose tax: sets only ppn_rate 11 gets 11 from a model
// default (the CEL tax global) and from print's taxrate helper, and pkp agrees between tax.pkp and .Org.pkp.
func TestPrintAndCELAgreeOnTax(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"apps/shop/app.rk": "name: shop\nversion: 1.0.0\ntitle: {id: Toko}\n",
		"apps/shop/models/quote.rk": "name: quote\nlabel: {id: Penawaran}\nfields:\n" +
			"  - {name: tax_rate, type: int, default: \"tax.pkp ? tax.ppn_rate : 0\"}\n" +
			"  - {name: pkp, type: check, default: \"tax.pkp\"}\n",
		"site/config.rk": "apps: [shop]\ntax: {pkp: true, ppn_rate: 12}\norg: {legal_name: PT Situs}\norgs:\n" +
			"  pt_sebelas: {name: PT Sebelas, config: {tax: {ppn_rate: 11}}}\n" +
			"  pt_kecil: {name: PT Kecil, config: {tax: {pkp: false}, org: {pkp: true}}}\n",
		"site/print/quote.html": "{{taxrate .Doc .Tax}}|{{.Org.pkp}}|{{dppfactor .Doc .Tax}}",
	}
	for p, body := range files {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	reg := meta.Load(filepath.Join(root, "apps"), filepath.Join(root, "site"), nil)
	m := reg.Model("quote")
	if m == nil {
		t.Fatal("quote did not load")
	}
	for org, want := range map[string]struct {
		rate  int64
		pkp   bool
		print string
	}{
		"pt_sebelas": {11, true, "11|true|11/12"},
		"pt_kecil":   {0, false, "12|false|11/12"}, // org: {pkp: true} is ignored by both
	} {
		a := engine.Actor{Org: org, ID: "a@example.test"}
		env := &cel.Env{Vars: func(n string) (any, bool) { return engine.GlobalVar(reg, a, n) }}
		rate, err := cel.Eval(m.Field("tax_rate").Default, env)
		if err != nil {
			t.Fatal(err)
		}
		pkp, err := cel.Eval(m.Field("pkp").Default, env)
		if err != nil {
			t.Fatal(err)
		}
		if rate != want.rate || pkp != want.pkp {
			t.Errorf("%s: CEL tax_rate %v pkp %v, want %d %v", org, rate, pkp, want.rate, want.pkp)
		}
		tpl := filepath.Join(root, "site", "print", "quote.html")
		out, err := docprint.Render(docprint.Input{
			Format: &meta.Print{Name: "quote", Model: "quote", Paper: "A4", Template: tpl, Root: filepath.Join(root, "site")},
			Model:  m, Doc: map[string]any{"name": "Q-1"}, Org: printOrg(reg, org), Tax: reg.TaxSettings(org),
		})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(out), want.print) {
			t.Errorf("%s: print does not contain %q:\n%s", org, want.print, out)
		}
	}
}
