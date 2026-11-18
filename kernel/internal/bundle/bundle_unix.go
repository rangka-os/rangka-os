//go:build unix

package bundle

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
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

// lockWait bounds how long lockRoot waits for another run's lock; past it the extract goes on unlocked,
// which is still correct (every copy is verified before it is served), only a repair race is possible.
var lockWait = 30 * time.Second

// lockRoot takes an exclusive flock on root/.lock and returns its release. When the lock cannot be taken
// (a filesystem without flock, or a holder that stopped) it returns a no-op after lockWait.
func lockRoot(root string) func() {
	f, err := os.OpenFile(filepath.Join(root, ".lock"), os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return func() {}
	}
	deadline := time.Now().Add(lockWait)
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				f.Close()
			}
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) || time.Now().After(deadline) {
			f.Close()
			return func() {}
		}
		time.Sleep(5 * time.Millisecond)
	}
}
