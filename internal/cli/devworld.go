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

type devworldOptions struct {
	dataDir    string
	httpAddr   string
	natsListen string
	leafListen string
	host       string
	socketPath string
}

func newDevworldUpCmd() *cobra.Command {
	var o devworldOptions
	cmd := &cobra.Command{
		Use:   "up",
		Short: "Run embedded NATS, a citadel, and a daemon in one process",
		Long: `Devworld up runs an embedded NATS server with JetStream, then a citadel and
a daemon that connect to it, all in one process. The daemon makes this
machine an island and the citadel serves the web application. Daemons on
other machines can join the world through the embedded server's leaf node
listener, which needs --leaf-listen on an address they can reach.

Nothing is authenticated yet, so every listener defaults to 127.0.0.1.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDevworld(cmd.Context(), o)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.dataDir, "data-dir", defaultDataDir(), "directory for the JetStream stores and daemon state")
	f.StringVar(&o.httpAddr, "http", "127.0.0.1:8080", "address the web application listens on")
	f.StringVar(&o.natsListen, "nats-listen", "127.0.0.1:4222", "address the embedded NATS server accepts clients on")
	f.StringVar(&o.leafListen, "leaf-listen", "127.0.0.1:7422", "address the embedded NATS server accepts daemon leaf nodes on")
	f.StringVar(&o.host, "host", daemon.DefaultHost(), "name of this machine's island")
	f.StringVar(&o.socketPath, "socket", defaultSocketPath(), "Unix socket the daemon listens on for the local CLI")
	return cmd
}

// runDevworld starts the embedded server, then runs the citadel and the
// daemon against it, the same code paths "citadel up" and "daemon up" run,
// until one of them stops or ctx is cancelled.
func runDevworld(ctx context.Context, o devworldOptions) error {
	srv, err := natsembed.Start(ctx, natsembed.Config{
		Name:       "skylands-devworld",
		ClientAddr: o.natsListen,
		LeafAddr:   o.leafListen,
		StoreDir:   filepath.Join(o.dataDir, "nats"),
		Domain:     "world",
	})
	if err != nil {
		return err
	}
	defer srv.Shutdown()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errc := make(chan error, 2)
	go func() {
		errc <- citadel.Run(ctx, citadel.Config{NATSURL: srv.ClientURL(), HTTPAddr: o.httpAddr})
	}()
	go func() {
		errc <- daemon.Run(ctx, daemon.Config{Host: o.host, NATSURL: srv.LeafURL(), DataDir: o.dataDir, SocketPath: o.socketPath, Version: versionString()})
	}()
	err = <-errc
	cancel()
	if other := <-errc; other != nil && !errors.Is(other, context.Canceled) {
		err = errors.Join(err, other)
	}
	return err
}
