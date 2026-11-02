package testdb

import (
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
	t.Run("missing", func(t *testing.T) {
		Sibling(t, "rangka-apps/apps")
		t.Fatal("expected skip")
	})
}
