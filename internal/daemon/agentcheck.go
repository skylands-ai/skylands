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
//
// Reading an agent's models opens a session in it, which starts what a
// session needs, such as its MCP servers, so the daemon reads them at
// startup and when "skylands preflight" asks it to check, and between
// those only for an agent whose login changed or whose models it has not
// read.
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
	// models keeps what each agent offered when its models were last read,
	// with the login they were read for.
	models map[string]v1.AgentAvailability
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

// check makes the checks now, reading every agent's models, records the
// outcome when it changed, and returns it.
func (c *agentChecker) check(ctx context.Context) ([]v1.AgentAvailability, error) {
	return c.checkAgents(ctx, true)
}

// recheck makes the checks again, reading models only where they may have
// changed.
func (c *agentChecker) recheck(ctx context.Context) ([]v1.AgentAvailability, error) {
	return c.checkAgents(ctx, false)
}

func (c *agentChecker) checkAgents(ctx context.Context, allModels bool) ([]v1.AgentAvailability, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	env := c.env()
	if !allModels {
		env.Models = func(string) bool { return false }
	}
	found := preflight.Availability(preflight.RunAgents(ctx, env), c.registry)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !allModels {
		// The agents whose models are not known for their login are
		// checked again, alone, reading them.
		if again := c.fromCache(found); len(again) > 0 {
			env := c.env()
			env.Registry = agents.Registry{}
			for _, name := range again {
				env.Registry[name] = c.registry[name]
			}
			more := preflight.Availability(preflight.RunAgents(ctx, env), env.Registry)
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			for _, a := range more {
				for i := range found {
					if found[i].Name == a.Name {
						found[i] = a
					}
				}
			}
		}
	}
	c.remember(found)
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
			var why, skipped []string
			for _, n := range a.Notes {
				if n.Failed {
					why = append(why, n.Check+": "+n.Found)
				} else {
					skipped = append(skipped, n.Check+": "+n.Found)
				}
			}
			// An agent that isn't installed, while another is, is no fault.
			if len(why) == 0 {
				slog.Info("daemon: agent not installed", "agent", a.Name, "why", strings.Join(skipped, "; "))
				continue
			}
			slog.Warn("daemon: agent cannot run", "agent", a.Name, "why", strings.Join(why, "; "))
		}
		slog.Info("daemon: agents checked", "ready", strings.Join(ready, ","), "changed", changed)
	}
	return found, nil
}

// fromCache gives each agent that can run the models last read for its
// login, and names the ones it has none for.
func (c *agentChecker) fromCache(found []v1.AgentAvailability) (unknown []string) {
	for i, a := range found {
		if !a.Available {
			continue
		}
		known, ok := c.models[a.Name]
		if !ok || known.Login != a.Login {
			unknown = append(unknown, a.Name)
			continue
		}
		found[i].ModelsRead, found[i].Models, found[i].DefaultModel, found[i].Modes = true, known.Models, known.DefaultModel, known.Modes
	}
	return unknown
}

// remember keeps the models read for each agent that can run, with its
// login, and forgets those of an agent that cannot.
func (c *agentChecker) remember(found []v1.AgentAvailability) {
	if c.models == nil {
		c.models = map[string]v1.AgentAvailability{}
	}
	for _, a := range found {
		switch {
		case !a.Available:
			delete(c.models, a.Name)
		case a.ModelsRead:
			c.models[a.Name] = v1.AgentAvailability{Login: a.Login, Models: a.Models, DefaultModel: a.DefaultModel, Modes: a.Modes}
		}
	}
}

// run checks at once, reading every agent's models, and then every
// interval, until ctx ends.
func (c *agentChecker) run(ctx context.Context, every time.Duration) {
	for first := true; ; first = false {
		check := c.recheck
		if first {
			check = c.check
		}
		if _, err := check(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("daemon: check agents", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}
