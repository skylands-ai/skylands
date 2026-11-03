//go:build unix

package watchdog

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	MainHook()
	os.Exit(m.Run())
}

// group starts a process that sleeps, in a process group of its own, the
// way an agent runs.
func group(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("sleep", "300")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// Reaped as soon as it exits, so an ended group really is gone.
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); <-done })
	return cmd.Process.Pid
}

func exists(pgid int) bool { return syscall.Kill(-pgid, 0) == nil }

func waitGone(pgid int) bool {
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if !exists(pgid) {
			return true
		}
	}
	return false
}

func TestServeEndsTheGroupsStillRegistered(t *testing.T) {
	registered, ended, other := group(t), group(t), group(t)
	r, w := io.Pipe()
	done := make(chan struct{})
	go func() { serve(r); close(done) }()
	fmt.Fprintf(w, "+%d\n+%d\n-%d\n", registered, ended, ended)
	// The daemon is gone.
	_ = w.Close()
	<-done
	if !waitGone(registered) {
		t.Errorf("group %d, still registered, was not ended", registered)
	}
	if !exists(ended) {
		t.Errorf("group %d, which had been unregistered, was ended", ended)
	}
	if !exists(other) {
		t.Errorf("group %d, never registered, was ended", other)
	}
}

func TestStartRefusesABinaryThatIsNotAWatchdog(t *testing.T) {
	start := time.Now()
	if _, err := Start("/bin/true"); err == nil {
		t.Error("/bin/true started as a watchdog")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("refusing took %s", d)
	}
}

func TestStartAndClose(t *testing.T) {
	w, err := Start(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	pg := group(t)
	w.Started(pg)
	w.Ended(pg)
	if err := w.Close(); err != nil {
		t.Errorf("Close = %v", err)
	}
	// A daemon that stops cleanly has ended its groups, so nothing else is.
	if !exists(pg) {
		t.Error("a group the daemon had unregistered was ended when it closed the watchdog")
	}
}
