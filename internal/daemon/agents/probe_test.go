package agents

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/skylands-ai/skylands/internal/fakeagent"
)

func TestProbeAgent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd, env := fakeagent.TestCommand()

	// An agent that does not report a login is not waited for.
	start := time.Now()
	p, err := ProbeAgent(ctx, Adapter{Name: "fake", Command: cmd, Env: env}, t.TempDir(), 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "skylands-fakeagent" || !p.LoadSession || p.ReportsAuth || p.Auth != nil {
		t.Errorf("probe = %+v, want the fake agent, able to load, reporting no login", p)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("probe took %s, want no wait for a login report", d)
	}

	// One that reports a login is waited for, and what it says is kept.
	for _, kind := range []string{"account", "none"} {
		p, err = ProbeAgent(ctx, Adapter{Name: "fake", Command: cmd, Env: append(env, fakeagent.EnvAuth+"="+kind)}, t.TempDir(), 10*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if !p.ReportsAuth || p.Auth == nil || p.Auth.Kind != kind || p.Auth.Label != "Fake "+kind {
			t.Errorf("auth %s: probe = %+v, auth %+v", kind, p, p.Auth)
		}
	}

	// A report that comes after the wait is not waited for.
	slow := append(env, fakeagent.EnvAuth+"=account", fakeagent.EnvDelay+"=2s")
	p, err = ProbeAgent(ctx, Adapter{Name: "fake", Command: cmd, Env: slow}, t.TempDir(), 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if !p.ReportsAuth || p.Auth != nil {
		t.Errorf("slow report: probe = %+v, want a login reported but not in time", p)
	}
}

func TestProbeAgentThatCannotStart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, err := ProbeAgent(ctx, Adapter{Name: "ghost", Command: []string{"/nonexistent/skylands-agent"}}, t.TempDir(), time.Second)
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Errorf("err = %v, want a failure to start that names the agent", err)
	}
}
