package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// taxEngine builds an engine (no database) over one model whose default reads org.pkp and tax.ppn_rate, on a
// site with a PKP org, a non-PKP org and an org with its own 11% rate.
func taxEngine(t *testing.T, siteTax string) *Engine {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"apps/shop/app.rk": "name: shop\nversion: 1.0.0\ntitle: {id: Toko}\n",
		"apps/shop/models/quote.rk": "name: quote\nlabel: {id: Penawaran}\nfields:\n" +
			"  - {name: tax_rate, type: int, default: \"org.pkp ? tax.ppn_rate : 0\"}\n" +
			"  - {name: dpp, type: data, default: \"string(tax.dpp_num) + '/' + string(tax.dpp_den)\"}\n" +
			"  - {name: who, type: data, default: \"org.id + ':' + org.code + ':' + org.legal_name\"}\n",
		"site/config.rk": "locale: id-ID\ncurrency: IDR\napps: [shop]\n" + siteTax +
			"org: {legal_name: PT Situs, npwp: \"01.000.000.0-000.000\", pkp: true}\n" +
			"orgs:\n" +
			"  pt_pkp: {name: PT PKP, code: PKP}\n" +
			"  pt_kecil: {name: PT Kecil, code: KCL, config: {org: {legal_name: PT Kecil, pkp: false}}}\n" +
			"  pt_lama: {name: PT Lama, config: {tax: {ppn_rate: 11, dpp_ratio: 10/11}}}\n" +
			"  pt_nada: {name: PT Nada, config: {org: {legal_name: PT Nada}}}\n",
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
	if reg.Models["quote"] == nil || reg.Orgs["pt_kecil"] == nil {
		t.Fatalf("test registry did not load: models %v orgs %v", reg.Models, reg.Orgs)
	}
	return New(nil, reg)
}

func quoteDefaults(e *Engine, org string) store.Record {
	rec := store.Record{}
	e.newScope(context.Background(), nil, Actor{Org: org, ID: "a@example.test"}, e.Reg().Models["quote"], rec, nil).applyDefaults()
	return rec
}

func TestOrgTaxGlobalsInDefaults(t *testing.T) {
	e := taxEngine(t, "")
	cases := []struct {
		org, dpp, who string
		rate          int64
	}{
		{"pt_pkp", "11/12", "pt_pkp:PKP:PT Situs", 12},    // site org: (pkp true), default tax
		{"pt_kecil", "11/12", "pt_kecil:KCL:PT Kecil", 0}, // the org's own org: says pkp false
		{"pt_lama", "10/11", "pt_lama::PT Situs", 11},     // the org's own tax: overrides the rate
		{"elsewhere", "11/12", "elsewhere::PT Situs", 12},
		{"pt_nada", "11/12", "pt_nada::PT Nada", 0}, // nothing declares it PKP → pkp false, no PPN // not in orgs: → the site's settings
	}
	for _, c := range cases {
		rec := quoteDefaults(e, c.org)
		if rec["tax_rate"] != c.rate || rec["dpp"] != c.dpp || rec["who"] != c.who {
			t.Errorf("%s: tax_rate %v dpp %v who %v; want %d %s %s", c.org, rec["tax_rate"], rec["dpp"], rec["who"], c.rate, c.dpp, c.who)
		}
	}
}

func TestTaxGlobalSiteRate(t *testing.T) {
	e := taxEngine(t, "tax: {ppn_rate: 11}\n")
	if got := quoteDefaults(e, "pt_pkp")["tax_rate"]; got != int64(11) {
		t.Errorf("tax_rate with tax.ppn_rate 11 = %v (%T)", got, got)
	}
	if got := quoteDefaults(e, "pt_kecil")["tax_rate"]; got != int64(0) {
		t.Errorf("non-PKP org tax_rate = %v", got)
	}
}

func TestGlobalVar(t *testing.T) {
	e := taxEngine(t, "")
	a := Actor{Org: "pt_kecil", ID: "a@example.test"}
	for _, n := range []string{"user", "org", "tax"} {
		if _, ok := GlobalVar(e.Reg(), a, n); !ok {
			t.Errorf("GlobalVar(%s) not found", n)
		}
	}
	if _, ok := GlobalVar(e.Reg(), a, "nope"); ok {
		t.Error("GlobalVar(nope) found")
	}
}
