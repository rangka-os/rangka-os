//go:build unix

package docprint

import (
	"os/exec"
	"syscall"
)

// killTree runs the command in its own process group and kills the whole group on cancel, so Chromium's
// helper processes do not outlive a timeout.
func killTree(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}
