package api

import (
	"context"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/plan"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
	"github.com/rangka-os/rangka-os/kernel/internal/theme"
)

// Contract 0065 §6: the theme gallery, derivation without saving, colours from a logo, and the person's own
// appearance (theme + mode).

// swatchTokens are the tokens a gallery card shows.
var swatchTokens = []string{"brand", "primary", "selected", "selected-ink", "link", "chart-1", "app", "soft", "surface", "ink", "line-strong"}

var modes = map[string]bool{"system": true, "light": true, "dark": true}

type themeCard struct {
	Name     string                       `json:"name"`
	Label    any                          `json:"label,omitempty"`
	Source   string                       `json:"source"` // bundled | site
	Accent   string                       `json:"accent,omitempty"`
	Button   string                       `json:"button,omitempty"`
	Neutral  string                       `json:"neutral,omitempty"`
	Custom   bool                         `json:"custom"`
	Swatches map[string]map[string]string `json:"swatches"`
}

func swatches(light, dark map[string]string) map[string]map[string]string {
	l, d := map[string]string{}, map[string]string{}
	for _, k := range swatchTokens {
		l[k], d[k] = light[k], dark[k]
	}
	return map[string]map[string]string{"light": l, "dark": d}
}

// themeNames lists the themes a person or the business may pick: the bundled templates (renamed ones left out),
// then the site's own; a site theme of the same name replaces the bundled one.
func (s *Server) themeCards(reg *meta.Registry) []themeCard {
	byName := map[string]themeCard{}
	var order []string
	add := func(c themeCard) {
		if _, seen := byName[c.Name]; !seen {
			order = append(order, c.Name)
		}
		byName[c.Name] = c
	}
	card := func(name, source string, data map[string]any, light, dark map[string]string) themeCard {
		spec, _ := meta.ThemeSpec(data, nil, s.TemplatesDir)
		str := func(k string) string { v, _ := spec[k].(string); return v }
		return themeCard{Name: name, Label: data["label"], Source: source, Accent: str("accent"), Button: str("button"),
			Neutral: str("neutral"), Custom: theme.Custom(data), Swatches: swatches(light, dark)}
	}
	if s.TemplatesDir != "" {
		files, _ := filepath.Glob(filepath.Join(s.TemplatesDir, "themes", "*.rk"))
		sort.Strings(files)
		// standard first, the rest in name order
		sort.SliceStable(files, func(a, b int) bool {
			return strings.HasSuffix(files[a], "/standard.rk") && !strings.HasSuffix(files[b], "/standard.rk")
		})
		for _, f := range files {
			name := strings.TrimSuffix(filepath.Base(f), ".rk")
			if theme.Renamed[name] != "" {
				continue
			}
			data := meta.LoadTemplate("themes", name, s.TemplatesDir)
			if data == nil {
				continue
			}
			spec, keys := meta.ThemeSpec(data, nil, s.TemplatesDir)
			l, d, _ := theme.Apply(spec, keys)
			add(card(name, "bundled", data, l, d))
		}
	}
	if reg != nil && reg.Site != nil {
		for _, n := range keysOfThemeDocs(reg) {
			doc := reg.Site.Themes[n]
			l, d, _ := meta.ThemeColours(doc, s.TemplatesDir)
			add(card(n, "site", meta.AsMap(doc.Data), l, d))
		}
	}
	out := make([]themeCard, 0, len(order))
	for _, n := range order {
		out = append(out, byName[n])
	}
	return out
}

func keysOfThemeDocs(reg *meta.Registry) []string {
	out := make([]string, 0, len(reg.Site.Themes))
	for k := range reg.Site.Themes {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (s *Server) businessTheme(reg *meta.Registry) string {
	if n, _ := reg.SiteConfig("theme").(string); n != "" {
		return n
	}
	return "standard"
}

func (s *Server) customAllowed(ctx context.Context, reg *meta.Registry) bool {
	cur, err := plan.Resolve(ctx, s.DB, reg)
	return err != nil || cur.Allows(plan.CustomTheme)
}

// listThemes is GET /api/v1/themes.
func (s *Server) listThemes(w http.ResponseWriter, r *http.Request, c *caller) {
	reg := s.Engine.Reg()
	writeJSON(w, 200, map[string]any{"themes": s.themeCards(reg), "current": s.businessTheme(reg), "fonts": theme.Fonts,
		"allowed": map[string]bool{"custom": s.customAllowed(r.Context(), reg)}})
}

// deriveTheme is POST /api/v1/themes/derive: {accent, button?, neutral?, color?, extends?} → tokens and contrast
// checks. Nothing is stored.
func (s *Server) deriveTheme(w http.ResponseWriter, r *http.Request, c *caller) {
	body, ok := readObject(w, r)
	if !ok {
		return
	}
	if a, has := body["accent"]; has {
		if as, _ := a.(string); !theme.IsHex(as) {
			writeError(w, http.StatusBadRequest, "bad_colour", "accent must be a colour like #7a4520")
			return
		}
	}
	if b, has := body["button"]; has && b != "ink" && b != "accent" {
		writeError(w, http.StatusBadRequest, "bad_button", "button must be ink or accent")
		return
	}
	if n, has := body["neutral"]; has && n != "stone" && n != "slate" && n != "gray" {
		writeError(w, http.StatusBadRequest, "bad_neutral", "neutral must be stone, slate or gray")
		return
	}
	for k := range meta.AsMap(body["color"]) {
		if theme.Locked(k) {
			writeError(w, http.StatusBadRequest, "locked_token", `"`+k+`" keeps its meaning in every theme and cannot be set`)
			return
		}
	}
	if _, has := body["extends"]; !has {
		body["extends"] = "rangka/standard"
	}
	spec, keys := meta.ThemeSpec(body, nil, s.TemplatesDir)
	light, dark, over := theme.Apply(spec, keys)
	checks := []map[string]any{}
	for _, p := range theme.Pairs {
		lc, dc := 0.0, 0.0
		if theme.IsHex(light[p.FG]) && theme.IsHex(light[p.BG]) {
			lc = theme.Contrast(light[p.FG], light[p.BG])
		}
		if theme.IsHex(dark[p.FG]) && theme.IsHex(dark[p.BG]) {
			dc = theme.Contrast(dark[p.FG], dark[p.BG])
		}
		checks = append(checks, map[string]any{"pair": p.FG + "/" + p.BG, "light": round2(lc), "dark": round2(dc), "floor": p.Floor,
			"ok": lc >= p.Floor && dc >= p.Floor})
	}
	writeJSON(w, 200, map[string]any{"light": light, "dark": dark, "overrides": over, "checks": checks})
}

func round2(f float64) float64 {
	v, _ := strconv.ParseFloat(strconv.FormatFloat(f, 'f', 2, 64), 64)
	return v
}

// themeFromImage is POST /api/v1/themes/from-image: a logo (multipart field "file", ≤ 2 MB, PNG, JPEG or GIF) →
// its main colours and a suggested accent.
func (s *Server) themeFromImage(w http.ResponseWriter, r *http.Request, c *caller) {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20+1<<16)
	f, _, err := r.FormFile("file")
	if err != nil {
		if isTooLarge(err) {
			writeError(w, http.StatusRequestEntityTooLarge, "too_large", "Logo maksimal 2 MB")
			return
		}
		writeError(w, http.StatusBadRequest, "bad_upload", "Pilih gambar logo (PNG, JPG, atau GIF)")
		return
	}
	defer f.Close()
	colours, suggested, err := theme.Palette(f)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_image", "Gambar tidak bisa dibaca; pakai PNG, JPG, atau GIF")
		return
	}
	if colours == nil {
		colours = []string{}
	}
	writeJSON(w, 200, map[string]any{"colours": colours, "suggested": suggested})
}

// appearance is the person's own pick: theme ("" = follow the business) and mode.
type appearance struct {
	Theme *string `json:"theme"`
	Mode  string  `json:"mode"`
}

func (s *Server) loadAppearance(ctx context.Context, userID int64) appearance {
	out := appearance{Mode: "system"}
	if userID == 0 || s.DB == nil {
		return out
	}
	res, err := s.DB.Query(ctx, "select theme, mode from rk_user_appearance where user_id = $1", userID)
	if err != nil || len(res.Rows) == 0 {
		return out
	}
	if t, ok := res.Rows[0][0].(string); ok && t != "" {
		out.Theme = &t
	}
	if m, ok := res.Rows[0][1].(string); ok && modes[m] {
		out.Mode = m
	}
	return out
}

// getAppearance is GET /api/v1/me/appearance.
func (s *Server) getAppearance(w http.ResponseWriter, r *http.Request, c *caller) {
	writeJSON(w, 200, s.loadAppearance(r.Context(), c.p.User.ID))
}

// putAppearance is PUT /api/v1/me/appearance: {theme: name|null, mode}. No approval: it changes only what this
// person sees. Their other tabs get event appearance.
func (s *Server) putAppearance(w http.ResponseWriter, r *http.Request, c *caller) {
	var in appearance
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", "body must be {theme, mode}")
		return
	}
	if in.Mode == "" {
		in.Mode = "system"
	}
	if !modes[in.Mode] {
		writeError(w, http.StatusBadRequest, "bad_mode", "mode must be system, light or dark")
		return
	}
	reg := s.Engine.Reg()
	if in.Theme != nil && *in.Theme != "" {
		found := false
		for _, t := range s.themeCards(reg) {
			found = found || t.Name == *in.Theme
		}
		if !found {
			writeError(w, http.StatusBadRequest, "unknown_theme", "Tema \""+*in.Theme+"\" tidak ada")
			return
		}
	} else {
		in.Theme = nil
	}
	if c.p.User.ID == 0 {
		writeError(w, http.StatusConflict, "no_user", "this session has no user to keep the appearance for")
		return
	}
	var t any
	if in.Theme != nil {
		t = *in.Theme
	}
	if _, err := s.DB.Query(r.Context(), `insert into rk_user_appearance (user_id, theme, mode, updated_at) values ($1, $2, $3, now())
		on conflict (user_id) do update set theme = excluded.theme, mode = excluded.mode, updated_at = now()`, c.p.User.ID, t, in.Mode); err != nil {
		fail(w, err)
		return
	}
	_ = store.Notify(r.Context(), s.DB, store.Event{Type: "appearance", Org: c.actor.Org, ID: c.actor.ID})
	writeJSON(w, 200, in)
}

// metaTheme adds personal and effective to /meta.theme for the signed-in person (contract 0065 §6).
func (s *Server) metaTheme(ctx context.Context, reg *meta.Registry, th map[string]any, userID int64) {
	ap := s.loadAppearance(ctx, userID)
	var pt any
	if ap.Theme != nil {
		pt = *ap.Theme
	}
	th["personal"] = map[string]any{"theme": pt, "mode": ap.Mode}
	eff := map[string]any{"name": th["name"], "tokens": th["tokens"], "overrides": th["overrides"]}
	for _, k := range []string{"font", "radius", "density"} {
		if v, ok := th[k]; ok {
			eff[k] = v
		}
	}
	if ap.Theme != nil && *ap.Theme != th["name"] {
		if data := s.resolve(reg, "themes", *ap.Theme); data != nil {
			var light, dark map[string]string
			var over map[string]theme.Override
			if reg.Site != nil && reg.Site.Themes[*ap.Theme] != nil {
				light, dark, over = meta.ThemeColours(reg.Site.Themes[*ap.Theme], s.TemplatesDir)
			} else {
				spec, keys := meta.ThemeSpec(data, nil, "")
				light, dark, over = theme.Apply(spec, keys)
			}
			eff = map[string]any{"name": *ap.Theme, "tokens": map[string]any{"light": light, "dark": dark}, "overrides": over}
			for _, k := range []string{"font", "radius", "density"} {
				if v, ok := data[k]; ok {
					eff[k] = v
				}
			}
		}
	}
	th["effective"] = eff
}
