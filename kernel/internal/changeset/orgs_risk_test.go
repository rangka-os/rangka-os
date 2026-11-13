package changeset

import (
	"reflect"
	"strings"
	"testing"
)

// Audit C3-1 F1: the tax an org uses is orgs.<id>.config.tax merged over the site's (meta.OrgConfig), so a
// change under an org is rated like the same key of the site, and adding or removing an org (a legal entity)
// is high. Contract 0045 §2b.
func TestAssessOrgsConfig(t *testing.T) {
	org := func(cfg map[string]any) map[string]any {
		return map[string]any{"name": "Kedai Kopi", "code": "KKL", "config": cfg}
	}
	base := map[string]any{
		"apps":  []any{"core"},
		"tax":   map[string]any{"pkp": false},
		"theme": "a",
		"orgs": map[string]any{
			"kedai": org(map[string]any{"tax": map[string]any{"pkp": false}, "theme": "a"}),
			"kopi":  map[string]any{"name": "Kopi Dua"},
		},
	}
	withOrgs := func(orgs map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range base {
			out[k] = v
		}
		out["orgs"] = orgs
		return out
	}
	kopi := base["orgs"].(map[string]any)["kopi"]
	cases := []struct {
		name string
		cfg  map[string]any
		risk string
		why  string
	}{
		{"org tax pkp", withOrgs(map[string]any{"kedai": org(map[string]any{"tax": map[string]any{"pkp": true}, "theme": "a"}), "kopi": kopi}), "high", "orgs.kedai.config.tax"},
		{"org adds ppn rate", withOrgs(map[string]any{"kedai": org(map[string]any{"tax": map[string]any{"pkp": false, "ppn_rate": 11}, "theme": "a"}), "kopi": kopi}), "high", "orgs.kedai.config.tax"},
		{"org without config gets tax", withOrgs(map[string]any{"kedai": base["orgs"].(map[string]any)["kedai"], "kopi": map[string]any{"name": "Kopi Dua", "config": map[string]any{"tax": map[string]any{"pkp": true}}}}), "high", "orgs.kopi.config.tax"},
		{"org ai", withOrgs(map[string]any{"kedai": org(map[string]any{"tax": map[string]any{"pkp": false}, "theme": "a", "ai": map[string]any{"approvers": []any{"kasir"}}}), "kopi": kopi}), "high", "orgs.kedai.config.ai"},
		{"org connector_auth", withOrgs(map[string]any{"kedai": org(map[string]any{"tax": map[string]any{"pkp": false}, "theme": "a", "connector_auth": []any{"kasir"}}), "kopi": kopi}), "high", "orgs.kedai.config.connector_auth"},
		{"add an org", withOrgs(map[string]any{"kedai": base["orgs"].(map[string]any)["kedai"], "kopi": kopi, "baru": map[string]any{"name": "Baru"}}), "high", "adds org baru"},
		{"remove an org", withOrgs(map[string]any{"kedai": base["orgs"].(map[string]any)["kedai"]}), "high", "removes org kopi"},
		{"remove all orgs", func() map[string]any { m := withOrgs(nil); delete(m, "orgs"); return m }(), "high", "removes org"},
		{"org config replaced by a scalar", withOrgs(map[string]any{"kedai": map[string]any{"name": "Kedai Kopi", "code": "KKL", "config": "x"}, "kopi": kopi}), "high", "orgs.kedai.config"},
		{"org theme", withOrgs(map[string]any{"kedai": org(map[string]any{"tax": map[string]any{"pkp": false}, "theme": "b"}), "kopi": kopi}), "low", "orgs.kedai.config.theme"},
		{"org name", withOrgs(map[string]any{"kedai": map[string]any{"name": "Kedai Kopi Baru", "code": "KKL", "config": map[string]any{"tax": map[string]any{"pkp": false}, "theme": "a"}}, "kopi": kopi}), "medium", "orgs.kedai.name"},
		{"org numbering", withOrgs(map[string]any{"kedai": org(map[string]any{"tax": map[string]any{"pkp": false}, "theme": "a", "numbering": map[string]any{"sales_invoice": "KK-.####"}}), "kopi": kopi}), "medium", "orgs.kedai.config.numbering"},
	}
	before := func(p string) (map[string]any, bool) { return base, p == "config.rk" }
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			after := func(p string) (map[string]any, bool) { return c.cfg, p == "config.rk" }
			a := Assess([]Op{{Op: "replace", Path: "config.rk"}}, before, after, nil)
			if a.Risk != c.risk || !strings.Contains(strings.Join(a.Reasons, "; "), c.why) {
				t.Fatalf("risk %s %v, want %s with %q", a.Risk, a.Reasons, c.risk, c.why)
			}
		})
	}
}

// Audit C3-1 note: an org's config may not let the assistant change what it may not change for the site.
func TestAssistantForbiddenOrgs(t *testing.T) {
	base := map[string]any{"orgs": map[string]any{"kedai": map[string]any{"name": "Kedai", "config": map[string]any{"theme": "a"}}}}
	before := func(p string) (map[string]any, bool) { return base, p == "config.rk" }
	for _, k := range []string{"ai", "sso", "plan", "connector_auth"} {
		cfg := map[string]any{"orgs": map[string]any{"kedai": map[string]any{"name": "Kedai", "config": map[string]any{"theme": "a", k: map[string]any{"x": 1}}}}}
		after := func(p string) (map[string]any, bool) { return cfg, p == "config.rk" }
		if got := AssistantForbidden([]Op{{Op: "patch", Path: "config.rk"}}, before, after); !reflect.DeepEqual(got, []string{"config.rk"}) {
			t.Errorf("orgs.kedai.config.%s: %v, want config.rk forbidden", k, got)
		}
	}
	ok := map[string]any{"orgs": map[string]any{"kedai": map[string]any{"name": "Kedai", "config": map[string]any{"theme": "b"}}}}
	after := func(p string) (map[string]any, bool) { return ok, p == "config.rk" }
	if got := AssistantForbidden([]Op{{Op: "patch", Path: "config.rk"}}, before, after); got != nil {
		t.Errorf("org theme: %v, want allowed", got)
	}
}
