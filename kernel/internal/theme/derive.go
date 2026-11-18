package theme

import (
	"math"
	"sort"
	"strings"
)

// Contract 0065: a theme may name a neutral family, one accent colour and the primary button style; the kernel
// derives the tokens (light and dark) so every floor in Pairs holds. rangka_validate.py mirrors this file.

// Neutrals are the neutral families of contract 0065 §1. stone is the base tokens (tokens.json) and is not listed.
var Neutrals = map[string][2]map[string]string{
	"slate": {
		{"app": "#ffffff", "surface": "#ffffff", "soft": "#f4f5f7", "ink": "#172b4d", "ink-2": "#44546f", "muted": "#5e6c84",
			"faint": "#8993a4", "line": "#ebecf0", "line-strong": "#dcdfe4", "border": "#8590a2",
			"hover": "rgba(23, 43, 77, .05)", "neutral": "rgba(23, 43, 77, .06)", "neutral-hover": "rgba(23, 43, 77, .13)",
			"disabled-bg": "rgba(23, 43, 77, .03)", "primary": "#172b4d", "primary-hover": "#2c3e5d", "primary-ink": "#ffffff"},
		{"app": "#1d2125", "surface": "#22272b", "soft": "#161a1d", "ink": "#dee4ea", "ink-2": "#b6c2cf", "muted": "#9fadbc",
			"faint": "#738496", "line": "#2c333a", "line-strong": "#38414a", "border": "#738496",
			"hover": "rgba(222, 228, 234, .07)", "neutral": "rgba(222, 228, 234, .08)", "neutral-hover": "rgba(222, 228, 234, .15)",
			"disabled-bg": "rgba(222, 228, 234, .04)", "primary": "#dee4ea", "primary-hover": "#ffffff", "primary-ink": "#1d2125"},
	},
	"gray": {
		{"app": "#ffffff", "surface": "#ffffff", "soft": "#f5f5f5", "ink": "#171717", "ink-2": "#404040", "muted": "#636363",
			"faint": "#a3a3a3", "line": "#ececec", "line-strong": "#dcdcdc", "border": "#8a8a8a",
			"hover": "rgba(23, 23, 23, .05)", "neutral": "rgba(23, 23, 23, .06)", "neutral-hover": "rgba(23, 23, 23, .13)",
			"disabled-bg": "rgba(23, 23, 23, .03)", "primary": "#171717", "primary-hover": "#333333", "primary-ink": "#ffffff"},
		{"app": "#171717", "surface": "#1f1f1f", "soft": "#111111", "ink": "#ededed", "ink-2": "#d4d4d4", "muted": "#a3a3a3",
			"faint": "#737373", "line": "#2a2a2a", "line-strong": "#383838", "border": "#737373",
			"hover": "rgba(237, 237, 237, .07)", "neutral": "rgba(237, 237, 237, .08)", "neutral-hover": "rgba(237, 237, 237, .15)",
			"disabled-bg": "rgba(237, 237, 237, .04)", "primary": "#ededed", "primary-hover": "#ffffff", "primary-ink": "#171717"},
	},
}

// buttonTokens are the tokens a family sets only when primary buttons stay ink.
var buttonTokens = map[string]bool{"primary": true, "primary-hover": true, "primary-ink": true}

// Locked are tokens a theme may not set (contract 0065 §1): status colours and Raka.
func Locked(name string) bool {
	for _, p := range []string{"ok", "warn", "bad", "info", "discovery", "raka"} {
		if name == p || strings.HasPrefix(name, p+"-") {
			return true
		}
	}
	switch name {
	case "on-bad", "on-warn", "spark", "helmet", "scrim", "mascot-ink":
		return true
	}
	return false
}

// Renamed maps old theme template names to the new ones (contract 0065 §3).
var Renamed = map[string]string{"clinic": "daun", "retail": "senja"}

// Fonts are the bundled font names for font.sans / font.display (contract 0065 §5).
var Fonts = []string{"system", "inter", "plus-jakarta-sans", "nunito-sans", "ibm-plex-sans", "source-serif-4"}

// KnownFont reports whether name is a bundled font.
func KnownFont(name string) bool {
	for _, f := range Fonts {
		if f == name {
			return true
		}
	}
	return false
}

func hls(h string) (float64, float64, float64) { return rgbToHLS(toRGB(h)) }
func mk(h, l, s float64) string                { return toHex(hlsToRGB(h, math.Min(math.Max(l, 0), 1), s)) }

// towards moves the lightness of c in 1 % steps (dir −1 darker, +1 lighter) until every bg reaches floor against it.
func towards(c string, dir float64, floor float64, bgs ...string) string {
	h, l, s := hls(c)
	ok := func(x string) bool {
		for _, b := range bgs {
			if IsHex(b) && Contrast(x, b) < floor {
				return false
			}
		}
		return true
	}
	out := mk(h, l, s)
	for k := 1; !ok(out) && k <= 100; k++ {
		out = mk(h, l+dir*float64(k)/100.0, s)
	}
	return out
}

// Derive returns the light and dark tokens one accent sets, against the neutrals in light and dark (contract 0065 §2).
// button "accent" also sets primary, primary-hover and primary-ink.
func Derive(accent, button string, light, dark map[string]string) (map[string]string, map[string]string) {
	h, l, s := hls(accent)
	L, D := map[string]string{}, map[string]string{}
	L["brand"] = towards(accent, -1, 3, light["surface"])
	L["selected"] = mk(h, 0.94, math.Min(s, 0.6))
	L["row-selected"] = mk(h, 0.975, math.Min(s, 0.5))
	L["selected-ink"] = towards(accent, -1, 4.5, L["selected"])
	L["link"] = towards(accent, -1, 4.5, light["surface"])
	L["focus"] = towards(accent, -1, 3, light["surface"])
	L["chart-1"] = towards(accent, -1, 3, light["surface"])

	D["brand"] = towards(mk(h, l+0.15, s), 1, 3, dark["surface"])
	D["selected"] = mk(h, 0.18, math.Min(s, 0.45))
	D["row-selected"] = mk(h, 0.14, math.Min(s, 0.35))
	D["selected-ink"] = towards(mk(h, 0.6, s), 1, 4.5, D["selected"])
	D["link"] = towards(mk(h, 0.6, s), 1, 4.5, dark["surface"])
	D["focus"] = towards(mk(h, 0.55, s), 1, 3, dark["surface"])
	D["chart-1"] = towards(mk(h, 0.55, s), 1, 3, dark["surface"])

	if button == "accent" {
		L["primary-ink"] = "#ffffff"
		L["primary"] = towards(accent, -1, 4.5, L["primary-ink"])
		ph, pl, ps := hls(L["primary"])
		L["primary-hover"] = mk(ph, pl-0.08, ps)
		D["primary-ink"] = dark["app"]
		D["primary"] = towards(mk(h, 0.6, s), 1, 4.5, D["primary-ink"])
		if Contrast(D["primary"], dark["surface"]) < 3 {
			D["primary"] = towards(D["primary"], 1, 3, dark["surface"])
		}
		dh, dl, ds := hls(D["primary"])
		D["primary-hover"] = mk(dh, dl+0.08, ds)
	}
	return L, D
}

// Apply resolves a theme (already merged with its template): base tokens, then the neutral family, then the accent
// and button, then color overrides in key order (contract 0065 §1). Every token that differs from the base is an
// override, with From naming the step that set it.
func Apply(spec map[string]any, keys []string) (light, dark map[string]string, over map[string]Override) {
	light, dark = Base()
	over = map[string]Override{}
	set := func(k, lv, dv, from string) {
		light[k], dark[k] = lv, dv
		over[k] = Override{Light: lv, Dark: dv, From: from}
	}
	button, _ := spec["button"].(string)
	if fam, ok := Neutrals[str(spec["neutral"])]; ok {
		for _, k := range sortedNames(fam[0]) {
			if buttonTokens[k] && button == "accent" {
				continue
			}
			set(k, fam[0][k], fam[1][k], "neutral")
		}
	}
	if a, _ := spec["accent"].(string); IsHex(a) {
		L, D := Derive(strings.ToLower(a), button, light, dark)
		for _, k := range sortedNames(L) {
			set(k, L[k], D[k], "accent")
		}
	}
	colours(light, dark, over, asMapAny(spec["color"]), keys)
	return
}

// Custom reports whether a theme document is one of the business's own (contract 0065 §7): an accent, colours,
// or a bundled font other than system. neutral, button, mode, radius, density and logo alone are not.
func Custom(data map[string]any) bool {
	if a, _ := data["accent"].(string); a != "" {
		return true
	}
	if len(asMapAny(data["color"])) > 0 {
		return true
	}
	for _, k := range []string{"sans", "display"} {
		if f, _ := asMapAny(data["font"])[k].(string); f != "" && f != "system" {
			return true
		}
	}
	return false
}

func str(v any) string { s, _ := v.(string); return s }

func asMapAny(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func sortedNames(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
