package daemon

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/skylands-ai/skylands/internal/approval"
)

// DefaultWorldDomain is the JetStream domain a daemon looks for the world's
// approver registry in, unless told another: the one devworld gives its
// NATS server.
const DefaultWorldDomain = "world"

// approvers is the world's approver registry as this daemon reads it, from
// the stream in the world's JetStream domain, reached through the island's
// leaf node. Nothing the citadel says goes into it: every record is
// signed, and the stream keeps every one.
type approvers struct {
	pin     *worldPin
	mu      sync.Mutex
	records []approval.Record
	// read is true once the daemon has read what the stream held.
	read bool
}

// follow reads the registry in domain until ctx ends, reading again if
// the leaf's link to the world drops it.
func (a *approvers) follow(ctx context.Context, nc *nats.Conn, domain string) {
	js, err := jetstream.NewWithDomain(nc, domain)
	if err != nil {
		slog.Warn("daemon: reach the world's approvers", "domain", domain, "err", err)
		return
	}
	logged := -1
	for ctx.Err() == nil {
		approval.Follow(ctx, js, func(records []approval.Record) {
			a.mu.Lock()
			a.records, a.read = records, true
			a.mu.Unlock()
			if n, ok := a.count(); ok && n != logged {
				logged = n
				slog.Info("daemon: approvers", "count", n, "domain", domain)
			}
		})
		select {
		case <-ctx.Done():
		case <-time.After(2 * time.Second):
		}
	}
}

// registry is the registry for the world this island belongs to, or nil
// before the daemon knows the world's owner or has read the stream.
func (a *approvers) registry() *approval.Registry {
	owner := a.pin.get()
	a.mu.Lock()
	defer a.mu.Unlock()
	if owner == "" || !a.read {
		return nil
	}
	return approval.NewRegistry(owner, a.records)
}

// count is how many approvers approve for the world, once known.
func (a *approvers) count() (int, bool) {
	r := a.registry()
	if r == nil {
		return 0, false
	}
	n := 0
	for _, x := range r.Approvers() {
		if r.Valid(x.Key) {
			n++
		}
	}
	return n, true
}
