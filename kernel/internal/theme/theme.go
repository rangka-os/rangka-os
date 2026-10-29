// Package theme computes theme colours: base design tokens (web/design-system/tokens.json, embedded),
// theme overrides, the dark derivation of CONTRACTS v0.1 decision 12 and WCAG contrast. The arithmetic
// mirrors rangka_validate.py (colorsys-style HLS) so both report the same values.
package theme

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
)

//go:embed tokens.json
var tokensJSON []byte

// Pair is a foreground/background token pair with its contrast floor.
type Pair struct {
	FG, BG string
	Floor  float64
}

// Pairs are checked (text 4.5:1, controls 3:1), in this order.
var Pairs = func() []Pair {
	p := []Pair{{"ink", "surface", 4.5}, {"ink-2", "surface", 4.5}, {"muted", "surface", 4.5}, {"link", "surface", 4.5},
		{"selected-ink", "selected", 4.5}, {"primary-ink", "primary", 4.5}}
	for _, s := range []string{"ok", "warn", "bad", "info", "discovery"} {
		p = append(p, Pair{s, s + "-tint", 4.5})
	}
	p = append(p, Pair{"primary", "surface", 3}, Pair{"brand", "surface", 3}, Pair{"focus", "surface", 3})
	for i := 1; i <= 6; i++ {
		p = append(p, Pair{fmt.Sprintf("chart-%d", i), "surface", 3})
	}
	return p
}()

var (
	baseOnce            sync.Once
	baseLight, baseDark map[string]string
)

// SetBase replaces the base tokens (from a tokens.json file); nil data restores the embedded copy.
func SetBase(data []byte) error {
	if data == nil {
		data = tokensJSON
	}
	l, d, err := parse(data)
	if err != nil {
		return err
	}
	baseOnce.Do(func() {})
	baseLight, baseDark = l, d
	return nil
}

func parse(data []byte) (map[string]string, map[string]string, error) {
	var tok struct {
		Color struct {
			Tokens []struct {
				Name  string          `json:"name"`
				Value json.RawMessage `json:"value"`
			} `json:"tokens"`
		} `json:"color"`
	}
	if err := json.Unmarshal(data, &tok); err != nil {
		return nil, nil, err
	}
	light, dark := map[string]string{}, map[string]string{}
	for _, t := range tok.Color.Tokens {
		var both map[string]string
		if json.Unmarshal(t.Value, &both) == nil {
			light[t.Name], dark[t.Name] = both["light"], both["dark"]
			continue
		}
		var one string
		if json.Unmarshal(t.Value, &one) == nil {
			light[t.Name], dark[t.Name] = one, one
		}
	}
	return light, dark, nil
}

// Base returns copies of the base light and dark tokens.
func Base() (map[string]string, map[string]string) {
	baseOnce.Do(func() { baseLight, baseDark, _ = parse(tokensJSON) })
	l, d := map[string]string{}, map[string]string{}
	for k, v := range baseLight {
		l[k] = v
	}
	for k, v := range baseDark {
		d[k] = v
	}
	return l, d
}

// ---------- colour arithmetic (Python colorsys semantics)

const (
	oneThird = 1.0 / 3.0
	oneSixth = 1.0 / 6.0
	twoThird = 2.0 / 3.0
)

// pymod is Python's float % (result has the sign of b).
func pymod(a, b float64) float64 {
	m := math.Mod(a, b)
	if m != 0 && (b < 0) != (m < 0) {
		m += b
	}
	return m
}

func rgbToHLS(r, g, b float64) (float64, float64, float64) {
	maxc, minc := math.Max(r, math.Max(g, b)), math.Min(r, math.Min(g, b))
	sumc, rangec := maxc+minc, maxc-minc
	l := sumc / 2.0
	if minc == maxc {
		return 0, l, 0
	}
	var s float64
	if l <= 0.5 {
		s = rangec / sumc
	} else {
		s = rangec / (2.0 - maxc - minc)
	}
	rc, gc, bc := (maxc-r)/rangec, (maxc-g)/rangec, (maxc-b)/rangec
	var h float64
	switch {
	case r == maxc:
		h = bc - gc
	case g == maxc:
		h = 2.0 + rc - bc
	default:
		h = 4.0 + gc - rc
	}
	return pymod(h/6.0, 1.0), l, s
}

func v(m1, m2, hue float64) float64 {
	hue = pymod(hue, 1.0)
	switch {
	case hue < oneSixth:
		return m1 + (m2-m1)*hue*6.0
	case hue < 0.5:
		return m2
	case hue < twoThird:
		return m1 + (m2-m1)*(twoThird-hue)*6.0
	}
	return m1
}

func hlsToRGB(h, l, s float64) (float64, float64, float64) {
	if s == 0 {
		return l, l, l
	}
	var m2 float64
	if l <= 0.5 {
		m2 = l * (1.0 + s)
	} else {
		m2 = l + s - (l * s)
	}
	m1 := 2.0*l - m2
	return v(m1, m2, h+oneThird), v(m1, m2, h), v(m1, m2, h-oneThird)
}

// IsHex reports #rrggbb.
func IsHex(s string) bool {
	if len(s) != 7 || s[0] != '#' {
		return false
	}
	_, err := strconv.ParseUint(s[1:], 16, 32)
	return err == nil
}

func toRGB(h string) (float64, float64, float64) {
	p := func(i int) float64 { n, _ := strconv.ParseUint(h[i:i+2], 16, 8); return float64(n) / 255.0 }
	return p(1), p(3), p(5)
}

func toHex(r, g, b float64) string {
	c := func(x float64) int { return int(math.Min(math.Max(x, 0), 1)*255.0 + 0.5) }
	return fmt.Sprintf("#%02x%02x%02x", c(r), c(g), c(b))
}

// Luminance is the WCAG relative luminance.
func Luminance(h string) float64 {
	r, g, b := toRGB(h)
	out := 0.0
	for i, c := range []float64{r, g, b} {
		w := []float64{0.2126, 0.7152, 0.0722}[i]
		if c <= 0.03928 {
			out += w * (c / 12.92)
		} else {
			out += w * math.Pow((c+0.055)/1.055, 2.4)
		}
	}
	return out
}

// Contrast is the WCAG contrast ratio.
func Contrast(a, b string) float64 {
	la, lb := Luminance(a), Luminance(b)
	return (math.Max(la, lb) + 0.05) / (math.Min(la, lb) + 0.05)
}

func isBackground(name string) bool { return name == "selected" || strings.HasSuffix(name, "-tint") }

// DeriveDark computes the dark value of a colour given only for light (decision 12): keep hue; backgrounds
// and tints get lightness 100 − L·0.6, everything else +15; then contrast floors against the dark partner
// tokens are enforced by moving lightness in 1 % steps.
func DeriveDark(name, light string, dark map[string]string) string {
	h, l, s := rgbToHLS(toRGB(light))
	bg := isBackground(name)
	var base float64
	if bg {
		base = (100.0 - l*100.0*0.6) / 100.0
	} else {
		base = math.Min(l+0.15, 1.0)
	}
	out := toHex(hlsToRGB(h, base, s))
	for _, p := range Pairs {
		if name == p.FG && IsHex(dark[p.BG]) {
			for k := 0; Contrast(out, dark[p.BG]) < p.Floor && base+float64(k)/100.0 <= 1.0; k++ {
				out = toHex(hlsToRGB(h, math.Min(base+float64(k)/100.0, 1.0), s))
			}
		} else if name == p.BG && IsHex(dark[p.FG]) {
			for k := 0; Contrast(out, dark[p.FG]) < p.Floor && base-float64(k)/100.0 >= 0.0; k++ {
				out = toHex(hlsToRGB(h, math.Max(base-float64(k)/100.0, 0.0), s))
			}
		}
	}
	return out
}

// Override is one theme colour after derivation.
type Override struct {
	Light       string `json:"light"`
	Dark        string `json:"dark"`
	DarkDerived bool   `json:"dark_derived"`
}

// Colours applies a theme's color map (in key order) to the base tokens.
func Colours(color map[string]any, keys []string) (light, dark map[string]string, over map[string]Override) {
	light, dark = Base()
	over = map[string]Override{}
	for _, k := range keys {
		switch x := color[k].(type) {
		case string:
			light[k] = x
		case map[string]any:
			if lv, _ := x["light"].(string); lv != "" {
				light[k] = lv
				if dv, _ := x["dark"].(string); dv != "" {
					dark[k] = dv
				}
			}
		}
	}
	for _, k := range keys {
		var lv, dv string
		switch x := color[k].(type) {
		case string:
			lv = x
		case map[string]any:
			lv, _ = x["light"].(string)
			dv, _ = x["dark"].(string)
		}
		if !IsHex(lv) {
			continue
		}
		derived := dv == ""
		if derived {
			dv = DeriveDark(k, lv, dark)
			dark[k] = dv
		}
		over[k] = Override{lv, dv, derived}
	}
	return
}

// Problem is a failed contrast floor.
type Problem struct {
	Token string
	Msg   string
}

// Check returns contrast problems for pairs that involve an overridden colour.
func Check(light, dark map[string]string, over map[string]Override) []Problem {
	var out []Problem
	for _, p := range Pairs {
		_, fo := over[p.FG]
		_, bo := over[p.BG]
		if !fo && !bo {
			continue
		}
		tok := p.BG
		if fo {
			tok = p.FG
		}
		for _, mv := range []struct {
			mode string
			vals map[string]string
		}{{"light", light}, {"dark", dark}} {
			a, b := mv.vals[p.FG], mv.vals[p.BG]
			if IsHex(a) && IsHex(b) {
				if r := Contrast(a, b); r < p.Floor {
					out = append(out, Problem{tok, fmt.Sprintf("%s %s on %s %s is %.2f:1 in %s; needs %g:1", p.FG, a, p.BG, b, r, mv.mode, p.Floor)})
				}
			}
		}
	}
	return out
}
