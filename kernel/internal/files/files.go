// Package files is the kernel's blob store for attachments: content-addressed files on local disk,
// one area per organisation (contract 0011).
package files

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Store keeps blobs by organisation and sha256.
type Store interface {
	// Put streams r into the org's area and returns the hex sha256 and size. It writes to <root>/<org>/tmp
	// and renames, so a failed upload leaves no blob.
	Put(org string, r io.Reader) (sum string, size int64, err error)
	// Open returns the blob; the error satisfies errors.Is(err, os.ErrNotExist) when it is missing.
	Open(org, sum string) (io.ReadSeekCloser, error)
	// Delete removes the blob; a missing blob is not an error.
	Delete(org, sum string) error
	Root() string
}

var (
	orgRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	sumRe = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Local stores blobs under a directory: <root>/<orgdir>/<sum[0:2]>/<sum>, where orgdir is the org id when it is a slug, else "_"+16 hex of sha256(org).
type Local struct{ root string }

// NewLocal creates the root directory (0750) and returns a store for it.
func NewLocal(root string) (*Local, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o750); err != nil {
		return nil, err
	}
	return &Local{root: abs}, nil
}

// Root returns the absolute root directory.
func (l *Local) Root() string { return l.root }

// orgDir maps an org id to its directory name: the id itself when it is a plain slug, otherwise
// "_" + the first 16 hex digits of sha256(org). The mapping never fails for a non-empty id.
func orgDir(org string) (string, error) {
	if org == "" {
		return "", fmt.Errorf("files: empty org id")
	}
	if orgRe.MatchString(org) {
		return org, nil
	}
	h := sha256.Sum256([]byte(org))
	return "_" + hex.EncodeToString(h[:])[:16], nil
}

func (l *Local) blobPath(org, sum string) (string, error) {
	dir, err := orgDir(org)
	if err != nil {
		return "", err
	}
	if !sumRe.MatchString(sum) {
		return "", fmt.Errorf("files: invalid sha256 %q", sum)
	}
	return filepath.Join(l.root, dir, sum[:2], sum), nil
}

// Put implements Store.
func (l *Local) Put(org string, r io.Reader) (string, int64, error) {
	dir, err := orgDir(org)
	if err != nil {
		return "", 0, err
	}
	tmpDir := filepath.Join(l.root, dir, "tmp")
	if err := os.MkdirAll(tmpDir, 0o750); err != nil {
		return "", 0, err
	}
	f, err := os.CreateTemp(tmpDir, "up-*")
	if err != nil {
		return "", 0, err
	}
	tmp := f.Name()
	done := false
	defer func() {
		if !done {
			f.Close()
			os.Remove(tmp)
		}
	}()
	h := sha256.New()
	size, err := io.Copy(io.MultiWriter(f, h), r)
	if err != nil {
		return "", 0, err
	}
	if err := f.Close(); err != nil {
		return "", 0, err
	}
	sum := hex.EncodeToString(h.Sum(nil))
	dst, err := l.blobPath(org, sum)
	if err != nil {
		return "", 0, err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return "", 0, err
	}
	if err := os.Chmod(tmp, 0o640); err != nil {
		return "", 0, err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return "", 0, err
	}
	done = true
	return sum, size, nil
}

// Open implements Store.
func (l *Local) Open(org, sum string) (io.ReadSeekCloser, error) {
	p, err := l.blobPath(org, sum)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	return f, nil
}

// Delete implements Store.
func (l *Local) Delete(org, sum string) error {
	p, err := l.blobPath(org, sum)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// SafeFilename keeps only the base name, drops control and format characters (such as U+202E) and path
// separators, and cuts it to 200 bytes on a rune boundary. An empty result becomes "lampiran".
func SafeFilename(name string) string {
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, name)
	name = path.Base(strings.ReplaceAll(name, `\`, "/"))
	name = strings.ReplaceAll(name, "/", "")
	if len(name) > 200 {
		cut := 200
		for cut > 0 && !utf8.RuneStart(name[cut]) {
			cut--
		}
		name = name[:cut]
	}
	if name == "" || name == "." || name == ".." {
		return "lampiran"
	}
	return name
}

// SniffType detects the content type from the first bytes. When that is only application/octet-stream or
// text/plain, the filename's extension decides if it maps to a known type, except that an extension never
// promotes such bytes to a type shown inline (plain text named x.png stays text/plain).
func SniffType(head []byte, filename string) string {
	ct := http.DetectContentType(head)
	if ct == "application/octet-stream" || strings.HasPrefix(ct, "text/plain") {
		if t := mime.TypeByExtension(strings.ToLower(filepath.Ext(filename))); t != "" && !Inline(t) {
			return t
		}
	}
	return ct
}

// Inline reports whether a type is safe to show in the browser (not downloaded): common images and PDF.
func Inline(contentType string) bool {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	switch mt {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "application/pdf":
		return true
	}
	return false
}
