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
// or when it cannot be written. It extracts into the user cache, or into the temp directory when the cache
// cannot be written (a container user whose HOME is "/" has an unwritable ~/.cache).
func Dir(name string) string {
	if !Has(name) {
		return ""
	}
	once.Do(func() { key = hash() })
	var bases []string
	if base, err := os.UserCacheDir(); err == nil && base != "" {
		bases = append(bases, base)
	}
	bases = append(bases, os.TempDir())
	for _, base := range bases {
		if dst := extract(filepath.Join(base, "rangka", key), name); dst != "" {
			return dst
		}
	}
	return ""
}

// extract writes the bundled `name` to root/name and returns it; "" when it cannot.
func extract(root, name string) string { return extractFrom(files, root, name) }

// extractFrom extracts src's files/<name> to root/name. The marker root/.name.ok is trusted only when the
// directory still holds every bundled file, so a cache left incomplete (an interrupted run, or several
// kernels starting at once on the same cache) is extracted again instead of served half empty. Each run
// extracts into its own temp directory, verifies it, and only then renames it into place and writes the
// marker, so concurrent runs never write into or delete each other's copy.
func extractFrom(src fs.FS, root, name string) string {
	dst := filepath.Join(root, name)
	marker := filepath.Join(root, "."+name+".ok")
	if st, err := os.Stat(marker); err == nil && !st.IsDir() && complete(src, name, dst) {
		return dst
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return ""
	}
	done := func() string {
		if writeMarker(root, marker) != nil {
			return ""
		}
		return dst
	}
	for attempt := 0; attempt < 5; attempt++ {
		if complete(src, name, dst) { // another run finished first, or only the marker was missing
			return done()
		}
		tmp, err := os.MkdirTemp(root, "."+name+".tmp-")
		if err != nil {
			return ""
		}
		if err := copyTree(src, name, tmp); err != nil || !complete(src, name, tmp) {
			os.RemoveAll(tmp)
			return ""
		}
		// rename fails while dst is a non-empty directory, so a copy another run just put in place stays
		if os.Rename(tmp, dst) == nil {
			if complete(src, name, dst) {
				return done()
			}
			continue
		}
		if complete(src, name, dst) {
			os.RemoveAll(tmp)
			return done()
		}
		// dst is broken (the t_981624a8 cache without app/): move it aside and put the verified copy in
		old := tmp + ".old"
		os.Remove(marker)
		os.Rename(dst, old)
		err = os.Rename(tmp, dst)
		os.RemoveAll(old)
		os.RemoveAll(tmp)
		if err == nil && complete(src, name, dst) {
			return done()
		}
	}
	return ""
}

// copyTree writes src's files/<name> into dir.
func copyTree(src fs.FS, name, dir string) error {
	base := path.Join("files", name)
	return fs.WalkDir(src, base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		out := filepath.Join(dir, filepath.FromSlash(p[len(base):]))
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		b, err := fs.ReadFile(src, p)
		if err != nil {
			return err
		}
		return os.WriteFile(out, b, 0o644)
	})
}

// complete reports whether dir holds every directory and file of src's files/<name>, each file with the
// bundled size.
func complete(src fs.FS, name, dir string) bool {
	base := path.Join("files", name)
	err := fs.WalkDir(src, base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		st, err := os.Stat(filepath.Join(dir, filepath.FromSlash(p[len(base):])))
		if err != nil {
			return err
		}
		if d.IsDir() != st.IsDir() {
			return fs.ErrInvalid
		}
		if !d.IsDir() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			if info.Size() != st.Size() {
				return fs.ErrInvalid
			}
		}
		return nil
	})
	return err == nil
}

// writeMarker writes the marker through a temp file and a rename, so it never exists half written.
func writeMarker(root, marker string) error {
	f, err := os.CreateTemp(root, ".marker-")
	if err != nil {
		return err
	}
	f.Close()
	if err := os.Rename(f.Name(), marker); err != nil {
		os.Remove(f.Name())
		return err
	}
	return nil
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
