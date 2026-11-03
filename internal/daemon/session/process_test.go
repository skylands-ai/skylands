//go:build unix

package session

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

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/daemon/agents"
	"github.com/skylands-ai/skylands/internal/fakeagent"
)

// running reports whether pid is a live process, not one that has exited,
// counting a zombie left for its new parent to reap as exited.
func running(pid int) bool {
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

func TestStoppingTheDaemonEndsAgentsAndWhatTheyStarted(t *testing.T) {
	f := setup(t)
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	ad := f.cfg.Registry["fake"]
	ad.Env = append(append([]string{}, ad.Env...), fakeagent.EnvChild+"="+pidFile)
	f.cfg.Registry = agents.Registry{"fake": ad}

	ctx1, stop1 := context.WithCancel(f.ctx)
	defer stop1()
	m1, err := Load(ctx1, f.store, f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m1.Start(f.ctx, v1.StartSession{ID: "c1", SessionID: "s1", RepoID: f.repo.ID, Agent: "fake", Prompt: "keep going"}); err != nil {
		t.Fatal(err)
	}
	waitPending(t, m1, "s1", 1)
	b, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	child, err := strconv.Atoi(string(b))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(child, syscall.SIGKILL) })
	if !running(child) {
		t.Fatal("the agent's child is not running")
	}

	// The daemon stops: once Wait returns, the agent and what it started
	// are gone, not left for the daemon's exit to end.
	stop1()
	m1.Wait()
	for deadline := time.Now().Add(2 * time.Second); running(child) && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
	}
	if running(child) {
		t.Errorf("the agent's child %d is still running after the daemon stopped", child)
	}

	// The session stays open, to be resumed, rather than failed.
	ctx2, stop2 := context.WithCancel(f.ctx)
	m2, err := Load(ctx2, f.store, f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { stop2(); m2.Wait() }()
	if s, _ := m2.Get("s1"); s.Status != StatusRunning {
		t.Errorf("after the daemon stopped, the session is %s, want running", s.Status)
	}
	waitResumed(t, m2, "s1", 1)
}
