//go:build !unix

package agents

import (
	"os/exec"
	"syscall"
)

// Without Unix process groups, an agent is its own process alone.

func ownGroup(*exec.Cmd) {}

func signalGroup(cmd *exec.Cmd, sig syscall.Signal) error {
	if cmd.Process == nil {
		return nil
	}
	if sig == syscall.SIGKILL {
		return cmd.Process.Kill()
	}
	return cmd.Process.Signal(sig)
}

func groupLeft(*exec.Cmd) bool { return false }

func leaderGone(*exec.Cmd) bool { return false }
