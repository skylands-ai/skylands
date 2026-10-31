package cli

import (
	"github.com/spf13/cobra"

	"github.com/skylands-ai/skylands/internal/citadel"
)

func newCitadelCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "citadel",
		GroupID: groupRoles,
		Short:   "Run the citadel that serves a world's web application",
	}
	cmd.AddCommand(newCitadelUpCmd())
	return cmd
}

func newCitadelUpCmd() *cobra.Command {
	var cfg citadel.Config
	cmd := &cobra.Command{
		Use:   "up",
		Short: "Serve the web application for a world",
		Long: `Citadel up serves the 3D client and the phone inbox, builds projections from
the world's streams, relays signed approvals to the daemon that owns each
session, sends push notifications, and answers NATS auth callout requests.
It connects to the world's NATS server as an ordinary client. Any number of
citadels can run against one world, and they are interchangeable.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return citadel.Run(cmd.Context(), cfg)
		},
	}
	f := cmd.Flags()
	f.StringVar(&cfg.NATSURL, "nats-url", "nats://127.0.0.1:4222", "URL of the world's NATS server or cluster")
	f.StringVar(&cfg.CredsFile, "creds", "", "path to the citadel's NATS credentials file")
	f.StringVar(&cfg.HTTPAddr, "http", ":8080", "address the web application listens on")
	return cmd
}
