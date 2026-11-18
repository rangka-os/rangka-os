//go:build unix

package bundle

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// unwritableCache points the user cache at a path under a regular file, so bases() falls back to the temp
// directory, and points TMPDIR at a fresh directory it returns.
func unwritableCache(t *testing.T) string {
	t.Helper()
	ro := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(ro, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CACHE_HOME", ro)
	t.Setenv("HOME", ro) // macOS: the user cache is $HOME/Library/Caches
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	return tmp
}

// t_28fae571: the temp fallback is per user (rangka-<euid>) and created 0700, not a shared /tmp/rangka.
func TestBasesTempFallbackIsPerUser(t *testing.T) {
	tmp := unwritableCache(t)
	got := bases()
	want := filepath.Join(tmp, "rangka-"+strconv.Itoa(os.Geteuid()))
	if len(got) != 1 || got[0] != want {
		t.Fatalf("bases = %v, want [%s]", got, want)
	}
	st, err := os.Lstat(want)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o700 {
		t.Fatalf("mode = %v, want 0700", st.Mode().Perm())
	}
}

// t_28fae571: a fallback dir planted before the kernel starts (a symlink to the attacker's tree, or a
// directory others can write) is skipped, so nothing in it is ever served.
func TestBasesRejectsPlantedFallback(t *testing.T) {
	for _, tc := range []struct {
		name  string
		plant func(t *testing.T, dir string)
	}{
		{"symlink", func(t *testing.T, dir string) {
			evil := t.TempDir()
			if err := os.Symlink(evil, dir); err != nil {
				t.Fatal(err)
			}
		}},
		{"world writable", func(t *testing.T, dir string) {
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(dir, 0o777); err != nil {
				t.Fatal(err)
			}
		}},
		{"group writable", func(t *testing.T, dir string) {
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(dir, 0o770); err != nil {
				t.Fatal(err)
			}
		}},
		{"regular file", func(t *testing.T, dir string) {
			if err := os.WriteFile(dir, nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmp := unwritableCache(t)
			tc.plant(t, filepath.Join(tmp, tempName()))
			if got := bases(); len(got) != 0 {
				t.Fatalf("bases = %v, want none", got)
			}
		})
	}
}

// A fallback dir owned by another user is skipped: "/" (owned by root, not writable by others) when the
// test runs as a normal user, a chowned directory when it runs as root.
func TestCheckPrivateRejectsForeignOwner(t *testing.T) {
	if os.Geteuid() != 0 {
		if err := checkPrivate("/"); err == nil {
			t.Fatal("a directory owned by root must not count as private to a normal user")
		}
		return
	}
	dir := filepath.Join(t.TempDir(), "foreign")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(dir, 65534, 65534); err != nil {
		t.Fatal(err)
	}
	if err := checkPrivate(dir); err == nil {
		t.Fatal("a directory owned by another user must not count as private")
	}
}

// t_28fae571 (S8): a cache whose web is a symlink to a complete tree elsewhere is not trusted; the next
// extract replaces the link with a real, verified directory.
func TestExtractReplacesSymlinkedDir(t *testing.T) {
	src := fakeWeb()
	elsewhere := extractFrom(src, t.TempDir(), "web")
	root := t.TempDir()
	dst := filepath.Join(root, "web")
	if err := os.Symlink(elsewhere, dst); err != nil {
		t.Fatal(err)
	}
	if err := writeMarker(root, filepath.Join(root, ".web.ok")); err != nil {
		t.Fatal(err)
	}
	if complete(src, "web", dst) {
		t.Fatal("a symlinked web must not count as complete")
	}
	if got := extractFrom(src, root, "web"); got != dst {
		t.Fatalf("extract = %q, want %q", got, dst)
	}
	st, err := os.Lstat(dst)
	if err != nil || !st.IsDir() {
		t.Fatalf("web after repair: %v %v, want a real directory", st, err)
	}
	mustExist(t, elsewhere, "app/index.html") // the link target is left alone
}

// t_28fae571 (S7 with a link): a single file swapped for a symlink to same-sized content is not trusted.
func TestExtractReplacesSymlinkedFile(t *testing.T) {
	src := fakeWeb()
	root := t.TempDir()
	d := extractFrom(src, root, "web")
	evil := filepath.Join(t.TempDir(), "evil.js")
	if err := os.WriteFile(evil, []byte(strings.Repeat("x", len("console.log('app')"))), 0o644); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(d, "app", "assets", "app.js")
	if err := os.Remove(f); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(evil, f); err != nil {
		t.Fatal(err)
	}
	if complete(src, "web", d) {
		t.Fatal("a symlinked file must not count as complete")
	}
	if extractFrom(src, root, "web") != d {
		t.Fatal("re-extract failed")
	}
	if b, _ := os.ReadFile(f); string(b) != "console.log('app')" {
		t.Fatalf("app.js = %q", b)
	}
}
