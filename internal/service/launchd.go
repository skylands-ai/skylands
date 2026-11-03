package service

import (
	"context"
	"encoding/xml"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// launchd installs a service as a launchd agent in the user's GUI domain.
type launchd struct {
	// dir is ~/Library/LaunchAgents, and logDir is where the service's
	// output goes, ~/Library/Logs/skylands.
	dir, logDir string
	// domain is the user's launchd domain, gui/<uid>.
	domain string
	run    runner
	// poll is how often stop checks that launchd has let go of the
	// service; zero means 200ms.
	poll time.Duration
}

// label is the launchd label for name: skylands-daemon becomes
// ai.skylands.daemon.
func label(name string) string { return "ai.skylands." + strings.TrimPrefix(name, "skylands-") }

func (m *launchd) Path(name string) string    { return filepath.Join(m.dir, label(name)+".plist") }
func (m *launchd) target(name string) string  { return m.domain + "/" + label(name) }
func (m *launchd) logPath(name string) string { return filepath.Join(m.logDir, name+".log") }

func (m *launchd) Install(ctx context.Context, s Spec) error {
	if err := os.MkdirAll(m.logDir, 0o755); err != nil {
		return err
	}
	if err := writeFile(m.Path(s.Name), []byte(renderPlist(s, label(s.Name), m.logPath(s.Name)))); err != nil {
		return err
	}
	m.stop(ctx, s.Name)
	if _, err := m.run(ctx, "launchctl", "enable", m.target(s.Name)); err != nil {
		return err
	}
	_, err := m.run(ctx, "launchctl", "bootstrap", m.domain, m.Path(s.Name))
	return err
}

// stop unloads the service if it is loaded, and waits until launchd has
// let go of it, because bootstrapping a label launchd is still tearing
// down fails.
func (m *launchd) stop(ctx context.Context, name string) {
	if _, err := m.run(ctx, "launchctl", "bootout", m.target(name)); err != nil {
		return
	}
	poll := m.poll
	if poll == 0 {
		poll = 200 * time.Millisecond
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := m.run(ctx, "launchctl", "print", m.target(name)); err != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(poll):
		}
	}
}

func (m *launchd) Uninstall(ctx context.Context, name string) error {
	path := m.Path(name)
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return ErrNotInstalled
	}
	m.stop(ctx, name)
	return os.Remove(path)
}

func (m *launchd) Active(ctx context.Context, name string) bool {
	out, err := m.run(ctx, "launchctl", "print", m.target(name))
	return err == nil && strings.Contains(out, "state = running")
}

func (m *launchd) Hints(ctx context.Context, name string) []string {
	return []string{
		"Logs:     tail -f " + m.logPath(name),
		"Restart:  launchctl kickstart -k " + m.target(name),
	}
}

// renderPlist writes s as a launchd agent that starts at login, restarts
// when it exits with an error, and appends its output to logPath.
func renderPlist(s Spec, label, logPath string) string {
	esc := func(v string) string {
		var b strings.Builder
		_ = xml.EscapeText(&b, []byte(v))
		return b.String()
	}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<!-- Written by skylands. Installing again replaces it, and uninstalling removes it. -->
<plist version="1.0">
<dict>
`)
	b.WriteString("\t<key>Label</key>\n\t<string>" + esc(label) + "</string>\n")
	b.WriteString("\t<key>ProgramArguments</key>\n\t<array>\n")
	for _, a := range s.Args {
		b.WriteString("\t\t<string>" + esc(a) + "</string>\n")
	}
	b.WriteString("\t</array>\n")
	if len(s.Env) > 0 {
		keys := make([]string, 0, len(s.Env))
		for k := range s.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteString("\t<key>EnvironmentVariables</key>\n\t<dict>\n")
		for _, k := range keys {
			b.WriteString("\t\t<key>" + esc(k) + "</key>\n\t\t<string>" + esc(s.Env[k]) + "</string>\n")
		}
		b.WriteString("\t</dict>\n")
	}
	b.WriteString(`	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>ThrottleInterval</key>
	<integer>5</integer>
	<key>ExitTimeOut</key>
	<integer>30</integer>
`)
	b.WriteString("\t<key>StandardOutPath</key>\n\t<string>" + esc(logPath) + "</string>\n")
	b.WriteString("\t<key>StandardErrorPath</key>\n\t<string>" + esc(logPath) + "</string>\n")
	b.WriteString("</dict>\n</plist>\n")
	return b.String()
}
