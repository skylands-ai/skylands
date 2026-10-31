// Package projections holds the citadel's in-memory views of the world. Each
// one is a pure function of what the citadel observes, so every citadel
// converges on the same state without talking to the others.
package projections

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"
)

// Island is one host as the citadel last heard it. An island is never
// removed: a host the citadel can't reach keeps its last state, along with
// when it was last heard.
type Island struct {
	Host string `json:"host"`
	// Reachable is false once the host has missed enough pings. The citadel
	// can't tell a dropped network from a stopped daemon, so both look the
	// same here.
	Reachable bool      `json:"reachable"`
	FirstSeen time.Time `json:"firstSeen"`
	LastSeen  time.Time `json:"lastSeen"`
	Daemon    Daemon    `json:"daemon"`
}

// Daemon is what a daemon reports about itself when pinged.
type Daemon struct {
	// ID is the service instance id. It changes when the daemon restarts.
	ID      string    `json:"id"`
	Version string    `json:"version"`
	OS      string    `json:"os"`
	Arch    string    `json:"arch"`
	Started time.Time `json:"started"`
}

// Reply is one daemon's answer to a ping.
type Reply struct {
	ID       string
	Version  string
	Metadata map[string]string
}

// Pinger asks every reachable daemon to answer within a window.
type Pinger interface {
	Ping(ctx context.Context) ([]Reply, error)
}

// MissesBeforeUnreachable is how many polls in a row a host may miss before
// its island goes grey.
const MissesBeforeUnreachable = 3

// Islands is the projection of hosts and their reachability. It is safe for
// concurrent use.
type Islands struct {
	mu       sync.Mutex
	byHost   map[string]*Island
	missed   map[string]int
	onChange func(Island)
}

// NewIslands returns an empty projection. onChange, if set, is called with a
// copy of each island whose state changed, from the goroutine that observed
// the change.
func NewIslands(onChange func(Island)) *Islands {
	if onChange == nil {
		onChange = func(Island) {}
	}
	return &Islands{byHost: map[string]*Island{}, missed: map[string]int{}, onChange: onChange}
}

// List returns every island, sorted by host.
func (p *Islands) List() []Island {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]Island, 0, len(p.byHost))
	for _, isl := range p.byHost {
		out = append(out, *isl)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Host < out[j].Host })
	return out
}

// Get returns one island by host.
func (p *Islands) Get(host string) (Island, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	isl, ok := p.byHost[host]
	if !ok {
		return Island{}, false
	}
	return *isl, true
}

// Observe applies one poll's replies at time now. Hosts that answered are
// reachable and up to date. Hosts that didn't accrue a miss, and become
// unreachable at MissesBeforeUnreachable.
func (p *Islands) Observe(replies []Reply, now time.Time) {
	var changed []Island
	p.mu.Lock()
	seen := map[string]bool{}
	for _, r := range replies {
		host := r.Metadata["host"]
		if host == "" || seen[host] {
			continue
		}
		seen[host] = true
		d := daemonFrom(r)
		isl, ok := p.byHost[host]
		switch {
		case !ok:
			isl = &Island{Host: host, Reachable: true, FirstSeen: now, LastSeen: now, Daemon: d}
			p.byHost[host] = isl
			changed = append(changed, *isl)
		case !isl.Reachable || isl.Daemon != d:
			isl.Reachable = true
			isl.LastSeen = now
			isl.Daemon = d
			changed = append(changed, *isl)
		default:
			isl.LastSeen = now
		}
		p.missed[host] = 0
	}
	for host, isl := range p.byHost {
		if seen[host] || !isl.Reachable {
			continue
		}
		p.missed[host]++
		if p.missed[host] >= MissesBeforeUnreachable {
			isl.Reachable = false
			changed = append(changed, *isl)
		}
	}
	p.mu.Unlock()
	for _, isl := range changed {
		p.onChange(isl)
	}
}

func daemonFrom(r Reply) Daemon {
	started, _ := time.Parse(time.RFC3339, r.Metadata["started"])
	version := r.Metadata["skylands_version"]
	if version == "" {
		version = r.Version
	}
	return Daemon{ID: r.ID, Version: version, OS: r.Metadata["os"], Arch: r.Metadata["arch"], Started: started}
}

// Poll pings on every tick of interval and feeds the replies to islands
// until ctx is cancelled. A failed ping counts as no replies, because the
// citadel can't tell the difference from here.
func Poll(ctx context.Context, pinger Pinger, islands *Islands, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		replies, err := pinger.Ping(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			slog.Debug("citadel: ping daemons", "err", err)
			replies = nil
		}
		islands.Observe(replies, time.Now())
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
