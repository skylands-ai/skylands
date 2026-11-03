//go:build unix

package agents

import (
	"errors"
	"os/exec"
	"syscall"
)

// ownGroup starts cmd in a process group of its own, which whatever the
// agent starts joins unless it leaves on purpose. Stopping the agent can
// then end the whole group, and a terminal's Ctrl-C reaches the daemon
// alone, which stops its agents itself.
func ownGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// signalGroup sends sig to every process in the group cmd leads. A group
// that has already ended is not an error.
func signalGroup(cmd *exec.Cmd, sig syscall.Signal) error {
	if cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

// groupLeft reports whether any process is still in the group cmd leads.
func groupLeft(cmd *exec.Cmd) bool {
	return cmd.Process != nil && syscall.Kill(-cmd.Process.Pid, 0) == nil
}

// leaderGone reports whether cmd's own process has exited and been
// reaped, which Wait does as soon as it exits even while it still waits
// for the process's output pipes to close.
func leaderGone(cmd *exec.Cmd) bool {
	return cmd.Process != nil && errors.Is(syscall.Kill(cmd.Process.Pid, 0), syscall.ESRCH)
}
