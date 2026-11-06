package cli

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/skylands-ai/skylands/internal/daemon/socket"
	"github.com/skylands-ai/skylands/internal/service"
)

// fakeManager stands in for systemd or launchd.
type fakeManager struct {
	active      bool
	installed   *service.Spec
	onInstall   func()
	uninstalled []string
}

func (m *fakeManager) Install(_ context.Context, s service.Spec) error {
	m.installed = &s
	if m.onInstall != nil {
		m.onInstall()
	}
	return nil
}

func (m *fakeManager) Uninstall(_ context.Context, name string) error {
	if m.installed == nil {
		return service.ErrNotInstalled
	}
	m.uninstalled = append(m.uninstalled, name)
	m.installed = nil
	return nil
}

func (m *fakeManager) Active(context.Context, string) bool { return m.active }
func (m *fakeManager) Path(name string) string             { return "/units/" + name + ".service" }
func (m *fakeManager) Hints(context.Context, string) []string {
	return []string{"Logs:     fake logs"}
}

// useFakes replaces the service manager and the binary for one test.
func useFakes(t *testing.T, m *fakeManager) {
	t.Helper()
	oldManager, oldExe, oldWait := newServiceManager, executable, installWait
	newServiceManager = func() (service.Manager, error) { return m, nil }
	executable = func() (string, error) { return "/usr/local/bin/skylands", nil }
	installWait = 2 * time.Second
	t.Cleanup(func() { newServiceManager, executable, installWait = oldManager, oldExe, oldWait })
}

// shortDir is a temporary directory with a path short enough for a Unix
// socket, which t.TempDir's may not be.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "sk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// serveStatus answers GET /v1/status on path the way a daemon does, until
// the test ends.
func serveStatus(t *testing.T, path string, st socket.StatusInfo) {
	t.Helper()
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(st)
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
}

func TestDaemonInstallRunsUpWithTheGivenFlags(t *testing.T) {
	dir := shortDir(t)
	sock := filepath.Join(dir, "d.sock")
	m := &fakeManager{}
	m.onInstall = func() {
		serveStatus(t, sock, socket.StatusInfo{Host: "wopr", World: "nats-leaf://w:7422", Version: "v9", Started: time.Now()})
	}
	useFakes(t, m)
	t.Setenv("PATH", "/usr/bin:/home/k/.npm-global/bin")
	t.Chdir(dir)

	code, stdout, stderr := run(t, "daemon", "install", "--host", "wopr", "--data-dir", "data", "--socket", sock)
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	if m.installed == nil {
		t.Fatal("nothing was installed")
	}
	want := service.Spec{
		Name:        "skylands-daemon",
		Description: "Skylands daemon, which makes this machine an island",
		// Flags in name order, paths absolute, and flags left at their
		// defaults left out.
		Args: []string{"/usr/local/bin/skylands", "daemon", "up", "--data-dir=" + filepath.Join(dir, "data"), "--host=wopr", "--socket=" + sock},
		Env:  map[string]string{"PATH": "/usr/bin:/home/k/.npm-global/bin"},
	}
	if !reflect.DeepEqual(*m.installed, want) {
		t.Errorf("installed %+v\nwant      %+v", *m.installed, want)
	}
	for _, line := range []string{"Installed skylands-daemon at /units/skylands-daemon.service", "Island:   wopr is up, skylands v9, joining nats-leaf://w:7422", "Logs:     fake logs"} {
		if !strings.Contains(stdout, line) {
			t.Errorf("stdout lacks %q:\n%s", line, stdout)
		}
	}
}

func TestDevworldInstallTakesItsOwnFlagsAndName(t *testing.T) {
	sock := filepath.Join(shortDir(t), "d.sock")
	m := &fakeManager{}
	m.onInstall = func() { serveStatus(t, sock, socket.StatusInfo{Host: "h", Started: time.Now()}) }
	useFakes(t, m)

	code, _, stderr := run(t, "devworld", "install", "--http", "127.0.0.1:9090", "--url", "https://box.example.ts.net", "--socket", sock, "--name", "sky-test")
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	if m.installed.Name != "sky-test" {
		t.Errorf("name = %q, want sky-test", m.installed.Name)
	}
	if want := []string{"/usr/local/bin/skylands", "devworld", "up", "--http=127.0.0.1:9090", "--socket=" + sock, "--url=https://box.example.ts.net"}; !reflect.DeepEqual(m.installed.Args, want) {
		t.Errorf("args = %q, want %q", m.installed.Args, want)
	}
}

func TestInstallRefusesWhileADaemonRunsOnTheSocket(t *testing.T) {
	sock := filepath.Join(shortDir(t), "d.sock")
	serveStatus(t, sock, socket.StatusInfo{Host: "wopr", Started: time.Now().Add(-time.Hour)})
	m := &fakeManager{}
	useFakes(t, m)

	code, _, stderr := run(t, "daemon", "install", "--socket", sock)
	if code != 1 || !strings.Contains(stderr, "island wopr is already running") {
		t.Errorf("exit %d, stderr %q; want a refusal naming the running island", code, stderr)
	}
	if m.installed != nil {
		t.Error("installed while another daemon held the socket")
	}

	// Reinstalling an active service restarts it, so its own daemon on the
	// socket is expected. This fake one never restarts, so install waits
	// for it in vain, briefly.
	m.active = true
	installWait = 300 * time.Millisecond
	code, _, _ = run(t, "daemon", "install", "--socket", sock)
	if m.installed == nil {
		t.Errorf("exit %d: reinstalling an active service was refused", code)
	}
}

func TestInstallFailsWhenTheDaemonNeverAnswers(t *testing.T) {
	sock := filepath.Join(shortDir(t), "d.sock")
	m := &fakeManager{}
	useFakes(t, m)
	installWait = 300 * time.Millisecond

	code, stdout, stderr := run(t, "daemon", "install", "--socket", sock)
	if code != 1 || !strings.Contains(stderr, "did not answer") {
		t.Errorf("exit %d, stderr %q; want a did-not-answer error", code, stderr)
	}
	// The logs are where the reason is, so they are named even then.
	if !strings.Contains(stdout, "Logs:     fake logs") {
		t.Errorf("stdout lacks the logs hint:\n%s", stdout)
	}
}

func TestUninstall(t *testing.T) {
	m := &fakeManager{installed: &service.Spec{Name: "skylands-devworld"}}
	useFakes(t, m)

	code, stdout, _ := run(t, "devworld", "uninstall")
	if code != 0 || !reflect.DeepEqual(m.uninstalled, []string{"skylands-devworld"}) || !strings.Contains(stdout, "Its data directory is untouched") {
		t.Errorf("exit %d, uninstalled %q, stdout %q", code, m.uninstalled, stdout)
	}
	code, stdout, _ = run(t, "devworld", "uninstall")
	if code != 0 || !strings.Contains(stdout, "skylands-devworld is not installed") {
		t.Errorf("second uninstall: exit %d, stdout %q; want a not-installed note and success", code, stdout)
	}
}
