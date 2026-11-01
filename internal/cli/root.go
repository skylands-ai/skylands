// Package cli wires the skylands binary's subcommands to the packages that
// implement them, and holds the Unix socket client the local CLI uses to talk
// to the daemon on this host.
//
// Every command is <noun> <operation>, such as "daemon up" or "request
// approve".
package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// Command groups, which order the root help output.
const (
	groupRoles = "roles"
	groupWorld = "world"
	groupLocal = "local"
)

// Main runs the skylands CLI with args, which exclude the program name, and
// returns the process exit code. Errors are written to stderr prefixed with
// the path of the command that failed, such as "skylands world init".
func Main(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	root := NewRoot()
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)
	cmd, err := root.ExecuteContextC(ctx)
	if err != nil {
		if cmd == nil {
			cmd = root
		}
		fmt.Fprintf(stderr, "%s: %v\n", cmd.CommandPath(), err)
		return 1
	}
	return 0
}

// NewRoot builds the full command tree. Each call returns an independent
// tree, so tests can run commands without sharing flag state.
func NewRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "skylands",
		Short: "Run, watch, and approve coding agents across machines",
		Long: `Skylands runs, watches, and approves coding agents from several providers
across several machines, shown as a 3D world of floating islands.

One binary serves every role. A daemon turns the machine it runs on into an
island, a citadel serves the web application for a world, and "skylands
devworld up" runs both with an embedded NATS server for a one-machine world.`,
		SilenceUsage:      true,
		SilenceErrors:     true,
		CompletionOptions: cobra.CompletionOptions{HiddenDefaultCmd: true},
	}
	root.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
		return fmt.Errorf("%w\nRun '%s --help' for usage", err, c.CommandPath())
	})
	root.AddGroup(
		&cobra.Group{ID: groupRoles, Title: "Roles:"},
		&cobra.Group{ID: groupWorld, Title: "Worlds:"},
		&cobra.Group{ID: groupLocal, Title: "This host:"},
	)
	root.AddCommand(
		newCitadelCmd(),
		newDaemonCmd(),
		newDevworldCmd(),
		newWorldCmd(),
		newRepoCmd(),
		newRequestCmd(),
		newSessionCmd(),
		newRuleCmd(),
		newVersionCmd(),
		newFakeAgentCmd(),
	)
	return root
}
