package cli

import (
	"os"
	"testing"

	"github.com/skylands-ai/skylands/internal/watchdog"
)

// TestMain lets this test binary stand in for the watchdog that a daemon
// started by a test runs, as the skylands binary does when a daemon starts
// it. Without it, the daemon's watchdog would run these tests again.
func TestMain(m *testing.M) {
	watchdog.MainHook()
	os.Exit(m.Run())
}
