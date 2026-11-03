package socket

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"time"
)

// Listen claims the Unix socket at path for a daemon, which does this
// before it starts anything else. A socket file left by a daemon that died
// is replaced. One that a daemon still answers on is not: replacing it
// would cut the CLI off from that daemon, and with the same island name
// and data directory, the two would resume the same sessions. Only the
// owning user can connect.
func Listen(ctx context.Context, path string) (net.Listener, error) {
	// Unix socket paths are limited to about 104 bytes on macOS and 108 on
	// Linux, and the OS error for a long one is unhelpful.
	if len(path) > 100 {
		return nil, fmt.Errorf("socket path %s is %d bytes long; the limit is about 104, so choose a shorter --socket", path, len(path))
	}
	// Binding fails while the file exists, so of two daemons starting at
	// once on a fresh path, one binds and the other finds it answering.
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
		return inUse(ctx, path)
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

// inUse describes the daemon listening on path, by its island's name when
// it answers. One that is still starting has not begun to.
func inUse(ctx context.Context, path string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if st, err := Dial(path).Status(ctx); err == nil {
		return fmt.Errorf("the daemon for island %s is already running on %s; stop it first", st.Host, path)
	}
	return fmt.Errorf("a daemon is already running on %s; stop it first", path)
}
