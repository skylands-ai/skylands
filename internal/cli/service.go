package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/skylands-ai/skylands/internal/daemon/socket"
	"github.com/skylands-ai/skylands/internal/service"
)

// newServiceManager is the service manager install and uninstall use.
// Tests replace it.
var newServiceManager = service.ForThisMachine

// executable is the binary the service runs. Tests replace it, because a
// test binary lives in a temporary directory, which install refuses.
var executable = service.Executable

// installWait is how long install waits for the new service's daemon to
// answer on its socket.
var installWait = 20 * time.Second

// installOptions describes the install command of one role, which runs
// "skylands <role> up" as a service.
type installOptions struct {
	role        string
	description string
	// flags registers the role's up flags.
	flags func(*pflag.FlagSet)
	// pathFlags are the flags that hold paths, which the service gets as
	// absolute paths because it does not start in this directory.
	pathFlags []string
	// socket is the daemon socket the flags resolve to.
	socket func() string
}

func newInstallCmd(o installOptions) *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "install",
		Short: fmt.Sprintf("Run %q as a service that starts at login and restarts when it fails", "skylands "+o.role+" up"),
		Long: fmt.Sprintf(`Install writes a user service that runs "skylands %[1]s up" from this
binary with the flags given here, and starts it. The service starts when
you log in, restarts when it fails, and keeps running after this terminal
closes: a systemd user unit on Linux, a launchd agent on macOS. It gets
this shell's PATH, so it finds npx, git, and gh where you do.

Installing again replaces the service with the new flags and restarts it.
The service follows its binary, so upgrading or uninstalling skylands, such
as with Homebrew, needs nothing more: once no agent is working on a turn,
it restarts on a new binary, or removes itself when the binary has been
gone for a few minutes. "skylands %[1]s uninstall" stops and removes it.`, o.role),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := service.CheckName(name); err != nil {
				return err
			}
			exe, err := executable()
			if err != nil {
				return err
			}
			upArgs, err := serviceArgs(cmd.Flags(), o.pathFlags)
			if err != nil {
				return err
			}
			m, err := newServiceManager()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			sock, err := filepath.Abs(o.socket())
			if err != nil {
				return err
			}
			// A daemon run by hand on the same socket would lose its socket
			// to the service and share its data directory.
			if !m.Active(ctx, name) {
				if st, err := socket.Dial(sock).Status(ctx); err == nil {
					return fmt.Errorf("the daemon for island %s is already running on %s; stop it first, so the service does not share its socket and data directory", st.Host, sock)
				}
			}
			spec := service.Spec{
				Name:        name,
				Description: o.description,
				Args:        append([]string{exe, o.role, "up"}, upArgs...),
				// So "up" knows it is this service, and follows its binary.
				Env: map[string]string{service.EnvName: name},
			}
			if path := os.Getenv("PATH"); path != "" {
				spec.Env["PATH"] = path
			}
			began := time.Now()
			if err := m.Install(ctx, spec); err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "Installed %s at %s\n", name, m.Path(name))
			fmt.Fprintf(w, "Runs:     %s\n", strings.Join(spec.Args, " "))
			st, waitErr := waitForDaemon(ctx, sock, began)
			if waitErr == nil {
				fmt.Fprintf(w, "Island:   %s is up, skylands %s, joining %s\n", st.Host, st.Version, st.World)
				if o.role == "devworld" {
					if link := waitForSetupLink(ctx, sock); link != "" {
						fmt.Fprintf(w, "Set up:   open %s to set the world up\n", link)
					}
				}
			}
			for _, h := range m.Hints(ctx, name) {
				fmt.Fprintln(w, h)
			}
			if waitErr != nil {
				return fmt.Errorf("%s is installed, but its daemon did not answer on %s within %s; its logs say why", name, sock, installWait)
			}
			return nil
		},
	}
	o.flags(cmd.Flags())
	cmd.Flags().StringVar(&name, "name", "skylands-"+o.role, "name of the service")
	return cmd
}

// serviceArgs turns the up flags set on the command line into arguments
// for the service, with paths made absolute. Flags left at their defaults
// are left out, so the service resolves them the way "up" does.
func serviceArgs(f *pflag.FlagSet, pathFlags []string) ([]string, error) {
	isPath := map[string]bool{}
	for _, p := range pathFlags {
		isPath[p] = true
	}
	var args []string
	var err error
	f.Visit(func(fl *pflag.Flag) {
		if fl.Name == "name" || err != nil {
			return
		}
		v := fl.Value.String()
		if isPath[fl.Name] && v != "" {
			v, err = filepath.Abs(v)
		}
		args = append(args, "--"+fl.Name+"="+v)
	})
	return args, err
}

// setupLinkWait is how long "devworld install" waits for the citadel to
// say whether the world needs setting up, which it knows once it has
// reached NATS.
const setupLinkWait = 10 * time.Second

// waitForSetupLink waits for devworld's daemon to report either the link
// to set the world up, or the world it joined, and returns the link, or
// "" when the world is already set up or nothing was reported in time.
func waitForSetupLink(ctx context.Context, path string) string {
	ctx, cancel := context.WithTimeout(ctx, setupLinkWait)
	defer cancel()
	for {
		if st, err := socket.Dial(path).Status(ctx); err == nil && (st.SetupURL != "" || st.WorldOwner != "") {
			return st.SetupURL
		}
		select {
		case <-ctx.Done():
			return ""
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// waitForDaemon waits for a daemon started after since to answer on its
// socket, so an old daemon still shutting down does not count.
func waitForDaemon(ctx context.Context, path string, since time.Time) (socket.StatusInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, installWait)
	defer cancel()
	for {
		st, err := socket.Dial(path).Status(ctx)
		if err == nil && !st.Started.Before(since.Add(-time.Second)) {
			return st, nil
		}
		select {
		case <-ctx.Done():
			return socket.StatusInfo{}, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func newUninstallCmd(role string) *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: fmt.Sprintf("Stop and remove the service %q wrote", "skylands "+role+" install"),
		Long: `Uninstall stops the service and removes its definition. The data
directory, with its sessions, transcripts, and worktrees, stays.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := service.CheckName(name); err != nil {
				return err
			}
			m, err := newServiceManager()
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			path := m.Path(name)
			switch err := m.Uninstall(cmd.Context(), name); {
			case errors.Is(err, service.ErrNotInstalled):
				fmt.Fprintf(w, "%s is not installed\n", name)
				return nil
			case err != nil:
				return err
			}
			fmt.Fprintf(w, "Stopped %s and removed %s. Its data directory is untouched.\n", name, path)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "skylands-"+role, "name of the service")
	return cmd
}
