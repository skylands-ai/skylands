package cli

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/skylands-ai/skylands/internal/citadel"
	"github.com/skylands-ai/skylands/internal/daemon"
	"github.com/skylands-ai/skylands/internal/natsembed"
)

func newDevworldCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "devworld",
		GroupID: groupWorld,
		Short:   "Run a one-machine world in a single process",
	}
	cmd.AddCommand(newDevworldUpCmd())
	return cmd
}

func newDevworldUpCmd() *cobra.Command {
	var (
		dataDir    string
		httpAddr   string
		socketPath string
	)
	cmd := &cobra.Command{
		Use:   "up",
		Short: "Run embedded NATS, a citadel, and a daemon in one process",
		Long: `Devworld up runs an embedded NATS server with JetStream, then a citadel and
a daemon that connect to it, all in one process. The daemon makes this
machine an island and the citadel serves the web application. Daemons on
other machines can join the world through the embedded server's leaf node
listener.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDevworld(cmd.Context(), dataDir, httpAddr, socketPath)
		},
	}
	f := cmd.Flags()
	f.StringVar(&dataDir, "data-dir", defaultDataDir(), "directory for the JetStream store and daemon state")
	f.StringVar(&httpAddr, "http", ":8080", "address the web application listens on")
	f.StringVar(&socketPath, "socket", defaultSocketPath(), "Unix socket the daemon listens on for the local CLI")
	return cmd
}

// runDevworld starts the embedded server, then runs the citadel and the
// daemon against it until one of them stops or ctx is cancelled.
func runDevworld(ctx context.Context, dataDir, httpAddr, socketPath string) error {
	srv, err := natsembed.Start(ctx, natsembed.Config{DataDir: filepath.Join(dataDir, "nats")})
	if err != nil {
		return err
	}
	defer srv.Shutdown()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errc := make(chan error, 2)
	go func() {
		errc <- citadel.Run(ctx, citadel.Config{NATSURL: srv.ClientURL(), HTTPAddr: httpAddr})
	}()
	go func() {
		errc <- daemon.Run(ctx, daemon.Config{NATSURL: srv.LeafURL(), DataDir: dataDir, SocketPath: socketPath})
	}()
	err = <-errc
	cancel()
	if other := <-errc; other != nil && !errors.Is(other, context.Canceled) {
		err = errors.Join(err, other)
	}
	return err
}
