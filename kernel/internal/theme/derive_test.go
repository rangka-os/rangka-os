package theme

import (
	"fmt"
	"math/rand"
	"testing"
)

func floors(t *testing.T, name string, light, dark map[string]string) {
	t.Helper()
	for _, p := range Pairs {
		for mode, vals := range map[string]map[string]string{"light": light, "dark": dark} {
			a, b := vals[p.FG], vals[p.BG]
			if IsHex(a) && IsHex(b) && Contrast(a, b) < p.Floor {
				t.Errorf("%s %s: %s %s on %s %s is %.2f, needs %g", name, mode, p.FG, a, p.BG, b, Contrast(a, b), p.Floor)
			}
		}
	}
}

// The six bundled themes (contract 0065 §3) pass every floor in light and dark.
func TestPresetsPass(t *testing.T) {
	for _, pr := range []struct{ name, accent, button, neutral string }{
		{"standard", "", "", ""}, {"laut", "#0c66e4", "accent", "slate"}, {"daun", "#1f845a", "accent", "gray"},
		{"senja", "#e56910", "accent", "stone"}, {"anggur", "#6e5dc6", "ink", "gray"}, {"arang", "#44403c", "ink", "gray"},
	} {
		spec := map[string]any{"accent": pr.accent, "button": pr.button, "neutral": pr.neutral}
		l, d, _ := Apply(spec, nil)
		floors(t, pr.name, l, d)
	}
}

// Any accent, button and neutral yields a readable theme (derived values always pass, §2).
func TestDeriveAnyColourPasses(t *testing.T) {
	r := rand.New(rand.NewSource(65))
	for i := 0; i < 300; i++ {
		acc := fmt.Sprintf("#%06x", r.Intn(1<<24))
		for _, b := range []string{"ink", "accent"} {
			for _, n := range []string{"stone", "slate", "gray"} {
				l, d, _ := Apply(map[string]any{"accent": acc, "button": b, "neutral": n}, nil)
				floors(t, acc+"/"+b+"/"+n, l, d)
			}
		}
	}
}

func TestApplyOrderAndOverrides(t *testing.T) {
	l, d, over := Apply(map[string]any{"accent": "#7a4520", "button": "accent", "neutral": "slate",
		"color": map[string]any{"chart-1": "#0055cc"}}, []string{"chart-1"})
	if l["chart-1"] != "#0055cc" || over["chart-1"].From != "" || !over["chart-1"].DarkDerived {
		t.Errorf("color must win over accent: %v %v", l["chart-1"], over["chart-1"])
	}
	if over["surface"].From != "neutral" || over["link"].From != "accent" || over["primary"].From != "accent" {
		t.Errorf("from: %v %v %v", over["surface"], over["link"], over["primary"])
	}
	if l["primary"] == "#172b4d" || d["primary-ink"] != "#1d2125" {
		t.Errorf("button accent must replace the family's ink button: %s %s", l["primary"], d["primary-ink"])
	}
	if _, _, o := Apply(map[string]any{}, nil); len(o) != 0 {
		t.Errorf("standard has no overrides: %v", o)
	}
}

func TestLockedAndFonts(t *testing.T) {
	for _, k := range []string{"ok", "ok-tint", "bad-solid", "raka", "raka-soft", "on-bad", "spark"} {
		if !Locked(k) {
			t.Errorf("%s must be locked", k)
		}
	}
	for _, k := range []string{"primary", "brand", "selected", "chart-1", "okay"} {
		if Locked(k) {
			t.Errorf("%s must not be locked", k)
		}
	}
	if !KnownFont("inter") || KnownFont("Comic Sans") {
		t.Error("fonts")
	}
}
