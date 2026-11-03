//go:build unix

package agents

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/skylands-ai/skylands/internal/fakeagent"
)

// alive reports whether pid is a running process: one that exists and, on
// Linux, is not a zombie left for its new parent to reap.
func alive(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	if b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid)); err == nil {
		if i := bytes.LastIndexByte(b, ')'); i >= 0 && i+2 < len(b) && b[i+2] == 'Z' {
			return false
		}
	}
	return true
}

// gone waits up to within for pid to stop running.
func gone(pid int, within time.Duration) bool {
	for deadline := time.Now().Add(within); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if !alive(pid) {
			return true
		}
	}
	return !alive(pid)
}

// childOf reads the PID of the child the fake agent started.
func childOf(t *testing.T, path string) int {
	t.Helper()
	for i := 0; i < 250; i++ {
		if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
			pid, err := strconv.Atoi(string(b))
			if err != nil {
				t.Fatal(err)
			}
			// Whatever happens below, nothing is left behind.
			t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
			return pid
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the fake agent never started its child")
	return 0
}

// agentWithChild is the fake agent, starting a child the way a shell tool
// leaves a command running, plus extra environment.
func agentWithChild(t *testing.T, extra ...string) (Adapter, string) {
	cmd, env := fakeagent.TestCommand()
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	env = append(env, fakeagent.EnvChild+"="+pidFile)
	return Adapter{Name: "fake", Command: cmd, Env: append(env, extra...)}, pidFile
}

func TestClosingAnAgentEndsWhatItStarted(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ad, pidFile := agentWithChild(t)
	r, err := Start(ctx, ad, t.TempDir(), "", quietObserver{})
	if err != nil {
		t.Fatal(err)
	}
	child := childOf(t, pidFile)
	leader := r.transport.cmd.Process.Pid
	if !alive(child) {
		t.Fatal("the child is not running")
	}
	_ = r.Close()
	if !gone(leader, 5*time.Second) {
		t.Errorf("the agent %d is still running", leader)
	}
	if !gone(child, 5*time.Second) {
		t.Errorf("the agent's child %d outlived the agent", child)
	}
}

func TestClosingAHungAgentEndsItAndWhatItStarted(t *testing.T) {
	old := stopWait
	stopWait = 300 * time.Millisecond
	t.Cleanup(func() { stopWait = old })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ad, pidFile := agentWithChild(t, fakeagent.EnvHang+"=1")
	r, err := Start(ctx, ad, t.TempDir(), "", quietObserver{})
	if err != nil {
		t.Fatal(err)
	}
	child := childOf(t, pidFile)
	leader := r.transport.cmd.Process.Pid
	t.Cleanup(func() { _ = syscall.Kill(leader, syscall.SIGKILL) })
	_ = r.Close()
	if !gone(leader, 5*time.Second) {
		t.Errorf("the hung agent %d is still running", leader)
	}
	if !gone(child, 5*time.Second) {
		t.Errorf("the hung agent's child %d outlived it", child)
	}
}

func TestCancellingAnAgentEndsWhatItStarted(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	runCtx, stop := context.WithCancel(ctx)
	ad, pidFile := agentWithChild(t)
	r, err := Start(runCtx, ad, t.TempDir(), "", quietObserver{})
	if err != nil {
		t.Fatal(err)
	}
	child := childOf(t, pidFile)
	leader := r.transport.cmd.Process.Pid
	// The daemon stopping cancels the context every agent runs under.
	stop()
	if !gone(leader, 10*time.Second) {
		t.Errorf("the agent %d is still running", leader)
	}
	if !gone(child, 10*time.Second) {
		t.Errorf("the agent's child %d outlived the agent", child)
	}
	_ = r.Close()
}
