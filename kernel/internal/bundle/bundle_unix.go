//go:build unix

package bundle

import (
	"os"
	"strconv"
	"syscall"
)

// tempName is the per-user directory under os.TempDir: the effective uid keeps users apart on a shared /tmp.
func tempName() string { return "rangka-" + strconv.Itoa(os.Geteuid()) }

// checkPrivate reports errNotPrivate unless dir is a real directory (not a symlink), owned by the effective
// user, and not writable by group or others.
func checkPrivate(dir string) error {
	st, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !st.IsDir() || st.Mode().Perm()&0o022 != 0 {
		return errNotPrivate
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok || int64(sys.Uid) != int64(os.Geteuid()) {
		return errNotPrivate
	}
	return nil
}
