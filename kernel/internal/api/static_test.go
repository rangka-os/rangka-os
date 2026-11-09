package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// The installable web app: sw.js and the manifest are always revalidated and the manifest has its media type.
func TestStaticPWAHeaders(t *testing.T) {
	dir := t.TempDir()
	app := filepath.Join(dir, "app")
	if err := os.MkdirAll(app, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"index.html":           "<!doctype html><title>Rangka</title>",
		"sw.js":                "self.addEventListener('fetch', function () {});",
		"manifest.webmanifest": `{"name":"Rangka"}`,
		"app.css":              "body{}",
	} {
		if err := os.WriteFile(filepath.Join(app, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	h := (&Server{WebDir: dir}).static()

	cases := []struct {
		path, cacheControl, contentType string
	}{
		{"/app/sw.js", "no-cache", "text/javascript; charset=utf-8"},
		{"/app/manifest.webmanifest", "no-cache", "application/manifest+json"},
		{"/app/", "no-cache", "text/html; charset=utf-8"},
		{"/app/app.css", "", "text/css; charset=utf-8"},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, c.path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d", c.path, rec.Code)
		}
		if got := rec.Header().Get("Cache-Control"); got != c.cacheControl {
			t.Errorf("%s: Cache-Control %q, want %q", c.path, got, c.cacheControl)
		}
		if got := rec.Header().Get("Content-Type"); got != c.contentType {
			t.Errorf("%s: Content-Type %q, want %q", c.path, got, c.contentType)
		}
	}
}
