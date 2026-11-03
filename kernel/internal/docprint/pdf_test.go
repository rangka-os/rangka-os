package docprint

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fakeChromium(t *testing.T, script string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "chromium")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RANGKA_CHROMIUM", p)
}

func TestPDFUnavailable(t *testing.T) {
	t.Setenv("RANGKA_CHROMIUM", "off")
	if _, err := PDF(context.Background(), []byte("<p>x</p>")); !errors.Is(err, ErrNoChromium) {
		t.Fatalf("got %v, want ErrNoChromium", err)
	}
}

func TestFindChromiumMissingPath(t *testing.T) {
	t.Setenv("RANGKA_CHROMIUM", filepath.Join(t.TempDir(), "nope"))
	if got := FindChromium(); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestPDF(t *testing.T) {
	if FindChromium() == "" {
		t.Skip("no chromium")
	}
	out, err := PDF(context.Background(), []byte("<!doctype html><p>Halo</p>"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(out, []byte("%PDF")) {
		t.Fatalf("not a PDF: %q", out[:min(len(out), 20)])
	}
}

func TestPDFFailureIncludesStderr(t *testing.T) {
	fakeChromium(t, "echo boom-from-chrome >&2; exit 1")
	_, err := PDF(context.Background(), []byte("<p>x</p>"))
	if err == nil || !strings.Contains(err.Error(), "boom-from-chrome") {
		t.Fatalf("got %v", err)
	}
}

func TestPDFNoOutputFile(t *testing.T) {
	fakeChromium(t, "echo quiet-failure >&2; exit 0")
	_, err := PDF(context.Background(), []byte("<p>x</p>"))
	if err == nil || !strings.Contains(err.Error(), "quiet-failure") {
		t.Fatalf("got %v", err)
	}
}

func TestTail(t *testing.T) {
	if got := tail("abc", 10); got != "abc" {
		t.Fatal(got)
	}
	// "é" is two bytes; cutting at 3 bytes of "aéé" would split a rune.
	if got := tail("aéé", 3); got != "é" {
		t.Fatalf("got %q", got)
	}
	if got := tail("\x80\x80\x80\x80", 2); got != "" {
		t.Fatalf("got %q", got)
	}
	if got := tail("xyz", 2); got != "yz" {
		t.Fatal(got)
	}
}

func TestPDFTimeoutError(t *testing.T) {
	fakeChromium(t, "sleep 5")
	old := pdfTimeout
	pdfTimeout = 200 * time.Millisecond
	defer func() { pdfTimeout = old }()
	if _, err := PDF(context.Background(), []byte("<p>x</p>")); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v, want deadline exceeded", err)
	}
}
