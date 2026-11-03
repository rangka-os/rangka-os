//go:build !unix

package docprint

import "os/exec"

// killTree is a no-op where process groups are not available; only the direct child is killed.
func killTree(cmd *exec.Cmd) {}
