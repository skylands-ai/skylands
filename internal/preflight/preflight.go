// Package preflight checks that a host has what Skylands needs to run
// agents: git for worktrees, Node for agents that come from npm, each agent
// starting over ACP and, where it says, logged in, gh for pull requests,
// and a daemon on the local socket.
package preflight

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/skylands-ai/skylands/internal/daemon/agents"
	"github.com/skylands-ai/skylands/internal/daemon/socket"
)

// Status is how a check came out. A failure means agents will not run
// until it is fixed; a warning means something works less well, or could
// not be confirmed.
type Status int

const (
	OK Status = iota
	Warn
	Fail
)

func (s Status) String() string {
	switch s {
	case OK:
		return "ok"
	case Warn:
		return "warn"
	default:
		return "fail"
	}
}

// Check is one thing preflight looked at.
type Check struct {
	Name   string
	Status Status
	// Found is what preflight found.
	Found string
	// Fix is what to do about a warning or a failure.
	Fix string
	// Agent, on an agent's checks, names the agent.
	Agent string
}

// Minimum versions. Sessions remove their worktrees with "git worktree
// remove", which came in git 2.17, and the Claude Code adapter that
// agents.ClaudeAdapterPackage pins declares node >= 22.
const (
	MinGitMajor, MinGitMinor = 2, 17
	MinNodeMajor             = 22
)

// Env is what the checks look at. Default fills it in for this machine,
// and tests replace its functions.
type Env struct {
	Registry   agents.Registry
	SocketPath string
	// Version is this binary's, which the running daemon's should match.
	Version string
	// AuthWait is how long an agent that reports its login is given to.
	AuthWait time.Duration
	// ProbeTimeout bounds starting one agent, which for an agent from npm
	// can include npx downloading it.
	ProbeTimeout time.Duration
	LookPath     func(file string) (string, error)
	// Output runs a command and returns what it printed. A command that
	// exits with an error returns one.
	Output func(ctx context.Context, name string, args ...string) (string, error)
	Probe  func(ctx context.Context, ad agents.Adapter, cwd string, wait time.Duration) (agents.Probe, error)
	Status func(ctx context.Context, socketPath string) (socket.StatusInfo, error)
}

// Default is the environment for checking this machine with the daemon
// socket at socketPath.
func Default(socketPath, version string) Env {
	return Env{
		Registry:     agents.DefaultRegistry(),
		SocketPath:   socketPath,
		Version:      version,
		AuthWait:     10 * time.Second,
		ProbeTimeout: 2 * time.Minute,
		LookPath:     exec.LookPath,
		Output: func(ctx context.Context, name string, args ...string) (string, error) {
			out, err := exec.CommandContext(ctx, name, args...).Output()
			return string(out), err
		},
		Probe: agents.ProbeAgent,
		Status: func(ctx context.Context, path string) (socket.StatusInfo, error) {
			return socket.Dial(path).Status(ctx)
		},
	}
}

// Run makes every check, in the order they are shown.
func Run(ctx context.Context, e Env) []Check {
	var checks []Check
	checks = append(checks, gitCheck(ctx, e))
	nodeUsable := true
	if needsNode(e.Registry) {
		node, npx := nodeCheck(ctx, e), npxCheck(ctx, e)
		nodeUsable = node.Status != Fail && npx.Status != Fail
		checks = append(checks, node, npx)
	}
	names := make([]string, 0, len(e.Registry))
	for name := range e.Registry {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		checks = append(checks, agentChecks(ctx, e, e.Registry[name], nodeUsable)...)
	}
	checks = append(checks, ghCheck(ctx, e), daemonCheck(ctx, e))
	return checks
}

// Ready lists the agents whose checks all passed or only warned. With no
// git, no session can start, so none is ready.
func Ready(checks []Check) []string {
	failed := map[string]bool{}
	seen := map[string]bool{}
	var names []string
	for _, c := range checks {
		if c.Name == "git" && c.Status == Fail {
			return nil
		}
		if c.Agent == "" {
			continue
		}
		if c.Status == Fail {
			failed[c.Agent] = true
		}
		if !seen[c.Agent] {
			seen[c.Agent] = true
			names = append(names, c.Agent)
		}
	}
	ready := names[:0]
	for _, n := range names {
		if !failed[n] {
			ready = append(ready, n)
		}
	}
	return ready
}

// needsNode says whether any agent runs on Node.
func needsNode(r agents.Registry) bool {
	for _, ad := range r {
		if runsOnNode(ad) {
			return true
		}
	}
	return false
}

func runsOnNode(ad agents.Adapter) bool {
	return len(ad.Command) > 0 && (ad.Command[0] == "npx" || ad.Command[0] == "node")
}

var versionNumber = regexp.MustCompile(`(\d+)\.(\d+)(?:\.(\d+))?`)

// parseVersion finds the first x.y or x.y.z in s.
func parseVersion(s string) (major, minor int, text string, ok bool) {
	m := versionNumber.FindStringSubmatch(s)
	if m == nil {
		return 0, 0, "", false
	}
	major, _ = strconv.Atoi(m[1])
	minor, _ = strconv.Atoi(m[2])
	return major, minor, m[0], true
}

func gitCheck(ctx context.Context, e Env) Check {
	c := Check{Name: "git"}
	path, err := e.LookPath("git")
	if err != nil {
		c.Status, c.Found, c.Fix = Fail, "not found on PATH", "install git; every session works in a git worktree"
		return c
	}
	out, err := e.Output(ctx, "git", "--version")
	major, minor, text, ok := parseVersion(out)
	switch {
	case err != nil || !ok:
		c.Status, c.Found, c.Fix = Warn, "found, but its version could not be read", fmt.Sprintf("make sure it is git %d.%d or newer", MinGitMajor, MinGitMinor)
	case major < MinGitMajor || major == MinGitMajor && minor < MinGitMinor:
		c.Status, c.Found, c.Fix = Fail, "git "+text, fmt.Sprintf("upgrade to git %d.%d or newer, which sessions need to remove their worktrees", MinGitMajor, MinGitMinor)
	default:
		c.Found = "git " + text
	}
	c.Found += " (" + path + ")"
	return c
}

func nodeCheck(ctx context.Context, e Env) Check {
	c := Check{Name: "node"}
	path, err := e.LookPath("node")
	if err != nil {
		c.Status, c.Found, c.Fix = Fail, "not found on PATH", fmt.Sprintf("install Node %d or newer; agents from npm, such as Claude Code, run on it", MinNodeMajor)
		return c
	}
	out, err := e.Output(ctx, "node", "--version")
	major, _, text, ok := parseVersion(out)
	switch {
	case err != nil || !ok:
		c.Status, c.Found, c.Fix = Warn, "found, but its version could not be read", fmt.Sprintf("make sure it is Node %d or newer", MinNodeMajor)
	case major < MinNodeMajor:
		c.Status, c.Found, c.Fix = Fail, "v"+text, fmt.Sprintf("upgrade to Node %d or newer, which the Claude Code adapter needs", MinNodeMajor)
	default:
		c.Found = "v" + text
	}
	// Machines often have more than one Node, and which one is first on
	// PATH differs between a shell and a service.
	c.Found += " (" + path + ")"
	return c
}

func npxCheck(ctx context.Context, e Env) Check {
	c := Check{Name: "npx"}
	path, err := e.LookPath("npx")
	if err != nil {
		c.Status, c.Found, c.Fix = Fail, "not found on PATH", "install npm, which comes with Node and provides npx"
		return c
	}
	out, err := e.Output(ctx, "npx", "--version")
	if _, _, text, ok := parseVersion(out); err == nil && ok {
		c.Found = text + " (" + path + ")"
	} else {
		c.Found = path
	}
	return c
}

// agentChecks starts one agent to see that it speaks ACP, and checks its
// login when it reports one. An agent that runs on Node is not started
// when the Node checks failed.
func agentChecks(ctx context.Context, e Env, ad agents.Adapter, nodeUsable bool) []Check {
	c := Check{Name: "agent " + ad.Name, Agent: ad.Name}
	if len(ad.Command) == 0 {
		c.Status, c.Found = Fail, "has no command"
		return []Check{c}
	}
	if runsOnNode(ad) && !nodeUsable {
		c.Status, c.Found, c.Fix = Fail, fmt.Sprintf("cannot start without Node %d or newer and npx", MinNodeMajor), "see node and npx above"
		return []Check{c}
	}
	if _, err := e.LookPath(ad.Command[0]); err != nil {
		c.Status, c.Found, c.Fix = Fail, "cannot start: "+ad.Command[0]+" is not on PATH", "fix the checks above"
		return []Check{c}
	}
	dir, err := os.MkdirTemp("", "skylands-preflight-")
	if err != nil {
		c.Status, c.Found = Fail, err.Error()
		return []Check{c}
	}
	defer func() { _ = os.RemoveAll(dir) }()
	pctx, cancel := context.WithTimeout(ctx, e.ProbeTimeout)
	defer cancel()
	p, err := e.Probe(pctx, ad, dir, e.AuthWait)
	if err != nil {
		c.Status, c.Found = Fail, "did not start: "+firstLine(err.Error())
		if ad.Command[0] == "npx" {
			c.Fix = "npx fetches the agent from npm the first time, so check that npm can reach its registry"
		}
		return []Check{c}
	}
	c.Found = strings.TrimSpace(p.Name + " " + p.Version)
	if !p.LoadSession {
		c.Status = Warn
		c.Fix = "it cannot load a session, so after a daemon restart its sessions continue from the transcript, without the agent's own memory"
	}
	checks := []Check{c}
	if p.ReportsAuth {
		checks = append(checks, loginCheck(e, ad, p.Auth))
	}
	return checks
}

func loginCheck(e Env, ad agents.Adapter, a *agents.AuthStatus) Check {
	c := Check{Name: "login " + ad.Name, Agent: ad.Name}
	login := ad.Login
	if login == "" {
		login = "log the agent in on this machine"
	}
	switch {
	case a == nil:
		c.Status = Warn
		c.Found = fmt.Sprintf("the agent did not say within %s", e.AuthWait)
		c.Fix = "if sessions fail to start, " + login
	case a.Kind == "none":
		c.Status, c.Found, c.Fix = Fail, "not logged in", login
	default:
		c.Found = a.Label
		if a.Detail != "" {
			c.Found += " (" + a.Detail + ")"
		}
		// A key from the environment is not passed to a service, which
		// gets only PATH from the shell that installed it.
		if a.Kind == "api_key" && (a.Detail == "env" || strings.HasPrefix(a.Detail, "ANTHROPIC_")) {
			c.Status = Warn
			c.Fix = "the key comes from this shell's environment, and a service from \"skylands daemon install\" or \"devworld install\" gets only PATH from it; if you run one, log in with claude instead"
		}
	}
	return c
}

func ghCheck(ctx context.Context, e Env) Check {
	c := Check{Name: "gh"}
	const why = "; gh is needed only to open pull requests from a conversation"
	if _, err := e.LookPath("gh"); err != nil {
		c.Status, c.Found, c.Fix = Warn, "not found on PATH", "install the GitHub CLI"+why
		return c
	}
	if _, err := e.Output(ctx, "gh", "auth", "status"); err != nil {
		c.Status, c.Found, c.Fix = Warn, "not logged in", "run gh auth login"+why
		return c
	}
	c.Found = "logged in"
	return c
}

func daemonCheck(ctx context.Context, e Env) Check {
	c := Check{Name: "daemon"}
	st, err := e.Status(ctx, e.SocketPath)
	if err != nil {
		c.Status = Warn
		if errors.Is(err, socket.ErrNoDaemon) {
			c.Found = "nothing answers on " + e.SocketPath
		} else {
			c.Found = firstLine(err.Error())
		}
		c.Fix = "start one with skylands devworld up, or keep one running with skylands devworld install"
		return c
	}
	c.Found = fmt.Sprintf("island %s, skylands %s, up %s", st.Host, st.Version, time.Since(st.Started).Round(time.Second))
	if e.Version != "" && st.Version != e.Version {
		c.Status = Warn
		c.Fix = fmt.Sprintf("it runs skylands %s and this binary is %s; restart it to run this one", st.Version, e.Version)
	}
	return c
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}
