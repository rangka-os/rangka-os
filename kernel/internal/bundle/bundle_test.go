package bundle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDirExtracts(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	if !Has("web") {
		if Dir("web") != "" {
			t.Fatal("Dir must be empty when nothing is bundled")
		}
		t.Skip("development build: web is not bundled")
	}
	d := Dir("web")
	if d == "" {
		t.Fatal("no directory")
	}
	// the Console embeds web/canvas and its demo mode fetches ../canvas/testdata/*.json
	for _, f := range []string{"console/index.html", "canvas/canvas.js", "canvas/testdata/lead_to_cash.json"} {
		if _, err := os.Stat(filepath.Join(d, filepath.FromSlash(f))); err != nil {
			t.Fatal(err)
		}
	}
	if again := Dir("web"); again != d {
		t.Fatalf("second call %s, want %s", again, d)
	}
}

// An unwritable user cache (a container user with HOME=/) falls back to the temp directory.
func TestDirFallsBackToTemp(t *testing.T) {
	if !Has("web") {
		t.Skip("development build: web is not bundled")
	}
	ro := filepath.Join(t.TempDir(), "file") // a cache under a regular file cannot be written, even by root
	if err := os.WriteFile(ro, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CACHE_HOME", ro)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	d := Dir("web")
	if d == "" || !strings.HasPrefix(d, tmp) {
		t.Fatalf("Dir = %q, want under %s", d, tmp)
	}
}

// extract reports "" when root cannot be written, and the directory when it can.
func TestExtractUnwritable(t *testing.T) {
	if !Has("web") {
		t.Skip("development build: web is not bundled")
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if d := extract(filepath.Join(file, "rangka"), "web"); d != "" { // a path under a regular file
		t.Fatalf("extract under a file = %q, want empty", d)
	}
	if d := extract(t.TempDir(), "web"); d == "" {
		t.Fatal("extract into a writable root failed")
	}
}
