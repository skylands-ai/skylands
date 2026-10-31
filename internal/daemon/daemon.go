// Package daemon implements the daemon role, which turns one host into an
// island. A daemon launches agents over the Agent Client Protocol, owns their
// event-sourced sessions, enforces the world's rules locally, verifies
// approval signatures, snapshots the terrain of registered repos, and buffers
// events in an embedded NATS leaf node while the world's server is
// unreachable. A local CLI reaches it over a Unix socket.
package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/micro"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/daemon/host"
	"github.com/skylands-ai/skylands/internal/daemon/socket"
	"github.com/skylands-ai/skylands/internal/es"
	"github.com/skylands-ai/skylands/internal/natsembed"
)

// ServiceName is the NATS micro service every daemon registers. Citadels
// ping it to learn which hosts they can reach.
const ServiceName = "skylands-daemon"

// Config holds what a daemon needs to join a world and run agents on this
// host.
type Config struct {
	// Host names this island. It must be unique in the world and is used in
	// subjects, so it holds only letters, digits, hyphens, and underscores.
	// Empty means DefaultHost.
	Host string
	// NATSURL is the URL of the world's NATS leaf node listener that the
	// embedded leaf node dials.
	NATSURL string
	// CredsFile is the path to the daemon's NATS credentials file.
	CredsFile string
	// DataDir holds the leaf node's JetStream store and agent worktrees. The
	// leaf store lives under DataDir/leaf/<host>, so daemons for different
	// hosts can share a DataDir.
	DataDir string
	// SocketPath is the Unix socket the local CLI connects to. Empty runs
	// no local API.
	SocketPath string
	// Version is the skylands version this daemon reports to the world.
	Version string
}

// Run joins the world and runs agents until ctx is cancelled. The daemon
// keeps running while the world's server is unreachable: its leaf node
// retries the link on its own, and its events wait in the leaf's stream.
func Run(ctx context.Context, cfg Config) error {
	hostName := cleanHost(cfg.Host)
	if hostName == "" {
		hostName = DefaultHost()
	}
	leaf, err := natsembed.StartLeaf(ctx, natsembed.LeafConfig{
		Name:      ServiceName + "-" + hostName,
		RemoteURL: cfg.NATSURL,
		CredsFile: cfg.CredsFile,
		StoreDir:  filepath.Join(cfg.DataDir, "leaf", hostName),
		Domain:    hostName,
	})
	if err != nil {
		return err
	}
	defer leaf.Shutdown()

	nc, err := nats.Connect("", leaf.InProcess(), nats.Name(ServiceName), nats.MaxReconnects(-1))
	if err != nil {
		return fmt.Errorf("connect to leaf node: %w", err)
	}
	defer nc.Close()

	// The host's stream lives in the leaf's JetStream domain, and the
	// daemon is its only writer.
	store, err := es.New(nc)
	if err != nil {
		return err
	}
	if err := store.EnsureStream(ctx, v1.IslandStreamName(hostName), v1.IslandSubjects(hostName)); err != nil {
		return err
	}
	agg, err := host.Load(ctx, store, hostName)
	if err != nil {
		return err
	}

	started := time.Now().UTC()
	svc, err := micro.AddService(nc, micro.Config{
		Name:        ServiceName,
		Version:     semver(cfg.Version),
		Description: "Skylands daemon on " + hostName,
		Metadata: map[string]string{
			"host":             hostName,
			"os":               runtime.GOOS,
			"arch":             runtime.GOARCH,
			"started":          started.Format(time.RFC3339),
			"skylands_version": cfg.Version,
		},
	})
	if err != nil {
		return fmt.Errorf("register %s service: %w", ServiceName, err)
	}
	defer func() { _ = svc.Stop() }()

	slog.Info("daemon: island up", "host", hostName, "world", cfg.NATSURL, "version", cfg.Version, "repos", len(agg.Repos()))

	api := &localAPI{host: hostName, world: cfg.NATSURL, version: cfg.Version, started: started, agg: agg}
	errc := make(chan error, 1)
	if cfg.SocketPath != "" {
		go func() { errc <- socket.Serve(ctx, cfg.SocketPath, api) }()
	}
	select {
	case <-ctx.Done():
		if cfg.SocketPath != "" {
			if err := <-errc; err != nil {
				slog.Warn("daemon: local API stopped", "err", err)
			}
		}
		slog.Info("daemon: stopping", "host", hostName)
		return nil
	case err := <-errc:
		return err
	}
}

// localAPI answers the local CLI from the daemon's aggregates.
type localAPI struct {
	host    string
	world   string
	version string
	started time.Time
	agg     *host.Host
}

func (a *localAPI) Status(context.Context) socket.StatusInfo {
	repos := a.agg.Repos()
	infos := make([]socket.RepoInfo, 0, len(repos))
	for _, r := range repos {
		infos = append(infos, repoInfo(r))
	}
	return socket.StatusInfo{Host: a.host, World: a.world, Version: a.version, Started: a.started, Repos: infos}
}

func (a *localAPI) RegisterRepo(ctx context.Context, path, commandID string) (socket.RepoInfo, error) {
	r, err := a.agg.RegisterRepo(ctx, v1.RegisterRepo{ID: commandID, Path: path})
	return repoInfo(r), err
}

func repoInfo(r host.Repo) socket.RepoInfo {
	return socket.RepoInfo{ID: r.ID, Name: r.Name, Path: r.Path, Commit: r.Commit, Files: r.TotalFiles, RegisteredAt: r.RegisteredAt}
}

// DefaultHost names this machine from its hostname: the first label, in
// lower case, with anything a subject can't carry replaced by a hyphen.
func DefaultHost() string {
	name, err := os.Hostname()
	if err != nil || name == "" {
		return "island"
	}
	name, _, _ = strings.Cut(name, ".")
	if h := cleanHost(name); h != "" {
		return h
	}
	return "island"
}

// cleanHost lowercases a name and replaces every character other than
// letters, digits, hyphens, and underscores with a hyphen, so it is safe in
// a NATS subject and as a JetStream domain.
func cleanHost(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, name)
}

var semverRe = regexp.MustCompile(`^v?(\d+\.\d+\.\d+)([-+][0-9A-Za-z.-]+)?$`)

// semver turns a build version into the semantic version the micro service
// registry requires. A git describe such as a7ab8e1-dirty becomes 0.0.0, and
// the real string still travels in the service metadata.
func semver(v string) string {
	if m := semverRe.FindStringSubmatch(v); m != nil {
		return m[1] + m[2]
	}
	return "0.0.0"
}
