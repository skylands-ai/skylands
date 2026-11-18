// Package clone checks what a browser asks to add as a repository, and
// clones one onto the host. A browser names only directories inside the
// daemon's clone root, and clones only over https or ssh, since git's other
// transports run commands or read the host's files.
package clone

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var (
	// ErrOutside is a path outside the clone root.
	ErrOutside = errors.New("outside the clone root")
	// ErrURL is a URL the daemon doesn't clone from.
	ErrURL = errors.New("clone from an https or ssh URL")
)

// Within resolves path, relative to root or absolute, to an absolute path
// inside root, or root itself, and refuses one outside it. ~ is the home
// directory. Symbolic links are followed as far as the path exists, so a
// link inside root can't lead out of it, and the path returned has none.
func Within(root, path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("a path is required")
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, path[1:])
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	realRoot, err := resolve(root)
	if err != nil {
		return "", err
	}
	real, err := resolve(path)
	if err != nil {
		return "", err
	}
	if !Inside(realRoot, real) {
		return "", fmt.Errorf("%s is %w, %s", path, ErrOutside, realRoot)
	}
	return real, nil
}

// Inside reports whether path is root or inside it. Both are clean and
// absolute.
func Inside(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// resolve makes path absolute and clean, and follows the symbolic links in
// as much of it as exists. What doesn't exist yet is kept as named.
func resolve(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	var rest []string
	for dir := abs; ; dir = filepath.Dir(dir) {
		real, err := filepath.EvalSymlinks(dir)
		if err == nil {
			return filepath.Join(append([]string{real}, rest...)...), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if filepath.Dir(dir) == dir {
			return abs, nil
		}
		rest = append([]string{filepath.Base(dir)}, rest...)
	}
}

// CheckURL refuses a URL the daemon doesn't clone from. It takes https and
// ssh URLs, and git's short form of ssh, user@host:path. An https URL
// can't carry a user name or password, which would be kept in the
// island's record; the host's own git credentials are used.
func CheckURL(raw string) error {
	if raw == "" {
		return errors.New("a clone URL is required")
	}
	if strings.TrimSpace(raw) != raw || strings.ContainsFunc(raw, func(r rune) bool { return r <= ' ' || r == 0x7f }) {
		return errors.New("a clone URL has no spaces or control characters")
	}
	if strings.HasPrefix(raw, "-") {
		return ErrURL
	}
	if scheme, _, ok := strings.Cut(raw, "://"); ok {
		u, err := url.Parse(raw)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrURL, err)
		}
		switch {
		case scheme != "https" && scheme != "ssh":
			return ErrURL
		case u.Hostname() == "" || strings.HasPrefix(u.Hostname(), "-"):
			return fmt.Errorf("%w: it names no host", ErrURL)
		case u.User != nil && scheme == "https":
			return errors.New("an https clone URL can't carry a user name or password")
		case u.User != nil:
			if _, has := u.User.Password(); has {
				return errors.New("an ssh clone URL can't carry a password")
			}
		}
		return nil
	}
	return checkShortSSH(raw)
}

// checkShortSSH takes git's scp-like form, [user@]host:path, which git
// reads as ssh when the part before the first colon has no slash. Its
// transport::address form, such as ext::, has another colon right after
// the first, and is refused.
func checkShortSSH(raw string) error {
	host, path, ok := strings.Cut(raw, ":")
	if !ok || strings.Contains(host, "/") || path == "" || strings.HasPrefix(path, ":") {
		return ErrURL
	}
	if user, h, ok := strings.Cut(host, "@"); ok {
		if user == "" || strings.ContainsAny(user, ":@") {
			return ErrURL
		}
		host = h
	}
	if host == "" || !alnum(host[0]) {
		return fmt.Errorf("%w: it names no host", ErrURL)
	}
	for _, r := range host {
		if !alnum(byte(r)) && r != '.' && r != '-' && r != '_' {
			return fmt.Errorf("%w: %q isn't a host name", ErrURL, host)
		}
	}
	return nil
}

func alnum(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

// Partial is where a clone into dir works until it is complete: a
// directory beside dir, named by the clone's id, which only that clone
// uses.
func Partial(dir, id string) string {
	return filepath.Join(filepath.Dir(dir), ".skylands-clone-"+id)
}

// Watch is told about the process group of each clone, as agents.Watch
// is, so the watchdog ends it if the daemon dies first.
type Watch interface {
	Started(pgid int)
	Ended(pgid int)
}

// Into clones url into dir, which must not exist. Git clones into partial,
// which must not exist either, and the clone moves to dir only once
// complete, so a clone that fails leaves nothing at dir; partial is
// removed whatever happens. Git runs with every transport but https and
// ssh turned off, on its own and with no terminal, so it never asks for a
// password or a host key: the host's credential helpers and ssh agent are
// used. Watch may be nil.
func Into(ctx context.Context, url, dir, partial string, watch Watch) error {
	if err := CheckURL(url); err != nil {
		return err
	}
	if _, err := os.Lstat(dir); err == nil {
		return fmt.Errorf("%s already exists", dir)
	}
	if err := os.MkdirAll(filepath.Dir(partial), 0o755); err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(partial) }()
	cmd := exec.CommandContext(ctx, "git",
		"-c", "protocol.allow=never", "-c", "protocol.https.allow=always", "-c", "protocol.ssh.allow=always",
		"clone", "--quiet", "--", url, partial)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "SSH_ASKPASS_REQUIRE=never", "GCM_INTERACTIVE=never")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	detach(cmd)
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("git clone: %w", err)
	}
	if watch != nil {
		watch.Started(cmd.Process.Pid)
	}
	err := cmd.Wait()
	if watch != nil {
		watch.Ended(cmd.Process.Pid)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("git clone: %s", msg)
	}
	if _, err := os.Lstat(dir); err == nil {
		return fmt.Errorf("%s already exists", dir)
	}
	return os.Rename(partial, dir)
}
