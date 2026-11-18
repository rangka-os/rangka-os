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
	"errors"
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
// or when it cannot be written. It extracts into the user cache, or into a per-user directory under the
// temp directory when the cache cannot be written (a container user whose HOME is "/" has an unwritable
// ~/.cache).
func Dir(name string) string {
	if !Has(name) {
		return ""
	}
	once.Do(func() { key = hash() })
	for _, base := range bases() {
		if dst := extract(filepath.Join(base, key), name); dst != "" {
			return dst
		}
	}
	return ""
}

// bases returns the parent directories a bundle may be extracted under, best first. Each one exists, is
// a real directory (not a symlink), belongs to the current user and cannot be written by anyone else, so
// another local user cannot plant files the kernel would then serve as the owner app (t_28fae571). The
// temp fallback is per user (rangka-<uid>, created 0700): a shared /tmp/rangka is guessable, and the key
// below it is a hash of the public binary. A base that fails the check is skipped, never repaired.
func bases() []string {
	var dirs []string
	if c, err := os.UserCacheDir(); err == nil && c != "" {
		dirs = append(dirs, filepath.Join(c, "rangka"))
	}
	dirs = append(dirs, filepath.Join(os.TempDir(), tempName()))
	var out []string
	for _, d := range dirs {
		if ensurePrivate(d) == nil {
			out = append(out, d)
		}
	}
	return out
}

// errNotPrivate marks a base directory another user could write into or swap.
var errNotPrivate = errors.New("bundle: directory is not private to the current user")

// ensurePrivate creates dir (0700) when missing and checks it with checkPrivate.
func ensurePrivate(dir string) error {
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return err
	}
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	return checkPrivate(dir)
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
	if st, err := os.Lstat(marker); err == nil && st.Mode().IsRegular() && complete(src, name, dst) {
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
// bundled size. It uses Lstat, so a symlink (to the directory itself or to any entry) never counts: a
// planted link could point anywhere.
func complete(src fs.FS, name, dir string) bool {
	base := path.Join("files", name)
	err := fs.WalkDir(src, base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		st, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(p[len(base):])))
		if err != nil {
			return err
		}
		if d.IsDir() != st.IsDir() || (!d.IsDir() && !st.Mode().IsRegular()) {
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
