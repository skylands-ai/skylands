package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func run(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = Main(context.Background(), args, &out, &errOut)
	return code, out.String(), errOut.String()
}

// TestEveryRunnableCommandIsListed fails when a command is added to the tree
// without being added here, so every command is accounted for.
func TestEveryRunnableCommandIsListed(t *testing.T) {
	implemented := map[string]bool{
		"skylands version":            true,
		"skylands help":               true,
		"skylands devworld up":        true,
		"skylands devworld install":   true,
		"skylands devworld uninstall": true,
		"skylands daemon install":     true,
		"skylands daemon uninstall":   true,
		"skylands citadel up":         true,
		"skylands daemon up":          true,
		"skylands daemon status":      true,
		"skylands repo register":      true,
		"skylands request list":       true,
		"skylands request history":    true,
		"skylands request approve":    true,
		"skylands request deny":       true,
		"skylands request answer":     true,
		"skylands session list":       true,
		"skylands session cancel":     true,
		"skylands session stop":       true,
		"skylands session remove":     true,
		"skylands session prune":      true,
		"skylands rule list":          true,
		"skylands rule add":           true,
		"skylands rule remove":        true,
		"skylands preflight":          true,
	}
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		if c.Runnable() && !c.Hidden {
			path := c.CommandPath()
			if !implemented[path] {
				t.Errorf("%q is runnable but not covered by a test", path)
			}
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(NewRoot())
}

func TestLocalCommandsNeedTheDaemon(t *testing.T) {
	sock := t.TempDir() + "/none.sock"
	for _, args := range [][]string{{"repo", "register", ".", "--socket", sock}, {"daemon", "status", "--socket", sock}, {"session", "list", "--socket", sock}, {"session", "prune", "--socket", sock}, {"rule", "list", "--socket", sock}, {"request", "history", "--socket", sock}, {"rule", "add", "r", "allow", "edit", "--socket", sock}, {"request", "answer", "q", "--decline", "--socket", sock}} {
		code, _, stderr := run(t, args...)
		if code != 1 || !strings.Contains(stderr, "no daemon is listening") {
			t.Errorf("%v: exit %d, stderr %q; want a no-daemon error", args, code, stderr)
		}
	}
}

func TestVersion(t *testing.T) {
	code, stdout, stderr := run(t, "version")
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if !strings.HasPrefix(stdout, "skylands ") {
		t.Errorf("stdout = %q, want a line starting with %q", stdout, "skylands ")
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
}

func TestNoArgsPrintsHelp(t *testing.T) {
	code, stdout, _ := run(t)
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	for _, want := range []string{"Usage:", "Roles:", "Worlds:", "This host:"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("help output lacks %q:\n%s", want, stdout)
		}
	}
}

func TestUnknownFlagNamesTheCommand(t *testing.T) {
	code, _, stderr := run(t, "citadel", "up", "--no-such-flag")
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	for _, want := range []string{"skylands citadel up: unknown flag", "skylands citadel up --help"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
}
