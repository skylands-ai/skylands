package cli

import (
	"github.com/spf13/cobra"

	"github.com/skylands-ai/skylands/internal/daemon"
	"github.com/skylands-ai/skylands/internal/stub"
	"github.com/skylands-ai/skylands/internal/world"
)

func newDaemonCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "daemon",
		GroupID: groupRoles,
		Short:   "Run, enroll, and inspect the daemon that makes this machine an island",
	}
	cmd.AddCommand(newDaemonUpCmd(), newDaemonEnrollCmd(), newDaemonStatusCmd())
	return cmd
}

func newDaemonUpCmd() *cobra.Command {
	var cfg daemon.Config
	cmd := &cobra.Command{
		Use:   "up",
		Short: "Turn this machine into an island that runs agents",
		Long: `Daemon up launches agents over the Agent Client Protocol, owns their
sessions, enforces the world's rules locally, and verifies approval
signatures. It runs a NATS leaf node in-process, so the host's events stay on
the host and sync to the world whenever the link is up. The local CLI reaches
it over a Unix socket.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg.Version = versionString()
			return daemon.Run(cmd.Context(), cfg)
		},
	}
	f := cmd.Flags()
	f.StringVar(&cfg.Host, "host", daemon.DefaultHost(), "name of this machine's island, unique in the world")
	f.StringVar(&cfg.NATSURL, "nats-url", "nats-leaf://127.0.0.1:7422", "URL of the world's NATS leaf node listener")
	f.StringVar(&cfg.CredsFile, "creds", "", "path to the daemon's NATS credentials file")
	f.StringVar(&cfg.DataDir, "data-dir", defaultDataDir(), "directory for the leaf node's JetStream store and agent worktrees")
	f.StringVar(&cfg.SocketPath, "socket", defaultSocketPath(), "Unix socket to listen on for the local CLI")
	return cmd
}

func newDaemonEnrollCmd() *cobra.Command {
	var opts world.EnrollDaemonOptions
	cmd := &cobra.Command{
		Use:   "enroll <host>",
		Short: "Enroll the daemon on a host and issue its credentials",
		Long: `Daemon enroll signs a daemon enrollment with the world's owner key and issues
the credentials the daemon on that host uses to join the world. Run it where
the owner key is kept, which should stay offline.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Host = args[0]
			return world.EnrollDaemon(cmd.Context(), opts)
		},
	}
	cmd.Flags().StringVar(&opts.WorldDir, "world", defaultWorldDir(), "directory holding the world's owner key")
	return cmd
}

func newDaemonStatusCmd() *cobra.Command {
	var socketPath string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Report the daemon's link to the world, its repos, and its agents",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return stub.ErrUnimplemented
		},
	}
	socketFlag(cmd, &socketPath)
	return cmd
}
