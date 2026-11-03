package docprint

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// ErrNoChromium is returned by PDF when no Chromium or Chrome binary is available.
var ErrNoChromium = errors.New("chromium not found")

// pdfTimeout bounds one conversion; a variable so tests can shorten it.
var pdfTimeout = 30 * time.Second

const macChrome = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"

// FindChromium returns the browser binary used for PDF output, or "" when there is none.
// RANGKA_CHROMIUM overrides the search: "off" disables PDF, a path is used when it exists.
func FindChromium() string {
	if v := os.Getenv("RANGKA_CHROMIUM"); v != "" {
		if v == "off" {
			return ""
		}
		if _, err := os.Stat(v); err != nil {
			return ""
		}
		return v
	}
	for _, n := range []string{"chromium", "chromium-browser", "google-chrome", "google-chrome-stable", "headless_shell"} {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	if _, err := os.Stat(macChrome); err == nil {
		return macChrome
	}
	return ""
}

// PDF renders an HTML document to PDF with headless Chromium.
func PDF(ctx context.Context, html []byte) ([]byte, error) {
	bin := FindChromium()
	if bin == "" {
		return nil, ErrNoChromium
	}
	dir, err := os.MkdirTemp("", "rangka-pdf-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	in, out := filepath.Join(dir, "in.html"), filepath.Join(dir, "out.pdf")
	if err := os.WriteFile(in, html, 0o600); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, pdfTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--headless", "--disable-gpu", "--no-sandbox", "--no-pdf-header-footer",
		"--user-data-dir="+filepath.Join(dir, "profile"), "--print-to-pdf="+out, (&url.URL{Scheme: "file", Path: in}).String())
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	killTree(cmd)
	cmd.WaitDelay = time.Second
	runErr := cmd.Run()
	detail := tail(strings.TrimSpace(stderr.String()), 2000)
	if ctx.Err() != nil {
		return nil, fmt.Errorf("chromium pdf: %w: %s", ctx.Err(), detail)
	}
	if runErr != nil {
		return nil, fmt.Errorf("chromium pdf: %w: %s", runErr, detail)
	}
	b, err := os.ReadFile(out)
	if err != nil || len(b) == 0 {
		return nil, fmt.Errorf("chromium pdf: no output produced: %s", detail)
	}
	return b, nil
}

// tail keeps the last n bytes of s without splitting a UTF-8 rune.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[len(s)-n:]
	for len(s) > 0 && !utf8.RuneStart(s[0]) {
		s = s[1:]
	}
	return s
}
