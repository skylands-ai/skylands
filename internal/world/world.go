// Package world creates and administers worlds. A world is one NATS account:
// its subject space is the system's public API, and holding credentials
// scoped to that account is what membership means. Init creates the world's
// owner key and account and generates the NATS settings the world needs, and
// EnrollDaemon signs daemon enrollments with the owner key, which stays
// offline with the world's owner.
package world

import (
	"context"

	"github.com/skylands-ai/skylands/internal/stub"
)

// InitOptions describes the world to create.
type InitOptions struct {
	// Name is the world's name.
	Name string
	// Dir is where the owner key and generated NATS settings are written.
	Dir string
}

// Init creates the owner key and NATS account and writes the NATS settings
// the world needs.
func Init(ctx context.Context, opts InitOptions) error {
	return stub.ErrUnimplemented
}

// EnrollDaemonOptions names the host to enroll.
type EnrollDaemonOptions struct {
	// WorldDir is the directory Init wrote, which holds the owner key.
	WorldDir string
	// Host is the name of the host the daemon runs on.
	Host string
}

// EnrollDaemon signs a daemon enrollment with the owner key and issues the
// daemon's credentials.
func EnrollDaemon(ctx context.Context, opts EnrollDaemonOptions) error {
	return stub.ErrUnimplemented
}
