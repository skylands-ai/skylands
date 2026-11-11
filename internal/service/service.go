// Package service installs a skylands command as a user service, so it
// starts when its user logs in, restarts when it fails, and outlives the
// terminal it was installed from: a systemd user unit on Linux and a
// launchd agent on macOS.
package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// Spec describes a service to install.
type Spec struct {
	// Name identifies the service, such as "skylands-daemon": the systemd
	// unit's name, and the last part of the launchd label.
	Name string
	// Description is a one-line summary for the service manager.
	Description string
	// Args is the command line, starting with the absolute path of the
	// binary.
	Args []string
	// Env is set in the service's environment. Service managers start
	// services with a minimal PATH, so the installing shell's PATH goes
	// here for the daemon to find npx, git, and gh where the shell does.
	Env map[string]string
}

// Manager installs and removes user services on this machine.
type Manager interface {
	// Install writes the service's definition, enables it to start at
	// login, and starts it, restarting it if it was already running so a
	// new definition takes effect.
	Install(ctx context.Context, s Spec) error
	// Uninstall stops the service and removes its definition. It returns
	// ErrNotInstalled when there is no definition to remove.
	Uninstall(ctx context.Context, name string) error
	// Remove removes the service's definition without stopping it, for
	// the service itself to call before it exits: asking the service
	// manager to stop the service would wait on the very process asking.
	// Exiting successfully afterwards leaves it stopped, as the manager
	// restarts it only when it fails. It returns ErrNotInstalled when
	// there is no definition to remove.
	Remove(ctx context.Context, name string) error
	// Active reports whether the service is running.
	Active(ctx context.Context, name string) bool
	// Path is where Install writes the service's definition.
	Path(name string) string
	// Hints are lines that tell a person how to follow and control the
	// service, such as where its logs are.
	Hints(ctx context.Context, name string) []string
}

// ErrNotInstalled is returned by Uninstall and Remove for a service with
// no definition.
var ErrNotInstalled = errors.New("not installed")

// EnvName is the variable install sets in a service's environment to the
// service's name, which tells the command it runs that it is that service.
const EnvName = "SKYLANDS_SERVICE"

// ForThisMachine returns the service manager for this operating system:
// systemd on Linux and launchd on macOS.
func ForThisMachine() (Manager, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	switch runtime.GOOS {
	case "linux":
		config := os.Getenv("XDG_CONFIG_HOME")
		if config == "" {
			config = filepath.Join(home, ".config")
		}
		return &systemd{dir: filepath.Join(config, "systemd", "user"), run: execRun}, nil
	case "darwin":
		return &launchd{
			dir:    filepath.Join(home, "Library", "LaunchAgents"),
			logDir: filepath.Join(home, "Library", "Logs", "skylands"),
			domain: fmt.Sprintf("gui/%d", os.Getuid()),
			run:    execRun,
		}, nil
	default:
		return nil, fmt.Errorf("installing a service is supported on Linux and macOS, not %s", runtime.GOOS)
	}
}

var validName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// CheckName reports whether name can name a service.
func CheckName(name string) error {
	if !validName.MatchString(name) {
		return fmt.Errorf("service name %q must be letters, digits, '.', '_', and '-', starting with a letter or digit", name)
	}
	return nil
}

// Executable is the absolute path of the running binary, for a service to
// run. A binary from "go run" or in a temporary directory is refused,
// because it would be gone when the service next starts.
//
// Of the paths that name the binary, it prefers the one on PATH, because
// a package manager keeps that path while it replaces the file behind it.
// Homebrew's bin/skylands is a link into a directory named for the
// version, which an upgrade deletes, so a service that ran the file the
// link resolves to would not start again after the next upgrade.
func Executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	resolved := exe
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		resolved = r
	}
	if strings.Contains(resolved, string(filepath.Separator)+"go-build") || within(resolved, os.TempDir()) {
		return "", fmt.Errorf("%s is a temporary binary, such as one from go run; install the service from a built or installed skylands", resolved)
	}
	return onPath(exe, resolved), nil
}

// onPath returns the path PATH finds for the binary named resolved when
// it is the same file, and exe, the path the binary was started by,
// otherwise.
func onPath(exe, resolved string) string {
	found, err := exec.LookPath(filepath.Base(resolved))
	if err != nil {
		return exe
	}
	if found, err = filepath.Abs(found); err != nil {
		return exe
	}
	a, err := os.Stat(found)
	if err != nil {
		return exe
	}
	b, err := os.Stat(resolved)
	if err != nil || !os.SameFile(a, b) {
		return exe
	}
	return found
}

// within says whether path is dir or inside it. dir counts both as given
// and with its symlinks resolved, since a path can name it either way: on
// macOS the temporary directory is under /var/folders, which is a link to
// /private/var/folders.
func within(path, dir string) bool {
	if under(path, dir) {
		return true
	}
	resolved, err := filepath.EvalSymlinks(dir)
	return err == nil && under(path, resolved)
}

func under(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// runner runs a command and returns its combined output. An error carries
// the output, which is where service managers explain themselves.
type runner func(ctx context.Context, name string, args ...string) (string, error)

func execRun(ctx context.Context, name string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return string(out), fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
		}
		return string(out), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, msg)
	}
	return string(out), nil
}

// writeFile writes data to path through a temporary file in the same
// directory, so a service manager never reads half a definition.
func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
