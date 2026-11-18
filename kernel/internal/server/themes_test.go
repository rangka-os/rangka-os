package server

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Contract 0065 §6: gallery, derive, colours from a logo, the person's own appearance and /meta.theme.
func TestThemesAPI(t *testing.T) {
	e := newEnv(t, false, nil)
	e.login()

	list := e.must(200, "GET", "/api/v1/themes", nil).JSON
	var names []string
	for _, x := range list["themes"].([]any) {
		names = append(names, get(x, "name").(string))
	}
	if strings.Join(names, ",") != "standard,anggur,arang,daun,laut,senja,kopi_senja" {
		t.Fatalf("themes: %v", names)
	}
	if list["current"] != "kopi_senja" || get(list, "allowed", "custom") != true || len(list["fonts"].([]any)) != 6 {
		t.Fatalf("list: %v %v %v", list["current"], list["allowed"], list["fonts"])
	}
	if get(list, "themes", 4, "swatches", "light", "primary") != "#0c66e4" || get(list, "themes", 4, "neutral") != "slate" {
		t.Fatalf("laut card: %v", get(list, "themes", 4))
	}

	// a yellow that fails on white still gives a readable theme
	d := e.must(200, "POST", "/api/v1/themes/derive", map[string]any{"accent": "#f5cd47", "button": "accent"}).JSON
	for _, c := range d["checks"].([]any) {
		if get(c, "ok") != true {
			t.Errorf("derive check failed: %v", c)
		}
	}
	if get(d, "light", "brand") == "#f5cd47" || get(d, "overrides", "link", "from") != "accent" {
		t.Fatalf("derive: brand %v link %v", get(d, "light", "brand"), get(d, "overrides", "link"))
	}
	if r := e.must(400, "POST", "/api/v1/themes/derive", map[string]any{"accent": "#7a4520", "color": map[string]any{"ok": "#00ff00"}}); errCode(r) != "locked_token" {
		t.Fatalf("locked: %s", r.Body)
	}
	e.must(400, "POST", "/api/v1/themes/derive", map[string]any{"accent": "coklat"})

	// a logo: brown mark on white
	img := image.NewRGBA(image.Rect(0, 0, 80, 80))
	for y := 0; y < 80; y++ {
		for x := 0; x < 80; x++ {
			c := color.RGBA{255, 255, 255, 255}
			if x > 20 && x < 60 && y > 20 && y < 60 {
				c = color.RGBA{0x7a, 0x45, 0x20, 255}
			}
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	up := e.upload("/api/v1/themes/from-image", "logo.png", buf.Bytes())
	if up.Status != 200 || get(up.JSON, "suggested") != "#7a4520" {
		t.Fatalf("from-image: %d %s", up.Status, up.Body)
	}

	// the person's own pick
	if a := e.must(200, "GET", "/api/v1/me/appearance", nil).JSON; a["theme"] != nil || a["mode"] != "system" {
		t.Fatalf("default appearance: %v", a)
	}
	e.must(200, "PUT", "/api/v1/me/appearance", map[string]any{"theme": "laut", "mode": "dark"})
	if r := e.must(400, "PUT", "/api/v1/me/appearance", map[string]any{"theme": "pelangi"}); errCode(r) != "unknown_theme" {
		t.Fatalf("unknown: %s", r.Body)
	}
	e.must(400, "PUT", "/api/v1/me/appearance", map[string]any{"mode": "senja"})
	m := e.must(200, "GET", "/api/v1/meta", nil).JSON
	if get(m, "theme", "name") != "kopi_senja" || get(m, "theme", "personal", "theme") != "laut" || get(m, "theme", "personal", "mode") != "dark" {
		t.Fatalf("meta personal: %v", get(m, "theme", "personal"))
	}
	if get(m, "theme", "effective", "name") != "laut" || get(m, "theme", "effective", "tokens", "light", "primary") != "#0c66e4" {
		t.Fatalf("meta effective: %v", get(m, "theme", "effective", "name"))
	}
	// back to following the business
	e.must(200, "PUT", "/api/v1/me/appearance", map[string]any{"theme": nil, "mode": "light"})
	if m := e.must(200, "GET", "/api/v1/meta", nil).JSON; get(m, "theme", "effective", "name") != "kopi_senja" {
		t.Fatalf("follow business: %v", get(m, "theme", "effective", "name"))
	}
}

// Contract 0065 §7: a plan with custom_theme false may switch to a bundled theme, not make one of its own.
func TestCustomThemePlanFlag(t *testing.T) {
	e := newEnv(t, false, func(apps, site string) {
		f := filepath.Join(site, "features.rk")
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		s := strings.Replace(string(b), "    extends: mulai\n", "    extends: mulai\n    flags: {custom_theme: false}\n", 1)
		if s == string(b) {
			t.Fatal("setup: tumbuh plan not found")
		}
		os.WriteFile(f, []byte(s), 0o644)
	})
	e.login()
	setPlan(e, "tumbuh")
	if get(e.must(200, "GET", "/api/v1/themes", nil).JSON, "allowed", "custom") != false {
		t.Fatal("custom must not be allowed")
	}
	own := e.must(201, "POST", "/api/v1/changesets", map[string]any{"title": "Warna merek", "ops": []any{map[string]any{"op": "patch", "path": "themes/kopi_senja.rk",
		"patch": []any{map[string]any{"op": "replace", "path": "/color/brand", "value": "#7a4520"}}}}}).JSON
	v := e.must(200, "POST", "/api/v1/changesets/"+own["id"].(string)+"/validate", nil)
	if v.JSON["status"] == "validated" || !strings.Contains(string(v.Body), "plan_limit") || !strings.Contains(string(v.Body), "Juragan Pro") {
		t.Fatalf("own colours on a plan without custom_theme: %s", v.Body)
	}
	bundled := e.must(201, "POST", "/api/v1/changesets", map[string]any{"title": "Tema Laut", "ops": []any{map[string]any{"op": "patch", "path": "config.rk",
		"patch": []any{map[string]any{"op": "replace", "path": "/theme", "value": "laut"}}}}}).JSON
	if v := e.must(200, "POST", "/api/v1/changesets/"+bundled["id"].(string)+"/validate", nil).JSON; v["status"] != "validated" {
		t.Fatalf("switching to a bundled theme: %v", v)
	}
}
