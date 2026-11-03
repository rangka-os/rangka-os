//go:build unix

package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

// TestBackupFileMode: rangka backup writes its archive 0600 (it holds password hashes and the signing key),
// whatever the umask, also over a stale .partial with wider permissions.
func TestBackupFileMode(t *testing.T) {
	url := testdb.New(t)
	old := syscall.Umask(0o022)
	t.Cleanup(func() { syscall.Umask(old) })
	dir := t.TempDir()
	apps, files := filepath.Join(dir, "apps"), filepath.Join(dir, "files")
	os.MkdirAll(apps, 0o755)
	out := filepath.Join(dir, "a.tar.gz")
	if err := os.WriteFile(out+".partial", []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := cmdBackup([]string{"--db", url, "--apps", apps, "--files", files, "--out", out}); code != 0 {
		t.Fatalf("rangka backup exited %d", code)
	}
	st, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if m := st.Mode().Perm(); m != 0o600 {
		t.Fatalf("archive mode = %o, want 600", m)
	}
}
