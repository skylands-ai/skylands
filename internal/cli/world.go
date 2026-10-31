package cli

import (
	"github.com/spf13/cobra"

	"github.com/skylands-ai/skylands/internal/world"
)

func newWorldCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "world",
		GroupID: groupWorld,
		Short:   "Create a world",
	}
	cmd.AddCommand(newWorldInitCmd())
	return cmd
}

func newWorldInitCmd() *cobra.Command {
	var opts world.InitOptions
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create a world's owner key, NATS account, and NATS settings",
		Long: `Init creates the world's owner key and NATS account, and generates the NATS
settings the world needs: JetStream with the world's domain, a leaf node
listener on WebSocket port 443, the world's account, and auth callout for
that account. "skylands devworld up" applies them to its embedded server, and a world
running its own nats-server adds them to its configuration.

Keep the owner key offline. It signs daemon and approver enrollments, and
nothing else can.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return world.Init(cmd.Context(), opts)
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.Name, "name", "skylands", "name of the world")
	f.StringVar(&opts.Dir, "dir", defaultWorldDir(), "directory to write the owner key and NATS settings to")
	return cmd
}
