//go:build unix

package docprint

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestPDFTimeoutKillsProcessTree(t *testing.T) {
	pidfile := filepath.Join(t.TempDir(), "pid")
	fakeChromium(t, "sleep 30 & echo $! > "+pidfile+"; wait")
	// Cancel the caller's context once the grandchild's pid is on disk, so the kill never races startup.
	// pdfTimeout stays a generous backstop.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
			if b, err := os.ReadFile(pidfile); err == nil && strings.HasSuffix(string(b), "\n") {
				cancel()
				return
			}
		}
	}()
	start := time.Now()
	if _, err := PDF(ctx, []byte("<p>x</p>")); err == nil {
		t.Fatal("want a cancellation error")
	}
	if time.Since(start) > 8*time.Second {
		t.Fatal("PDF did not stop after cancellation")
	}
	b, err := os.ReadFile(pidfile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if syscall.Kill(pid, 0) != nil {
			return
		}
	}
	syscall.Kill(pid, syscall.SIGKILL)
	t.Fatalf("grandchild %d survived the timeout", pid)
}
