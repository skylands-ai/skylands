// Package daemon implements the daemon role, which turns one host into an
// island. A daemon launches agents over the Agent Client Protocol, owns their
// event-sourced sessions, enforces the world's rules locally, verifies
// approval signatures, snapshots the terrain of registered repos, and buffers
// events in an embedded NATS leaf node while the world's server is
// unreachable. A local CLI reaches it over a Unix socket.
package daemon

import (
	"context"

	"github.com/skylands-ai/skylands/internal/stub"
)

// Config holds what a daemon needs to join a world and run agents on this
// host.
type Config struct {
	// NATSURL is the URL of the world's NATS leaf node listener that the
	// embedded leaf node dials.
	NATSURL string
	// CredsFile is the path to the daemon's NATS credentials file.
	CredsFile string
	// DataDir holds the leaf node's JetStream store and agent worktrees.
	DataDir string
	// SocketPath is the Unix socket the local CLI connects to.
	SocketPath string
}

// Run joins the world and runs agents until ctx is cancelled.
func Run(ctx context.Context, cfg Config) error {
	return stub.ErrUnimplemented
}
