// Package placement is the daemon's part in placing work asked for at the
// world or an island. It keeps a keyword index of the island's
// repositories, scores a prompt against it when a citadel asks, and asks
// one of the island's agents to break a close call between them. No code
// leaves the host to make the choice: a citadel gets each candidate's
// name, confidence, and the reasons a person reads.
package placement

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"sort"
	"sync"
	"time"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/daemon/agents"
	"github.com/skylands-ai/skylands/internal/daemon/host"
	"github.com/skylands-ai/skylands/internal/daemon/session"
	"github.com/skylands-ai/skylands/internal/dispatch"
)

// Repos is the island's registered repositories and the agents it can
// run, which the Host aggregate provides.
type Repos interface {
	Repos() []host.Repo
	Agents() []v1.AgentAvailability
}

// Sessions is the island's sessions, whose titles the index reads, and
// on which what a pick cost is recorded, which the session manager
// provides.
type Sessions interface {
	List() []session.Session
	RecordTokens(ctx context.Context, id, purpose string, t v1.TokenUsage) error
}

// AskTimeout is how long an agent gets to start and pick a repository.
const AskTimeout = 90 * time.Second

// costsFor is how long what a pick cost waits for the session it chose
// for to start, after which it is dropped.
const costsFor = time.Hour

// Placer keeps the island's index and answers for it.
type Placer struct {
	host     string
	repos    Repos
	sessions Sessions
	adapters agents.Registry
	// dir is where an agent asked to pick starts: an empty directory in
	// no repository.
	dir string

	// buildMu keeps one build of the index going at a time.
	buildMu sync.Mutex
	mu      sync.Mutex
	built   map[string]built
	costs   map[string]cost
	// asking holds a place for each agent asked to pick right now, so a
	// burst of picks starts no more than maxAsking agents at once.
	asking chan struct{}
}

// maxAsking is the most agents asked to pick at once.
const maxAsking = 2

// built is one repository's entry as of a commit.
type built struct {
	entry  *dispatch.Entry
	path   string
	commit string
}

// cost is what a pick cost, waiting for its session to start.
type cost struct {
	tokens v1.TokenUsage
	at     time.Time
}

// New returns a placer for host's repositories. dir is where it starts an
// agent to break a tie, and is made if missing.
func New(hostName string, repos Repos, sessions Sessions, adapters agents.Registry, dir string) *Placer {
	return &Placer{host: hostName, repos: repos, sessions: sessions, adapters: adapters, dir: dir, built: map[string]built{}, costs: map[string]cost{}, asking: make(chan struct{}, maxAsking)}
}

// Entries is the index, brought up to date first: a repository is indexed
// again when its checkout's HEAD has moved since it last was. One that
// can't be read is left out until it can.
func (p *Placer) Entries(ctx context.Context) []*dispatch.Entry {
	p.buildMu.Lock()
	defer p.buildMu.Unlock()
	repos := p.repos.Repos()
	out := make([]*dispatch.Entry, 0, len(repos))
	keep := map[string]bool{}
	for _, r := range repos {
		keep[r.ID] = true
		commit := head(ctx, r.Path)
		p.mu.Lock()
		b, ok := p.built[r.ID]
		p.mu.Unlock()
		if !ok || b.path != r.Path || b.commit != commit || commit == "" {
			src, at, err := gather(ctx, r)
			if err != nil {
				slog.Warn("placement: can't index the repository", "host", p.host, "repo", r.Name, "err", err)
				continue
			}
			b = built{entry: dispatch.Build(src), path: r.Path, commit: at}
			p.mu.Lock()
			p.built[r.ID] = b
			p.mu.Unlock()
		}
		out = append(out, b.entry)
	}
	p.mu.Lock()
	for id := range p.built {
		if !keep[id] {
			delete(p.built, id)
		}
	}
	p.mu.Unlock()
	return out
}

// titles is the titles of the island's conversations, by repository.
func (p *Placer) titles() map[string][]dispatch.Title {
	out := map[string][]dispatch.Title{}
	for _, s := range p.sessions.List() {
		if s.Title != "" {
			out[s.RepoID] = append(out[s.RepoID], dispatch.Title{Session: s.ID, Title: s.Title})
		}
	}
	return out
}

// Match scores a prompt against the island's repositories.
func (p *Placer) Match(ctx context.Context, req v1.MatchRequest) v1.MatchResult {
	entries := p.Entries(ctx)
	ranked := dispatch.Rank(entries, dispatch.ParsePrompt(req.Prompt), p.titles(), "")
	if ranked == nil {
		ranked = []v1.MatchCandidate{}
	}
	return v1.MatchResult{Candidates: ranked, Repos: len(entries)}
}

// agentOrder is the order agents are asked in when the one named can't
// run here; the fake agent, built in for tests, comes after real ones.
var agentOrder = []string{"claude", "codex", "gemini", "goose"}

// agentFor is the agent to ask: want, when the island can run it, or the
// first it can. Until the island has checked which agents it can run, it
// tries want, as a session would.
func (p *Placer) agentFor(want string) (string, error) {
	checked := p.repos.Agents()
	if _, ok := p.adapters[want]; ok && len(checked) == 0 {
		return want, nil
	}
	can := map[string]bool{}
	var any []string
	for _, a := range checked {
		if _, ok := p.adapters[a.Name]; ok && a.Available {
			can[a.Name] = true
			any = append(any, a.Name)
		}
	}
	if can[want] {
		return want, nil
	}
	for _, name := range agentOrder {
		if can[name] {
			return name, nil
		}
	}
	if len(any) == 0 {
		return "", fmt.Errorf("%s can't run any agent to choose a repository", p.host)
	}
	sort.Strings(any)
	return any[0], nil
}

// Pick asks one of the island's agents which of the repositories in
// req.Among a prompt is for. The agent is told only what Describe gives of
// each, in a side session that asks before acting. What it costs is held
// for req.SessionID and recorded once that session starts.
func (p *Placer) Pick(ctx context.Context, req v1.PickRequest) (v1.PickResult, error) {
	if len(req.Among) < 2 {
		return v1.PickResult{}, errors.New("a pick is between two repositories or more")
	}
	entries := p.Entries(ctx)
	var among []*dispatch.Entry
	for _, id := range req.Among {
		i := slices.IndexFunc(entries, func(e *dispatch.Entry) bool { return e.ID == id })
		if i < 0 {
			return v1.PickResult{}, fmt.Errorf("no repository %s on %s", id, p.host)
		}
		if slices.Contains(among, entries[i]) {
			return v1.PickResult{}, fmt.Errorf("repository %s is named twice", id)
		}
		among = append(among, entries[i])
	}
	name, err := p.agentFor(req.Agent)
	if err != nil {
		return v1.PickResult{}, err
	}
	prompt := dispatch.ParsePrompt(req.Prompt)
	titles := p.titles()
	described := make([]string, len(among))
	for i, e := range among {
		described[i] = e.Describe(e.Score(prompt, titles[e.ID], "").Reasons)
	}
	reply, spent, err := p.ask(ctx, name, dispatch.PickQuestion(req.Prompt, described))
	p.held(req.SessionID, spent)
	if err != nil {
		return v1.PickResult{}, fmt.Errorf("%s couldn't choose: %w", name, err)
	}
	out := v1.PickResult{Agent: name}
	if n := dispatch.ParsePick(reply, len(among)); n > 0 {
		out.RepoID = among[n-1].ID
	}
	slog.Info("placement: an agent chose a repository", "host", p.host, "agent", name, "among", len(among), "chose", out.RepoID)
	return out, nil
}

// ask starts the agent where no repository is, asks it one question in a
// side session, and stops it, returning its answer and what it cost.
func (p *Placer) ask(ctx context.Context, name, question string) (string, v1.TokenUsage, error) {
	ad, ok := p.adapters[name]
	if !ok {
		return "", v1.TokenUsage{}, errors.New("no adapter for " + name)
	}
	select {
	case p.asking <- struct{}{}:
		defer func() { <-p.asking }()
	case <-ctx.Done():
		return "", v1.TokenUsage{}, ctx.Err()
	}
	if err := os.MkdirAll(p.dir, 0o700); err != nil {
		return "", v1.TokenUsage{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, AskTimeout)
	defer cancel()
	obs := &spent{}
	r, err := agents.Start(ctx, ad, p.dir, agents.Settings{}, obs)
	if err != nil {
		return "", v1.TokenUsage{}, err
	}
	defer func() { _ = r.Close() }()
	reply, err := r.Ask(ctx, question, v1.TokensForDispatch, AskTimeout)
	return reply, obs.total(), err
}

// held keeps what a pick cost for the session it chose for.
func (p *Placer) held(sessionID string, t v1.TokenUsage) {
	if sessionID == "" || t.IsZero() {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, c := range p.costs {
		if time.Since(c.at) > costsFor {
			delete(p.costs, id)
		}
	}
	c := p.costs[sessionID]
	c.tokens.Add(t)
	c.at = time.Now()
	p.costs[sessionID] = c
}

// Started records on a session that just started what the pick that
// chose its repository cost, if one did.
func (p *Placer) Started(ctx context.Context, sessionID string) {
	p.mu.Lock()
	c, ok := p.costs[sessionID]
	delete(p.costs, sessionID)
	p.mu.Unlock()
	if !ok {
		return
	}
	if err := p.sessions.RecordTokens(ctx, sessionID, v1.TokensForDispatch, c.tokens); err != nil {
		slog.Warn("placement: can't record what choosing the repository cost", "session", sessionID, "err", err)
	}
}

// spent is an observer that counts what the agent's requests cost and
// takes nothing else: the side session's answer comes back from Ask, and
// it may neither act nor ask anyone anything.
type spent struct {
	mu     sync.Mutex
	tokens v1.TokenUsage
}

func (o *spent) total() v1.TokenUsage {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.tokens
}

func (o *spent) Tokens(_ string, t v1.TokenUsage) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.tokens.Add(t)
}

func (*spent) Spawned(string, string, string) {}
func (*spent) ToolCall(agents.ToolCall)       {}
func (*spent) Plan([]v1.PlanEntry)            {}
func (*spent) Usage(uint64, uint64)           {}
func (*spent) Message(string)                 {}
func (*spent) ModeChanged(string, string)     {}
func (*spent) ModelChanged(string)            {}
func (*spent) ModeLost(error)                 {}
func (*spent) Question(context.Context, agents.Question) agents.Answer {
	return agents.Answer{Cancelled: true}
}
func (*spent) Permission(context.Context, agents.PermissionRequest) agents.Decision {
	return agents.Decision{Cancelled: true}
}
