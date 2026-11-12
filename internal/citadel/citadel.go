// Package citadel implements the citadel role: the world's web application
// and observer. A citadel serves the 3D client and the phone inbox to
// browsers that sign in with an approver key, builds projections from the
// world's streams, relays signed approvals to the daemon that owns each
// session, and sends push notifications. It
// connects to NATS as an ordinary client and embeds no server. Any number of
// citadels can run against one world, and they are interchangeable.
package citadel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/citadel/projections"
	"github.com/skylands-ai/skylands/internal/citadel/web"
	"github.com/skylands-ai/skylands/internal/es"
	webclient "github.com/skylands-ai/skylands/web"
)

// Config holds what a citadel needs to join a world.
type Config struct {
	// Version is the skylands version this citadel runs, which the web
	// client shows beside the Skylands name.
	Version string
	// NATSURL is the URL of the world's NATS server or cluster.
	NATSURL string
	// CredsFile is the path to the citadel's NATS credentials file.
	CredsFile string
	// HTTPAddr is the address the web application listens on.
	HTTPAddr string
	// PublicURL is where people open the citadel, used in the links it
	// prints and shows. Empty means http:// and HTTPAddr.
	PublicURL string
	// LeafURL is the address daemons on other hosts dial to join the
	// world, shown where the citadel tells a person how to add a host.
	LeafURL string
	// OnSetupLink, if set, is told the link to set the world up while
	// there is no world, and "" once there is one, and shows it to whoever
	// started the citadel. Without it, the citadel logs the link.
	OnSetupLink func(link string)
	// PollInterval is how often the citadel pings daemons, and PollWindow
	// how long it collects replies. Zero means the defaults of two seconds
	// and one second.
	PollInterval time.Duration
	PollWindow   time.Duration
	// NoPush turns push notifications off, for a citadel that mustn't call
	// the browser vendors' push services. Without it, a citadel whose
	// PublicURL is HTTPS sends them.
	NoPush bool
	// PushClient, if set, sends pushes in place of http.DefaultClient, and
	// PushHold, if set, is how long a push waits while its approver has the
	// world's page in sight, in place of 30 seconds. Tests set both.
	PushClient *http.Client
	PushHold   time.Duration
}

// citadel is one running citadel.
type citadel struct {
	cfg      Config
	ctx      context.Context
	web      *web.Server
	islands  *projections.Islands
	terrain  *projections.Terrain
	sessions *projections.Sessions
	store    *es.Store
	// push sends push notifications, or is nil when this citadel doesn't.
	push *pusher
	// catch is what the citadel still has to read before browsers are sent
	// what it has of a host, and listed lists the hosts it waits for once.
	catch  *catchUp
	listed sync.Once

	mu sync.Mutex
	// world is what the citadel knows about the world it serves.
	world worldView
	nc    *nats.Conn
	// following holds the hosts whose streams this citadel mirrors and
	// follows, and applied the latest sequence of each host's stream the
	// projections have taken in.
	following map[string]bool
	applied   map[string]uint64
}

// Run connects to the world and serves until ctx is cancelled. It keeps
// serving while NATS is unreachable and reconnects on its own, so a citadel
// and the world's server restart independently.
func Run(ctx context.Context, cfg Config) error {
	if err := CheckPublicURL(cfg.PublicURL); err != nil {
		return err
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 2 * time.Second
	}
	if cfg.PollWindow <= 0 {
		cfg.PollWindow = time.Second
	}
	c := &citadel{cfg: cfg, ctx: ctx, following: map[string]bool{}, applied: map[string]uint64{}}
	if on, why := pushOn(cfg, c.publicURL()); on {
		c.push = newPusher(c)
	} else {
		slog.Info("citadel: push notifications off", "why", why)
	}
	c.catch = newCatchUp(c.onLoading)
	c.islands = projections.NewIslands(c.onIsland)
	c.terrain = projections.NewTerrain(c.onRepo)
	c.sessions = projections.NewSessions(c.onSession)
	dist, err := fs.Sub(webclient.Dist, "dist")
	if err != nil {
		return fmt.Errorf("web client: %w", err)
	}
	// go install builds without the client, because web/dist is built by
	// npm and not committed. Such a binary is fine for a daemon, but a
	// citadel from it serves only a page that says so.
	if _, err := fs.Stat(dist, "index.html"); err != nil {
		slog.Warn("web: this binary has no web client, so the browser gets a placeholder page; " +
			"use a release binary from " + web.ReleasesURL + ", or build with make web && make build")
	}
	c.web = web.New(web.Config{Addr: cfg.HTTPAddr, Snapshot: c.snapshot, Dist: dist, API: c.api(), Live: c.live, Terminal: terminals{c}, NeedsSetup: c.needsSetup, Canonical: c.publicURL(), SignedIn: c.signedInAs, InSight: c.inSight})

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
	store, err := es.New(nc)
	if err != nil {
		return err
	}
	// Daemons ask any citadel for the world's record, to check it against
	// the owner key they pinned.
	recordSub, err := nc.QueueSubscribe(v1.WorldRecordSubject, "skylands-citadel", c.answerWorldRecord)
	if err != nil {
		return fmt.Errorf("answer world record requests: %w", err)
	}
	defer func() { _ = recordSub.Unsubscribe() }()
	c.mu.Lock()
	c.nc = nc
	c.store = store
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

// live relays one session's transcript appends from its island to a
// browser, for as long as the browser watches.
func (c *citadel) live(host, id string, deliver func(v1.Live)) (func(), error) {
	if !plainToken(host) || !plainToken(id) {
		return nil, errors.New("not a session")
	}
	c.mu.Lock()
	nc := c.nc
	c.mu.Unlock()
	if nc == nil {
		return nil, errors.New("not connected to NATS")
	}
	sub, err := nc.Subscribe(v1.SessionLiveSubject(host, id), func(m *nats.Msg) {
		var l v1.Live
		if json.Unmarshal(m.Data, &l) == nil {
			deliver(l)
		}
	})
	if err != nil {
		return nil, err
	}
	return func() { _ = sub.Unsubscribe() }, nil
}

// plainToken is true for a subject token with no wildcards or separators.
func plainToken(s string) bool {
	return s != "" && !strings.ContainsAny(s, ".*> \t\r\n")
}

// snapshot is the world as this citadel sees it, sent to each browser as it
// connects, and to all of them once the citadel has caught up. While it is
// still reading a host, the snapshot leaves the host out, so a browser sees
// none of it rather than some point in its history.
func (c *citadel) snapshot() web.Message {
	info := c.serverInfo()
	m := web.Message{Type: "snapshot", Server: &info, World: c.worldInfo(), Approvers: c.approverInfo(), Loading: c.catch.state()}
	m.Islands = slices.DeleteFunc(c.islands.List(), func(i projections.Island) bool { return holding(m.Loading, i.Host) })
	m.Repos = slices.DeleteFunc(c.terrain.List(), func(r projections.Repo) bool { return holding(m.Loading, r.Host) })
	m.Sessions = slices.DeleteFunc(c.sessions.List(), func(s projections.Session) bool { return holding(m.Loading, s.Host) })
	return m
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
	go c.checkWorld(nc)
	c.listed.Do(func() { go c.listHosts(nc) })
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
		c.followHost(isl.Host)
	} else {
		slog.Info("citadel: island unreachable", "host", isl.Host, "lastSeen", isl.LastSeen.Format(time.RFC3339))
	}
	if c.catch.holds(isl.Host) {
		return
	}
	c.web.Broadcast(web.Message{Type: "island", Island: &isl})
}

// onRepo runs whenever a repository is registered or its terrain changes.
func (c *citadel) onRepo(r projections.Repo) {
	if c.catch.holds(r.Host) {
		return
	}
	slog.Info("citadel: terrace", "host", r.Host, "repo", r.Name, "files", len(r.Files))
	c.web.Broadcast(web.Message{Type: "repo", Repo: &r})
}

// onSession runs whenever an agent session changes.
func (c *citadel) onSession(s projections.Session) {
	if c.catch.holds(s.Host) {
		return
	}
	c.web.Broadcast(web.Message{Type: "session", Session: &s})
}

// followHost mirrors a host's stream into the world's domain the first time
// the host is seen, then projects its events. Creating a mirror that already
// exists changes nothing. Until the events the mirror held have been read,
// nothing about the host goes to browsers.
func (c *citadel) followHost(host string) {
	c.mu.Lock()
	if c.following[host] {
		c.mu.Unlock()
		return
	}
	c.following[host] = true
	store := c.store
	c.mu.Unlock()
	c.catch.start(host)

	go func() {
		name := v1.IslandStreamName(host)
		for {
			err := store.EnsureMirror(c.ctx, name, name, host)
			if err == nil {
				break
			}
			slog.Warn("citadel: mirror host stream", "host", host, "err", err)
			select {
			case <-c.ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
		}
		slog.Info("citadel: following host events", "host", host)
		if c.push != nil {
			c.push.follow(host)
		}
		err := store.Follow(c.ctx, name, []string{v1.IslandEventFilter(host), v1.SessionEventFilter(host)}, func(seq uint64, ev v1.Event) {
			if v1.IsSessionEvent(ev.Type) {
				c.sessions.Apply(host, ev)
			} else {
				c.terrain.Apply(host, ev)
				c.islands.Apply(host, ev)
			}
			c.markApplied(host, seq)
		}, func() {
			slog.Info("citadel: read host events", "host", host)
			c.catch.done(host, true)
		})
		if err != nil && c.ctx.Err() == nil {
			slog.Warn("citadel: follow host events", "host", host, "err", err)
			c.mu.Lock()
			delete(c.following, host)
			c.mu.Unlock()
			c.catch.done(host, false)
		}
	}()
}

// terminals relays terminals between browsers and their islands over plain
// NATS. The citadel keeps none of what passes through, and can't make up
// input: the daemon takes only what an approver signed.
type terminals struct{ c *citadel }

func (t terminals) conn(host, id, terminal string) (*nats.Conn, error) {
	if !plainToken(host) || !plainToken(id) || !plainToken(terminal) {
		return nil, errors.New("not a terminal")
	}
	t.c.mu.Lock()
	nc := t.c.nc
	t.c.mu.Unlock()
	if nc == nil {
		return nil, errors.New("not connected to NATS")
	}
	return nc, nil
}

func (t terminals) Watch(host, id, terminal string, deliver func(v1.TerminalOutput)) (func(), error) {
	nc, err := t.conn(host, id, terminal)
	if err != nil {
		return nil, err
	}
	sub, err := nc.Subscribe(v1.TerminalOutputSubject(host, id, terminal), func(m *nats.Msg) {
		var o v1.TerminalOutput
		if json.Unmarshal(m.Data, &o) == nil {
			deliver(o)
		}
	})
	if err != nil {
		return nil, err
	}
	return func() { _ = sub.Unsubscribe() }, nil
}

func (t terminals) Input(host, id, terminal string, in v1.TerminalInput) error {
	return t.publish(host, id, terminal, v1.TerminalInputSubject(host, id, terminal), in)
}

func (t terminals) Resize(host, id, terminal string, size v1.TerminalSize) error {
	return t.publish(host, id, terminal, v1.TerminalSizeSubject(host, id, terminal), size)
}

func (t terminals) publish(host, id, terminal, subject string, v any) error {
	nc, err := t.conn(host, id, terminal)
	if err != nil {
		return err
	}
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return nc.Publish(subject, data)
}
