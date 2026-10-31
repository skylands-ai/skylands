// Package natsembed runs nats-server in-process. "skylands devworld up" uses it for
// the world's server, and the daemon uses it for its leaf node, which dials
// out to the world's server and holds the host's JetStream domain while the
// link is down.
package natsembed

import (
	"context"

	"github.com/skylands-ai/skylands/internal/stub"
)

// Config describes the embedded server for a one-machine world.
type Config struct {
	// DataDir is the JetStream store directory.
	DataDir string
}

// Server is a running embedded nats-server.
type Server struct{}

// Start runs an embedded nats-server configured for a one-machine world and
// returns once it accepts connections.
func Start(ctx context.Context, cfg Config) (*Server, error) {
	return nil, stub.ErrUnimplemented
}

// ClientURL is the URL citadels connect to.
func (s *Server) ClientURL() string { return "" }

// LeafURL is the URL daemons' leaf nodes connect to.
func (s *Server) LeafURL() string { return "" }

// Shutdown stops the server and waits for it to exit.
func (s *Server) Shutdown() {}
