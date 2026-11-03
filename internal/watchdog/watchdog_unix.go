//go:build unix

package watchdog

import (
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

const supported = true

// ownGroup keeps the watchdog out of the daemon's process group, so a
// terminal's Ctrl-C or hangup, which goes to the daemon's group, does not
// reach it.
func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// ignoreSignals keeps the watchdog running until the daemon is gone, even
// when the signal that stops the daemon reaches it too, as "pkill
// skylands" would.
func ignoreSignals() {
	signal.Ignore(syscall.SIGINT, syscall.SIGHUP, syscall.SIGTERM)
}

// endGroups sends SIGTERM to every group, waits up to Grace for them to
// empty, and sends SIGKILL to those that have not.
func endGroups(groups map[int]bool) {
	left := func() []int {
		var out []int
		for pg := range groups {
			if syscall.Kill(-pg, 0) == nil {
				out = append(out, pg)
			}
		}
		return out
	}
	for _, pg := range left() {
		_ = syscall.Kill(-pg, syscall.SIGTERM)
	}
	for deadline := time.Now().Add(Grace); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if len(left()) == 0 {
			return
		}
	}
	for _, pg := range left() {
		_ = syscall.Kill(-pg, syscall.SIGKILL)
	}
}
