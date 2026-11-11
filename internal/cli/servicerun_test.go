package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/skylands-ai/skylands/internal/service"
)

// asService makes runService run as the service name, started from a
// binary at the path it returns, with the binary checked every few
// milliseconds.
func asService(t *testing.T, name string, m *fakeManager) string {
	t.Helper()
	useFakes(t, m)
	path := filepath.Join(t.TempDir(), "skylands")
	writeBinary(t, path, "v1")
	t.Setenv(service.EnvName, name)
	oldBinary, oldCheck, oldGrace := serviceBinary, binaryCheck, binaryGrace
	serviceBinary = func() string { return path }
	binaryCheck, binaryGrace = 5*time.Millisecond, 50*time.Millisecond
	t.Cleanup(func() { serviceBinary, binaryCheck, binaryGrace = oldBinary, oldCheck, oldGrace })
	return path
}

// writeBinary puts a new file at path, the way an installer does: written
// beside it and renamed over it.
func writeBinary(t *testing.T, path, contents string) {
	t.Helper()
	tmp := path + ".new"
	if err := os.WriteFile(tmp, []byte(contents), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
}

// untilStopped is a role's "up" that does something to its binary after
// the watch has begun, then runs until told to stop, as the daemon does
// once it is idle.
func untilStopped(t *testing.T, change func(), afterStop func()) func(context.Context, <-chan struct{}) error {
	return func(ctx context.Context, stop <-chan struct{}) error {
		if got := os.Getenv(service.EnvName); got != "" {
			t.Errorf("%s = %q while the daemon runs, so its agents would inherit it", service.EnvName, got)
		}
		time.Sleep(30 * time.Millisecond)
		change()
		select {
		case <-stop:
		case <-time.After(5 * time.Second):
			return errors.New("never told to stop")
		}
		if afterStop != nil {
			afterStop()
		}
		return nil
	}
}

func TestRunServiceRestartsOnANewBinary(t *testing.T) {
	m := &fakeManager{installed: &service.Spec{Name: "skylands-daemon"}}
	path := asService(t, "skylands-daemon", m)

	err := runService(context.Background(), untilStopped(t, func() { writeBinary(t, path, "v2, a longer binary") }, nil))
	if err == nil || !strings.Contains(err.Error(), "restarting on the new binary at "+path) {
		t.Errorf("runService = %v, want the failure that has the service manager start it again", err)
	}
	if m.removed != nil {
		t.Errorf("removed %q after an upgrade", m.removed)
	}
}

func TestRunServiceRemovesItselfOnceItsBinaryIsGone(t *testing.T) {
	m := &fakeManager{installed: &service.Spec{Name: "sky-test"}}
	path := asService(t, "sky-test", m)

	err := runService(context.Background(), untilStopped(t, func() {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}, nil))
	if err != nil {
		t.Errorf("runService = %v, want a successful exit, which the service manager leaves stopped", err)
	}
	if want := []string{"sky-test"}; !reflect.DeepEqual(m.removed, want) {
		t.Errorf("removed %q, want %q", m.removed, want)
	}
	if m.uninstalled != nil {
		t.Errorf("uninstalled %q, which would wait on this very process to stop", m.uninstalled)
	}
}

func TestRunServiceFollowsAReinstallWhileItWaitedToBeIdle(t *testing.T) {
	m := &fakeManager{installed: &service.Spec{Name: "skylands-daemon"}}
	path := asService(t, "skylands-daemon", m)

	err := runService(context.Background(), untilStopped(t,
		func() {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		},
		func() { writeBinary(t, path, "installed again") },
	))
	if err == nil || !strings.Contains(err.Error(), "restarting on the new binary") {
		t.Errorf("runService = %v, want a restart on the binary installed again", err)
	}
	if m.removed != nil {
		t.Errorf("removed %q, though its binary is back", m.removed)
	}
}

func TestRunServiceIgnoresABriefAbsence(t *testing.T) {
	m := &fakeManager{installed: &service.Spec{Name: "skylands-daemon"}}
	path := asService(t, "skylands-daemon", m)
	binaryGrace = time.Hour

	// Homebrew removes the old link before it adds the new one.
	err := runService(context.Background(), untilStopped(t, func() {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
		writeBinary(t, path, "v2 after the gap")
	}, nil))
	if err == nil || !strings.Contains(err.Error(), "restarting on the new binary") {
		t.Errorf("runService = %v, want a restart, not a removal", err)
	}
	if m.removed != nil {
		t.Errorf("removed %q during an upgrade", m.removed)
	}
}

func TestRunServiceKeepsTheDaemonsOwnFailure(t *testing.T) {
	m := &fakeManager{installed: &service.Spec{Name: "skylands-daemon"}}
	asService(t, "skylands-daemon", m)
	boom := errors.New("wrong world")

	err := runService(context.Background(), func(context.Context, <-chan struct{}) error { return boom })
	if !errors.Is(err, boom) {
		t.Errorf("runService = %v, want %v", err, boom)
	}
}

func TestRunServiceOnlyRunsWhenNotAService(t *testing.T) {
	t.Setenv(service.EnvName, "")
	ran := false
	err := runService(context.Background(), func(_ context.Context, stop <-chan struct{}) error {
		ran = true
		if stop != nil {
			t.Error("a daemon run by hand was given a stop channel")
		}
		return nil
	})
	if err != nil || !ran {
		t.Errorf("runService = %v, ran %v; want nil, true", err, ran)
	}
}
