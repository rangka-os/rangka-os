package bundle

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"
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

// t_c850700e: leftovers of runs that died mid-extract (SIGKILL, OOM) are swept once stale; younger temp
// entries may belong to a run in progress and stay.
func TestExtractSweepsStaleTemps(t *testing.T) {
	src := fakeWeb()
	root := t.TempDir()
	if extractFrom(src, root, "web") == "" {
		t.Fatal("first extract failed")
	}
	old := time.Now().Add(-2 * staleAfter)
	stale := []string{".web.tmp-111", ".web.tmp-222.old", ".marker-333"}
	for _, n := range stale {
		p := filepath.Join(root, n)
		if strings.HasPrefix(n, ".web") {
			if err := os.MkdirAll(filepath.Join(p, "app"), 0o755); err != nil {
				t.Fatal(err)
			}
		} else if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	fresh := filepath.Join(root, ".web.tmp-444")
	if err := os.Mkdir(fresh, 0o755); err != nil {
		t.Fatal(err)
	}
	if extractFrom(src, root, "web") == "" { // the fast path sweeps too
		t.Fatal("extract failed")
	}
	for _, n := range stale {
		if _, err := os.Lstat(filepath.Join(root, n)); !os.IsNotExist(err) {
			t.Fatalf("stale %s not swept (err %v)", n, err)
		}
	}
	mustExist(t, root, ".web.tmp-444", ".web.ok", "web/app/index.html")
}

// bigWeb is fakeWeb with many files under app (which sorts first), so a racing copy has a wide window.
func bigWeb() fstest.MapFS {
	src := fakeWeb()
	pad := []byte(strings.Repeat("x", 2048))
	for i := 0; i < 160; i++ {
		src[fmt.Sprintf("files/web/app/gen/f%03d.js", i)] = &fstest.MapFile{Data: pad}
	}
	return src
}

// Helper-process mode for TestExtractMultiProcess: when BUNDLE_MP_ROOT is set this test binary is one of
// the kernels starting at once. It signals ready, waits for the start file, extracts, then re-checks the
// returned directory while its siblings are still running: a sibling's repair must never move it away.
func runMultiProcessHelper(t *testing.T, root string) {
	ready, start := os.Getenv("BUNDLE_MP_READY"), os.Getenv("BUNDLE_MP_START")
	if err := os.WriteFile(ready, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, err := os.Stat(start); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("start file never appeared")
		}
		time.Sleep(time.Millisecond)
	}
	src := bigWeb()
	d := extractFrom(src, root, "web")
	if d != filepath.Join(root, "web") {
		t.Fatalf("extract = %q", d)
	}
	for i := 0; i < 50; i++ {
		if !complete(src, "web", d) {
			t.Fatalf("check %d: %s incomplete after extract returned", i, d)
		}
		time.Sleep(time.Millisecond)
	}
	fmt.Println("MP-OK")
}

// t_d55c15ee: several kernels (separate OS processes, as on the 16 Nov host) starting at once on one
// cache, empty or broken (marker present, app missing), all get a complete directory, and the root ends
// with no temp leftovers. The test re-executes its own binary as the processes.
func TestExtractMultiProcess(t *testing.T) {
	if root := os.Getenv("BUNDLE_MP_ROOT"); root != "" {
		runMultiProcessHelper(t, root)
		return
	}
	if testing.Short() {
		t.Skip("spawns processes")
	}
	const procs, rounds = 8, 4
	src := bigWeb()
	for _, broken := range []bool{false, true} {
		for r := 0; r < rounds; r++ {
			name := fmt.Sprintf("broken=%v/round=%d", broken, r)
			root := filepath.Join(t.TempDir(), "root")
			if broken {
				d := extractFrom(src, root, "web")
				if err := os.RemoveAll(filepath.Join(d, "app")); err != nil {
					t.Fatal(err)
				}
				mustExist(t, root, ".web.ok")
			}
			syncDir := t.TempDir()
			start := filepath.Join(syncDir, "start")
			cmds := make([]*exec.Cmd, procs)
			outs := make([]strings.Builder, procs)
			for i := range cmds {
				c := exec.Command(os.Args[0], "-test.run=^TestExtractMultiProcess$", "-test.count=1", "-test.v")
				c.Env = append(os.Environ(),
					"BUNDLE_MP_ROOT="+root,
					"BUNDLE_MP_READY="+filepath.Join(syncDir, "ready-"+strconv.Itoa(i)),
					"BUNDLE_MP_START="+start)
				c.Stdout, c.Stderr = &outs[i], &outs[i]
				if err := c.Start(); err != nil {
					t.Fatal(err)
				}
				cmds[i] = c
			}
			waitReady(t, syncDir, procs)
			if err := os.WriteFile(start, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			for i, c := range cmds {
				if err := c.Wait(); err != nil || !strings.Contains(outs[i].String(), "MP-OK") {
					t.Fatalf("%s: process %d: %v\n%s", name, i, err, outs[i].String())
				}
			}
			if !complete(src, "web", filepath.Join(root, "web")) {
				t.Fatalf("%s: cache incomplete at the end", name)
			}
			entries, _ := os.ReadDir(root)
			for _, e := range entries {
				switch e.Name() {
				case ".web.ok", "web", ".lock":
				default:
					t.Fatalf("%s: leftover %s in root", name, e.Name())
				}
			}
		}
	}
}

// waitReady waits until n helper processes have written their ready file.
func waitReady(t *testing.T, dir string, n int) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		entries, _ := os.ReadDir(dir)
		if len(entries) >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d helper processes started", len(entries), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
