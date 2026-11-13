package changeset

import (
	"math"
	"strings"
	"testing"
)

// Audit orgs-risk A2: a value holding NaN or Inf used to normalize to nil as a whole, so a tax change next to it
// read as "config.rk unchanged".
func TestAssessSeesChangesNextToNonFinite(t *testing.T) {
	cfg := func(pkp bool) map[string]any {
		return map[string]any{"theme": "a", "orgs": map[string]any{"kedai": map[string]any{"name": "Kedai",
			"config": map[string]any{"layout": math.Inf(1), "tax": map[string]any{"pkp": pkp}}}}}
	}
	before, after := cfg(false), cfg(true)
	a := Assess([]Op{{Op: "replace", Path: "config.rk"}}, func(p string) (map[string]any, bool) { return before, true },
		func(p string) (map[string]any, bool) { return after, true }, nil)
	if a.Risk != "high" || !strings.Contains(strings.Join(a.Reasons, "; "), "orgs.kedai.config.tax") {
		t.Fatalf("risk %s %v, want high for the org tax", a.Risk, a.Reasons)
	}
	if n := normalize(map[string]any{"x": math.NaN(), "y": 1}); n == nil {
		t.Fatal("NaN made the whole value nil")
	}
	if got := normalize(map[string]any{"y": int64(1)}).(map[string]any)["y"]; got != float64(1) {
		t.Fatalf("finite values keep the JSON shape: %#v", got)
	}
}
