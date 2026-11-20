package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/engine"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
)

// Contract 0072 §6: the app's pages allow images from the kernel, data:, blob: and the tile source of system.rk
// maps, and nothing else; other files and other areas (console, portal) are not given the app's policy.
func TestStaticAppCSP(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []string{"app/index.html", "app/tur.html", "app/app.css", "console/index.html"} {
		f := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	osm := "img-src 'self' data: blob: https://tile.openstreetmap.org"
	mirror := map[string]any{"tiles": map[string]any{"url": "https://{s}.tiles.kopisenja.id/{z}/{x}/{y}.png",
		"attribution": "© OpenStreetMap contributors", "subdomains": []any{"a", "b"}}}
	cases := []struct {
		name   string
		system map[string]any
		path   string
		want   string
	}{
		{"no site: defaults", nil, "/app/", osm},
		{"no maps block: defaults", map[string]any{}, "/app/", osm},
		{"another app page", map[string]any{}, "/app/tur.html", osm},
		{"mirror with subdomains", map[string]any{"maps": mirror}, "/app/",
			"img-src 'self' data: blob: https://a.tiles.kopisenja.id https://b.tiles.kopisenja.id"},
		{"maps off", map[string]any{"maps": map[string]any{"enabled": false}}, "/app/", "img-src 'self' data: blob:"},
		{"not a page", map[string]any{}, "/app/app.css", ""},
		{"console", map[string]any{}, "/console/", ""},
	}
	for _, c := range cases {
		reg := &meta.Registry{}
		if c.system != nil {
			reg.Site = &meta.Site{System: c.system}
		}
		h := (&Server{WebDir: dir, Engine: engine.New(nil, reg)}).static()
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, c.path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d", c.name, rec.Code)
		}
		if got := rec.Header().Get("Content-Security-Policy"); got != c.want {
			t.Errorf("%s: CSP %q, want %q", c.name, got, c.want)
		}
	}
}
