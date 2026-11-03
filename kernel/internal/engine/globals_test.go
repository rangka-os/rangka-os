package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// taxEngine builds an engine (no database) over one model whose defaults read the org and tax globals, and a
// role whose scope.from reads org.code, on a site whose tax: is siteTax and whose orgs are: pt_pkp (no
// override), pt_kecil (tax: {pkp: false}, keeping the site's rate) and pt_lama (its own rate and ratio).
func taxEngine(t *testing.T, siteTax string) *Engine {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"apps/shop/app.rk": "name: shop\nversion: 1.0.0\ntitle: {id: Toko}\n",
		"apps/shop/models/quote.rk": "name: quote\nlabel: {id: Penawaran}\nfields:\n" +
			"  - {name: outlet, type: data}\n" +
			"  - {name: tax_rate, type: int, default: \"tax.pkp ? tax.ppn_rate : 0\"}\n" +
			"  - {name: site_rate, type: int, default: \"tax.ppn_rate\"}\n" +
			"  - {name: dpp, type: data, default: \"string(tax.dpp_num) + '/' + string(tax.dpp_den)\"}\n" +
			"  - {name: who, type: data, default: \"org.id + ':' + org.code + ':' + org.legal_name\"}\n" +
			"permissions:\n  - {role: owner, read: true}\n",
		"apps/shop/roles/org_staff.rk": "name: org_staff\nlabel: {id: Staf}\nscope: {field: outlet, from: \"[org.code + '-01']\"}\n",
		"site/config.rk": "locale: id-ID\ncurrency: IDR\napps: [shop]\n" + siteTax +
			"org: {legal_name: PT Situs, npwp: \"01.000.000.0-000.000\"}\n" +
			"orgs:\n" +
			"  pt_pkp: {name: PT PKP, code: PKP}\n" +
			"  pt_kecil: {name: PT Kecil, code: KCL, config: {org: {legal_name: PT Kecil}, tax: {pkp: false}}}\n" +
			"  pt_lama: {name: PT Lama, config: {tax: {ppn_rate: 11, dpp_ratio: 10/11}}}\n",
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
	if reg.Models["quote"] == nil || reg.Orgs["pt_kecil"] == nil || reg.RoleScope("org_staff") == nil {
		t.Fatalf("test registry did not load: models %v orgs %v", reg.Models, reg.Orgs)
	}
	return New(nil, reg)
}

func actorIn(org string) Actor { return Actor{Org: org, ID: "a@example.test"} }

func quoteDefaults(e *Engine, org string) store.Record {
	rec := store.Record{}
	e.newScope(context.Background(), nil, actorIn(org), e.Reg().Models["quote"], rec, nil).applyDefaults()
	return rec
}

func TestOrgTaxGlobalsInDefaults(t *testing.T) {
	e := taxEngine(t, "tax: {ppn_rate: 12, pkp: true}\n")
	cases := []struct {
		org, dpp, who  string
		rate, siteRate int64
	}{
		{"pt_pkp", "11/12", "pt_pkp:PKP:PT Situs", 12, 12},    // a PKP org: tax.pkp true from the site
		{"pt_kecil", "11/12", "pt_kecil:KCL:PT Kecil", 0, 12}, // tax: {pkp: false} keeps the site's rate
		{"pt_lama", "10/11", "pt_lama::PT Situs", 11, 11},     // its own rate and ratio, the site's pkp
		{"elsewhere", "11/12", "elsewhere::PT Situs", 12, 12}, // not in orgs: → the site's settings
	}
	for _, c := range cases {
		rec := quoteDefaults(e, c.org)
		if rec["tax_rate"] != c.rate || rec["site_rate"] != c.siteRate || rec["dpp"] != c.dpp || rec["who"] != c.who {
			t.Errorf("%s: tax_rate %v site_rate %v dpp %v who %v; want %d %d %s %s", c.org, rec["tax_rate"], rec["site_rate"], rec["dpp"], rec["who"], c.rate, c.siteRate, c.dpp, c.who)
		}
	}
}

func TestTaxGlobalSiteRate(t *testing.T) {
	e := taxEngine(t, "tax: {ppn_rate: 11, pkp: true}\n")
	if got := quoteDefaults(e, "pt_pkp")["tax_rate"]; got != int64(11) {
		t.Errorf("tax_rate with tax.ppn_rate 11 = %v (%T)", got, got)
	}
	if got := quoteDefaults(e, "pt_kecil"); got["tax_rate"] != int64(0) || got["site_rate"] != int64(11) {
		t.Errorf("non-PKP org: tax_rate %v site_rate %v", got["tax_rate"], got["site_rate"])
	}
}

func TestTaxGlobalPKPDefaultsFalse(t *testing.T) {
	e := taxEngine(t, "")
	if got := quoteDefaults(e, "pt_pkp")["tax_rate"]; got != int64(0) {
		t.Errorf("no tax.pkp anywhere: tax_rate = %v, want 0", got)
	}
}

func TestGlobalVar(t *testing.T) {
	e := taxEngine(t, "")
	a := actorIn("pt_kecil")
	for _, n := range []string{"user", "org", "tax"} {
		if _, ok := GlobalVar(e.Reg(), a, n); !ok {
			t.Errorf("GlobalVar(%s) not found", n)
		}
	}
	if _, ok := GlobalVar(e.Reg(), a, "nope"); ok {
		t.Error("GlobalVar(nope) found")
	}
}

func TestOrgGlobalInRoleScope(t *testing.T) {
	e := taxEngine(t, "")
	a := Actor{Org: "pt_kecil", ID: "a@example.test", Roles: []string{"org_staff"}}
	s := e.RowScope(a)
	if s == nil || !s.Values["KCL-01"] || len(s.Values) != 1 {
		t.Fatalf("RowScope from [org.code + '-01'] = %+v", s)
	}
}

func TestOrgTaxGlobalsInFlowStep(t *testing.T) {
	e := taxEngine(t, "tax: {pkp: true}\n")
	for org, want := range map[string]int64{"pt_pkp": 12, "pt_kecil": 0} {
		r := &flowRun{e: e, a: actorIn(org), vars: map[string]any{}}
		got, err := r.setValues(map[string]any{"set": map[string]any{"rate": "tax.pkp ? tax.ppn_rate : 0", "code": "org.code"}}, "0")
		if err != nil {
			t.Fatal(err)
		}
		if got["rate"] != want || got["code"] != e.Reg().Orgs[org].Code {
			t.Errorf("%s: flow set = %v, want rate %d", org, got, want)
		}
	}
}

// A widget whose if: holds goes on to the permission check; the test actor has no role, so a shown widget
// comes back as permission_denied (no query runs), a hidden one as hidden.
func TestTaxGlobalInDashboardIf(t *testing.T) {
	e := taxEngine(t, "tax: {pkp: true}\n")
	for _, c := range []struct {
		org, cond string
		shown     bool
	}{
		{"pt_kecil", "tax.pkp", false}, // non-PKP org (tax: {pkp: false})
		{"pt_pkp", "tax.pkp && org.code == 'PKP'", true},
		{"pt_pkp", "org.code != 'PKP'", false},
	} {
		dc := &dashCtx{e: e, ctx: context.Background(), a: actorIn(c.org)}
		out, err := dc.widget(&meta.Widget{Model: "quote", If: c.cond}, 30)
		if err != nil {
			t.Fatal(err)
		}
		if shown := out["hidden"] != true; shown != c.shown {
			t.Errorf("%s: widget if %q = %v; want shown %v", c.org, c.cond, out, c.shown)
		}
	}
}
