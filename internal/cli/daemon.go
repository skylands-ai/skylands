package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/skylands-ai/skylands/internal/daemon"
	"github.com/skylands-ai/skylands/internal/daemon/socket"
)

func newDaemonCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "daemon",
		GroupID: groupRoles,
		Short:   "Run, enroll, and inspect the daemon that makes this machine an island",
	}
	cmd.AddCommand(newDaemonUpCmd(), newDaemonInstallCmd(), newUninstallCmd("daemon"), newDaemonStatusCmd())
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
			return runService(cmd.Context(), func(ctx context.Context, stop <-chan struct{}) error {
				cfg.StopWhenIdle = stop
				return daemon.Run(ctx, cfg)
			})
		},
	}
	daemonUpFlags(cmd.Flags(), &cfg)
	return cmd
}

// daemonUpFlags registers the flags "daemon up" takes, which "daemon
// install" takes too.
func daemonUpFlags(f *pflag.FlagSet, cfg *daemon.Config) {
	f.StringVar(&cfg.Host, "host", daemon.DefaultHost(), "name of this machine's island, unique in the world")
	f.StringVar(&cfg.NATSURL, "nats-url", "nats-leaf://127.0.0.1:7422", "URL of the world's NATS leaf node listener")
	f.StringVar(&cfg.CredsFile, "creds", "", "path to the daemon's NATS credentials file")
	f.StringVar(&cfg.DataDir, "data-dir", defaultDataDir(), "directory for the leaf node's JetStream store and agent worktrees")
	cloneRootFlag(f, &cfg.CloneRoot)
	f.StringVar(&cfg.SocketPath, "socket", defaultSocketPath(), "Unix socket to listen on for the local CLI")
	f.StringVar(&cfg.World, "world", "", `owner key of the world to join, from "Add a host" on the citadel`)
	f.StringVar(&cfg.WorldDomain, "world-domain", daemon.DefaultWorldDomain, "JetStream domain of the world's NATS server, where its approvers are")
}

// cloneRootEnv names the clone root when --clone-root doesn't.
const cloneRootEnv = "SKYLANDS_CLONE_ROOT"

func newDaemonInstallCmd() *cobra.Command {
	var cfg daemon.Config
	return newInstallCmd(installOptions{
		role:        "daemon",
		description: "Skylands daemon, which makes this machine an island",
		flags:       func(f *pflag.FlagSet) { daemonUpFlags(f, &cfg) },
		pathFlags:   []string{"creds", "data-dir", "socket", "clone-root"},
		always:      map[string]func() (string, error){"clone-root": cloneRootHere(&cfg.CloneRoot)},
		socket:      func() string { return cfg.SocketPath },
	})
}

// cloneRootFlag registers --clone-root, which "daemon up" and "devworld
// up" take.
func cloneRootFlag(f *pflag.FlagSet, root *string) {
	f.StringVar(root, "clone-root", os.Getenv(cloneRootEnv), "directory a browser adds repositories in, by path or clone URL; $"+cloneRootEnv+", or the working directory")
}

// cloneRootHere is the clone root "up" would use in this shell: root as
// the flags set it, or the working directory. A service doesn't start in
// this directory, so install gives it this one.
func cloneRootHere(root *string) func() (string, error) {
	return func() (string, error) {
		if *root != "" {
			return *root, nil
		}
		return os.Getwd()
	}
}

func newDaemonStatusCmd() *cobra.Command {
	var socketPath string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Report the daemon's link to the world, its repos, and its agents",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := socket.Dial(socketPath).Status(cmd.Context())
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "Host:     %s\n", st.Host)
			fmt.Fprintf(w, "World:    %s\n", st.World)
			if st.WorldOwner != "" {
				fmt.Fprintf(w, "Owner:    %s\n", st.WorldOwner)
			}
			if st.Approvers != nil {
				fmt.Fprintf(w, "Approvers: %d\n", *st.Approvers)
			}
			if st.SetupURL != "" {
				fmt.Fprintf(w, "Set up:   %s\n", st.SetupURL)
			}
			fmt.Fprintf(w, "Daemon:   skylands %s, up %s\n", st.Version, time.Since(st.Started).Round(time.Second))
			if st.CloneRoot != "" {
				fmt.Fprintf(w, "Clone root: %s\n", st.CloneRoot)
			}
			if len(st.Repos) == 0 {
				fmt.Fprintln(w, "Repos:    none registered")
			}
			for i, r := range st.Repos {
				label := "          "
				if i == 0 {
					label = "Repos:    "
				}
				fmt.Fprintf(w, "%s%s  %d files  %s\n", label, r.Name, r.Files, r.Path)
			}
			// Only the agents this host can run are listed; skylands
			// preflight says why another can't.
			var ready []string
			for _, a := range st.Agents {
				if a.Available {
					name := a.Name
					if a.Login != "" {
						name += " (" + a.Login + ")"
					}
					ready = append(ready, name)
				}
			}
			switch {
			case st.Agents == nil:
				fmt.Fprintln(w, "Agents:   not checked yet")
			case len(ready) == 0:
				fmt.Fprintln(w, "Agents:   none can run here; skylands preflight says why")
			default:
				fmt.Fprintf(w, "Agents:   %s\n", strings.Join(ready, ", "))
			}
			for i, s := range st.Sessions {
				label := "          "
				if i == 0 {
					label = "Sessions: "
				}
				prompt := s.Prompt
				if len(prompt) > 48 {
					prompt = prompt[:45] + "..."
				}
				fmt.Fprintf(w, "%s%s  %s on %s  %s  %q\n", label, s.ID, s.Agent, s.Repo, s.Status, prompt)
			}
			return nil
		},
	}
	socketFlag(cmd, &socketPath)
	return cmd
}
