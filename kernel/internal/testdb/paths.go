package testdb

import (
	"os"
	"path/filepath"
	"testing"
)

// RepoRoot is the absolute path of this rangka-os checkout (or git worktree): the first directory above the
// working directory that contains kernel/go.mod. Repo-owned paths (web/, templates/, testdata/) hang off it.
func RepoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "kernel", "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// Workspace is the directory that holds the sibling checkouts rangka-apps/ and rangka-connectors/:
// $RANGKA_WORKSPACE when set, else the first ancestor of the repo root's parent that has rangka-apps/apps.
// It is "" when none is found.
func Workspace() string {
	if ws := os.Getenv("RANGKA_WORKSPACE"); ws != "" {
		return ws
	}
	root := RepoRoot()
	if root == "" {
		return ""
	}
	dir := filepath.Dir(root)
	for {
		if st, err := os.Stat(filepath.Join(dir, "rangka-apps", "apps")); err == nil && st.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// Sibling is Workspace()/rel with symlinks resolved (so filepath.WalkDir descends into it); the test is
// skipped when that path does not exist.
func Sibling(t testing.TB, rel string) string {
	t.Helper()
	p := filepath.Join(Workspace(), rel)
	if Workspace() == "" {
		p = ""
	} else if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	t.Skipf("sibling checkout %s not found; set RANGKA_WORKSPACE", rel)
	return p
}

// SkipHidden reports whether a directory walk should not descend into d: hidden directories such as .git,
// .claude and .worktrees hold other checkouts and tool state, not this repo's files.
func SkipHidden(d os.DirEntry) bool {
	return d != nil && d.IsDir() && len(d.Name()) > 1 && d.Name()[0] == '.'
}
