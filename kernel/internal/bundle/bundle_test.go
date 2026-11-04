package bundle

import (
	"os"
	"path/filepath"
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
