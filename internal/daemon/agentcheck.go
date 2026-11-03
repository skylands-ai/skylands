package daemon

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/daemon/agents"
	"github.com/skylands-ai/skylands/internal/daemon/host"
	"github.com/skylands-ai/skylands/internal/preflight"
)

// DefaultAgentCheckInterval is how often a daemon checks again which agents
// it can launch, after its check at startup. A login fixed in between
// shows when "skylands preflight" asks the daemon to check, or at the next
// interval.
const DefaultAgentCheckInterval = 10 * time.Minute

// agentChecker finds which agents this host can launch, with the same
// checks "skylands preflight" makes but in the daemon's own environment,
// which for a service is not the shell's, and records on the Host
// aggregate what changed.
type agentChecker struct {
	agg      *host.Host
	registry agents.Registry
	// env makes the checks; tests replace it.
	env func() preflight.Env
	// mu runs one check at a time.
	mu sync.Mutex
	// logged is whether a check's outcome has been logged, which the first
	// one always is.
	logged bool
}

func newAgentChecker(agg *host.Host, registry agents.Registry) *agentChecker {
	if registry == nil {
		registry = agents.DefaultRegistry()
	}
	c := &agentChecker{agg: agg, registry: registry}
	c.env = func() preflight.Env {
		e := preflight.Default("", "")
		e.Registry = c.registry
		return e
	}
	return c
}

// check makes the checks now, records the outcome when it changed, and
// returns it.
func (c *agentChecker) check(ctx context.Context) ([]v1.AgentAvailability, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	checks := preflight.RunAgents(ctx, c.env())
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	found := preflight.Availability(checks, c.registry)
	changed, err := c.agg.RecordAgents(ctx, found)
	if err != nil {
		return found, err
	}
	if changed || !c.logged {
		c.logged = true
		var ready []string
		for _, a := range found {
			if a.Available {
				ready = append(ready, a.Name)
				continue
			}
			var why []string
			for _, n := range a.Notes {
				if n.Failed {
					why = append(why, n.Check+": "+n.Found)
				}
			}
			slog.Warn("daemon: agent cannot run", "agent", a.Name, "why", strings.Join(why, "; "))
		}
		slog.Info("daemon: agents checked", "ready", strings.Join(ready, ","), "changed", changed)
	}
	return found, nil
}

// run checks at once and then every interval, until ctx ends.
func (c *agentChecker) run(ctx context.Context, every time.Duration) {
	for {
		if _, err := c.check(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("daemon: check agents", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}
