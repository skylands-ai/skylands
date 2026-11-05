package cli

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"sync/atomic"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

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
	cmd.AddCommand(newDevworldUpCmd(), newDevworldInstallCmd(), newUninstallCmd("devworld"))
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

The embedded NATS server asks for no credentials, so every listener defaults
to 127.0.0.1.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDevworld(cmd.Context(), o, cmd.ErrOrStderr())
		},
	}
	devworldUpFlags(cmd.Flags(), &o)
	return cmd
}

// devworldUpFlags registers the flags "devworld up" takes, which
// "devworld install" takes too.
func devworldUpFlags(f *pflag.FlagSet, o *devworldOptions) {
	f.StringVar(&o.dataDir, "data-dir", defaultDataDir(), "directory for the JetStream stores and daemon state")
	f.StringVar(&o.httpAddr, "http", "127.0.0.1:8080", "address the web application listens on")
	f.StringVar(&o.natsListen, "nats-listen", "127.0.0.1:4222", "address the embedded NATS server accepts clients on")
	f.StringVar(&o.leafListen, "leaf-listen", "127.0.0.1:7422", "address the embedded NATS server accepts daemon leaf nodes on")
	f.StringVar(&o.host, "host", daemon.DefaultHost(), "name of this machine's island")
	f.StringVar(&o.socketPath, "socket", defaultSocketPath(), "Unix socket the daemon listens on for the local CLI")
}

func newDevworldInstallCmd() *cobra.Command {
	var o devworldOptions
	return newInstallCmd(installOptions{
		role:        "devworld",
		description: "Skylands one-machine world: NATS, a citadel, and a daemon",
		flags:       func(f *pflag.FlagSet) { devworldUpFlags(f, &o) },
		pathFlags:   []string{"data-dir", "socket"},
		socket:      func() string { return o.socketPath },
	})
}

// runDevworld starts the embedded server, then runs the citadel and the
// daemon against it, the same code paths "citadel up" and "daemon up" run,
// until one of them stops or ctx is cancelled. The setup link, while there
// is no world, goes to stderr apart from the log.
func runDevworld(ctx context.Context, o devworldOptions, stderr io.Writer) error {
	srv, err := natsembed.Start(ctx, natsembed.Config{
		Name:       "skylands-devworld",
		ClientAddr: o.natsListen,
		LeafAddr:   o.leafListen,
		StoreDir:   filepath.Join(o.dataDir, "nats"),
		Domain:     daemon.DefaultWorldDomain,
	})
	if err != nil {
		return err
	}
	defer srv.Shutdown()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// The citadel says where to set the world up, and the daemon reports
	// it in its status, which is how "devworld install" prints it.
	var setupLink atomic.Value
	setupLink.Store("")
	errc := make(chan error, 2)
	go func() {
		errc <- citadel.Run(ctx, citadel.Config{
			NATSURL:  srv.ClientURL(),
			HTTPAddr: o.httpAddr,
			LeafURL:  srv.LeafURL(),
			OnSetupLink: func(link string) {
				setupLink.Store(link)
				showSetupLink(stderr, link)
			},
		})
	}()
	go func() {
		// devworld's daemon runs in the same process as the citadel and the
		// NATS server, so it pins the owner key of the first world it finds.
		errc <- daemon.Run(ctx, daemon.Config{
			Host: o.host, NATSURL: srv.LeafURL(), DataDir: o.dataDir, SocketPath: o.socketPath, Version: versionString(),
			TrustFirstWorld: true,
			WorldDomain:     daemon.DefaultWorldDomain,
			SetupURL:        func() string { return setupLink.Load().(string) },
		})
	}()
	err = <-errc
	cancel()
	if other := <-errc; other != nil && !errors.Is(other, context.Canceled) {
		err = errors.Join(err, other)
	}
	return err
}
