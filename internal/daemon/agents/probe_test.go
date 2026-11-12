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
	p, err := ProbeAgent(ctx, Adapter{Name: "fake", Command: cmd, Env: env}, t.TempDir(), ProbeOptions{AuthWait: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "skylands-fakeagent" || !p.LoadSession || p.ReportsAuth || p.Auth != nil || p.Accepts != (Accepts{}) {
		t.Errorf("probe = %+v, want the fake agent, able to load, reporting no login, taking text and links alone", p)
	}
	// What a prompt can carry is read from the agent's prompt capabilities.
	for _, tc := range []struct {
		env  []string
		want Accepts
	}{
		{[]string{fakeagent.EnvImages + "=1"}, Accepts{Images: true}},
		{[]string{fakeagent.EnvEmbeddedContext + "=1"}, Accepts{EmbeddedContext: true}},
		{[]string{fakeagent.EnvImages + "=1", fakeagent.EnvEmbeddedContext + "=1"}, Accepts{Images: true, EmbeddedContext: true}},
	} {
		got, err := ProbeAgent(ctx, Adapter{Name: "fake", Command: cmd, Env: append(append([]string(nil), env...), tc.env...)}, t.TempDir(), ProbeOptions{AuthWait: time.Second})
		if err != nil || got.Accepts != tc.want {
			t.Errorf("%v: accepts %+v, %v; want %+v", tc.env, got.Accepts, err, tc.want)
		}
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("probe took %s, want no wait for a login report", d)
	}

	// One that reports a login is waited for, and what it says is kept.
	for _, kind := range []string{"account", "none"} {
		p, err = ProbeAgent(ctx, Adapter{Name: "fake", Command: cmd, Env: append(env, fakeagent.EnvAuth+"="+kind)}, t.TempDir(), ProbeOptions{AuthWait: 10 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		if !p.ReportsAuth || p.Auth == nil || p.Auth.Kind != kind || p.Auth.Label != "Fake "+kind {
			t.Errorf("auth %s: probe = %+v, auth %+v", kind, p, p.Auth)
		}
	}

	// A report that comes after the wait is not waited for.
	slow := append(env, fakeagent.EnvAuth+"=account", fakeagent.EnvDelay+"=2s")
	p, err = ProbeAgent(ctx, Adapter{Name: "fake", Command: cmd, Env: slow}, t.TempDir(), ProbeOptions{AuthWait: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if !p.ReportsAuth || p.Auth != nil {
		t.Errorf("slow report: probe = %+v, want a login reported but not in time", p)
	}
}

// TestProbeAgentReadsModels opens a session, when asked, to read the
// models the agent offers from its ACP model setting.
func TestProbeAgentReadsModels(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd, env := fakeagent.TestCommand()
	probe := func(more ...string) Probe {
		t.Helper()
		p, err := ProbeAgent(ctx, Adapter{Name: "fake", Command: cmd, Env: append(append([]string(nil), env...), more...)}, t.TempDir(), ProbeOptions{AuthWait: 5 * time.Second, Models: true})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}

	p := probe(fakeagent.EnvConfigOptions + "=1")
	var ids []string
	for _, m := range p.Models {
		ids = append(ids, m.ID)
	}
	if !p.ModelsRead || p.ModelsErr != nil || strings.Join(ids, ",") != strings.Join(fakeagent.Models, ",") || p.DefaultModel != fakeagent.Models[0] {
		t.Errorf("probe = %+v, want the fake agent's models", p)
	}

	// An agent that offers its mode through session modes has the modes
	// the adapter lists read from them.
	p, err := ProbeAgent(ctx, Adapter{Name: "fake", Command: cmd, Env: append(append([]string(nil), env...), fakeagent.EnvConfigOptions+"=modes"), Modes: []string{"plan", "ask"}},
		t.TempDir(), ProbeOptions{AuthWait: 5 * time.Second, Models: true})
	if err != nil {
		t.Fatal(err)
	}
	if !p.ModelsRead || len(p.Models) != 0 || len(p.Modes) != 2 || p.Modes[0].ID != "plan" || p.Modes[0].Name != "Plan" || p.Modes[1].ID != "ask" {
		t.Errorf("probe = %+v, want the listed modes from session modes, and no models", p)
	}

	// An agent with no model setting has its models read, and none.
	if p := probe(); !p.ModelsRead || len(p.Models) != 0 || p.ModelsErr != nil {
		t.Errorf("probe = %+v, want models read and none offered", p)
	}

	// One that says it is logged out is not asked.
	if p := probe(fakeagent.EnvConfigOptions+"=1", fakeagent.EnvAuth+"=none"); p.ModelsRead || p.ModelsErr != nil {
		t.Errorf("probe = %+v, want no session for a logged-out agent", p)
	}
}

func TestProbeAgentThatCannotStart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, err := ProbeAgent(ctx, Adapter{Name: "ghost", Command: []string{"/nonexistent/skylands-agent"}}, t.TempDir(), ProbeOptions{AuthWait: time.Second})
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Errorf("err = %v, want a failure to start that names the agent", err)
	}
}
