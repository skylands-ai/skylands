package service

import (
	"context"
	"encoding/xml"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fakeRun records every command and fails those that fail says to.
type fakeRun struct {
	calls []string
	fail  func(cmd string) bool
	out   func(cmd string) string
}

func (f *fakeRun) run(_ context.Context, name string, args ...string) (string, error) {
	cmd := strings.Join(append([]string{name}, args...), " ")
	f.calls = append(f.calls, cmd)
	out := ""
	if f.out != nil {
		out = f.out(cmd)
	}
	if f.fail != nil && f.fail(cmd) {
		return out, errors.New("exit status 1")
	}
	return out, nil
}

var spec = Spec{
	Name:        "skylands-daemon",
	Description: "Skylands daemon at 100%",
	Args:        []string{"/opt/sky lands/skylands", "daemon", "up", "--host=wopr", `--data-dir=/data/a"b$c\d`},
	Env:         map[string]string{"PATH": "/usr/bin:/home/k/.npm global/bin", "A": "x%y"},
}

func TestRenderUnit(t *testing.T) {
	got := renderUnit(spec)
	for _, want := range []string{
		"Description=Skylands daemon at 100%%\n",
		`ExecStart="/opt/sky lands/skylands" daemon up --host=wopr "--data-dir=/data/a\"b$$c\\d"` + "\n",
		// Sorted, so the file is the same on every install.
		"Environment=\"A=x%%y\"\nEnvironment=\"PATH=/usr/bin:/home/k/.npm global/bin\"\n",
		"Restart=on-failure\n",
		"KillMode=mixed\n",
		"WantedBy=default.target\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("unit lacks %q:\n%s", want, got)
		}
	}
}

func TestSystemdInstallAndUninstall(t *testing.T) {
	dir := t.TempDir()
	f := &fakeRun{}
	m := &systemd{dir: dir, run: f.run}
	if err := m.Install(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"systemctl --user show-environment",
		"systemctl --user daemon-reload",
		"systemctl --user enable skylands-daemon.service",
		"systemctl --user restart skylands-daemon.service",
	}
	if !reflect.DeepEqual(f.calls, want) {
		t.Errorf("install ran\n%s\nwant\n%s", strings.Join(f.calls, "\n"), strings.Join(want, "\n"))
	}
	path := filepath.Join(dir, "skylands-daemon.service")
	if m.Path("skylands-daemon") != path {
		t.Errorf("Path = %s, want %s", m.Path("skylands-daemon"), path)
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != renderUnit(spec) {
		t.Errorf("unit file = %q, %v; want the rendered unit", b, err)
	}

	f.calls = nil
	if err := m.Uninstall(context.Background(), "skylands-daemon"); err != nil {
		t.Fatal(err)
	}
	want = []string{
		"systemctl --user disable --now skylands-daemon.service",
		"systemctl --user daemon-reload",
		"systemctl --user reset-failed skylands-daemon.service",
	}
	if !reflect.DeepEqual(f.calls, want) {
		t.Errorf("uninstall ran\n%s\nwant\n%s", strings.Join(f.calls, "\n"), strings.Join(want, "\n"))
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("unit file still there: %v", err)
	}
	if err := m.Uninstall(context.Background(), "skylands-daemon"); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("second uninstall = %v, want ErrNotInstalled", err)
	}
}

func TestSystemdInstallNeedsAUserManager(t *testing.T) {
	dir := t.TempDir()
	f := &fakeRun{fail: func(cmd string) bool { return strings.HasSuffix(cmd, "show-environment") }}
	m := &systemd{dir: dir, run: f.run}
	err := m.Install(context.Background(), spec)
	if err == nil || !strings.Contains(err.Error(), "no systemd user manager") {
		t.Errorf("Install = %v, want a no-user-manager error", err)
	}
	if _, err := os.Stat(m.Path(spec.Name)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a unit was written without a user manager: %v", err)
	}
}

func TestSystemdHintsMentionLingerOnlyWhenOff(t *testing.T) {
	for linger, want := range map[string]bool{"no\n": true, "yes\n": false} {
		f := &fakeRun{out: func(cmd string) string {
			if strings.HasPrefix(cmd, "loginctl") {
				return linger
			}
			return ""
		}}
		m := &systemd{dir: t.TempDir(), run: f.run}
		hints := strings.Join(m.Hints(context.Background(), "skylands-daemon"), "\n")
		if !strings.Contains(hints, "journalctl --user -u skylands-daemon -f") {
			t.Errorf("hints lack the logs command:\n%s", hints)
		}
		if got := strings.Contains(hints, "enable-linger"); got != want {
			t.Errorf("linger %q: hints mention enable-linger = %v, want %v", strings.TrimSpace(linger), got, want)
		}
	}
}

func TestRenderPlist(t *testing.T) {
	got := renderPlist(spec, "ai.skylands.daemon", "/Users/k/Library/Logs/skylands/skylands-daemon.log")
	// It must be well-formed XML, with the arguments in order and escaped.
	var doc struct {
		Dict struct {
			Inner []byte `xml:",innerxml"`
		} `xml:"dict"`
	}
	if err := xml.Unmarshal([]byte(got), &doc); err != nil {
		t.Fatalf("plist is not XML: %v\n%s", err, got)
	}
	var strs []string
	d := xml.NewDecoder(strings.NewReader(got))
	d.Strict = false
	inString := false
	for {
		tok, err := d.Token()
		if err != nil {
			break
		}
		switch tok := tok.(type) {
		case xml.StartElement:
			inString = tok.Name.Local == "string"
		case xml.CharData:
			if inString {
				strs = append(strs, string(tok))
			}
		case xml.EndElement:
			inString = false
		}
	}
	want := []string{"ai.skylands.daemon", "/opt/sky lands/skylands", "daemon", "up", "--host=wopr", `--data-dir=/data/a"b$c\d`, "x%y", "/usr/bin:/home/k/.npm global/bin"}
	if len(strs) < len(want) || !reflect.DeepEqual(strs[:len(want)], want) {
		t.Errorf("plist strings = %q, want them to start with %q", strs, want)
	}
	for _, want := range []string{"<key>RunAtLoad</key>\n\t<true/>", "<key>SuccessfulExit</key>\n\t\t<false/>", "<key>StandardErrorPath</key>"} {
		if !strings.Contains(got, want) {
			t.Errorf("plist lacks %q:\n%s", want, got)
		}
	}
}

func TestLaunchdInstallAndUninstall(t *testing.T) {
	home := t.TempDir()
	loaded := true
	f := &fakeRun{}
	f.fail = func(cmd string) bool {
		switch {
		case strings.HasPrefix(cmd, "launchctl bootout"):
			if !loaded {
				return true
			}
			loaded = false
		case strings.HasPrefix(cmd, "launchctl print"):
			return !loaded
		case strings.HasPrefix(cmd, "launchctl bootstrap"):
			loaded = true
		}
		return false
	}
	m := &launchd{dir: filepath.Join(home, "LaunchAgents"), logDir: filepath.Join(home, "Logs"), domain: "gui/501", run: f.run, poll: 1}
	if err := m.Install(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	plist := filepath.Join(home, "LaunchAgents", "ai.skylands.daemon.plist")
	want := []string{
		"launchctl bootout gui/501/ai.skylands.daemon",
		"launchctl print gui/501/ai.skylands.daemon",
		"launchctl enable gui/501/ai.skylands.daemon",
		"launchctl bootstrap gui/501 " + plist,
	}
	if !reflect.DeepEqual(f.calls, want) {
		t.Errorf("install ran\n%s\nwant\n%s", strings.Join(f.calls, "\n"), strings.Join(want, "\n"))
	}
	if _, err := os.Stat(plist); err != nil {
		t.Errorf("plist not written: %v", err)
	}

	f.calls = nil
	if err := m.Uninstall(context.Background(), "skylands-daemon"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(plist); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("plist still there: %v", err)
	}
	if err := m.Uninstall(context.Background(), "skylands-daemon"); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("second uninstall = %v, want ErrNotInstalled", err)
	}
}

func TestCheckName(t *testing.T) {
	for name, ok := range map[string]bool{"skylands-daemon": true, "sky.lands_2": true, "": false, "-x": false, "a/b": false, "a b": false} {
		if err := CheckName(name); (err == nil) != ok {
			t.Errorf("CheckName(%q) = %v, want ok %v", name, err, ok)
		}
	}
}

func TestWithin(t *testing.T) {
	dir := t.TempDir()
	if !within(filepath.Join(dir, "a", "b"), dir) {
		t.Error("a file under the directory is not within it")
	}
	if within(filepath.Join(filepath.Dir(dir), "elsewhere"), dir) {
		t.Error("a sibling is within the directory")
	}
}

// TestUnitPassesSystemdAnalyze has systemd itself read a rendered unit,
// where systemd is installed, without installing the unit.
func TestUnitPassesSystemdAnalyze(t *testing.T) {
	analyze, err := exec.LookPath("systemd-analyze")
	if err != nil {
		t.Skip("no systemd-analyze")
	}
	s := spec
	s.Args = append([]string{"/bin/true"}, spec.Args[1:]...)
	path := filepath.Join(t.TempDir(), "skylands-daemon.service")
	if err := os.WriteFile(path, []byte(renderUnit(s)), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(analyze, "--user", "verify", path).CombinedOutput()
	if err != nil {
		t.Errorf("systemd-analyze verify: %v\n%s", err, out)
	}
	// It also warns about other units on the machine; only ours matter.
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, path) {
			t.Errorf("systemd-analyze verify: %s", line)
		}
	}
}
