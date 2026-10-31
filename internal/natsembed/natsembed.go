// Package natsembed runs nats-server in-process. "skylands devworld up" uses it
// for the world's server, and the daemon uses it for its leaf node, which
// dials out to the world's server and holds the host's JetStream domain while
// the link is down.
package natsembed

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

// Config describes the embedded server for a one-machine world.
type Config struct {
	// Name is the server name clients see.
	Name string
	// ClientAddr is the host:port citadels connect to. Port 0 picks a free
	// port.
	ClientAddr string
	// LeafAddr is the host:port daemons' leaf nodes connect to. Empty
	// disables the leaf node listener.
	LeafAddr string
	// StoreDir is the JetStream store directory.
	StoreDir string
	// Domain is the world's JetStream domain.
	Domain string
}

// LeafConfig describes a daemon's leaf node: a server with no client
// listener that dials out to the world's server and extends it.
type LeafConfig struct {
	// Name is the server name.
	Name string
	// RemoteURL is the world's leaf node listener, such as
	// nats-leaf://host:7422.
	RemoteURL string
	// CredsFile is the daemon's NATS credentials file, or empty.
	CredsFile string
	// StoreDir is the leaf's JetStream store directory.
	StoreDir string
	// Domain is this host's JetStream domain. It must differ from the
	// world's.
	Domain string
}

// Server is a running embedded nats-server.
type Server struct {
	ns      *server.Server
	leafURL string
}

// Start runs an embedded nats-server for a world and returns once it accepts
// connections.
func Start(ctx context.Context, cfg Config) (*Server, error) {
	host, port, err := splitAddr(cfg.ClientAddr)
	if err != nil {
		return nil, fmt.Errorf("client address: %w", err)
	}
	opts := baseOptions(cfg.Name, cfg.StoreDir, cfg.Domain)
	opts.Host = host
	opts.Port = port
	var leafURL string
	if cfg.LeafAddr != "" {
		lh, lp, err := splitAddr(cfg.LeafAddr)
		if err != nil {
			return nil, fmt.Errorf("leaf address: %w", err)
		}
		if lp == server.RANDOM_PORT {
			return nil, errors.New("leaf address needs an explicit port")
		}
		opts.LeafNode.Host = lh
		opts.LeafNode.Port = lp
		leafURL = "nats-leaf://" + net.JoinHostPort(lh, strconv.Itoa(lp))
	}

	ns, err := run(ctx, opts)
	if err != nil {
		return nil, err
	}
	s := &Server{ns: ns, leafURL: leafURL}
	slog.Info("nats: ready", "name", cfg.Name, "version", server.VERSION, "client", s.ClientURL(), "leaf", leafURL, "store", cfg.StoreDir, "domain", cfg.Domain)
	return s, nil
}

// StartLeaf runs an embedded leaf node that dials the world's server. It
// opens no client port: the daemon connects to it in-process through
// InProcess. It returns once the server runs, whether or not the world is
// reachable yet, because the leaf keeps retrying on its own.
func StartLeaf(ctx context.Context, cfg LeafConfig) (*Server, error) {
	u, err := url.Parse(cfg.RemoteURL)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("remote URL %q: want a URL such as nats-leaf://host:7422", cfg.RemoteURL)
	}
	opts := baseOptions(cfg.Name, cfg.StoreDir, cfg.Domain)
	opts.DontListen = true
	opts.LeafNode.Remotes = []*server.RemoteLeafOpts{{URLs: []*url.URL{u}, Credentials: cfg.CredsFile}}

	ns, err := run(ctx, opts)
	if err != nil {
		return nil, err
	}
	slog.Info("nats: leaf node ready", "name", cfg.Name, "remote", cfg.RemoteURL, "store", cfg.StoreDir, "domain", cfg.Domain)
	return &Server{ns: ns}, nil
}

func baseOptions(name, storeDir, domain string) *server.Options {
	return &server.Options{
		ServerName:      name,
		JetStream:       true,
		StoreDir:        storeDir,
		JetStreamDomain: domain,
		NoSigs:          true,
		NoLog:           true,
	}
}

// run starts a server from opts and waits for it to be ready.
func run(ctx context.Context, opts *server.Options) (*server.Server, error) {
	ns, err := server.NewServer(opts)
	if err != nil {
		return nil, fmt.Errorf("configure nats-server: %w", err)
	}
	go ns.Start()

	ready := make(chan bool, 1)
	go func() { ready <- ns.ReadyForConnections(10 * time.Second) }()
	select {
	case ok := <-ready:
		if !ok {
			ns.Shutdown()
			ns.WaitForShutdown()
			return nil, errors.New("nats-server did not become ready")
		}
	case <-ctx.Done():
		ns.Shutdown()
		ns.WaitForShutdown()
		return nil, ctx.Err()
	}
	return ns, nil
}

// ClientURL is the URL clients connect to. It is empty for a leaf node,
// which has no client listener.
func (s *Server) ClientURL() string {
	if s.ns.Addr() == nil {
		return ""
	}
	return s.ns.ClientURL()
}

// LeafURL is the URL daemons' leaf nodes connect to, or empty when the leaf
// node listener is disabled.
func (s *Server) LeafURL() string { return s.leafURL }

// InProcess is the nats.go option that connects to this server without a
// socket. Pass it to nats.Connect with an empty URL.
func (s *Server) InProcess() nats.Option { return nats.InProcessServer(s.ns) }

// Shutdown stops the server and waits for it to exit.
func (s *Server) Shutdown() {
	s.ns.Shutdown()
	s.ns.WaitForShutdown()
}

// splitAddr parses host:port, mapping port 0 to nats-server's random port.
func splitAddr(addr string) (string, int, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, err
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return "", 0, fmt.Errorf("port %q: %w", portStr, err)
	}
	if port == 0 {
		port = server.RANDOM_PORT
	}
	return host, port, nil
}
