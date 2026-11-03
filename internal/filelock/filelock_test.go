//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package filelock

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// envHold makes the test binary a process that takes the lock at the path
// it names, says so, and holds it until it is killed.
const envHold = "SKYLANDS_TEST_HOLD_LOCK"

func TestMain(m *testing.M) {
	if path := os.Getenv(envHold); path != "" {
		if _, err := Acquire(path); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		fmt.Println("held")
		for {
			time.Sleep(time.Hour)
		}
	}
	os.Exit(m.Run())
}

func TestASecondAcquireIsRefusedUntilTheFirstIsReleased(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	first, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Acquire(path)
	var held *HeldError
	if !errors.As(err, &held) || held.PID != os.Getpid() {
		t.Fatalf("second Acquire: err = %v, want it held by this process", err)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	again, err := Acquire(path)
	if err != nil {
		t.Fatalf("after Release: %v", err)
	}
	_ = again.Release()
}

func TestALockIsFreedWhenItsHolderIsKilled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), envHold+"="+path)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	if line, _ := bufio.NewReader(out).ReadString('\n'); line != "held\n" {
		t.Fatalf("holder said %q", line)
	}

	_, err = Acquire(path)
	var held *HeldError
	if !errors.As(err, &held) || held.PID != cmd.Process.Pid {
		t.Fatalf("while held: err = %v, want it held by process %d", err, cmd.Process.Pid)
	}
	_ = cmd.Process.Signal(syscall.SIGKILL)
	_ = cmd.Wait()
	l, err := Acquire(path)
	if err != nil {
		t.Fatalf("after the holder was killed: %v", err)
	}
	_ = l.Release()
}
