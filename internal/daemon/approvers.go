package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	v1 "github.com/skylands-ai/skylands/api/v1"
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
	// read is true once the daemon has read what the stream held, and
	// lastSeq is the stream sequence of the last record it read.
	read    bool
	lastSeq uint64
	// js reaches the world's JetStream domain.
	js jetstream.JetStream
}

// follow reads the registry in domain until ctx ends, reading again if
// the leaf's link to the world drops it.
func (a *approvers) follow(ctx context.Context, nc *nats.Conn, domain string) {
	js, err := jetstream.NewWithDomain(nc, domain)
	if err != nil {
		slog.Warn("daemon: reach the world's approvers", "domain", domain, "err", err)
		return
	}
	a.mu.Lock()
	a.js = js
	a.mu.Unlock()
	logged := -1
	for ctx.Err() == nil {
		approval.Follow(ctx, js, func(records []approval.Record) {
			a.mu.Lock()
			a.records, a.read = records, true
			for _, r := range records {
				a.lastSeq = max(a.lastSeq, r.Revision)
			}
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

// verify checks a decision that came through the citadel: its signer
// approves for the world, as the registry stands once the daemon has read
// every record in it, and the signature is that approver's over d.
func (a *approvers) verify(ctx context.Context, d approval.Decision, s v1.DecisionSignature) error {
	if err := a.catchUp(ctx); err != nil {
		return err
	}
	reg := a.registry()
	if reg == nil {
		return errors.New("the daemon hasn't read the world's approvers yet; try again")
	}
	ap, known := reg.Approver(s.Approver)
	switch {
	case !known:
		return errors.New("the decision is signed by a key that isn't an approver for this world")
	case !reg.Valid(s.Approver):
		return fmt.Errorf("%s no longer approves for this world", ap.Name)
	}
	return approval.VerifyDecision(d, s)
}

// catchUp waits until the daemon has read the registry stream as far as
// it goes right now, so a revocation stored before a decision arrived
// counts against it. It gives up after a few seconds.
func (a *approvers) catchUp(ctx context.Context) error {
	a.mu.Lock()
	js := a.js
	a.mu.Unlock()
	if js == nil {
		return errors.New("the daemon hasn't reached the world's approvers yet; try again")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	stream, err := js.Stream(ctx, approval.StreamName)
	if err != nil {
		return fmt.Errorf("the daemon can't reach the world's approvers to check the signature; try again: %w", err)
	}
	info, err := stream.Info(ctx)
	if err != nil {
		return fmt.Errorf("the daemon can't reach the world's approvers to check the signature; try again: %w", err)
	}
	for {
		a.mu.Lock()
		got := a.lastSeq
		a.mu.Unlock()
		if got >= info.State.LastSeq {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("the daemon hasn't caught up with the world's approvers; try again")
		case <-time.After(50 * time.Millisecond):
		}
	}
}
