// Package bundle carries rangka-os/web and rangka-os/templates inside release binaries, so an installed
// `rangka` (npm, Homebrew, Docker, a downloaded archive) works without the source workspace.
//
// Release builds fill files/ with scripts/bundle-assets.sh before `go build`. Dir extracts a bundled
// directory once into the user cache (keyed by a hash of the bundle) and returns its path, so the rest of
// the kernel keeps working with plain directories.
package bundle

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"sync"
)

//go:embed all:files
var files embed.FS

var (
	once sync.Once
	key  string
)

// Has reports whether the binary carries the named directory (web or templates).
func Has(name string) bool {
	st, err := fs.Stat(files, path.Join("files", name))
	return err == nil && st.IsDir()
}

// Dir returns a directory holding the bundled `name`, extracting it on first use; "" when not bundled
// or when it cannot be written.
func Dir(name string) string {
	if !Has(name) {
		return ""
	}
	once.Do(func() { key = hash() })
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		base = os.TempDir()
	}
	root := filepath.Join(base, "rangka", key)
	dst := filepath.Join(root, name)
	if st, err := os.Stat(filepath.Join(root, "."+name+".ok")); err == nil && !st.IsDir() {
		return dst
	}
	tmp := dst + ".tmp"
	os.RemoveAll(tmp)
	err = fs.WalkDir(files, path.Join("files", name), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := filepath.FromSlash(p[len("files/"+name):])
		out := filepath.Join(tmp, rel)
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		b, err := files.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(out, b, 0o644)
	})
	if err != nil {
		return ""
	}
	os.RemoveAll(dst)
	if err := os.Rename(tmp, dst); err != nil {
		return ""
	}
	os.WriteFile(filepath.Join(root, "."+name+".ok"), nil, 0o644)
	return dst
}

func hash() string {
	h := sha256.New()
	var names []string
	fs.WalkDir(files, "files", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			names = append(names, p)
		}
		return nil
	})
	sort.Strings(names)
	for _, n := range names {
		b, _ := files.ReadFile(n)
		h.Write([]byte(n))
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}
