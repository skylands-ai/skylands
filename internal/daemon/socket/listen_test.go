package socket

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// serve runs Serve on a socket from Listen until the test ends.
func serve(t *testing.T, path string) {
	t.Helper()
	ln, err := Listen(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, ln, &fakeAPI{}) }()
	t.Cleanup(func() {
		stop()
		if err := <-done; err != nil {
			t.Errorf("Serve returned %v", err)
		}
	})
}

func TestListenReplacesASocketLeftByADaemonThatDied(t *testing.T) {
	path := shortSocket(t)
	// A daemon that dies leaves its socket file with nothing listening.
	old, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	old.SetUnlinkOnClose(false)
	_ = old.Close()
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("no stale socket to replace: %v", err)
	}

	serve(t, path)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if st, err := Dial(path).Status(ctx); err != nil || st.Host != "ws" {
		t.Errorf("Status = %+v, %v; want the new daemon", st, err)
	}
}

func TestListenLeavesASocketADaemonAnswersOn(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	path := shortSocket(t)
	serve(t, path)

	ln, err := Listen(ctx, path)
	if err == nil {
		_ = ln.Close()
		t.Fatal("a second daemon claimed the socket of one that is running")
	}
	if want := fmt.Sprintf("the daemon for island ws (process %d) is already running on %s", os.Getpid(), path); !strings.Contains(err.Error(), want) {
		t.Errorf("err = %v, want it to say %q", err, want)
	}
	// The first daemon still has its socket.
	if st, err := Dial(path).Status(ctx); err != nil || st.Host != "ws" {
		t.Errorf("after the refusal, Status = %+v, %v", st, err)
	}
}

func TestListenLeavesASocketOfADaemonStillStarting(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	path := shortSocket(t)
	// A daemon claims its socket before it is ready to answer on it.
	first, err := Listen(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()

	ln, err := Listen(ctx, path)
	if err == nil {
		_ = ln.Close()
		t.Fatal("a second daemon claimed the socket of one that is starting")
	}
	if want := fmt.Sprintf("a daemon (process %d) is already running on %s", os.Getpid(), path); !strings.Contains(err.Error(), want) {
		t.Errorf("err = %v, want it to say %q", err, want)
	}
	if _, err := os.Lstat(path); err != nil {
		t.Errorf("the first daemon's socket file is gone: %v", err)
	}
}

func TestListenLeavesASocketOfADaemonThatTakesNoLock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	path := shortSocket(t)
	// A daemon of an earlier version listens without the lock.
	old, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = old.Close() }()

	ln, err := Listen(ctx, path)
	if err == nil {
		_ = ln.Close()
		t.Fatal("a daemon claimed the socket of one from an earlier version")
	}
	if want := "a daemon is already running on " + path; !strings.Contains(err.Error(), want) {
		t.Errorf("err = %v, want it to say %q", err, want)
	}
}

func TestOneOfManyDaemonsStartingAtOnceClaimsAStaleSocket(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	path := shortSocket(t)
	stale, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	stale.SetUnlinkOnClose(false)
	_ = stale.Close()

	const daemons = 8
	type claim struct {
		ln  net.Listener
		err error
	}
	start := make(chan struct{})
	claims := make(chan claim, daemons)
	for range daemons {
		go func() {
			<-start
			ln, err := Listen(ctx, path)
			claims <- claim{ln, err}
		}()
	}
	close(start)
	var won []net.Listener
	for range daemons {
		c := <-claims
		switch {
		case c.err == nil:
			won = append(won, c.ln)
		case !strings.Contains(c.err.Error(), "is already running on "+path):
			t.Errorf("a refused daemon got %v", c.err)
		}
	}
	if len(won) != 1 {
		t.Errorf("%d daemons claimed the socket, want 1", len(won))
	}
	for _, ln := range won {
		_ = ln.Close()
	}
}

func TestListenCreatesTheSocketsDirectory(t *testing.T) {
	path := filepath.Join(filepath.Dir(shortSocket(t)), "new", "d.sock")
	ln, err := Listen(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Dir(path)); err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("socket directory: %v, %v; want mode 0700", info.Mode(), err)
	}
	// Closing the socket gives it up for the next daemon.
	_ = ln.Close()
	again, err := Listen(context.Background(), path)
	if err != nil {
		t.Fatalf("after the first closed: %v", err)
	}
	_ = again.Close()
}

func TestListenLeavesAFileThatIsNotASocket(t *testing.T) {
	path := shortSocket(t)
	if err := os.WriteFile(path, []byte("notes"), 0o600); err != nil {
		t.Fatal(err)
	}
	ln, err := Listen(context.Background(), path)
	if err == nil {
		_ = ln.Close()
		t.Fatal("Listen replaced a file that is not a socket")
	}
	if !strings.Contains(err.Error(), "is not a socket") {
		t.Errorf("err = %v", err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "notes" {
		t.Errorf("the file is now %q, %v", data, err)
	}
}

func TestListenRefusesAPathTooLongForASocket(t *testing.T) {
	path := "/tmp/" + strings.Repeat("s", 100) + ".sock"
	if _, err := Listen(context.Background(), path); err == nil || !strings.Contains(err.Error(), "choose a shorter --socket") {
		t.Errorf("err = %v", err)
	}
}
