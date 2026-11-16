package cli

import (
	"os"
	"testing"

	"github.com/skylands-ai/skylands/internal/fakeagent"
)

// TestMain lets this test binary stand in for the watchdog that a daemon
// started by a test runs, as the skylands binary does when a daemon starts
// it, and for the fake agent its sessions run. Without it, the daemon's
// watchdog and agents would run these tests again.
func TestMain(m *testing.M) {
	fakeagent.TestMainHook()
	os.Exit(m.Run())
}
