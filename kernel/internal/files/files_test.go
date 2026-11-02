package files

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestLocalPutOpenDelete(t *testing.T) {
	root := t.TempDir()
	s, err := NewLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256([]byte("halo"))
	want := hex.EncodeToString(h[:])
	sum, size, err := s.Put("kopi-senja", strings.NewReader("halo"))
	if err != nil || sum != want || size != 4 {
		t.Fatalf("put: %q %d %v", sum, size, err)
	}
	if _, err := os.Stat(filepath.Join(s.Root(), "kopi-senja", want[:2], want)); err != nil {
		t.Fatal(err)
	}
	f, err := s.Open("kopi-senja", sum)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(f)
	f.Close()
	if string(b) != "halo" {
		t.Fatalf("read %q", b)
	}
	if sum2, _, err := s.Put("kopi-senja", strings.NewReader("halo")); err != nil || sum2 != sum {
		t.Fatalf("second put: %q %v", sum2, err)
	}
	if err := s.Delete("kopi-senja", sum); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open("kopi-senja", sum); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("open after delete: %v", err)
	}
	if err := s.Delete("kopi-senja", sum); err != nil {
		t.Fatalf("second delete: %v", err)
	}
	left, _ := os.ReadDir(filepath.Join(s.Root(), "kopi-senja", "tmp"))
	if len(left) != 0 {
		t.Fatalf("tmp not empty: %d files", len(left))
	}
}

func TestLocalRejectsBadOrgAndSum(t *testing.T) {
	s, err := NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Put("../x", strings.NewReader("a")); err == nil {
		t.Fatal("bad org accepted")
	}
	if _, err := s.Open("kopi-senja", "../../etc/passwd"); err == nil {
		t.Fatal("bad sum accepted")
	}
	if err := s.Delete("kopi-senja", "zz"); err == nil {
		t.Fatal("bad sum accepted by Delete")
	}
}

func TestSafeFilename(t *testing.T) {
	for in, want := range map[string]string{
		"../../etc/passwd": "passwd",
		`a\b.pdf`:          "b.pdf",
		"x\x00y.png":       "xy.png",
		"":                 "lampiran",
		"..":               "lampiran",
		".":                "lampiran",
	} {
		if got := SafeFilename(in); got != want {
			t.Errorf("SafeFilename(%q) = %q, want %q", in, got, want)
		}
	}
	got := SafeFilename(strings.Repeat("é", 300))
	if len(got) > 200 || !utf8.ValidString(got) || got == "" {
		t.Fatalf("long name: len %d valid %v", len(got), utf8.ValidString(got))
	}
}

func TestSniffAndInline(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	if ct := SniffType(png, "a.png"); ct != "image/png" || !Inline(ct) {
		t.Errorf("png: %q", ct)
	}
	if ct := SniffType([]byte("%PDF-1.4"), "a.pdf"); ct != "application/pdf" || !Inline(ct) {
		t.Errorf("pdf: %q", ct)
	}
	if ct := SniffType([]byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`), "x.svg"); Inline(ct) {
		t.Errorf("svg inline: %q", ct)
	}
	if ct := SniffType([]byte("<html>"), "x.html"); ct != "text/html; charset=utf-8" || Inline(ct) {
		t.Errorf("html: %q", ct)
	}
	if ct := SniffType([]byte("a,b\n1,2\n"), "a.csv"); !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("csv: %q", ct)
	}
	if !Inline("image/png; charset=x") {
		t.Error("params should be ignored")
	}
}
