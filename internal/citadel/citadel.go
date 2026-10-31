// Package citadel implements the citadel role: the world's web application
// and observer. A citadel serves the 3D client and the phone inbox,
// authenticates people with passkeys, builds projections from the world's
// streams, relays signed approvals to the daemon that owns each session,
// sends push notifications, and answers NATS auth callout requests. It
// connects to NATS as an ordinary client and embeds no server. Any number of
// citadels can run against one world, and they are interchangeable.
package citadel

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"sync"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/skylands-ai/skylands/internal/citadel/projections"
	"github.com/skylands-ai/skylands/internal/citadel/web"
	webclient "github.com/skylands-ai/skylands/web"
)

// Config holds what a citadel needs to join a world.
type Config struct {
	// NATSURL is the URL of the world's NATS server or cluster.
	NATSURL string
	// CredsFile is the path to the citadel's NATS credentials file.
	CredsFile string
	// HTTPAddr is the address the web application listens on.
	HTTPAddr string
	// PollInterval is how often the citadel pings daemons, and PollWindow
	// how long it collects replies. Zero means the defaults of two seconds
	// and one second.
	PollInterval time.Duration
	PollWindow   time.Duration
}

// citadel is one running citadel.
type citadel struct {
	cfg     Config
	web     *web.Server
	islands *projections.Islands

	mu sync.Mutex
	nc *nats.Conn
}

// Run connects to the world and serves until ctx is cancelled. It keeps
// serving while NATS is unreachable and reconnects on its own, so a citadel
// and the world's server restart independently.
func Run(ctx context.Context, cfg Config) error {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 2 * time.Second
	}
	if cfg.PollWindow <= 0 {
		cfg.PollWindow = time.Second
	}
	c := &citadel{cfg: cfg}
	c.islands = projections.NewIslands(c.onIsland)
	dist, err := fs.Sub(webclient.Dist, "dist")
	if err != nil {
		return fmt.Errorf("web client: %w", err)
	}
	c.web = web.New(web.Config{Addr: cfg.HTTPAddr, Snapshot: c.snapshot, Dist: dist})

	opts := []nats.Option{
		nats.Name("skylands-citadel"),
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2 * time.Second),
		nats.ConnectHandler(c.onConnect),
		nats.ReconnectHandler(c.onConnect),
		nats.DisconnectErrHandler(c.onDisconnect),
	}
	if cfg.CredsFile != "" {
		opts = append(opts, nats.UserCredentials(cfg.CredsFile))
	}
	nc, err := nats.Connect(cfg.NATSURL, opts...)
	if err != nil {
		return fmt.Errorf("connect to NATS at %s: %w", cfg.NATSURL, err)
	}
	c.mu.Lock()
	c.nc = nc
	c.mu.Unlock()
	defer nc.Close()
	if !nc.IsConnected() {
		slog.Info("citadel: waiting for NATS", "url", cfg.NATSURL)
	}

	pollCtx, stopPolling := context.WithCancel(ctx)
	defer stopPolling()
	go projections.Poll(pollCtx, natsPinger{nc: nc, window: cfg.PollWindow}, c.islands, cfg.PollInterval)

	return c.web.Run(ctx)
}

// snapshot is the world as this citadel sees it, sent to each browser as it
// connects.
func (c *citadel) snapshot() web.Message {
	info := c.serverInfo()
	return web.Message{Type: "snapshot", Server: &info, Islands: c.islands.List()}
}

// serverInfo describes the NATS server as the citadel sees it right now.
func (c *citadel) serverInfo() web.ServerInfo {
	c.mu.Lock()
	nc := c.nc
	c.mu.Unlock()
	if nc == nil || !nc.IsConnected() {
		return web.ServerInfo{Connected: false, URL: c.cfg.NATSURL}
	}
	return infoFrom(nc)
}

func infoFrom(nc *nats.Conn) web.ServerInfo {
	return web.ServerInfo{
		Connected: true,
		URL:       nc.ConnectedUrlRedacted(),
		Name:      nc.ConnectedServerName(),
		ID:        nc.ConnectedServerId(),
		Version:   nc.ConnectedServerVersion(),
		Cluster:   nc.ConnectedClusterName(),
		Servers:   len(nc.Servers()),
		Domain:    nc.ConnectedDomain(),
	}
}

func (c *citadel) onConnect(nc *nats.Conn) {
	info := infoFrom(nc)
	slog.Info("citadel: connected to NATS", "server", info.Name, "version", info.Version, "url", info.URL, "domain", info.Domain)
	c.web.Broadcast(web.Message{Type: "server", Server: &info})
}

func (c *citadel) onDisconnect(nc *nats.Conn, err error) {
	if err != nil {
		slog.Warn("citadel: lost NATS", "err", err)
	}
	info := web.ServerInfo{Connected: false, URL: c.cfg.NATSURL}
	c.web.Broadcast(web.Message{Type: "server", Server: &info})
}

// onIsland runs whenever a host's state changes.
func (c *citadel) onIsland(isl projections.Island) {
	if isl.Reachable {
		slog.Info("citadel: island reachable", "host", isl.Host, "daemon", isl.Daemon.Version, "os", isl.Daemon.OS+"/"+isl.Daemon.Arch)
	} else {
		slog.Info("citadel: island unreachable", "host", isl.Host, "lastHeard", isl.LastSeen.Format(time.RFC3339))
	}
	c.web.Broadcast(web.Message{Type: "island", Island: &isl})
}
