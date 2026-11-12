package citadel

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/citadel/projections"
	"github.com/skylands-ai/skylands/internal/citadel/web"
)

// catchUp keeps track of what the citadel still has to read before its
// projections show the world as it is now.
//
// A host's stream holds every event its sessions ever had, so following it
// from the start takes the projections through every state they were ever
// in. A browser sent each of those states as a restarted citadel read them
// would watch a conversation's controls flicker through its history. So
// nothing about a host goes to browsers while its stored events are read,
// and once nothing is being read, every browser gets a new snapshot. Until
// then, browsers are told what is still being read.
//
// As it starts, the citadel also waits for the hosts it followed before,
// which it knows by their mirrors in the world's JetStream, since until
// they answer, the world it would show is missing them. It waits until a
// host has answered and been read, or as long as it takes to call a host
// unreachable.
type catchUp struct {
	// report keeps changes and their reports in the same order.
	report sync.Mutex

	mu sync.Mutex
	// listed is true once the citadel has listed the hosts it followed before.
	listed bool
	// expected holds the hosts followed before that haven't been read yet,
	// while the citadel still waits for them.
	expected map[string]bool
	// reading holds the hosts whose stored events are being read, and read
	// the hosts whose stored events have been.
	reading, read map[string]bool
	// last is what is still being read, or nil when nothing is.
	last *web.Loading
	// changed is told each time last changes.
	changed func(*web.Loading)
}

func newCatchUp(changed func(*web.Loading)) *catchUp {
	if changed == nil {
		changed = func(*web.Loading) {}
	}
	return &catchUp{expected: map[string]bool{}, reading: map[string]bool{}, read: map[string]bool{}, last: &web.Loading{NATS: true}, changed: changed}
}

// state is what is still being read, or nil when nothing is.
func (c *catchUp) state() *web.Loading {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last
}

// holds says whether what the projections have of host is kept from
// browsers for now.
func (c *catchUp) holds(host string) bool {
	return holding(c.state(), host)
}

func holding(l *web.Loading, host string) bool {
	return l != nil && (l.NATS || slices.Contains(l.Hosts, host))
}

// listHosts records the hosts followed before, which the citadel waits for
// as it starts. Only the first list counts.
func (c *catchUp) listHosts(hosts []string) {
	c.update(func() {
		if c.listed {
			return
		}
		c.listed = true
		for _, h := range hosts {
			if !c.read[h] {
				c.expected[h] = true
			}
		}
	})
}

// stopWaiting stops waiting for the hosts followed before that haven't
// answered.
func (c *catchUp) stopWaiting() {
	c.update(func() { clear(c.expected) })
}

// start records that host's stored events are about to be read.
func (c *catchUp) start(host string) {
	c.update(func() { c.reading[host] = true })
}

// done records that host's stored events have been read, or, when read is
// false, that reading them stopped short and won't be waited for.
func (c *catchUp) done(host string, read bool) {
	c.update(func() {
		delete(c.reading, host)
		delete(c.expected, host)
		if read {
			c.read[host] = true
		}
	})
}

func (c *catchUp) update(change func()) {
	c.report.Lock()
	defer c.report.Unlock()
	c.mu.Lock()
	change()
	var now *web.Loading
	if !c.listed || len(c.expected) > 0 || len(c.reading) > 0 {
		hosts := make([]string, 0, len(c.expected)+len(c.reading))
		for h := range c.expected {
			hosts = append(hosts, h)
		}
		for h := range c.reading {
			if !c.expected[h] {
				hosts = append(hosts, h)
			}
		}
		slices.Sort(hosts)
		now = &web.Loading{NATS: !c.listed, Hosts: hosts}
	}
	same := sameLoading(now, c.last)
	c.last = now
	c.mu.Unlock()
	if !same {
		c.changed(now)
	}
}

func sameLoading(a, b *web.Loading) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.NATS == b.NATS && slices.Equal(a.Hosts, b.Hosts)
}

// onLoading tells browsers what the citadel is still reading, and once
// it's nothing, sends them the world as it is now.
func (c *citadel) onLoading(l *web.Loading) {
	if c.web == nil {
		return
	}
	if l != nil {
		slog.Debug("citadel: catching up", "nats", l.NATS, "hosts", l.Hosts)
		c.web.Broadcast(web.Message{Type: "loading", Loading: l})
		return
	}
	slog.Info("citadel: caught up with the world's events")
	c.web.Broadcast(c.snapshot())
}

// listHosts lists the hosts this world's citadels followed before, by
// their mirrors, and waits for them for as long as it takes to call a host
// unreachable.
func (c *citadel) listHosts(nc *nats.Conn) {
	ctx, cancel := context.WithTimeout(c.ctx, 15*time.Second)
	defer cancel()
	var hosts []string
	js, err := jetstream.New(nc)
	if err == nil {
		names := js.StreamNames(ctx)
		for name := range names.Name() {
			if host, ok := strings.CutPrefix(name, v1.IslandStreamName("")); ok && host != "" {
				hosts = append(hosts, host)
			}
		}
		err = names.Err()
	}
	if err != nil {
		slog.Warn("citadel: list the hosts followed before, so the world shows without waiting for them", "err", err)
	}
	c.catch.listHosts(hosts)
	if len(hosts) > 0 {
		time.AfterFunc(projections.MissesBeforeUnreachable*c.cfg.PollInterval+c.cfg.PollWindow, c.catch.stopWaiting)
	}
}
