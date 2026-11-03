//go:build unix

package agents

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/skylands-ai/skylands/internal/fakeagent"
	"github.com/skylands-ai/skylands/internal/watchdog"
)

// envCrasher, set to a file path, makes TestCrashHelper a stand-in daemon
// whose agent writes its child's PID to that file.
const envCrasher = "SKYLANDS_TEST_CRASHER"

// TestCrashHelper is not a test of its own. Run by
// TestADaemonsDeathEndsWhatItsAgentsStarted, it stands in for a daemon:
// it starts the watchdog and an agent that starts a child, and waits to
// be killed.
func TestCrashHelper(t *testing.T) {
	pidFile := os.Getenv(envCrasher)
	if pidFile == "" {
		t.Skip("runs only as a stand-in daemon")
	}
	wd, err := watchdog.Start(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithWatch(context.Background(), wd)
	cmd, env := fakeagent.TestCommand()
	ad := Adapter{Name: "fake", Command: cmd, Env: append(env, fakeagent.EnvChild+"="+pidFile)}
	if _, err := Start(ctx, ad, filepath.Dir(pidFile), "", quietObserver{}); err != nil {
		t.Fatal(err)
	}
	for {
		time.Sleep(time.Hour)
	}
}

func TestADaemonsDeathEndsWhatItsAgentsStarted(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	daemon := exec.Command(os.Args[0], "-test.run=^TestCrashHelper$")
	daemon.Env = append(os.Environ(), envCrasher+"="+pidFile)
	daemon.Stderr = os.Stderr
	if err := daemon.Start(); err != nil {
		t.Fatal(err)
	}
	child := childOf(t, pidFile)
	if !alive(child) {
		t.Fatal("the agent's child is not running")
	}
	// The daemon dies the hardest way, with no chance to end anything.
	_ = daemon.Process.Kill()
	_ = daemon.Wait()
	if !gone(child, watchdog.Grace+3*time.Second) {
		t.Errorf("the agent's child %d outlived its daemon", child)
	}
}
