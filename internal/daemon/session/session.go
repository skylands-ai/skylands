// Package session is the Session aggregate on a daemon. It handles
// StartSession, runs the agent over ACP in its own git worktree, turns what
// the agent reports into session events, and records them in the host's
// stream. The daemon is the only writer of that stream, so this manager
// serializes its commands.
package session

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nuid"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/daemon/agents"
	"github.com/skylands-ai/skylands/internal/daemon/host"
	"github.com/skylands-ai/skylands/internal/es"
)

// Status is where a session is in its life.
type Status string

const (
	StatusRunning Status = "running"
	StatusEnded   Status = "ended"
	StatusFailed  Status = "failed"
)

// ToolCall is one tool call as the session last recorded it.
type ToolCall struct {
	ID        string
	Title     string
	Kind      string
	Status    string
	Locations []string
}

// Session is one agent run on one repository.
type Session struct {
	ID         string
	RepoID     string
	Agent      string
	Model      string
	AgentName  string
	Prompt     string
	Worktree   string
	Branch     string
	Status     Status
	StopReason string
	Error      string
	StartedAt  time.Time
	EndedAt    time.Time
	ToolCalls  []ToolCall
	Plan       []v1.PlanEntry
	Used       uint64
	Size       uint64
}

// Repos finds registered repositories, which the Host aggregate provides.
type Repos interface {
	Repo(id string) (host.Repo, bool)
}

// Config describes where a manager keeps worktrees and transcripts and what
// it can launch.
type Config struct {
	Host string
	// WorktreesDir holds one git worktree per session.
	WorktreesDir string
	// TranscriptsDir holds each session's agent messages, which stay on the
	// host.
	TranscriptsDir string
	Registry       agents.Registry
	Repos          Repos
}

var (
	ErrUnknownRepo  = errors.New("no such repository on this host")
	ErrUnknownAgent = errors.New("no such agent on this host")
	ErrEmptyPrompt  = errors.New("a prompt is required")
)

// Manager is the aggregate for every session on one island.
type Manager struct {
	cfg   Config
	store *es.Store
	// ctx outlives any one command: agents run on it, and a cancelled ctx
	// stops every agent.
	ctx context.Context

	cmdMu    sync.Mutex
	mu       sync.Mutex
	sessions map[string]*Session
	handled  map[string]string
	runs     sync.WaitGroup
}

// Load rebuilds the aggregate from the host's stream. A session that was
// still running when the daemon last stopped is marked failed, because its
// agent process died with the daemon.
func Load(ctx context.Context, store *es.Store, cfg Config) (*Manager, error) {
	if cfg.Registry == nil {
		cfg.Registry = agents.DefaultRegistry()
	}
	m := &Manager{cfg: cfg, store: store, ctx: ctx, sessions: map[string]*Session{}, handled: map[string]string{}}
	err := store.Replay(ctx, v1.IslandStreamName(cfg.Host), []string{v1.SessionEventFilter(cfg.Host)}, func(seq uint64, ev v1.Event) error {
		m.apply(ev)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("load sessions for %s: %w", cfg.Host, err)
	}
	for _, s := range m.List() {
		if s.Status == StatusRunning {
			if err := m.record(ctx, s.ID, "", v1.EventAgentFailed, v1.AgentFailed{SessionID: s.ID, Error: "the daemon restarted while the session was running"}); err != nil {
				return nil, err
			}
		}
	}
	return m, nil
}

// List returns every session, newest first.
func (m *Manager) List() []Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out
}

// Get returns one session.
func (m *Manager) Get(id string) (Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return Session{}, false
	}
	return *s, true
}

// Wait blocks until every running agent has stopped. Cancel the manager's
// context first.
func (m *Manager) Wait() { m.runs.Wait() }

// Start handles StartSession: it creates the worktree, records
// SessionStarted and PromptSent, and launches the agent in the background. A
// command id already in the history gets its original session back.
func (m *Manager) Start(ctx context.Context, cmd v1.StartSession) (Session, error) {
	m.cmdMu.Lock()
	defer m.cmdMu.Unlock()

	m.mu.Lock()
	if id, ok := m.handled[cmd.ID]; ok {
		s := *m.sessions[id]
		m.mu.Unlock()
		return s, nil
	}
	m.mu.Unlock()

	if strings.TrimSpace(cmd.Prompt) == "" {
		return Session{}, ErrEmptyPrompt
	}
	repo, ok := m.cfg.Repos.Repo(cmd.RepoID)
	if !ok {
		return Session{}, fmt.Errorf("%w: %s", ErrUnknownRepo, cmd.RepoID)
	}
	adapter, ok := m.cfg.Registry[cmd.Agent]
	if !ok {
		return Session{}, fmt.Errorf("%w: %s", ErrUnknownAgent, cmd.Agent)
	}
	id := cmd.SessionID
	if id == "" {
		id = nuid.Next()
	}
	worktree := filepath.Join(m.cfg.WorktreesDir, id)
	branch := "skylands/" + id
	if err := addWorktree(ctx, repo.Path, worktree, branch); err != nil {
		return Session{}, err
	}

	if err := m.record(ctx, id, cmd.ID, v1.EventSessionStarted, v1.SessionStarted{SessionID: id, RepoID: repo.ID, Agent: cmd.Agent, Model: cmd.Model, Worktree: worktree, Branch: branch}); err != nil {
		return Session{}, err
	}
	if err := m.record(ctx, id, cmd.ID, v1.EventPromptSent, v1.PromptSent{SessionID: id, Text: cmd.Prompt}); err != nil {
		return Session{}, err
	}
	slog.Info("session: started", "session", id, "repo", repo.Name, "agent", cmd.Agent, "model", cmd.Model, "worktree", worktree)
	appendTranscript(m.transcriptPath(id), "# Prompt\n\n"+strings.TrimSpace(cmd.Prompt)+"\n\n# Agent\n\n")

	m.runs.Add(1)
	go m.run(id, adapter, worktree, cmd.Prompt, cmd.Model)
	s, _ := m.Get(id)
	return s, nil
}

// run drives the agent for one session and records how it ends.
func (m *Manager) run(id string, adapter agents.Adapter, worktree, prompt, model string) {
	defer m.runs.Done()
	obs := &observer{m: m, id: id, transcript: m.transcriptPath(id)}
	stop, err := agents.RunPrompt(m.ctx, adapter, worktree, prompt, model, obs)
	if m.ctx.Err() != nil {
		// The daemon is stopping. The next start records what happened.
		return
	}
	if err != nil {
		slog.Warn("session: agent failed", "session", id, "err", err)
		appendTranscript(obs.transcript, "\n\n---\n\n*Failed: "+err.Error()+"*\n")
		_ = m.record(m.ctx, id, "", v1.EventAgentFailed, v1.AgentFailed{SessionID: id, Error: err.Error()})
		return
	}
	slog.Info("session: ended", "session", id, "stop", stop)
	appendTranscript(obs.transcript, "\n\n---\n\n*Finished: "+stop+"*\n")
	_ = m.record(m.ctx, id, "", v1.EventSessionEnded, v1.SessionEnded{SessionID: id, StopReason: stop})
}

func (m *Manager) transcriptPath(id string) string {
	return filepath.Join(m.cfg.TranscriptsDir, id+".md")
}

// appendTranscript adds text to a session's transcript on the host.
func appendTranscript(path, text string) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = f.WriteString(text)
}

// record appends one event for a session and folds it into the state.
func (m *Manager) record(ctx context.Context, id, commandID, eventType string, data any) error {
	ev, err := v1.NewEvent(eventType, commandID, data)
	if err != nil {
		return err
	}
	if _, err := m.store.Append(ctx, v1.SessionEventSubject(m.cfg.Host, id, eventType), ev); err != nil {
		return err
	}
	m.apply(ev)
	return nil
}

// apply folds one event into the state.
func (m *Manager) apply(ev v1.Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch ev.Type {
	case v1.EventSessionStarted:
		var d v1.SessionStarted
		if ev.Decode(&d) != nil {
			return
		}
		m.sessions[d.SessionID] = &Session{ID: d.SessionID, RepoID: d.RepoID, Agent: d.Agent, Model: d.Model, Worktree: d.Worktree, Branch: d.Branch, Status: StatusRunning, StartedAt: ev.Time}
		if ev.CommandID != "" {
			m.handled[ev.CommandID] = d.SessionID
		}
	case v1.EventPromptSent:
		var d v1.PromptSent
		if ev.Decode(&d) == nil {
			if s, ok := m.sessions[d.SessionID]; ok {
				s.Prompt = d.Text
			}
		}
	case v1.EventAgentSpawned:
		var d v1.AgentSpawned
		if ev.Decode(&d) == nil {
			if s, ok := m.sessions[d.SessionID]; ok {
				s.AgentName = d.Name
			}
		}
	case v1.EventToolCallStarted:
		var d v1.ToolCallStarted
		if ev.Decode(&d) == nil {
			if s, ok := m.sessions[d.SessionID]; ok {
				s.ToolCalls = append(s.ToolCalls, ToolCall{ID: d.ToolCallID, Title: d.Title, Kind: d.Kind, Status: "in_progress", Locations: d.Locations})
			}
		}
	case v1.EventToolCallCompleted:
		var d v1.ToolCallCompleted
		if ev.Decode(&d) == nil {
			if s, ok := m.sessions[d.SessionID]; ok {
				for i := range s.ToolCalls {
					if s.ToolCalls[i].ID == d.ToolCallID {
						s.ToolCalls[i].Status = d.Status
						if len(d.Locations) > 0 {
							s.ToolCalls[i].Locations = d.Locations
						}
					}
				}
			}
		}
	case v1.EventPlanUpdated:
		var d v1.PlanUpdated
		if ev.Decode(&d) == nil {
			if s, ok := m.sessions[d.SessionID]; ok {
				s.Plan = d.Entries
			}
		}
	case v1.EventUsageReported:
		var d v1.UsageReported
		if ev.Decode(&d) == nil {
			if s, ok := m.sessions[d.SessionID]; ok {
				s.Used, s.Size = d.Used, d.Size
			}
		}
	case v1.EventAgentFailed:
		var d v1.AgentFailed
		if ev.Decode(&d) == nil {
			if s, ok := m.sessions[d.SessionID]; ok {
				s.Status, s.Error, s.EndedAt = StatusFailed, d.Error, ev.Time
			}
		}
	case v1.EventSessionEnded:
		var d v1.SessionEnded
		if ev.Decode(&d) == nil {
			if s, ok := m.sessions[d.SessionID]; ok {
				s.Status, s.StopReason, s.EndedAt = StatusEnded, d.StopReason, ev.Time
			}
		}
	}
}

// observer turns what the agent reports into session events, one at a time.
type observer struct {
	m          *Manager
	id         string
	transcript string
	mu         sync.Mutex
}

func (o *observer) Spawned(name, version string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	_ = o.m.record(o.m.ctx, o.id, "", v1.EventAgentSpawned, v1.AgentSpawned{SessionID: o.id, AgentID: o.id, Name: name, Version: version})
}

func (o *observer) ToolCall(tc agents.ToolCall) {
	o.mu.Lock()
	defer o.mu.Unlock()
	s, _ := o.m.Get(o.id)
	var known *ToolCall
	for i := range s.ToolCalls {
		if s.ToolCalls[i].ID == tc.ID {
			known = &s.ToolCalls[i]
		}
	}
	if known == nil {
		_ = o.m.record(o.m.ctx, o.id, "", v1.EventToolCallStarted, v1.ToolCallStarted{SessionID: o.id, ToolCallID: tc.ID, Title: tc.Title, Kind: tc.Kind, Locations: tc.Locations})
	}
	finished := tc.Status == "completed" || tc.Status == "failed"
	if finished && (known == nil || (known.Status != "completed" && known.Status != "failed")) {
		_ = o.m.record(o.m.ctx, o.id, "", v1.EventToolCallCompleted, v1.ToolCallCompleted{SessionID: o.id, ToolCallID: tc.ID, Status: tc.Status, Locations: tc.Locations})
	}
}

func (o *observer) Plan(entries []v1.PlanEntry) {
	o.mu.Lock()
	defer o.mu.Unlock()
	_ = o.m.record(o.m.ctx, o.id, "", v1.EventPlanUpdated, v1.PlanUpdated{SessionID: o.id, Entries: entries})
}

func (o *observer) Usage(used, size uint64) {
	o.mu.Lock()
	defer o.mu.Unlock()
	_ = o.m.record(o.m.ctx, o.id, "", v1.EventUsageReported, v1.UsageReported{SessionID: o.id, Used: used, Size: size})
}

// Message keeps agent text on the host, in the session's transcript.
func (o *observer) Message(text string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	appendTranscript(o.transcript, text)
}

// addWorktree checks out a new branch of repo into dir.
func addWorktree(ctx context.Context, repo, dir, branch string) error {
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "git", "-C", repo, "worktree", "add", "-b", branch, dir)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("create worktree for %s: %s", filepath.Base(repo), msg)
	}
	return nil
}
