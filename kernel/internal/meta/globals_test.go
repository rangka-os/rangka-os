package meta

import (
	"reflect"
	"testing"
)

func TestParseDPPRatio(t *testing.T) {
	good := map[string][2]int64{"11/12": {11, 12}, " 11 / 12 ": {11, 12}, "1/1": {1, 1}, "10/11": {10, 11}}
	for s, want := range good {
		n, d, ok := ParseDPPRatio(s)
		if !ok || n != want[0] || d != want[1] {
			t.Errorf("ParseDPPRatio(%q) = %d, %d, %v", s, n, d, ok)
		}
	}
	for _, s := range []string{"", "11", "11:12", "0/12", "11/0", "-1/12", "11/12/13", "1.5/2", "a/b", "1234567890/1", "/12"} {
		if _, _, ok := ParseDPPRatio(s); ok {
			t.Errorf("ParseDPPRatio(%q) ok", s)
		}
	}
}

func regWith(cfg map[string]any) *Registry {
	r := &Registry{Site: &Site{Config: cfg}}
	r.loadOrgs()
	return r
}

func TestTaxVar(t *testing.T) {
	if got, want := (&Registry{}).TaxVar("x"), map[string]any{"ppn_rate": int64(12), "dpp_num": int64(11), "dpp_den": int64(12)}; !reflect.DeepEqual(got, want) {
		t.Errorf("no site: %v", got)
	}
	r := regWith(map[string]any{
		"tax": map[string]any{"ppn_rate": int64(11), "dpp_ratio": "bad", "pkp": false},
		"orgs": map[string]any{
			"pt_a": map[string]any{"name": "PT A", "config": map[string]any{"tax": map[string]any{"ppn_rate": 12.0, "dpp_ratio": "10/11"}}},
		},
	})
	if got, want := r.TaxVar("other"), map[string]any{"ppn_rate": int64(11), "dpp_num": int64(11), "dpp_den": int64(12), "pkp": false}; !reflect.DeepEqual(got, want) {
		t.Errorf("site tax: %v", got)
	}
	// the org's tax: replaces the site's whole map (OrgConfig), so pkp is not inherited
	if got, want := r.TaxVar("pt_a"), map[string]any{"ppn_rate": int64(12), "dpp_num": int64(10), "dpp_den": int64(11)}; !reflect.DeepEqual(got, want) {
		t.Errorf("org tax: %v", got)
	}
}

func TestOrgVar(t *testing.T) {
	if got, want := (&Registry{}).OrgVar("x"), map[string]any{"id": "x", "code": "", "name": "", "legal_name": "", "npwp": "", "pkp": true}; !reflect.DeepEqual(got, want) {
		t.Errorf("no site: %v", got)
	}
	r := regWith(map[string]any{
		"tax": map[string]any{"pkp": false},
		"org": map[string]any{"legal_name": "PT Situs", "npwp": "01"},
		"orgs": map[string]any{
			"pt_a": map[string]any{"name": "PT A", "code": "A", "config": map[string]any{"org": map[string]any{"legal_name": "PT A Tbk", "pkp": true}}},
			"pt_b": map[string]any{"name": "PT B"},
		},
	})
	cases := map[string]map[string]any{
		// the org's own org: wins; npwp is not inherited from the site (the whole org: map is replaced)
		"pt_a": {"id": "pt_a", "code": "A", "name": "PT A", "legal_name": "PT A Tbk", "npwp": "", "pkp": true},
		// no org: of its own: the site's org:, pkp from the legacy tax.pkp
		"pt_b": {"id": "pt_b", "code": "", "name": "PT B", "legal_name": "PT Situs", "npwp": "01", "pkp": false},
	}
	for org, want := range cases {
		if got := r.OrgVar(org); !reflect.DeepEqual(got, want) {
			t.Errorf("OrgVar(%s) = %v, want %v", org, got, want)
		}
	}
}

func TestOrgTaxAreGlobals(t *testing.T) {
	for _, n := range []string{"user", "org", "tax"} {
		if !Globals[n] {
			t.Errorf("%s is not a global", n)
		}
	}
}
