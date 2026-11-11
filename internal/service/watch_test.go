package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// install puts a binary at path the way an installer does: written beside
// it and renamed over it.
func install(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	tmp := path + ".new"
	if err := os.WriteFile(tmp, []byte(contents), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
}

// link points path at target, replacing what was there, as Homebrew does
// with bin/skylands.
func link(t *testing.T, target, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(path)
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
}

// watch runs WatchBinary on path, checking every millisecond, and calls
// change once it has started.
func watch(t *testing.T, path string, grace time.Duration, change func()) (Change, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	type result struct {
		c   Change
		err error
	}
	done := make(chan result, 1)
	go func() {
		c, err := WatchBinary(ctx, path, time.Millisecond, grace)
		done <- result{c, err}
	}()
	time.Sleep(20 * time.Millisecond)
	change()
	r := <-done
	return r.c, r.err
}

func TestWatchBinarySeesAnUpgradeThroughALink(t *testing.T) {
	prefix := t.TempDir()
	bin := filepath.Join(prefix, "bin", "skylands")
	install(t, filepath.Join(prefix, "Caskroom", "skylands", "1.0", "skylands"), "v1")
	link(t, filepath.Join(prefix, "Caskroom", "skylands", "1.0", "skylands"), bin)

	c, err := watch(t, bin, time.Hour, func() {
		install(t, filepath.Join(prefix, "Caskroom", "skylands", "1.1", "skylands"), "v1")
		link(t, filepath.Join(prefix, "Caskroom", "skylands", "1.1", "skylands"), bin)
		_ = os.RemoveAll(filepath.Join(prefix, "Caskroom", "skylands", "1.0"))
	})
	if err != nil || c != Replaced {
		t.Errorf("WatchBinary = %v, %v; want replaced", c, err)
	}
}

func TestWatchBinarySeesAFileReplacedInPlace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "skylands")
	install(t, path, "v1")
	c, err := watch(t, path, time.Hour, func() {
		// Copied over the old one, keeping its inode.
		if err := os.WriteFile(path, []byte("v2, longer"), 0o755); err != nil {
			t.Fatal(err)
		}
	})
	if err != nil || c != Replaced {
		t.Errorf("WatchBinary = %v, %v; want replaced", c, err)
	}
}

func TestWatchBinarySeesAnUninstallOnlyAfterTheGrace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "skylands")
	install(t, path, "v1")
	var removed time.Time
	c, err := watch(t, path, 50*time.Millisecond, func() {
		removed = time.Now()
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	})
	if err != nil || c != Removed {
		t.Errorf("WatchBinary = %v, %v; want removed", c, err)
	}
	if waited := time.Since(removed); waited < 50*time.Millisecond {
		t.Errorf("took the binary as removed after %s, before the grace ended", waited)
	}
}

func TestWatchBinaryReturnsWhenCtxEnds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "skylands")
	install(t, path, "v1")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if c, err := WatchBinary(ctx, path, time.Millisecond, time.Hour); !errors.Is(err, context.DeadlineExceeded) || c != 0 {
		t.Errorf("WatchBinary = %v, %v; want the deadline", c, err)
	}
	if _, err := WatchBinary(ctx, filepath.Join(t.TempDir(), "missing"), time.Millisecond, time.Hour); err == nil {
		t.Error("WatchBinary of a missing binary started watching")
	}
}

func TestOnPathPrefersTheLinkHomebrewKeeps(t *testing.T) {
	prefix := t.TempDir()
	versioned := filepath.Join(prefix, "Caskroom", "skylands", "1.0", "skylands")
	install(t, versioned, "v1")
	bin := filepath.Join(prefix, "bin", "skylands")
	link(t, versioned, bin)

	t.Setenv("PATH", filepath.Dir(bin))
	if got := onPath(versioned, versioned); got != bin {
		t.Errorf("onPath = %q, want %q", got, bin)
	}

	// Another skylands on PATH is not this one.
	other := filepath.Join(t.TempDir(), "skylands")
	install(t, other, "another build")
	t.Setenv("PATH", filepath.Dir(other))
	if got := onPath(versioned, versioned); got != versioned {
		t.Errorf("onPath with another skylands on PATH = %q, want %q", got, versioned)
	}

	// With none on PATH, the path it was started by stays.
	t.Setenv("PATH", t.TempDir())
	if got := onPath(bin, versioned); got != bin {
		t.Errorf("onPath with none on PATH = %q, want %q", got, bin)
	}
}

func TestLaunchdRemoveLeavesTheJobToExit(t *testing.T) {
	home := t.TempDir()
	// Nothing is loaded, so install does not wait for a bootout.
	f := &fakeRun{fail: func(cmd string) bool { return strings.HasPrefix(cmd, "launchctl print") }}
	m := &launchd{dir: filepath.Join(home, "LaunchAgents"), logDir: filepath.Join(home, "Logs"), domain: "gui/501", run: f.run, poll: 1}
	if err := m.Install(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	f.calls = nil
	if err := m.Remove(context.Background(), "skylands-daemon"); err != nil {
		t.Fatal(err)
	}
	if f.calls != nil {
		t.Errorf("remove ran %q; booting out the job that asks would wait on it", f.calls)
	}
	if _, err := os.Stat(m.Path("skylands-daemon")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("plist still there: %v", err)
	}
	if err := m.Remove(context.Background(), "skylands-daemon"); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("second remove = %v, want ErrNotInstalled", err)
	}
}

func TestSystemdRemoveDisablesWithoutStopping(t *testing.T) {
	f := &fakeRun{}
	m := &systemd{dir: t.TempDir(), run: f.run}
	if err := m.Install(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	f.calls = nil
	if err := m.Remove(context.Background(), "skylands-daemon"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"systemctl --user disable skylands-daemon.service",
		"systemctl --user daemon-reload",
	}
	if !reflect.DeepEqual(f.calls, want) {
		t.Errorf("remove ran\n%s\nwant\n%s", strings.Join(f.calls, "\n"), strings.Join(want, "\n"))
	}
	if _, err := os.Stat(m.Path("skylands-daemon")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("unit still there: %v", err)
	}
	if err := m.Remove(context.Background(), "skylands-daemon"); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("second remove = %v, want ErrNotInstalled", err)
	}
}
