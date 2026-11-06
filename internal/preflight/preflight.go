// Package preflight checks that a host has what Skylands needs to run
// agents: git for worktrees, Node for agents that come from npm, each agent
// starting over ACP and, where it says, logged in, no allow rules in
// Claude Code's own settings, gh for pull requests, and a daemon on the
// local socket.
package preflight

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/daemon/agents"
	"github.com/skylands-ai/skylands/internal/daemon/socket"
)

// Status is how a check came out. A failure means agents will not run
// until it is fixed; a warning means something works less well, or could
// not be confirmed. A skip is an agent that isn't installed while another
// is: it can't run, but nothing has failed.
type Status int

const (
	OK Status = iota
	Warn
	Fail
	Skip
)

func (s Status) String() string {
	switch s {
	case OK:
		return "ok"
	case Warn:
		return "warn"
	case Skip:
		return "skip"
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
	// Login, on a login check, is the login the agent reported.
	Login string
	// Missing, on an agent check, means the agent isn't installed: its
	// command isn't on PATH, or Node isn't there to run it.
	Missing bool
	// On a models check: whether a session opened to read the agent's
	// models, what it offered, and the one the session started on.
	ModelsRead   bool
	Models       []v1.AgentModel
	DefaultModel string
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
	Probe  func(ctx context.Context, ad agents.Adapter, cwd string, opts agents.ProbeOptions) (agents.Probe, error)
	// Models says whether to read an agent's models, which opens a session
	// in it without prompting. Nil reads every agent's.
	Models func(agent string) bool
	Status func(ctx context.Context, socketPath string) (socket.StatusInfo, error)
	// CheckAgents asks the daemon to check its agents now, in its own
	// environment, which also updates its island.
	CheckAgents func(ctx context.Context, socketPath string) ([]v1.AgentAvailability, error)
	// ClaudeDir is where Claude Code keeps its user settings:
	// $CLAUDE_CONFIG_DIR, or ~/.claude. Empty when there is no home.
	ClaudeDir string
	// ReadFile reads a whole file, as os.ReadFile does.
	ReadFile func(name string) ([]byte, error)
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
		CheckAgents: func(ctx context.Context, path string) ([]v1.AgentAvailability, error) {
			return socket.Dial(path).CheckAgents(ctx)
		},
		ClaudeDir: claudeDir(),
		ReadFile:  os.ReadFile,
	}
}

// claudeDir is where Claude Code keeps its user settings.
func claudeDir() string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude")
}

// Run makes every check, in the order they are shown.
func Run(ctx context.Context, e Env) []Check {
	checks := withClaudeSettings(ctx, e, RunAgents(ctx, e))
	return append(checks, ghCheck(ctx, e), daemonCheck(ctx, e, Ready(checks)))
}

// withClaudeSettings adds the check of Claude Code's own settings after
// Claude Code's other checks, when it is installed.
func withClaudeSettings(ctx context.Context, e Env, checks []Check) []Check {
	last := -1
	for i, c := range checks {
		if c.Agent != "claude" {
			continue
		}
		if c.Name == "agent claude" && (c.Missing || c.Status == Fail || c.Status == Skip) {
			return checks
		}
		last = i
	}
	if last < 0 || e.ReadFile == nil {
		return checks
	}
	return slices.Insert(checks, last+1, claudeSettingsCheck(ctx, e))
}

// claudeSettingsCheck looks for allow rules in Claude Code's own settings:
// the user's, and the project and local settings of each repository the
// daemon has registered. Claude Code runs a call that one of them allows
// without asking, even in the mode the daemon sets, so the call never
// reaches the host's rules or a person.
func claudeSettingsCheck(ctx context.Context, e Env) Check {
	c := Check{Name: "settings claude", Agent: "claude"}
	var files []string
	if e.ClaudeDir != "" {
		files = append(files, filepath.Join(e.ClaudeDir, "settings.json"))
	}
	repos := -1
	if e.Status != nil {
		if st, err := e.Status(ctx, e.SocketPath); err == nil {
			repos = len(st.Repos)
			for _, r := range st.Repos {
				files = append(files, filepath.Join(r.Path, ".claude", "settings.json"), filepath.Join(r.Path, ".claude", "settings.local.json"))
			}
		}
	}
	var allowing, unreadable []string
	for _, f := range files {
		data, err := e.ReadFile(f)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		var settings struct {
			Permissions struct {
				Allow []string `json:"allow"`
			} `json:"permissions"`
		}
		if err == nil {
			err = json.Unmarshal(data, &settings)
		}
		if err != nil {
			unreadable = append(unreadable, f)
			continue
		}
		if n := len(settings.Permissions.Allow); n > 0 {
			allowing = append(allowing, fmt.Sprintf("%s (%d)", f, n))
		}
	}
	switch {
	case len(allowing) > 0:
		c.Status, c.Found = Warn, "allow rules in "+strings.Join(allowing, ", ")
		c.Fix = "Claude Code runs a call these rules allow without asking, so neither the host's rules nor a person sees it; remove the rules for calls Skylands should decide"
		if len(unreadable) > 0 {
			c.Found += "; could not read " + strings.Join(unreadable, ", ")
		}
	case len(unreadable) > 0:
		c.Status, c.Found = Warn, "could not read "+strings.Join(unreadable, ", ")
		c.Fix = "check them for allow rules: Claude Code runs a call one allows without asking, so neither the host's rules nor a person sees it"
	case repos < 0:
		c.Found = "no allow rules in your settings; no daemon answered to say which repositories to read"
	case repos == 0:
		c.Found = "no allow rules in your settings, and no repositories are registered"
	case repos == 1:
		c.Found = "no allow rules in your settings or the registered repository's"
	default:
		c.Found = fmt.Sprintf("no allow rules in your settings or those of %d registered repositories", repos)
	}
	return c
}

// RunAgents makes the checks that decide which agents can run: git, Node
// and npx when an agent runs on Node, and each agent. A daemon makes these
// itself, in its own environment, to record what it can launch.
func RunAgents(ctx context.Context, e Env) []Check {
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
	skipMissing(checks, e.Registry)
	return checks
}

// skipMissing makes the agents that aren't installed optional while the
// host has another: their checks, and the Node and npx checks only the
// agents from npm need, are skipped rather than failed. With no agent
// installed but the built-in fake one, they fail, since then no real
// session can run.
func skipMissing(checks []Check, r agents.Registry) {
	installed := false
	for _, c := range checks {
		if strings.HasPrefix(c.Name, "agent ") && !c.Missing && !r[c.Agent].BuiltIn {
			installed = true
		}
	}
	if !installed {
		return
	}
	for i := range checks {
		c := &checks[i]
		if c.Status == Fail && (c.Missing || c.Name == "node" || c.Name == "npx") {
			c.Status = Skip
		}
	}
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
		if c.Status == Fail || c.Status == Skip {
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

// Availability turns checks into what each agent in r can do, in name
// order: whether it can run, what it is, its login, and the checks that
// failed or warned for it. A failed git, Node, or npx check is noted on
// each agent it stops, in place of the agent check it caused.
func Availability(checks []Check, r agents.Registry) []v1.AgentAvailability {
	byName := map[string]Check{}
	for _, c := range checks {
		byName[c.Name] = c
	}
	ready := map[string]bool{}
	for _, name := range Ready(checks) {
		ready[name] = true
	}
	names := make([]string, 0, len(r))
	for name := range r {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]v1.AgentAvailability, 0, len(names))
	for _, name := range names {
		a := v1.AgentAvailability{Name: name, Available: ready[name]}
		host := []string{"git"}
		if runsOnNode(r[name]) {
			host = append(host, "node", "npx")
		}
		hostFailed := false
		for _, h := range host {
			if c, ok := byName[h]; ok && c.Status != OK {
				a.Notes = append(a.Notes, note(c))
				hostFailed = hostFailed || c.Status == Fail || c.Status == Skip
			}
		}
		if c, ok := byName["agent "+name]; ok {
			if c.Status == OK || c.Status == Warn {
				a.Agent = c.Found
			}
			// A failure caused by a failed git, Node, or npx check is noted
			// by that check, not again here.
			if c.Status == Warn || (c.Status == Fail || c.Status == Skip) && !hostFailed {
				a.Notes = append(a.Notes, note(c))
			}
		}
		if c, ok := byName["login "+name]; ok {
			a.Login = c.Login
			if c.Status != OK {
				a.Notes = append(a.Notes, note(c))
			}
		}
		if c, ok := byName["models "+name]; ok {
			a.ModelsRead, a.Models, a.DefaultModel = c.ModelsRead, c.Models, c.DefaultModel
			if c.Status != OK {
				a.Notes = append(a.Notes, note(c))
			}
		}
		out = append(out, a)
	}
	return out
}

func note(c Check) v1.AvailabilityNote {
	return v1.AvailabilityNote{Check: c.Name, Failed: c.Status == Fail, Found: c.Found, Fix: c.Fix}
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
		c.Missing = true
		return []Check{c}
	}
	if _, err := e.LookPath(ad.Command[0]); err != nil {
		c.Status, c.Found, c.Fix = Fail, "cannot start: "+ad.Command[0]+" is not on PATH", "fix the checks above"
		if ad.Install != "" {
			c.Fix = ad.Install
		}
		c.Missing = true
		return []Check{c}
	}
	dir, err := probeDir()
	if err != nil {
		c.Status, c.Found = Fail, err.Error()
		return []Check{c}
	}
	pctx, cancel := context.WithTimeout(ctx, e.ProbeTimeout)
	defer cancel()
	readModels := e.Models == nil || e.Models(ad.Name)
	p, err := e.Probe(pctx, ad, dir, agents.ProbeOptions{AuthWait: e.AuthWait, Models: readModels})
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
	if readModels && (p.ModelsRead || p.ModelsErr != nil) {
		checks = append(checks, modelsCheck(ad, p))
	}
	return checks
}

// probeDir is where an agent is started to be checked, the same every
// time: an agent may keep something for each directory it opens a session
// in, as Claude Code keeps a project folder, and one directory leaves one.
// With no cache directory, as without a home, it is a new private one.
func probeDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return os.MkdirTemp("", "skylands-probe-")
	}
	dir := filepath.Join(base, "skylands", "probe")
	return dir, os.MkdirAll(dir, 0o700)
}

// modelsCheck reports the models an agent offers its login. An agent that
// would not open a session to say is a warning: its sessions may not
// start either.
func modelsCheck(ad agents.Adapter, p agents.Probe) Check {
	c := Check{Name: "models " + ad.Name, Agent: ad.Name, ModelsRead: p.ModelsRead, Models: p.Models, DefaultModel: p.DefaultModel}
	switch {
	case !p.ModelsRead:
		c.Status, c.Found = Warn, "could not open a session to read them: "+firstLine(p.ModelsErr.Error())
		c.Fix = "if sessions fail to start, this is likely why"
	case len(p.Models) == 0:
		c.Found = "no model setting; sessions run on the agent's own"
	default:
		names := make([]string, 0, len(p.Models))
		for _, m := range p.Models {
			name := m.ID
			if m.ID == p.DefaultModel {
				name += " (default)"
			}
			names = append(names, name)
		}
		c.Found = strings.Join(names, ", ")
	}
	return c
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
		c.Login = c.Found
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

// daemonCheck asks the daemon on the socket what it runs, and has it check
// its agents in its own environment. An agent this shell can run but the
// daemon cannot points at the difference between the two environments,
// such as the PATH a service was installed with.
func daemonCheck(ctx context.Context, e Env, shellReady []string) Check {
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
	var fixes []string
	if e.Version != "" && st.Version != e.Version {
		fixes = append(fixes, fmt.Sprintf("it runs skylands %s and this binary is %s; restart it to run this one", st.Version, e.Version))
	}
	if e.CheckAgents != nil {
		pctx, cancel := context.WithTimeout(ctx, e.ProbeTimeout)
		found, err := e.CheckAgents(pctx, e.SocketPath)
		cancel()
		// A daemon from before agents were checked has nothing to say.
		if err == nil {
			here := map[string]bool{}
			for _, name := range shellReady {
				here[name] = true
			}
			var ready, differ []string
			for _, a := range found {
				if a.Available {
					ready = append(ready, a.Name)
				} else if here[a.Name] {
					differ = append(differ, a.Name+" ("+firstFailure(a)+")")
				}
			}
			if len(ready) > 0 {
				c.Found += "; runs " + strings.Join(ready, ", ")
			} else {
				c.Found += "; runs no agent"
			}
			if len(differ) > 0 {
				fixes = append(fixes, "this shell can run "+strings.Join(differ, ", ")+" but the daemon cannot, so its environment differs; for a service, install it again from this shell to give it this PATH")
			}
		}
	}
	if len(fixes) > 0 {
		c.Status = Warn
		c.Fix = strings.Join(fixes, "; and ")
	}
	return c
}

// firstFailure says why an agent cannot run.
func firstFailure(a v1.AgentAvailability) string {
	for _, n := range a.Notes {
		if n.Failed {
			return n.Check + ": " + n.Found
		}
	}
	return "unavailable"
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}
