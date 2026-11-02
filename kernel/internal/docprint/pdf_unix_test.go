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
	old := pdfTimeout
	pdfTimeout = 300 * time.Millisecond
	defer func() { pdfTimeout = old }()
	start := time.Now()
	if _, err := PDF(context.Background(), []byte("<p>x</p>")); err == nil {
		t.Fatal("want a timeout error")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("PDF did not stop at the timeout")
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
