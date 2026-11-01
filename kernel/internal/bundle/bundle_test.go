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
	if _, err := os.Stat(filepath.Join(d, "console", "index.html")); err != nil {
		t.Fatal(err)
	}
	if again := Dir("web"); again != d {
		t.Fatalf("second call %s, want %s", again, d)
	}
}
