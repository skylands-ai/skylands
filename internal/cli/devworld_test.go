package cli

import (
	"bytes"
	"context"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// lockedBuffer is a buffer the devworld and the test can share.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().String()
}

// TestDevworldSetsUpAtItsURL runs a devworld given the address people open
// it at, whose setup link names that address rather than the one it
// listens on.
func TestDevworldSetsUpAtItsURL(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var stderr lockedBuffer
	o := devworldOptions{
		dataDir: t.TempDir(), httpAddr: freeAddr(t), natsListen: freeAddr(t), leafListen: freeAddr(t),
		host: "box", socketPath: filepath.Join(shortDir(t), "d.sock"), url: "https://box.example.ts.net",
	}
	done := make(chan error, 1)
	go func() { done <- runDevworld(ctx, o, &stderr) }()
	for !strings.Contains(stderr.String(), "https://box.example.ts.net/setup#") {
		select {
		case err := <-done:
			t.Fatalf("devworld stopped: %v; it printed %q", err, stderr.String())
		case <-ctx.Done():
			t.Fatalf("no setup link at the URL; it printed %q", stderr.String())
		case <-time.After(50 * time.Millisecond):
		}
	}
	if strings.Contains(stderr.String(), o.httpAddr) {
		t.Errorf("the setup link names the listen address: %q", stderr.String())
	}
	cancel()
	if err := <-done; err != nil && ctx.Err() == nil {
		t.Errorf("devworld returned %v", err)
	}
}

func TestDevworldRefusesABadURL(t *testing.T) {
	code, _, stderr := run(t, "devworld", "up", "--url", "box.example.ts.net:8080", "--data-dir", t.TempDir(), "--socket", filepath.Join(shortDir(t), "d.sock"))
	if code != 1 || !strings.Contains(stderr, "starting http:// or https://") {
		t.Errorf("exit %d, stderr %q; want a refusal naming what --url needs", code, stderr)
	}
}
