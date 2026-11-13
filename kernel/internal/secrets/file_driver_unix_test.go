//go:build unix

package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Audit secrets-openroot F1: a FIFO named like the variable (or a symlink to one) reads as unset without blocking.
func TestFileDriverFIFO(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, "PIPE"), 0o600); err != nil {
		t.Skip("mkfifo:", err)
	}
	os.Symlink("PIPE", filepath.Join(dir, "TO_PIPE"))
	for _, name := range []string{"PIPE", "TO_PIPE"} {
		done := make(chan bool, 1)
		go func() { _, ok := readSecretFile(dir, name); done <- ok }()
		select {
		case ok := <-done:
			if ok {
				t.Errorf("%s: a FIFO read as set", name)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s: reading a FIFO blocked", name)
		}
	}
}

// Audit secrets-openroot F2: a file over maxSecretFile is refused whole, never cut to the limit; one at the limit reads.
func TestFileDriverSizeLimit(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "BIG"), []byte(strings.Repeat("a", maxSecretFile+5)), 0o600)
	os.WriteFile(filepath.Join(dir, "EDGE"), []byte(strings.Repeat("b", maxSecretFile)), 0o600)
	if v, ok := readSecretFile(dir, "BIG"); ok || v != "" {
		t.Fatalf("a file over the limit read as set (%d bytes)", len(v))
	}
	if v, ok := readSecretFile(dir, "EDGE"); !ok || len(v) != maxSecretFile {
		t.Fatalf("a file at the limit: ok=%v len=%d", ok, len(v))
	}
}
