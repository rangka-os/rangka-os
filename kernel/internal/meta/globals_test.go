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
	if got, want := (&Registry{}).TaxVar("x"), map[string]any{"ppn_rate": int64(12), "dpp_num": int64(11), "dpp_den": int64(12), "pkp": false}; !reflect.DeepEqual(got, want) {
		t.Errorf("no site: %v", got)
	}
	r := regWith(map[string]any{
		"tax": map[string]any{"ppn_rate": int64(11), "dpp_ratio": "10/11", "pkp": true},
		"orgs": map[string]any{
			"pt_kecil": map[string]any{"name": "PT Kecil", "config": map[string]any{"tax": map[string]any{"pkp": false}}},
			"pt_bad":   map[string]any{"name": "PT Bad", "config": map[string]any{"tax": map[string]any{"dpp_ratio": "bad", "ppn_rate": 12.0}}},
		},
	})
	cases := map[string]map[string]any{
		"other": {"ppn_rate": int64(11), "dpp_num": int64(10), "dpp_den": int64(11), "pkp": true}, // the site's tax:
		// a non-PKP org overriding only pkp keeps the site's rate and ratio (deep merge)
		"pt_kecil": {"ppn_rate": int64(11), "dpp_num": int64(10), "dpp_den": int64(11), "pkp": false},
		// a malformed org dpp_ratio falls back to 11/12, not to the site's; 12.0 is a whole number
		"pt_bad": {"ppn_rate": int64(12), "dpp_num": int64(11), "dpp_den": int64(12), "pkp": true},
	}
	for org, want := range cases {
		if got := r.TaxVar(org); !reflect.DeepEqual(got, want) {
			t.Errorf("TaxVar(%s) = %v, want %v", org, got, want)
		}
	}
	if got := regWith(map[string]any{"tax": map[string]any{"ppn_rate": 11.5}}).TaxVar("x")["ppn_rate"]; got != 11.5 {
		t.Errorf("fractional ppn_rate = %v (%T), want float64 11.5", got, got)
	}
}

func TestOrgVar(t *testing.T) {
	if got, want := (&Registry{}).OrgVar("x"), map[string]any{"id": "x", "code": "", "name": "", "legal_name": "", "npwp": ""}; !reflect.DeepEqual(got, want) {
		t.Errorf("no site: %v", got)
	}
	r := regWith(map[string]any{
		"org": map[string]any{"legal_name": "PT Situs", "npwp": "01", "pkp": true},
		"orgs": map[string]any{
			"pt_a": map[string]any{"name": "PT A", "code": "A", "config": map[string]any{"org": map[string]any{"legal_name": "PT A Tbk"}}},
			"pt_b": map[string]any{"name": "PT B"},
		},
	})
	cases := map[string]map[string]any{
		// the org's own org: wins wholesale; npwp is not inherited from the site
		"pt_a": {"id": "pt_a", "code": "A", "name": "PT A", "legal_name": "PT A Tbk", "npwp": ""},
		// listed without an org: of its own: OrgIdentity gives its orgs: name as legal_name, never the site's
		"pt_b": {"id": "pt_b", "code": "", "name": "PT B", "legal_name": "PT B", "npwp": ""},
		// unlisted: the site's org:; no pkp member (that is tax.pkp)
		"lain": {"id": "lain", "code": "", "name": "", "legal_name": "PT Situs", "npwp": "01"},
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

func TestTaxSettings(t *testing.T) {
	r := regWith(map[string]any{
		"tax": map[string]any{"ppn_rate": 11.0, "dpp_ratio": "11:12", "efaktur": "x"},
		"orgs": map[string]any{
			"pt_a": map[string]any{"name": "PT A", "config": map[string]any{"tax": map[string]any{"pkp": true, "dpp_ratio": "10/11"}}},
		},
	})
	// malformed dpp_ratio → the default; whole float → int; pkp always a bool; other keys copied
	if got, want := r.TaxSettings("other"), map[string]any{"ppn_rate": int64(11), "dpp_ratio": "11/12", "pkp": false, "efaktur": "x"}; !reflect.DeepEqual(got, want) {
		t.Errorf("site: %v", got)
	}
	if got, want := r.TaxSettings("pt_a"), map[string]any{"ppn_rate": int64(11), "dpp_ratio": "10/11", "pkp": true, "efaktur": "x"}; !reflect.DeepEqual(got, want) {
		t.Errorf("pt_a (deep merge): %v", got)
	}
	if id := r.OrgIdentity("pt_a"); id["pkp"] != true {
		t.Errorf("OrgIdentity(pt_a).pkp = %v, want TaxSettings' true", id["pkp"])
	}
}
