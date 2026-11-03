//go:build unix

package docprint

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// gatedChromium is a fake browser that records its start as started/<pid>, waits for release/<pid> or
// release/all, then writes a PDF.
const gatedChromium = `for a in "$@"; do case "$a" in --print-to-pdf=*) out="${a#--print-to-pdf=}";; esac; done
touch "$PDF_TEST_DIR/started/$$"
while [ ! -e "$PDF_TEST_DIR/release/$$" ] && [ ! -e "$PDF_TEST_DIR/release/all" ]; do sleep 0.02; done
printf '%%PDF-fake' > "$out"`

func startedPIDs(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(filepath.Join(dir, "started"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}

func waitStarted(t *testing.T, dir string, n int) []string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if got := startedPIDs(t, dir); len(got) >= n {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("want %d browsers started, have %v", n, startedPIDs(t, dir))
	return nil
}

// With two slots, a third PDF waits until one of the running two finishes; a caller whose context ends
// while waiting gets the context's error without starting a browser.
func TestPDFConcurrencyLimit(t *testing.T) {
	dir := t.TempDir()
	for _, d := range []string{"started", "release"} {
		if err := os.Mkdir(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PDF_TEST_DIR", dir)
	fakeChromium(t, gatedChromium)
	old := pdfSlots
	pdfSlots = make(chan struct{}, 2)
	defer func() { pdfSlots = old }()
	release := func(name string) {
		if err := os.WriteFile(filepath.Join(dir, "release", name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	defer release("all") // never leave a fake browser spinning

	var wg sync.WaitGroup
	errs := make(chan error, 3)
	pdf := func() {
		defer wg.Done()
		_, err := PDF(context.Background(), []byte("<p>x</p>"))
		errs <- err
	}
	wg.Add(2)
	go pdf()
	go pdf()
	first := waitStarted(t, dir, 2)

	// both slots busy: a caller whose context ends while waiting gives up, nothing started
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := PDF(ctx, []byte("<p>x</p>")); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting caller: got %v, want deadline exceeded", err)
	}

	wg.Add(1)
	go pdf()
	time.Sleep(300 * time.Millisecond)
	if got := startedPIDs(t, dir); len(got) != 2 {
		t.Fatalf("a third browser started while two run: %v", got)
	}
	release(first[0])
	waitStarted(t, dir, 3)
	release("all")
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("PDF: %v", err)
		}
	}
}

func TestPDFConcurrencyFromEnv(t *testing.T) {
	for v, want := range map[string]int{"": 2, "4": 4, "1": 1, "0": 2, "-3": 2, "x": 2} {
		t.Setenv("RANGKA_PDF_CONCURRENCY", v)
		if got := pdfConcurrency(); got != want {
			t.Errorf("RANGKA_PDF_CONCURRENCY=%q: %d, want %d", v, got, want)
		}
	}
}
