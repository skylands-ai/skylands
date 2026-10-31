// Package session is the Session aggregate on a daemon. It handles
// StartSession, runs the agent over ACP in its own git worktree, turns what
// the agent reports into session events, and records them in the host's
// stream. The daemon is the only writer of that stream, so this manager
// serializes its commands.
package session

import (
	"bytes"
	"context"
	"encoding/json"
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
	// StatusRunning means the agent is working on a turn.
	StatusRunning Status = "running"
	// StatusIdle means the agent finished a turn and waits for a person.
	StatusIdle   Status = "idle"
	StatusEnded  Status = "ended"
	StatusFailed Status = "failed"
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
	// Title is the name the agent gave the conversation, once it has.
	Title string
	// Trusted means tool calls are allowed without asking.
	Trusted bool
	// Pending holds the tool calls waiting on a person, oldest first.
	Pending []Permission
	// Turns counts the prompts sent so far.
	Turns     int
	StartedAt time.Time
	EndedAt   time.Time
	// UpdatedAt is the time of the session's latest event.
	UpdatedAt time.Time
	ToolCalls []ToolCall
	Plan      []v1.PlanEntry
	Used      uint64
	Size      uint64
}

// Permission is a tool call waiting on a person.
type Permission struct {
	RequestID  string
	ToolCallID string
	Title      string
	Kind       string
	Locations  []string
	Input      json.RawMessage
	Options    []v1.PermissionOption
	At         time.Time
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
	// Publish, when set, sends each transcript append on the session's live
	// subject so a browser can read along.
	Publish func(subject string, data []byte) error
	// AllowKinds are the tool call kinds the host's rules allow without
	// asking anyone. Nil means DefaultAllowKinds.
	AllowKinds []string
	// PermissionTimeout is how long a request waits for a person before it
	// expires. Zero means 30 minutes.
	PermissionTimeout time.Duration
}

// DefaultAllowKinds are the tool call kinds allowed without asking: what
// only looks at the worktree. Everything else waits for a person, until
// the session is trusted.
var DefaultAllowKinds = []string{"read", "search", "think"}

var (
	ErrUnknownRepo    = errors.New("no such repository on this host")
	ErrUnknownAgent   = errors.New("no such agent on this host")
	ErrEmptyPrompt    = errors.New("a prompt is required")
	ErrUnknownSession = errors.New("no such session on this host")
	ErrNotIdle        = errors.New("the session is not waiting for a prompt")
	ErrOver           = errors.New("the session has ended")
	ErrUnknownRequest = errors.New("no such request is waiting")
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
	// live holds the agent process of every session that has not ended.
	live map[string]*live
	// transcripts holds each session's transcript writer once it is in use.
	transcripts map[string]*transcript
	runs        sync.WaitGroup
}

// live is a session's running agent and the turn it may be on.
type live struct {
	run *agents.Run
	// turnDone is closed when the current turn's goroutine has finished,
	// so Stop can record the end after it.
	turnDone chan struct{}
	stopping bool
	// asks holds, by request id, where each waiting permission request
	// hears its decision.
	asks map[string]chan agents.Decision
}

// Load rebuilds the aggregate from the host's stream. A session that was
// still running when the daemon last stopped is marked failed, because its
// agent process died with the daemon.
func Load(ctx context.Context, store *es.Store, cfg Config) (*Manager, error) {
	if cfg.Registry == nil {
		cfg.Registry = agents.DefaultRegistry()
	}
	m := &Manager{cfg: cfg, store: store, ctx: ctx, sessions: map[string]*Session{}, handled: map[string]string{}, live: map[string]*live{}, transcripts: map[string]*transcript{}}
	err := store.Replay(ctx, v1.IslandStreamName(cfg.Host), []string{v1.SessionEventFilter(cfg.Host)}, func(seq uint64, ev v1.Event) error {
		m.apply(ev)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("load sessions for %s: %w", cfg.Host, err)
	}
	for _, s := range m.List() {
		if s.Status == StatusRunning || s.Status == StatusIdle {
			for _, p := range s.Pending {
				if err := m.record(ctx, s.ID, "", v1.EventPermissionExpired, v1.PermissionExpired{SessionID: s.ID, RequestID: p.RequestID, Reason: "restart"}); err != nil {
					return nil, err
				}
			}
			if err := m.record(ctx, s.ID, "", v1.EventAgentFailed, v1.AgentFailed{SessionID: s.ID, Error: "the daemon restarted while the session was open"}); err != nil {
				return nil, err
			}
		}
	}
	return m, nil
}

// Approve handles ApproveToolCall: it answers a waiting request with an
// allow option and records who did. By names the path the answer came
// over, "local" or "citadel".
func (m *Manager) Approve(ctx context.Context, cmd v1.ApproveToolCall, by string) (Session, error) {
	return m.decide(ctx, cmd.ID, cmd.SessionID, cmd.RequestID, by, cmd.OptionID, true)
}

// Deny handles DenyToolCall: it answers a waiting request with a reject
// option and records who did.
func (m *Manager) Deny(ctx context.Context, cmd v1.DenyToolCall, by string) (Session, error) {
	return m.decide(ctx, cmd.ID, cmd.SessionID, cmd.RequestID, by, "", false)
}

func (m *Manager) decide(ctx context.Context, commandID, sessionID, requestID, by, optionID string, allow bool) (Session, error) {
	m.cmdMu.Lock()
	defer m.cmdMu.Unlock()
	m.mu.Lock()
	if id, ok := m.handled[commandID]; ok {
		s := *m.sessions[id]
		m.mu.Unlock()
		return s, nil
	}
	s, ok := m.sessions[sessionID]
	l := m.live[sessionID]
	if !ok {
		m.mu.Unlock()
		return Session{}, fmt.Errorf("%w: %s", ErrUnknownSession, sessionID)
	}
	var pending *Permission
	for i := range s.Pending {
		if s.Pending[i].RequestID == requestID {
			pending = &s.Pending[i]
		}
	}
	var ask chan agents.Decision
	if l != nil {
		ask = l.asks[requestID]
	}
	m.mu.Unlock()
	if pending == nil || ask == nil {
		return *s, fmt.Errorf("%w: %s", ErrUnknownRequest, requestID)
	}
	option := pickOption(pending.Options, allow, optionID)
	if option == "" {
		if allow {
			return *s, errors.New("the agent offers no allow option for this request")
		}
		// No reject option: cancelling the request is the refusal.
		if err := m.record(ctx, sessionID, commandID, v1.EventPermissionDenied, v1.PermissionDenied{SessionID: sessionID, RequestID: requestID, By: by}); err != nil {
			return Session{}, err
		}
		m.transcript(sessionID).Line(ToolLinePrefix + pending.Title + " (denied)")
		ask <- agents.Decision{Cancelled: true}
		out, _ := m.Get(sessionID)
		return out, nil
	}
	if allow {
		if err := m.record(ctx, sessionID, commandID, v1.EventPermissionGranted, v1.PermissionGranted{SessionID: sessionID, RequestID: requestID, OptionID: option, By: by}); err != nil {
			return Session{}, err
		}
	} else {
		if err := m.record(ctx, sessionID, commandID, v1.EventPermissionDenied, v1.PermissionDenied{SessionID: sessionID, RequestID: requestID, OptionID: option, By: by}); err != nil {
			return Session{}, err
		}
		m.transcript(sessionID).Line(ToolLinePrefix + pending.Title + " (denied)")
	}
	slog.Info("session: request decided", "session", sessionID, "request", requestID, "allowed", allow, "by", by)
	ask <- agents.Decision{OptionID: option}
	out, _ := m.Get(sessionID)
	return out, nil
}

// pickOption chooses the option to answer with: the one asked for, else
// the first allow option (once before always) or the first reject option.
func pickOption(options []v1.PermissionOption, allow bool, wanted string) string {
	if wanted != "" {
		for _, o := range options {
			if o.ID == wanted && (strings.HasPrefix(o.Kind, "allow") == allow) {
				return o.ID
			}
		}
		return ""
	}
	first, second := "allow_once", "allow_always"
	if !allow {
		first, second = "reject_once", "reject_always"
	}
	for _, want := range []string{first, second} {
		for _, o := range options {
			if o.Kind == want {
				return o.ID
			}
		}
	}
	return ""
}

// Trust handles TrustSession: from now on the session's tool calls are
// allowed without asking, or asked about again. Trusting a session answers
// whatever is waiting.
func (m *Manager) Trust(ctx context.Context, cmd v1.TrustSession) (Session, error) {
	m.cmdMu.Lock()
	defer m.cmdMu.Unlock()
	m.mu.Lock()
	if id, ok := m.handled[cmd.ID]; ok {
		s := *m.sessions[id]
		m.mu.Unlock()
		return s, nil
	}
	s, ok := m.sessions[cmd.SessionID]
	if !ok {
		m.mu.Unlock()
		return Session{}, fmt.Errorf("%w: %s", ErrUnknownSession, cmd.SessionID)
	}
	if s.Status == StatusEnded || s.Status == StatusFailed {
		m.mu.Unlock()
		return *s, ErrOver
	}
	m.mu.Unlock()
	if err := m.record(ctx, cmd.SessionID, cmd.ID, v1.EventSessionTrusted, v1.SessionTrusted{SessionID: cmd.SessionID, Trusted: cmd.Trusted}); err != nil {
		return Session{}, err
	}
	if cmd.Trusted {
		m.cmdMu.Unlock()
		for _, p := range s.Pending {
			_, _ = m.Approve(ctx, v1.ApproveToolCall{ID: cmd.ID + "/" + p.RequestID, SessionID: cmd.SessionID, RequestID: p.RequestID}, "trust")
		}
		m.cmdMu.Lock()
	}
	slog.Info("session: trust", "session", cmd.SessionID, "trusted", cmd.Trusted)
	out, _ := m.Get(cmd.SessionID)
	return out, nil
}

// Requests lists every tool call waiting on a person, across sessions.
func (m *Manager) Requests() []Request {
	var out []Request
	for _, s := range m.List() {
		for _, p := range s.Pending {
			out = append(out, Request{Session: s, Permission: p})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Permission.At.Before(out[j].Permission.At) })
	return out
}

// Request is a waiting tool call with its session.
type Request struct {
	Session    Session
	Permission Permission
}

// FindRequest finds the session a waiting request belongs to.
func (m *Manager) FindRequest(requestID string) (Session, bool) {
	for _, s := range m.List() {
		for _, p := range s.Pending {
			if p.RequestID == requestID {
				return s, true
			}
		}
	}
	return Session{}, false
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

// Prompt handles SendPrompt: it continues an idle session with another
// turn. A command id already in the history gets the session back.
func (m *Manager) Prompt(ctx context.Context, cmd v1.SendPrompt) (Session, error) {
	m.cmdMu.Lock()
	defer m.cmdMu.Unlock()

	m.mu.Lock()
	if id, ok := m.handled[cmd.ID]; ok {
		s := *m.sessions[id]
		m.mu.Unlock()
		return s, nil
	}
	s, ok := m.sessions[cmd.SessionID]
	l := m.live[cmd.SessionID]
	if !ok {
		m.mu.Unlock()
		return Session{}, fmt.Errorf("%w: %s", ErrUnknownSession, cmd.SessionID)
	}
	switch {
	case s.Status == StatusEnded || s.Status == StatusFailed || l == nil:
		m.mu.Unlock()
		return *s, ErrOver
	case s.Status != StatusIdle:
		m.mu.Unlock()
		return *s, ErrNotIdle
	}
	m.mu.Unlock()
	if strings.TrimSpace(cmd.Text) == "" {
		return *s, ErrEmptyPrompt
	}
	if err := m.record(ctx, cmd.SessionID, cmd.ID, v1.EventPromptSent, v1.PromptSent{SessionID: cmd.SessionID, Text: cmd.Text}); err != nil {
		return Session{}, err
	}
	m.transcript(cmd.SessionID).Append("## You\n\n" + strings.TrimSpace(cmd.Text) + "\n\n## Agent\n\n")
	l.turnDone = make(chan struct{})
	m.runs.Add(1)
	go m.turn(cmd.SessionID, l, cmd.Text)
	out, _ := m.Get(cmd.SessionID)
	return out, nil
}

// Stop handles StopAgent: it cancels any turn in progress, stops the agent,
// and records SessionEnded.
func (m *Manager) Stop(ctx context.Context, cmd v1.StopAgent) (Session, error) {
	m.cmdMu.Lock()
	defer m.cmdMu.Unlock()

	m.mu.Lock()
	if id, ok := m.handled[cmd.ID]; ok {
		s := *m.sessions[id]
		m.mu.Unlock()
		return s, nil
	}
	s, ok := m.sessions[cmd.SessionID]
	l := m.live[cmd.SessionID]
	if !ok {
		m.mu.Unlock()
		return Session{}, fmt.Errorf("%w: %s", ErrUnknownSession, cmd.SessionID)
	}
	if s.Status == StatusEnded || s.Status == StatusFailed {
		m.mu.Unlock()
		return *s, nil
	}
	if l != nil {
		l.stopping = true
	}
	pending := append([]Permission(nil), s.Pending...)
	m.mu.Unlock()

	if l != nil {
		// Whatever was waiting on a person is cancelled with the turn.
		for _, p := range pending {
			_ = m.record(ctx, cmd.SessionID, "", v1.EventPermissionExpired, v1.PermissionExpired{SessionID: cmd.SessionID, RequestID: p.RequestID, Reason: "stopped"})
			m.mu.Lock()
			ask := l.asks[p.RequestID]
			m.mu.Unlock()
			if ask != nil {
				ask <- agents.Decision{Cancelled: true}
			}
		}
		l.run.Cancel(ctx)
		_ = l.run.Close()
		if l.turnDone != nil {
			select {
			case <-l.turnDone:
			case <-time.After(10 * time.Second):
			}
		}
		m.mu.Lock()
		delete(m.live, cmd.SessionID)
		m.mu.Unlock()
	}
	m.transcript(cmd.SessionID).Append("\n\n---\n\n*Stopped*\n")
	if err := m.record(ctx, cmd.SessionID, cmd.ID, v1.EventSessionEnded, v1.SessionEnded{SessionID: cmd.SessionID, StopReason: "stopped"}); err != nil {
		return Session{}, err
	}
	slog.Info("session: stopped", "session", cmd.SessionID)
	out, _ := m.Get(cmd.SessionID)
	return out, nil
}

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
	m.transcript(id).Append("## You\n\n" + strings.TrimSpace(cmd.Prompt) + "\n\n## Agent\n\n")

	m.runs.Add(1)
	go m.begin(id, adapter, worktree, cmd.Prompt, cmd.Model)
	s, _ := m.Get(id)
	return s, nil
}

// begin launches a session's agent and runs its first turn.
func (m *Manager) begin(id string, adapter agents.Adapter, worktree, prompt, model string) {
	defer m.runs.Done()
	obs := &observer{m: m, id: id, t: m.transcript(id)}
	run, err := agents.Start(m.ctx, adapter, worktree, model, obs)
	if err != nil {
		if m.ctx.Err() == nil {
			m.failed(id, err)
		}
		return
	}
	l := &live{run: run, turnDone: make(chan struct{}), asks: map[string]chan agents.Decision{}}
	m.mu.Lock()
	m.live[id] = l
	m.mu.Unlock()
	m.runs.Add(2)
	go m.title(id, run, prompt)
	m.turn(id, l, prompt)
}

// title asks the agent to name the conversation while its first turn runs,
// and records the answer. A failure leaves the session untitled: the
// citadel then shows the first line of the prompt instead.
func (m *Manager) title(id string, run *agents.Run, prompt string) {
	defer m.runs.Done()
	title, err := run.Title(m.ctx, prompt, 90*time.Second)
	if err != nil {
		if m.ctx.Err() == nil {
			slog.Debug("session: no title from the agent", "session", id, "err", err)
		}
		return
	}
	slog.Info("session: titled", "session", id, "title", title)
	_ = m.record(m.ctx, id, "", v1.EventSessionTitled, v1.SessionTitled{SessionID: id, Title: title})
}

// turn runs one prompt and records how it ends: TurnEnded leaves the
// session idle for the next prompt, AgentFailed ends it.
func (m *Manager) turn(id string, l *live, text string) {
	defer m.runs.Done()
	defer close(l.turnDone)
	stop, err := l.run.Prompt(m.ctx, text)
	m.mu.Lock()
	stopping := l.stopping
	m.mu.Unlock()
	if m.ctx.Err() != nil || stopping {
		// The daemon is stopping, or Stop is recording the end itself.
		return
	}
	if err != nil {
		_ = l.run.Close()
		m.mu.Lock()
		delete(m.live, id)
		m.mu.Unlock()
		m.failed(id, err)
		return
	}
	slog.Info("session: turn ended", "session", id, "stop", stop)
	m.transcript(id).Append("\n\n")
	_ = m.record(m.ctx, id, "", v1.EventTurnEnded, v1.TurnEnded{SessionID: id, StopReason: stop})
}

func (m *Manager) failed(id string, err error) {
	slog.Warn("session: agent failed", "session", id, "err", err)
	m.transcript(id).Append("\n\n---\n\n*Failed: " + err.Error() + "*\n")
	_ = m.record(m.ctx, id, "", v1.EventAgentFailed, v1.AgentFailed{SessionID: id, Error: err.Error()})
}

func (m *Manager) transcriptPath(id string) string {
	return filepath.Join(m.cfg.TranscriptsDir, id+".md")
}

// transcript is one session's transcript file on the host, and the live
// feed of whatever is appended to it.
type transcript struct {
	mu   sync.Mutex
	path string
	// size is the file's length, which is where the next append lands.
	size int64
	// last is the file's final byte, so a line can be started cleanly.
	last    byte
	publish func(l v1.Live)
}

// transcript returns the session's transcript writer, picking up the size
// of whatever is already on disk.
func (m *Manager) transcript(id string) *transcript {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.transcripts[id]
	if t != nil {
		return t
	}
	t = &transcript{path: m.transcriptPath(id)}
	if f, err := os.Open(t.path); err == nil {
		if fi, err := f.Stat(); err == nil && fi.Size() > 0 {
			t.size = fi.Size()
			var b [1]byte
			if _, err := f.ReadAt(b[:], t.size-1); err == nil {
				t.last = b[0]
			}
		}
		_ = f.Close()
	}
	if m.cfg.Publish != nil {
		subject := v1.SessionLiveSubject(m.cfg.Host, id)
		publish := m.cfg.Publish
		t.publish = func(l v1.Live) {
			data, err := json.Marshal(l)
			if err == nil {
				_ = publish(subject, data)
			}
		}
	}
	m.transcripts[id] = t
	return t
}

// Append writes text to the end of the transcript and tells anyone reading
// along where it landed.
func (t *transcript) Append(text string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.append(text)
}

// Line appends text on a line of its own.
func (t *transcript) Line(text string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.size > 0 && t.last != '\n' {
		text = "\n" + text
	}
	t.append(text + "\n")
}

func (t *transcript) append(text string) {
	if text == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(t.path), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(t.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	n, _ := f.WriteString(text)
	_ = f.Close()
	if n == 0 {
		return
	}
	off := t.size
	t.size += int64(n)
	t.last = text[n-1]
	if t.publish != nil {
		t.publish(v1.Live{Off: off, Text: text[:n]})
	}
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
	touch := func(id string) *Session {
		s := m.sessions[id]
		if s != nil && ev.Time.After(s.UpdatedAt) {
			s.UpdatedAt = ev.Time
		}
		return s
	}
	switch ev.Type {
	case v1.EventSessionStarted:
		var d v1.SessionStarted
		if ev.Decode(&d) != nil {
			return
		}
		m.sessions[d.SessionID] = &Session{ID: d.SessionID, RepoID: d.RepoID, Agent: d.Agent, Model: d.Model, Worktree: d.Worktree, Branch: d.Branch, Status: StatusRunning, StartedAt: ev.Time, UpdatedAt: ev.Time}
		if ev.CommandID != "" {
			m.handled[ev.CommandID] = d.SessionID
		}
	case v1.EventPromptSent:
		var d v1.PromptSent
		if ev.Decode(&d) == nil {
			if s := touch(d.SessionID); s != nil {
				if s.Turns == 0 {
					s.Prompt = d.Text
				}
				s.Turns++
				s.Status = StatusRunning
				if ev.CommandID != "" {
					m.handled[ev.CommandID] = d.SessionID
				}
			}
		}
	case v1.EventTurnEnded:
		var d v1.TurnEnded
		if ev.Decode(&d) == nil {
			if s := touch(d.SessionID); s != nil && s.Status == StatusRunning {
				s.Status, s.StopReason = StatusIdle, d.StopReason
			}
		}
	case v1.EventSessionTitled:
		var d v1.SessionTitled
		if ev.Decode(&d) == nil {
			if s := touch(d.SessionID); s != nil {
				s.Title = d.Title
			}
		}
	case v1.EventSessionTrusted:
		var d v1.SessionTrusted
		if ev.Decode(&d) == nil {
			if s := touch(d.SessionID); s != nil {
				s.Trusted = d.Trusted
			}
		}
	case v1.EventPermissionRequested:
		var d v1.PermissionRequested
		if ev.Decode(&d) == nil {
			if s := touch(d.SessionID); s != nil {
				s.Pending = append(s.Pending, Permission{RequestID: d.RequestID, ToolCallID: d.ToolCallID, Title: d.Title, Kind: d.Kind, Locations: d.Locations, Input: d.Input, Options: d.Options, At: ev.Time})
			}
		}
	case v1.EventPermissionGranted, v1.EventPermissionDenied, v1.EventPermissionExpired:
		var d struct {
			SessionID string `json:"sessionId"`
			RequestID string `json:"requestId"`
		}
		if ev.Decode(&d) == nil {
			if s := touch(d.SessionID); s != nil {
				kept := s.Pending[:0]
				for _, p := range s.Pending {
					if p.RequestID != d.RequestID {
						kept = append(kept, p)
					}
				}
				s.Pending = kept
				if ev.CommandID != "" {
					m.handled[ev.CommandID] = d.SessionID
				}
			}
		}
	case v1.EventAgentSpawned:
		var d v1.AgentSpawned
		if ev.Decode(&d) == nil {
			if s := touch(d.SessionID); s != nil {
				s.AgentName = d.Name
			}
		}
	case v1.EventToolCallStarted:
		var d v1.ToolCallStarted
		if ev.Decode(&d) == nil {
			if s := touch(d.SessionID); s != nil {
				s.ToolCalls = append(s.ToolCalls, ToolCall{ID: d.ToolCallID, Title: d.Title, Kind: d.Kind, Status: "in_progress", Locations: d.Locations})
			}
		}
	case v1.EventToolCallCompleted:
		var d v1.ToolCallCompleted
		if ev.Decode(&d) == nil {
			if s := touch(d.SessionID); s != nil {
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
			if s := touch(d.SessionID); s != nil {
				s.Plan = d.Entries
			}
		}
	case v1.EventUsageReported:
		var d v1.UsageReported
		if ev.Decode(&d) == nil {
			if s := touch(d.SessionID); s != nil {
				s.Used, s.Size = d.Used, d.Size
			}
		}
	case v1.EventAgentFailed:
		var d v1.AgentFailed
		if ev.Decode(&d) == nil {
			if s := touch(d.SessionID); s != nil {
				s.Status, s.Error, s.EndedAt = StatusFailed, d.Error, ev.Time
			}
		}
	case v1.EventSessionEnded:
		var d v1.SessionEnded
		if ev.Decode(&d) == nil {
			if s := touch(d.SessionID); s != nil {
				s.Status, s.StopReason, s.EndedAt = StatusEnded, d.StopReason, ev.Time
				if ev.CommandID != "" {
					m.handled[ev.CommandID] = d.SessionID
				}
			}
		}
	}
}

// observer turns what the agent reports into session events, one at a time.
type observer struct {
	m  *Manager
	id string
	t  *transcript
	mu sync.Mutex
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
		// The transcript notes each tool call on a line of its own, between
		// the agent's words, so a reader sees what it did where it did it.
		o.t.Line(ToolLinePrefix + tc.Title)
	}
	finished := tc.Status == "completed" || tc.Status == "failed"
	if finished && (known == nil || (known.Status != "completed" && known.Status != "failed")) {
		_ = o.m.record(o.m.ctx, o.id, "", v1.EventToolCallCompleted, v1.ToolCallCompleted{SessionID: o.id, ToolCallID: tc.ID, Status: tc.Status, Locations: tc.Locations})
		if tc.Status == "failed" {
			o.t.Line(ToolLinePrefix + tc.Title + " (failed)")
		}
	}
}

// ToolLinePrefix opens a transcript line that records a tool call.
const ToolLinePrefix = "\u2699 "

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
	o.t.Append(text)
}

// Permission settles a tool call by the host's rules or the session's
// trust, and otherwise records it as waiting and blocks until a person
// answers, the request expires, or the agent gives up.
func (o *observer) Permission(ctx context.Context, req agents.PermissionRequest) agents.Decision {
	m := o.m
	s, _ := m.Get(o.id)
	if s.Trusted || m.allowedByRule(req.ToolCall.Kind) {
		if opt := pickOption(req.Options, true, ""); opt != "" {
			return agents.Decision{OptionID: opt}
		}
	}
	requestID := nuid.Next()
	ask := make(chan agents.Decision, 1)
	m.mu.Lock()
	l := m.live[o.id]
	if l != nil {
		l.asks[requestID] = ask
	}
	m.mu.Unlock()
	if l == nil {
		return agents.Decision{Cancelled: true}
	}
	defer func() {
		m.mu.Lock()
		delete(l.asks, requestID)
		m.mu.Unlock()
	}()
	tc := req.ToolCall
	if err := m.record(m.ctx, o.id, "", v1.EventPermissionRequested, v1.PermissionRequested{SessionID: o.id, RequestID: requestID, ToolCallID: tc.ID, Title: tc.Title, Kind: tc.Kind, Locations: tc.Locations, Input: req.Input, Options: req.Options}); err != nil {
		return agents.Decision{Cancelled: true}
	}
	slog.Info("session: waiting for a person", "session", o.id, "request", requestID, "tool", tc.Title)
	timeout := m.cfg.PermissionTimeout
	if timeout == 0 {
		timeout = 30 * time.Minute
	}
	select {
	case d := <-ask:
		return d
	case <-time.After(timeout):
		_ = m.record(m.ctx, o.id, "", v1.EventPermissionExpired, v1.PermissionExpired{SessionID: o.id, RequestID: requestID, Reason: "timeout"})
		o.t.Line(ToolLinePrefix + tc.Title + " (nobody answered in time)")
		return agents.Decision{Cancelled: true}
	case <-ctx.Done():
		// The agent stopped waiting, or the turn was cancelled; Stop records
		// the expiry itself.
		if _, ok := m.Get(o.id); ok {
			m.mu.Lock()
			still := false
			for _, p := range m.sessions[o.id].Pending {
				still = still || p.RequestID == requestID
			}
			m.mu.Unlock()
			if still {
				_ = m.record(m.ctx, o.id, "", v1.EventPermissionExpired, v1.PermissionExpired{SessionID: o.id, RequestID: requestID, Reason: "stopped"})
			}
		}
		return agents.Decision{Cancelled: true}
	}
}

// allowedByRule says whether a tool call of this kind needs no one's answer.
func (m *Manager) allowedByRule(kind string) bool {
	kinds := m.cfg.AllowKinds
	if kinds == nil {
		kinds = DefaultAllowKinds
	}
	for _, k := range kinds {
		if k == kind {
			return true
		}
	}
	return false
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
