package socket

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/skylands-ai/skylands/internal/filelock"
)

// Listen claims the Unix socket at path for a daemon, which does this
// before it starts anything else. A socket file left by a daemon that died
// is replaced. One that a daemon still answers on is not: replacing it
// would cut the CLI off from that daemon. Only the owning user can
// connect.
func Listen(ctx context.Context, path string) (net.Listener, error) {
	// Unix socket paths are limited to about 104 bytes on macOS and 108 on
	// Linux, and the OS error for a long one is unhelpful.
	if len(path) > 100 {
		return nil, fmt.Errorf("socket path %s is %d bytes long; the limit is about 104, so choose a shorter --socket", path, len(path))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create the socket's directory: %w", err)
	}
	// A daemon holds the lock beside its socket while it runs, so of two
	// starting at once, only one goes on to replace a stale socket file.
	lock, err := filelock.Acquire(path + ".lock")
	if held := (*filelock.HeldError)(nil); errors.As(err, &held) {
		return nil, inUse(ctx, path, held.PID)
	}
	if err != nil {
		return nil, err
	}
	ln, err := listen(ctx, path)
	if err != nil {
		_ = lock.Release()
		return nil, err
	}
	return &lockedListener{Listener: ln, lock: lock}, nil
}

// lockedListener releases the socket's lock once the socket is closed, and
// with it, removed.
type lockedListener struct {
	net.Listener
	lock *filelock.Lock
	once sync.Once
}

func (l *lockedListener) Close() error {
	err := l.Listener.Close()
	l.once.Do(func() { _ = l.lock.Release() })
	return err
}

// listen binds the socket at path. Binding fails while the file exists,
// so a daemon of a version that takes no lock, still running on it, is
// found answering rather than replaced.
func listen(ctx context.Context, path string) (net.Listener, error) {
	for range 3 {
		ln, err := net.Listen("unix", path)
		if err == nil {
			if err := os.Chmod(path, 0o600); err != nil {
				_ = ln.Close()
				return nil, fmt.Errorf("protect socket %s: %w", path, err)
			}
			return ln, nil
		}
		if !errors.Is(err, syscall.EADDRINUSE) {
			return nil, fmt.Errorf("listen on %s: %w", path, err)
		}
		if err := clearStale(ctx, path); err != nil {
			return nil, err
		}
	}
	return nil, fmt.Errorf("listen on %s: the socket file keeps coming back; is another daemon starting on it?", path)
}

// clearStale removes the socket file at path if nothing listens on it.
func clearStale(ctx context.Context, path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("%s is not a socket; move it, or choose another --socket", path)
	}
	d := net.Dialer{Timeout: time.Second}
	conn, err := d.DialContext(ctx, "unix", path)
	switch {
	case err == nil:
		_ = conn.Close()
		return inUse(ctx, path, 0)
	case errors.Is(err, syscall.ECONNREFUSED):
		// Nothing listens on it, so the daemon that made it has died.
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("replace stale socket %s: %w", path, err)
		}
		return nil
	default:
		// Something may be listening; leave it be.
		return fmt.Errorf("check socket %s: %w", path, err)
	}
}

// inUse describes the daemon on path, by its island's name when it
// answers, which one still starting does not yet, and by its process ID
// when that is known.
func inUse(ctx context.Context, path string, pid int) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	who := "a daemon"
	if st, err := Dial(path).Status(ctx); err == nil {
		who = "the daemon for island " + st.Host
	}
	if pid > 0 {
		who += fmt.Sprintf(" (process %d)", pid)
	}
	return fmt.Errorf("%s is already running on %s; stop it first", who, path)
}
