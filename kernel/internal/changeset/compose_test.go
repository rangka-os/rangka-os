package changeset

import (
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/schema"
)

// Contract 0032 §3: a config.rk change is judged by the difference in installed units, not by the key it touches.
func TestAssessConfigModules(t *testing.T) {
	reg, rep := meta.ValidateOpts(meta.Options{AppsDir: "../../testdata/config-modules/apps", SiteDir: "../../testdata/config-modules/site"}, nil, schema.Embedded())
	if len(rep.All()) > 0 {
		t.Fatalf("testdata: %+v", rep.All()[0])
	}
	base := map[string]any{"plan": "sewa", "apps": []any{}, "modules": map[string]any{"payments": "^1.0", "invoicing": "^1.2"}}
	with := func(mods map[string]any, extra ...string) map[string]any {
		out := map[string]any{"plan": "sewa", "apps": []any{}, "modules": mods}
		for i := 0; i+1 < len(extra); i += 2 {
			out[extra[i]] = extra[i+1]
		}
		return out
	}
	for _, c := range []struct {
		name, risk, reason string
		after             map[string]any
	}{
		{"adds a module and its requires", "medium", "installs stock", with(map[string]any{"payments": "^1.0", "invoicing": "^1.2", "stock": "^1.0"})},
		{"adds an app", "medium", "installs crm, pipeline", map[string]any{"plan": "sewa", "apps": []any{"crm"}, "modules": base["modules"]}},
		{"changes a range", "medium", "changes the range of module invoicing", with(map[string]any{"payments": "^1.0", "invoicing": "^1.0"})},
		{"removes a module", "medium", "removes payments", with(map[string]any{"invoicing": "^1.2"})},
		{"names a required module and drops it again: no difference", "low", "stay the same", with(map[string]any{"payments": "^1.0"})},
		{"theme only", "low", "changes config.theme", with(base["modules"].(map[string]any), "theme", "b")},
	} {
		ops := []Op{{Op: "replace", Path: "config.rk"}}
		before := func(string) (map[string]any, bool) { return base, true }
		after := func(string) (map[string]any, bool) { return c.after, true }
		a := AssessWith(ops, before, after, Env{Installed: reg.InstalledFor})
		if a.Risk != c.risk || !strings.Contains(strings.Join(a.Reasons, "; "), c.reason) {
			t.Errorf("%s: risk %s %v, want %s with %q", c.name, a.Risk, a.Reasons, c.risk, c.reason)
		}
	}
	// payments leaves config.modules; invoicing (still named) keeps contacts, nothing else changes
	a := AssessWith([]Op{{Op: "replace", Path: "config.rk"}},
		func(string) (map[string]any, bool) { return with(map[string]any{"invoicing": "^1.2", "contacts": "^1.0"}), true },
		func(string) (map[string]any, bool) { return with(map[string]any{"invoicing": "^1.2"}), true }, Env{Installed: reg.InstalledFor})
	if a.Risk != "low" || !strings.Contains(strings.Join(a.Reasons, "; "), "contacts leaves config.modules but stays installed") {
		t.Errorf("kept module: %s %v", a.Risk, a.Reasons)
	}
}
