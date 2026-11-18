//go:build unix

package clone

import (
	"os/exec"
	"syscall"
)

// detach runs cmd in a session of its own, with no controlling terminal,
// so neither git nor ssh can ask on the daemon's terminal, and ends the
// whole group, ssh and git's helpers too, when the clone is cancelled.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}
