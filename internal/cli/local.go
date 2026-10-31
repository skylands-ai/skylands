package cli

// The local CLI talks to the daemon on this host over its Unix socket. It
// registers repos, lists what is waiting on a person, approves or denies
// those requests, and reports status. The daemon accepts these from the OS
// user that owns it, which unblocks agents even while the host is offline.

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/skylands-ai/skylands/internal/daemon/socket"
	"github.com/skylands-ai/skylands/internal/stub"
)

// socketFlag adds the --socket flag shared by every local command.
func socketFlag(cmd *cobra.Command, socketPath *string) {
	cmd.Flags().StringVar(socketPath, "socket", defaultSocketPath(), "Unix socket of the daemon on this host")
}

func newRepoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "repo",
		GroupID: groupLocal,
		Short:   "Manage the repositories the daemon on this host works in",
	}
	cmd.AddCommand(newRepoRegisterCmd())
	return cmd
}

func newRepoRegisterCmd() *cobra.Command {
	var socketPath string
	cmd := &cobra.Command{
		Use:   "register <path>",
		Short: "Register a repository as a terrace on this island",
		Long: `Repo register tells the daemon about a repository on this host. The daemon
snapshots its tracked files and their sizes, and a terrace with a building
for every file appears on the island.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, err := socket.Dial(socketPath).RegisterRepo(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Registered %s: %d tracked files at %s\n", repo.Name, repo.Files, repo.Path)
			return nil
		},
	}
	socketFlag(cmd, &socketPath)
	return cmd
}

func newRequestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "request",
		GroupID: groupLocal,
		Short:   "List, approve, and deny requests waiting on a person on this host",
	}
	cmd.AddCommand(newRequestListCmd(), newRequestApproveCmd(), newRequestDenyCmd())
	return cmd
}

func newRequestListCmd() *cobra.Command {
	var socketPath string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List what is waiting on a person on this host",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return stub.ErrUnimplemented
		},
	}
	socketFlag(cmd, &socketPath)
	return cmd
}

func newRequestApproveCmd() *cobra.Command {
	var socketPath string
	cmd := &cobra.Command{
		Use:   "approve <request>",
		Short: "Approve a waiting request on this host",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return stub.ErrUnimplemented
		},
	}
	socketFlag(cmd, &socketPath)
	return cmd
}

func newRequestDenyCmd() *cobra.Command {
	var socketPath string
	cmd := &cobra.Command{
		Use:   "deny <request>",
		Short: "Deny a waiting request on this host",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return stub.ErrUnimplemented
		},
	}
	socketFlag(cmd, &socketPath)
	return cmd
}
