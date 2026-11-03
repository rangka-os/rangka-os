package testdb

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestRepoRoot(t *testing.T) {
	root := RepoRoot()
	for _, rel := range []string{"kernel/go.mod", "web/app/index.html"} {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Fatalf("RepoRoot() = %q lacks %s", root, rel)
		}
	}
}

func TestWorkspaceEnv(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("RANGKA_WORKSPACE", tmp)
	if got := Workspace(); got != tmp {
		t.Fatalf("Workspace() = %q, want %q", got, tmp)
	}
}

func TestWorkspaceSearchesUpward(t *testing.T) {
	t.Setenv("RANGKA_WORKSPACE", "")
	a, _ := filepath.EvalSymlinks(t.TempDir())
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(a, "rangka-apps", "apps"), 0o755))
	cwd := filepath.Join(a, "b", "c", "kernel", "x")
	must(os.MkdirAll(cwd, 0o755))
	must(os.WriteFile(filepath.Join(a, "b", "c", "kernel", "go.mod"), []byte("module x\n"), 0o644))
	old, _ := os.Getwd()
	must(os.Chdir(cwd))
	defer os.Chdir(old)
	if got := Workspace(); got != a {
		t.Fatalf("Workspace() = %q, want %q", got, a)
	}
}

func TestSiblingSkipsWhenMissing(t *testing.T) {
	t.Setenv("RANGKA_WORKSPACE", t.TempDir())
	t.Setenv("CI", "")
	t.Setenv("RANGKA_REQUIRE_SIBLINGS", "")
	t.Run("missing", func(t *testing.T) {
		Sibling(t, "rangka-apps/apps")
		t.Fatal("expected skip")
	})
}

// recTB records Skipf and Fatalf instead of stopping the test, so both branches of Sibling can be checked.
type recTB struct {
	testing.TB
	skipped, fatal string
}

func (r *recTB) Helper()                   {}
func (r *recTB) Skipf(f string, a ...any)  { r.skipped = fmt.Sprintf(f, a...) }
func (r *recTB) Fatalf(f string, a ...any) { r.fatal = fmt.Sprintf(f, a...) }

func TestSiblingMissingSkipsOrFails(t *testing.T) {
	cases := []struct {
		ci, require string
		wantFatal   bool
	}{
		{"", "", false},
		{"", "0", false},
		{"true", "", true},
		{"", "1", true},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("CI=%q,RANGKA_REQUIRE_SIBLINGS=%q", c.ci, c.require), func(t *testing.T) {
			t.Setenv("RANGKA_WORKSPACE", t.TempDir())
			t.Setenv("CI", c.ci)
			t.Setenv("RANGKA_REQUIRE_SIBLINGS", c.require)
			r := &recTB{}
			Sibling(r, "rangka-apps/apps")
			if c.wantFatal && (r.fatal == "" || r.skipped != "") {
				t.Fatalf("want a failure, got fatal=%q skipped=%q", r.fatal, r.skipped)
			}
			if !c.wantFatal && (r.skipped == "" || r.fatal != "") {
				t.Fatalf("want a skip, got fatal=%q skipped=%q", r.fatal, r.skipped)
			}
		})
	}
}
