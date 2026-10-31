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

	"github.com/skylands-ai/skylands/internal/stub"
)

// Config holds what a citadel needs to join a world.
type Config struct {
	// NATSURL is the URL of the world's NATS server or cluster.
	NATSURL string
	// CredsFile is the path to the citadel's NATS credentials file.
	CredsFile string
	// HTTPAddr is the address the web application listens on.
	HTTPAddr string
}

// Run connects to the world and serves until ctx is cancelled.
func Run(ctx context.Context, cfg Config) error {
	return stub.ErrUnimplemented
}
