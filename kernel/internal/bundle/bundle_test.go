package bundle

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
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
	t.Setenv("HOME", ro) // macOS: the user cache is $HOME/Library/Caches
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

// fakeWeb mirrors the shape of the bundled web: app sorts first, so a walk interrupted after it (the
// 16 Nov host had canvas, console and design-system but no app) is the case that must not be trusted.
func fakeWeb() fstest.MapFS {
	return fstest.MapFS{
		"files/web/app/index.html":           {Data: []byte("<html>app</html>")},
		"files/web/app/assets/app.js":        {Data: []byte("console.log('app')")},
		"files/web/canvas/canvas.js":         {Data: []byte("canvas")},
		"files/web/console/index.html":       {Data: []byte("console")},
		"files/web/design-system/tokens.css": {Data: []byte(":root{}")},
	}
}

func mustExist(t *testing.T, dir string, rels ...string) {
	t.Helper()
	for _, r := range rels {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(r))); err != nil {
			t.Fatal(err)
		}
	}
}

// Regression t_981624a8: the marker .web.ok exists but web/app is missing. The cache must be detected as
// broken and extracted again, not served without the owner app.
func TestExtractRepairsMarkerWithoutApp(t *testing.T) {
	src := fakeWeb()
	root := t.TempDir()
	d := extractFrom(src, root, "web")
	if d == "" {
		t.Fatal("first extract failed")
	}
	if err := os.RemoveAll(filepath.Join(d, "app")); err != nil {
		t.Fatal(err)
	}
	mustExist(t, root, ".web.ok")
	if complete(src, "web", d) {
		t.Fatal("a cache without app must not count as complete")
	}
	again := extractFrom(src, root, "web")
	if again != d {
		t.Fatalf("extract = %q, want %q", again, d)
	}
	mustExist(t, d, "app/index.html", "app/assets/app.js", "canvas/canvas.js", "design-system/tokens.css")
}

// A truncated file (same names, wrong size) is also broken.
func TestExtractRepairsTruncatedFile(t *testing.T) {
	src := fakeWeb()
	root := t.TempDir()
	d := extractFrom(src, root, "web")
	f := filepath.Join(d, "app", "index.html")
	if err := os.WriteFile(f, []byte("<ht"), 0o644); err != nil {
		t.Fatal(err)
	}
	if extractFrom(src, root, "web") != d {
		t.Fatal("re-extract failed")
	}
	if b, _ := os.ReadFile(f); string(b) != "<html>app</html>" {
		t.Fatalf("index.html = %q", b)
	}
}

// The marker is written only after a complete copy is in place: a failed extract leaves no marker and
// no partial directory.
func TestExtractFailureLeavesNoMarker(t *testing.T) {
	src := fakeWeb()
	src["files/web/zz-broken"] = &fstest.MapFile{Mode: fs.ModeSymlink} // unreadable entry fails the copy
	root := t.TempDir()
	if d := extractFrom(src, root, "web"); d != "" {
		t.Fatalf("extract = %q, want failure", d)
	}
	for _, p := range []string{".web.ok", "web"} {
		if _, err := os.Stat(filepath.Join(root, p)); !os.IsNotExist(err) {
			t.Fatalf("%s left behind after a failed extract (err %v)", p, err)
		}
	}
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".web.tmp-") {
			t.Fatalf("temp dir %s left behind", e.Name())
		}
	}
}

// Several kernels starting at once on one cache (8 on the 16 Nov host) all get a complete directory.
func TestExtractConcurrent(t *testing.T) {
	src := fakeWeb()
	root := t.TempDir()
	var wg sync.WaitGroup
	got := make([]string, 16)
	for i := range got {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got[i] = extractFrom(src, root, "web")
		}(i)
	}
	wg.Wait()
	for i, d := range got {
		if d != filepath.Join(root, "web") {
			t.Fatalf("run %d: extract = %q", i, d)
		}
	}
	if !complete(src, "web", filepath.Join(root, "web")) {
		t.Fatal("cache incomplete after concurrent extracts")
	}
	mustExist(t, root, ".web.ok")
}
