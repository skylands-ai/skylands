package socket

import (
	"context"
	"net"
	"os"
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
	if want := "the daemon for island ws is already running on " + path; !strings.Contains(err.Error(), want) {
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
	if want := "a daemon is already running on " + path; !strings.Contains(err.Error(), want) {
		t.Errorf("err = %v, want it to say %q", err, want)
	}
	if _, err := os.Lstat(path); err != nil {
		t.Errorf("the first daemon's socket file is gone: %v", err)
	}
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
