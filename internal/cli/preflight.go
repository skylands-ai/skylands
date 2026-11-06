package cli

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/skylands-ai/skylands/internal/preflight"
)

// runPreflight makes the checks. Tests replace it.
var runPreflight = func(ctx context.Context, socketPath, version string) []preflight.Check {
	return preflight.Run(ctx, preflight.Default(socketPath, version))
}

func newPreflightCmd() *cobra.Command {
	var socketPath string
	cmd := &cobra.Command{
		Use:     "preflight",
		GroupID: groupLocal,
		Short:   "Check that this host has what it needs to run agents",
		Long: `Preflight checks what a daemon on this host needs: git for session
worktrees, Node and npx for agents from npm, each agent starting over ACP
and, when it reports one, its login, allow rules and hooks in Claude Code's
own settings and plugins, which let calls skip the host's approvals, gh for
pull requests, and a daemon answering on the local socket. Starting an
agent asks no model anything.

It checks with this shell's PATH and environment. A service from
"skylands daemon install" or "devworld install" gets only the PATH. It
exits with status 1 when a check fails.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "Preflight for skylands %s on %s/%s. Starting each agent takes a few seconds.\n\n", versionString(), runtime.GOOS, runtime.GOARCH)
			checks := runPreflight(cmd.Context(), socketPath, versionString())
			tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
			failed := 0
			for _, c := range checks {
				fmt.Fprintf(tw, "%s\t%s\t%s\n", c.Status, c.Name, c.Found)
				if c.Fix != "" {
					fmt.Fprintf(tw, "\t\tfix: %s\n", c.Fix)
				}
				if c.Status == preflight.Fail {
					failed++
				}
			}
			if err := tw.Flush(); err != nil {
				return err
			}
			fmt.Fprintln(w)
			if ready := preflight.Ready(checks); len(ready) > 0 {
				fmt.Fprintf(w, "Agents this host can run: %s.\n", strings.Join(ready, ", "))
			} else {
				fmt.Fprintln(w, "No agent can run on this host until the failed checks are fixed.")
			}
			switch failed {
			case 0:
				return nil
			case 1:
				return fmt.Errorf("1 check failed")
			default:
				return fmt.Errorf("%d checks failed", failed)
			}
		},
	}
	socketFlag(cmd, &socketPath)
	return cmd
}
