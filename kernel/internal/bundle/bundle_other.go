//go:build !unix

package bundle

import "os"

// tempName is the directory under os.TempDir. On Windows the temp directory is already per user
// (%LOCALAPPDATA%\Temp), so no uid is needed.
func tempName() string { return "rangka" }

// checkPrivate reports errNotPrivate unless dir is a real directory (not a symlink). Ownership is left to
// the per-user temp and cache directories' ACLs.
func checkPrivate(dir string) error {
	st, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return errNotPrivate
	}
	return nil
}

// lockRoot is a no-op here: extract stays correct without it (every copy is verified before it is served),
// only two concurrent repairs of a broken cache can briefly race.
func lockRoot(string) func() { return func() {} }
