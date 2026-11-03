package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/skylands-ai/skylands/internal/preflight"
)

func usePreflight(t *testing.T, checks []preflight.Check) {
	t.Helper()
	old := runPreflight
	runPreflight = func(context.Context, string, string) []preflight.Check { return checks }
	t.Cleanup(func() { runPreflight = old })
}

func TestPreflightReportsEachCheck(t *testing.T) {
	usePreflight(t, []preflight.Check{
		{Name: "git", Status: preflight.OK, Found: "git 2.43.0"},
		{Name: "agent claude", Status: preflight.OK, Found: "adapter 0.100.0", Agent: "claude"},
		{Name: "gh", Status: preflight.Warn, Found: "not logged in", Fix: "run gh auth login"},
	})
	code, stdout, stderr := run(t, "preflight")
	if code != 0 || stderr != "" {
		t.Errorf("exit %d, stderr %q; want success with only warnings", code, stderr)
	}
	for _, want := range []string{"ok    git           git 2.43.0", "warn  gh            not logged in", "fix: run gh auth login", "Agents this host can run: claude."} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
}

func TestPreflightFailsWhenACheckFails(t *testing.T) {
	usePreflight(t, []preflight.Check{
		{Name: "git", Status: preflight.Fail, Found: "not found on PATH", Fix: "install git"},
		{Name: "agent fake", Status: preflight.OK, Found: "fake", Agent: "fake"},
	})
	code, stdout, stderr := run(t, "preflight")
	if code != 1 || stderr != "skylands preflight: 1 check failed\n" {
		t.Errorf("exit %d, stderr %q; want 1 and the count of failures", code, stderr)
	}
	if !strings.Contains(stdout, "No agent can run on this host") {
		t.Errorf("stdout lacks the no-agent summary:\n%s", stdout)
	}
}
