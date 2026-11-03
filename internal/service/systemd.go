package service

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
)

// systemd installs a service as a systemd user unit.
type systemd struct {
	// dir is the user unit directory, ~/.config/systemd/user.
	dir string
	run runner
}

func (m *systemd) Path(name string) string { return filepath.Join(m.dir, name+".service") }

func (m *systemd) Install(ctx context.Context, s Spec) error {
	if _, err := m.run(ctx, "systemctl", "--user", "show-environment"); err != nil {
		return fmt.Errorf("there is no systemd user manager to install into, so run the command under your own supervisor: %w", err)
	}
	if err := writeFile(m.Path(s.Name), []byte(renderUnit(s))); err != nil {
		return err
	}
	unit := s.Name + ".service"
	for _, args := range [][]string{{"daemon-reload"}, {"enable", unit}, {"restart", unit}} {
		if _, err := m.run(ctx, "systemctl", append([]string{"--user"}, args...)...); err != nil {
			return err
		}
	}
	return nil
}

func (m *systemd) Uninstall(ctx context.Context, name string) error {
	path := m.Path(name)
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return ErrNotInstalled
	}
	unit := name + ".service"
	if _, err := m.run(ctx, "systemctl", "--user", "disable", "--now", unit); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	if _, err := m.run(ctx, "systemctl", "--user", "daemon-reload"); err != nil {
		return err
	}
	// Forget a failed state, so the unit's name is not left listed.
	_, _ = m.run(ctx, "systemctl", "--user", "reset-failed", unit)
	return nil
}

func (m *systemd) Active(ctx context.Context, name string) bool {
	_, err := m.run(ctx, "systemctl", "--user", "is-active", "--quiet", name+".service")
	return err == nil
}

func (m *systemd) Hints(ctx context.Context, name string) []string {
	hints := []string{
		"Logs:     journalctl --user -u " + name + " -f",
		"Restart:  systemctl --user restart " + name,
	}
	// A user's services stop when the user's last session ends, unless
	// the user lingers.
	if u, err := user.Current(); err == nil {
		out, err := m.run(ctx, "loginctl", "show-user", u.Username, "--property=Linger", "--value")
		if err == nil && strings.TrimSpace(out) == "no" {
			hints = append(hints, "It runs while you are logged in. To start it at boot and keep it running after you log out, run: loginctl enable-linger")
		}
	}
	return hints
}

// renderUnit writes s as a systemd unit.
func renderUnit(s Spec) string {
	var b strings.Builder
	b.WriteString("# Written by skylands. Installing again replaces it, and uninstalling removes it.\n")
	b.WriteString("[Unit]\n")
	fmt.Fprintf(&b, "Description=%s\n", strings.ReplaceAll(s.Description, "%", "%%"))
	b.WriteString("Documentation=https://github.com/skylands-ai/skylands\n\n")
	b.WriteString("[Service]\n")
	b.WriteString("Type=simple\n")
	args := make([]string, len(s.Args))
	for i, a := range s.Args {
		args[i] = quoteExec(a)
	}
	fmt.Fprintf(&b, "ExecStart=%s\n", strings.Join(args, " "))
	keys := make([]string, 0, len(s.Env))
	for k := range s.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "Environment=%s\n", quoteEnv(k+"="+s.Env[k]))
	}
	b.WriteString("Restart=on-failure\n")
	b.WriteString("RestartSec=2s\n")
	b.WriteString("# SIGTERM goes to skylands alone. It stops its agents itself, so their\n")
	b.WriteString("# sessions resume after a restart instead of failing, and whatever is\n")
	b.WriteString("# left when it exits is killed.\n")
	b.WriteString("KillMode=mixed\n")
	b.WriteString("TimeoutStopSec=30s\n\n")
	b.WriteString("[Install]\n")
	b.WriteString("WantedBy=default.target\n")
	return b.String()
}

// quoteExec quotes one ExecStart argument. systemd expands % specifiers
// and $ variables there, splits on spaces, and reads C escapes in quotes.
func quoteExec(a string) string {
	a = strings.ReplaceAll(a, "%", "%%")
	a = strings.ReplaceAll(a, "$", "$$")
	if a != "" && !strings.ContainsAny(a, " \t\"'\\;") {
		return a
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(a) + `"`
}

// quoteEnv quotes one Environment= assignment, where systemd expands %
// specifiers but not $ variables.
func quoteEnv(kv string) string {
	kv = strings.ReplaceAll(kv, "%", "%%")
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(kv) + `"`
}
