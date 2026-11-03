package preflight

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/skylands-ai/skylands/internal/daemon/agents"
	"github.com/skylands-ai/skylands/internal/daemon/socket"
)

// host is a pretend machine: the commands on its PATH, what they print,
// what its agents say when started, and its daemon.
type host struct {
	missing  map[string]bool
	output   map[string]string
	failing  map[string]bool
	probes   map[string]agents.Probe
	probeErr map[string]error
	daemon   *socket.StatusInfo
	probed   []string
}

func healthy() *host {
	return &host{
		missing: map[string]bool{},
		output: map[string]string{
			"git --version":  "git version 2.43.0\n",
			"node --version": "v22.14.0\n",
			"npx --version":  "10.9.2\n",
		},
		failing: map[string]bool{},
		probes: map[string]agents.Probe{
			"claude": {Name: "@agentclientprotocol/claude-agent-acp", Version: "0.100.0", LoadSession: true, ReportsAuth: true, Auth: &agents.AuthStatus{Kind: "account", Label: "Claude Max"}},
			"fake":   {Name: "skylands-fakeagent", Version: "dev", LoadSession: true},
		},
		probeErr: map[string]error{},
		daemon:   &socket.StatusInfo{Host: "wopr", Version: "v1.0.0", Started: time.Now().Add(-time.Hour)},
	}
}

func (h *host) env() Env {
	return Env{
		Registry: agents.Registry{
			"claude": {Name: "claude", Command: []string{"npx", "-y", "adapter@1.0.0"}, Login: "log in with claude"},
			"fake":   {Name: "fake", Command: []string{"/usr/local/bin/skylands", "fakeagent"}},
		},
		SocketPath:   "/run/d.sock",
		Version:      "v1.0.0",
		AuthWait:     time.Second,
		ProbeTimeout: time.Second,
		LookPath: func(file string) (string, error) {
			if h.missing[file] {
				return "", errors.New("not found")
			}
			return "/usr/bin/" + file, nil
		},
		Output: func(_ context.Context, name string, args ...string) (string, error) {
			cmd := strings.Join(append([]string{name}, args...), " ")
			if h.failing[cmd] {
				return "", errors.New("exit status 1")
			}
			return h.output[cmd], nil
		},
		Probe: func(_ context.Context, ad agents.Adapter, _ string, _ time.Duration) (agents.Probe, error) {
			h.probed = append(h.probed, ad.Name)
			if err := h.probeErr[ad.Name]; err != nil {
				return agents.Probe{}, err
			}
			return h.probes[ad.Name], nil
		},
		Status: func(_ context.Context, path string) (socket.StatusInfo, error) {
			if h.daemon == nil {
				return socket.StatusInfo{}, fmt.Errorf("%w (%s)", socket.ErrNoDaemon, path)
			}
			return *h.daemon, nil
		},
	}
}

// byName indexes checks by name.
func byName(checks []Check) map[string]Check {
	m := map[string]Check{}
	for _, c := range checks {
		m[c.Name] = c
	}
	return m
}

func TestHealthyHost(t *testing.T) {
	h := healthy()
	checks := Run(context.Background(), h.env())
	var names []string
	for _, c := range checks {
		names = append(names, c.Name)
		if c.Status != OK {
			t.Errorf("%s: %s %q, %q; want ok", c.Name, c.Status, c.Found, c.Fix)
		}
	}
	want := []string{"git", "node", "npx", "agent claude", "login claude", "agent fake", "gh", "daemon"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("checks %q, want %q", names, want)
	}
	m := byName(checks)
	for name, found := range map[string]string{"git": "git 2.43.0 (/usr/bin/git)", "node": "v22.14.0 (/usr/bin/node)", "npx": "10.9.2 (/usr/bin/npx)", "agent claude": "@agentclientprotocol/claude-agent-acp 0.100.0", "login claude": "Claude Max", "gh": "logged in"} {
		if m[name].Found != found {
			t.Errorf("%s found %q, want %q", name, m[name].Found, found)
		}
	}
	if !strings.HasPrefix(m["daemon"].Found, "island wopr, skylands v1.0.0, up 1h0m") {
		t.Errorf("daemon found %q", m["daemon"].Found)
	}
	if got := Ready(checks); !reflect.DeepEqual(got, []string{"claude", "fake"}) {
		t.Errorf("Ready = %q, want claude and fake", got)
	}
}

func TestProblems(t *testing.T) {
	for _, tc := range []struct {
		name   string
		break_ func(h *host)
		check  string
		status Status
		found  string
		fix    string
		ready  []string
	}{
		{"no git", func(h *host) { h.missing["git"] = true }, "git", Fail, "not found on PATH", "install git", nil},
		{"old git", func(h *host) { h.output["git --version"] = "git version 2.16.6" }, "git", Fail, "git 2.16.6", "2.17 or newer", nil},
		{"apple git", func(h *host) { h.output["git --version"] = "git version 2.39.3 (Apple Git-145)" }, "git", OK, "git 2.39.3", "", []string{"claude", "fake"}},
		{"no node", func(h *host) { h.missing["node"] = true }, "node", Fail, "not found on PATH", "Node 22", []string{"fake"}},
		{"old node", func(h *host) { h.output["node --version"] = "v20.11.1" }, "node", Fail, "v20.11", "Node 22 or newer", []string{"fake"}},
		{"old node, so no claude", func(h *host) { h.output["node --version"] = "v20.11.1" }, "agent claude", Fail, "cannot start without Node 22", "see node and npx", []string{"fake"}},
		{"no npx, so no claude", func(h *host) { h.missing["npx"] = true }, "agent claude", Fail, "cannot start without Node 22 or newer and npx", "see node and npx", []string{"fake"}},
		{"adapter will not start", func(h *host) { h.probeErr["claude"] = errors.New("initialize claude: EOF\nnpm ERR! 404") }, "agent claude", Fail, "did not start: initialize claude: EOF", "npm can reach its registry", []string{"fake"}},
		{"logged out", func(h *host) {
			h.probes["claude"] = withAuth(h.probes["claude"], &agents.AuthStatus{Kind: "none", Label: "Not logged in"})
		}, "login claude", Fail, "not logged in", "log in with claude", []string{"fake"}},
		{"login not reported in time", func(h *host) { h.probes["claude"] = withAuth(h.probes["claude"], nil) }, "login claude", Warn, "did not say within 1s", "log in with claude", []string{"claude", "fake"}},
		{"key from the environment", func(h *host) {
			h.probes["claude"] = withAuth(h.probes["claude"], &agents.AuthStatus{Kind: "api_key", Label: "Anthropic API key", Detail: "ANTHROPIC_API_KEY"})
		}, "login claude", Warn, "Anthropic API key (ANTHROPIC_API_KEY)", "gets only PATH", []string{"claude", "fake"}},
		{"cannot load sessions", func(h *host) { p := h.probes["fake"]; p.LoadSession = false; h.probes["fake"] = p }, "agent fake", Warn, "skylands-fakeagent dev", "cannot load a session", []string{"claude", "fake"}},
		{"no gh", func(h *host) { h.missing["gh"] = true }, "gh", Warn, "not found on PATH", "only to open pull requests", []string{"claude", "fake"}},
		{"gh logged out", func(h *host) { h.failing["gh auth status"] = true }, "gh", Warn, "not logged in", "gh auth login", []string{"claude", "fake"}},
		{"no daemon", func(h *host) { h.daemon = nil }, "daemon", Warn, "nothing answers on /run/d.sock", "devworld install", []string{"claude", "fake"}},
		{"older daemon", func(h *host) { h.daemon.Version = "v0.9.0" }, "daemon", Warn, "skylands v0.9.0", "restart it", []string{"claude", "fake"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := healthy()
			tc.break_(h)
			checks := Run(context.Background(), h.env())
			c := byName(checks)[tc.check]
			if c.Status != tc.status || !strings.Contains(c.Found, tc.found) || !strings.Contains(c.Fix, tc.fix) {
				t.Errorf("%s = %s %q, fix %q; want %s with %q, fix with %q", tc.check, c.Status, c.Found, c.Fix, tc.status, tc.found, tc.fix)
			}
			if got := Ready(checks); !reflect.DeepEqual(got, tc.ready) {
				t.Errorf("Ready = %q, want %q", got, tc.ready)
			}
		})
	}
}

func TestNoNodeChecksWithoutNodeAgents(t *testing.T) {
	h := healthy()
	e := h.env()
	delete(e.Registry, "claude")
	m := byName(Run(context.Background(), e))
	if _, ok := m["node"]; ok {
		t.Error("node was checked with no agent that runs on it")
	}
}

func TestAnAgentThatCannotStartIsNotProbed(t *testing.T) {
	h := healthy()
	h.missing["npx"] = true
	Run(context.Background(), h.env())
	if !reflect.DeepEqual(h.probed, []string{"fake"}) {
		t.Errorf("probed %q, want only the fake agent", h.probed)
	}
}

func withAuth(p agents.Probe, a *agents.AuthStatus) agents.Probe {
	p.ReportsAuth, p.Auth = true, a
	return p
}
