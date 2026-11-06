// Package session is the Session aggregate on a daemon. It handles
// StartSession, runs the agent over ACP in its own git worktree, turns what
// the agent reports into session events, and records them in the host's
// stream. The daemon is the only writer of that stream, so this manager
// serializes its commands. A daemon restart kills every agent process, so
// Load brings each open session's agent back, through the agent's own
// session when it can load one and otherwise through a new session seeded
// with the transcript.
package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/nats-io/nuid"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/approval"
	"github.com/skylands-ai/skylands/internal/canonical"
	"github.com/skylands-ai/skylands/internal/daemon/agents"
	"github.com/skylands-ai/skylands/internal/daemon/blobs"
	"github.com/skylands-ai/skylands/internal/daemon/host"
	"github.com/skylands-ai/skylands/internal/es"
	"github.com/skylands-ai/skylands/internal/rules"
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
	// OutputHash and OutputSize name what a finished call produced, which
	// the host keeps whole.
	OutputHash string
	OutputSize int
}

// Session is one agent run on one repository.
type Session struct {
	ID     string
	RepoID string
	Agent  string
	// Model is the model the session runs on, or empty for the agent's
	// own default, and Mode the mode it is in, one its adapter lists, or
	// empty for the first, in a session from before modes were recorded.
	// Both follow the session as a person or the agent changes them.
	Model     string
	Mode      string
	AgentName string
	Prompt    string
	// Worktree is where the agent works: a worktree of its own, or the
	// registered checkout when InCheckout is set.
	Worktree string
	Branch   string
	// Base is the branch a worktree session's changes merge into.
	Base       string
	InCheckout bool
	// Commit is the latest commit made through Skylands, and Commits counts
	// them. MergedInto and MergedCommit record the latest merge, and
	// PullRequest the pull request opened from the branch.
	Commit          string
	Commits         int
	MergedInto      string
	MergedCommit    string
	PullRequest     string
	WorktreeRemoved bool
	Status          Status
	StopReason      string
	Error           string
	// Title is the name the agent gave the conversation, once it has.
	Title string
	// Trusted means tool calls are allowed without asking, unless a deny
	// rule denies them. TrustedBy is who last set trust ("citadel",
	// "local", or "revocation"), TrustApprover the approver key behind it,
	// and TrustRevision how many times it has been set.
	Trusted       bool
	TrustedBy     string
	TrustApprover string
	TrustRevision int
	// Cancelling means a person interrupted the turn in progress and the
	// agent has not stopped yet.
	Cancelling bool
	// Rules settle tool calls before they reach a person: the repository's
	// rules as copied at the start, and any set on the session since.
	// RulesBy, RulesApprover, and RulesSignature are who stands behind the
	// list: the repository list's signer for the copy, and whoever set the
	// session's own. RulesRevision counts the times the session's rules
	// were set, and RulesLapsed means the approver behind them no longer
	// approves, so their allow rules ask.
	Rules          []v1.Rule
	RulesBy        string
	RulesApprover  string
	RulesSignature string
	RulesRevision  int
	RulesLapsed    bool
	// AgentSessionID is the agent's own id for the conversation, which a
	// restart asks it to load again.
	AgentSessionID string
	// Resumes counts the daemon restarts the session has lived through, and
	// Resumed says how its agent last came back.
	Resumes int
	Resumed Resumed
	// Pending holds the tool calls waiting on a person, oldest first, and
	// Questions the questions the agent is waiting on an answer to.
	Pending []Permission
	// Decided is every request answered or expired, oldest first.
	Decided   []Decided
	Questions []Question
	// Turns counts the prompts sent so far.
	Turns     int
	StartedAt time.Time
	EndedAt   time.Time
	// UpdatedAt is the time of the session's latest event.
	UpdatedAt time.Time
	ToolCalls []ToolCall
	Plan      []v1.PlanEntry
	// Used and Size are the latest reading of the context window.
	Used uint64
	Size uint64
	// Tokens is what the session has cost so far: the sum of every turn's
	// tokens and the title request's.
	Tokens v1.TokenUsage
}

// Resumed says how a session's agent came back after a daemon restart.
type Resumed struct {
	At          time.Time
	RestartedAt time.Time
	// How is v1.ResumedByLoad or v1.ResumedBySeed.
	How         string
	TurnResumed bool
}

// Question is a question the agent is waiting on a person to answer.
type Question struct {
	QuestionID string
	ToolCallID string
	Message    string
	Fields     []v1.QuestionField
	At         time.Time
}

// Permission is a tool call waiting on a person.
type Permission struct {
	RequestID  string
	ToolCallID string
	Title      string
	Kind       string
	Locations  []string
	// InputHash, InputSize, and InputPreview describe the input the agent
	// proposed, as PermissionRequested records it. The whole input is
	// kept while the request waits; RequestInput returns it.
	InputHash    string
	InputSize    int
	InputPreview string
	Options      []v1.PermissionOption
	At           time.Time
}

// Decided is a request that was answered or expired: what the agent asked,
// and how it was settled. The history lists these.
type Decided struct {
	Permission Permission
	// Answer is "allowed", "denied", or "expired".
	Answer   string
	OptionID string
	// By is "local", "citadel", "trust", or "rule", and empty when the
	// request expired.
	By string
	// Approver and Signature are the approver key and signature of a
	// decision through the citadel.
	Approver  string
	Signature string
	// Reason is why a request expired.
	Reason string
	At     time.Time
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
	// Blobs, when set, keeps each request's exact input and each finished
	// tool call's whole output under their SHA-256.
	Blobs    *blobs.Store
	Registry agents.Registry
	Repos    Repos
	// Publish, when set, sends each transcript append on the session's live
	// subject so a browser can read along.
	Publish func(subject string, data []byte) error
	// AllowKinds are the tool call kinds the host allows when no rule of
	// the session's applies. Nil means DefaultAllowKinds.
	AllowKinds []string
	// PermissionTimeout is how long a request waits for a person before it
	// expires. Zero means 30 minutes.
	PermissionTimeout time.Duration
	// World is the owner key of the world the host belongs to, or "" before
	// it is known. Once it is known, a decision through the citadel must be
	// signed by one of the world's approvers.
	World func() string
	// VerifyDecision checks an approver's signature over a decision that
	// came through the citadel, against the world's approvers.
	VerifyDecision func(ctx context.Context, d approval.Decision, s v1.DecisionSignature) error
	// Valid reports whether an approver approves for the world now. Trust
	// and allow rules stand on the approver who signed them only while it
	// does.
	Valid func(ctx context.Context, approver string) bool
}

// DefaultAllowKinds are the tool call kinds a host allows when no rule
// applies: what only looks at the worktree. Everything else waits for a
// person, unless a rule or the session's trust settles it.
var DefaultAllowKinds = []string{"read", "search", "think"}

var (
	ErrUnknownRepo     = errors.New("no such repository on this host")
	ErrUnknownAgent    = errors.New("no such agent on this host")
	ErrEmptyPrompt     = errors.New("a prompt is required")
	ErrUnknownSession  = errors.New("no such session on this host")
	ErrNotIdle         = errors.New("the session is not waiting for a prompt")
	ErrOver            = errors.New("the session has ended")
	ErrUnknownRequest  = errors.New("no such request is waiting")
	ErrUnknownQuestion = errors.New("no such question is waiting")
	ErrResuming        = errors.New("the agent is being brought back after a restart; try again in a moment")
	ErrNotRunning      = errors.New("no turn is in progress")
	ErrCheckoutBusy    = errors.New("another session is working in the checkout")
	ErrBusy            = errors.New("the agent is working; wait for the turn to end or stop the session")
	ErrOpen            = errors.New("the session is still open; stop it first")
	ErrInCheckout      = errors.New("the session works in the checkout itself, so it has no branch or worktree of its own")
	ErrNoWorktree      = errors.New("the worktree has been removed")
	ErrModeNotListed   = agents.ErrModeNotListed
	ErrNothingToCommit = errors.New("there is nothing to commit")
	ErrUncommitted     = errors.New("the worktree has uncommitted changes; commit them first")
	ErrNothingToMerge  = errors.New("the branch has no commits its base lacks")
)

// Manager is the aggregate for every session on one island.
type Manager struct {
	cfg   Config
	store *es.Store
	// ctx outlives any one command: agents run on it, and a cancelled ctx
	// stops every agent.
	ctx context.Context
	// started is when this manager loaded, which a resumed session records
	// as the daemon's restart time.
	started time.Time

	cmdMu    sync.Mutex
	mu       sync.Mutex
	sessions map[string]*Session
	handled  map[string]string
	// live holds the agent process of every session that has not ended.
	live map[string]*live
	// resuming marks the sessions whose agent is on its way back after a
	// restart and so has no live entry yet.
	resuming map[string]bool
	// transcripts holds each session's transcript writer once it is in use.
	transcripts map[string]*transcript
	// toolMu serializes the tool call records, which are read, changed,
	// and written whole.
	toolMu sync.Mutex
	runs   sync.WaitGroup
}

// live is a session's running agent and the turn it may be on.
type live struct {
	run *agents.Run
	// turnDone is closed when the current turn's goroutine has finished,
	// so Stop can record the end after it.
	turnDone chan struct{}
	stopping bool
	// asks holds, by request id, where each waiting permission request
	// hears its decision, and answers the same for questions.
	asks    map[string]chan agents.Decision
	answers map[string]chan agents.Answer
	// inputs holds, by request id, the whole input of each waiting
	// permission request, which its event names only by hash.
	inputs map[string][]byte
}

// Load rebuilds the aggregate from the host's stream. A session that was
// still open when the daemon last stopped lost its agent process with it,
// so whatever it was waiting on a person for expires, and the agent is
// brought back in the background: see resume. Until it is, the session
// keeps the status it had, and SessionResumed or AgentFailed says how
// that went.
func Load(ctx context.Context, store *es.Store, cfg Config) (*Manager, error) {
	if cfg.Registry == nil {
		cfg.Registry = agents.DefaultRegistry()
	}
	m := &Manager{cfg: cfg, store: store, ctx: ctx, started: time.Now().UTC(), sessions: map[string]*Session{}, handled: map[string]string{}, live: map[string]*live{}, resuming: map[string]bool{}, transcripts: map[string]*transcript{}}
	err := store.Replay(ctx, v1.IslandStreamName(cfg.Host), []string{v1.SessionEventFilter(cfg.Host)}, func(seq uint64, ev v1.Event) error {
		m.apply(ev)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("load sessions for %s: %w", cfg.Host, err)
	}
	for _, s := range m.List() {
		if s.Status != StatusRunning && s.Status != StatusIdle {
			continue
		}
		for _, p := range s.Pending {
			if err := m.record(ctx, s.ID, "", v1.EventPermissionExpired, v1.PermissionExpired{SessionID: s.ID, RequestID: p.RequestID, Reason: "restart"}); err != nil {
				return nil, err
			}
		}
		for _, q := range s.Questions {
			if err := m.record(ctx, s.ID, "", v1.EventQuestionExpired, v1.QuestionExpired{SessionID: s.ID, QuestionID: q.QuestionID, Reason: "restart"}); err != nil {
				return nil, err
			}
		}
		m.mu.Lock()
		m.resuming[s.ID] = true
		m.mu.Unlock()
		m.runs.Add(1)
		go m.resume(s.ID)
	}
	return m, nil
}

// continuation is the prompt a resumed agent gets for a turn the restart
// cut short.
const continuation = "The daemon that runs this session restarted while you were working on the last request, so that turn was cut short. " +
	"Pick up where you left off and finish the request. Anything you had asked permission for and did not get an answer to, ask again."

// resume brings a session's agent back after a daemon restart: through the
// agent's own session when it can load one, otherwise through a new
// session seeded with the transcript. A turn the restart cut short is run
// again, so anything that was waiting on a person is asked again. Only an
// agent that cannot be brought back fails the session.
func (m *Manager) resume(id string) {
	defer m.runs.Done()
	defer func() {
		m.mu.Lock()
		delete(m.resuming, id)
		m.mu.Unlock()
	}()
	s, ok := m.Get(id)
	if !ok {
		return
	}
	adapter, ok := m.cfg.Registry[s.Agent]
	if !ok {
		m.failed(id, fmt.Errorf("%w: %s", ErrUnknownAgent, s.Agent))
		return
	}
	if _, err := os.Stat(s.Worktree); err != nil {
		m.failed(id, fmt.Errorf("the worktree is gone: %s", s.Worktree))
		return
	}
	// The transcript is read before the note below is added to it, so a
	// seeded agent sees the conversation as it was.
	transcript, _ := os.ReadFile(m.transcriptPath(id))
	obs := &observer{m: m, id: id, t: m.transcript(id)}
	// The session comes back on its latest mode and model. A mode the
	// adapter no longer lists gives way to the first it does.
	settings := agents.Settings{Mode: s.Mode, Model: s.Model}
	if !slices.Contains(adapter.Modes, settings.Mode) {
		settings.Mode = ""
	}
	run, how, err := agents.Resume(m.ctx, adapter, s.Worktree, settings, s.AgentSessionID, string(transcript), obs)
	if err != nil {
		if m.ctx.Err() == nil {
			m.failed(id, err)
		}
		return
	}

	// Installing the agent is a command of sorts: it must not interleave
	// with a Stop, which would otherwise miss the agent it should close.
	m.cmdMu.Lock()
	m.mu.Lock()
	cur := m.sessions[id]
	if cur.Status == StatusEnded || cur.Status == StatusFailed {
		m.mu.Unlock()
		m.cmdMu.Unlock()
		_ = run.Close()
		return
	}
	midTurn := cur.Status == StatusRunning
	l := &live{run: run, asks: map[string]chan agents.Decision{}, answers: map[string]chan agents.Answer{}, inputs: map[string][]byte{}}
	if midTurn {
		l.turnDone = make(chan struct{})
	}
	m.live[id] = l
	m.mu.Unlock()
	m.cmdMu.Unlock()

	note := "The daemon restarted at " + m.started.Local().Format("2006-01-02 15:04") + "; "
	switch {
	case how == v1.ResumedByLoad && midTurn:
		note += "the agent is back with its own memory of the conversation and continues the interrupted turn."
	case how == v1.ResumedByLoad:
		note += "the agent is back with its own memory of the conversation."
	case midTurn:
		note += "a new agent was given the conversation so far and continues the interrupted turn."
	default:
		note += "a new agent will be given the conversation so far."
	}
	m.transcript(id).Append("\n\n---\n\n*" + note + "*\n\n")
	_ = m.record(m.ctx, id, "", v1.EventSessionResumed, v1.SessionResumed{SessionID: id, RestartedAt: m.started, How: how, TurnResumed: midTurn})
	slog.Info("session: resumed", "session", id, "how", how, "turn", midTurn)
	if s.Title == "" {
		m.runs.Add(1)
		go m.title(id, run, s.Prompt)
	}
	if midTurn {
		m.transcript(id).Append("## Agent\n\n")
		m.runs.Add(1)
		m.turn(id, l, continuation)
	}
}

// Approve handles ApproveToolCall: it answers a waiting request with an
// allow option and records who did. By names the path the answer came
// over, "local" or "citadel".
//
// An approval through the citadel must name the hash of the input the
// person approved, and it is refused unless that is the hash of the input
// the agent proposed, so an input changed on the way to the person isn't
// approved. Approvals on the host, and through a session's trust, need
// none.
//
// Once the world exists, an approval or a denial through the citadel must
// also carry an approver's signature over the decision, which is checked
// against the daemon's own record of the request.
func (m *Manager) Approve(ctx context.Context, cmd v1.ApproveToolCall, by string) (Session, error) {
	return m.decide(ctx, cmd.ID, cmd.SessionID, cmd.RequestID, by, cmd.OptionID, cmd.InputHash, cmd.Signature, true)
}

// checkSigned checks a decision on a tool call through the citadel, once
// the world exists: an approver must have signed it, and the signature
// must be over the decision as the daemon itself records it, with the
// host, session, request, option, and input hash it knows. It returns the
// approver and signature to record, or none for a decision that needs no
// signature.
func (m *Manager) checkSigned(ctx context.Context, sessionID, requestID, by, optionID, option string, sig *v1.DecisionSignature, pending *Permission, allow bool) (string, string, error) {
	if allow && by == "citadel" && m.world() != "" && (optionID == "" || option != optionID) {
		// The option signed is the option chosen, which must be one of the
		// agent's "once" options.
		return "", "", fmt.Errorf("%w: a signed approval must name the agent's \"allow once\" option", ErrUnsigned)
	}
	return m.signed(ctx, by, sig, "request "+requestID, func(world string) approval.Decision {
		d := approval.ToolCall{V: approval.DecisionVersion, Kind: approval.KindToolCall, World: world, Host: m.cfg.Host, Session: sessionID, Request: requestID, Decision: "deny", Input: pending.InputHash}
		if allow {
			d.Decision, d.Option = "allow", optionID
		}
		return d
	})
}

// world is the owner key of the world the host belongs to, or "" before
// it is known.
func (m *Manager) world() string {
	if m.cfg.World == nil {
		return ""
	}
	return m.cfg.World()
}

// signed checks a decision through the citadel once the world exists: an
// approver must have signed the decision build makes from the daemon's own
// record, and must approve for the world. It returns the approver and
// signature to record, or none for a decision that needs no signature:
// one on the host, or one before the world exists.
func (m *Manager) signed(ctx context.Context, by string, sig *v1.DecisionSignature, what string, build func(world string) approval.Decision) (string, string, error) {
	world := m.world()
	if by != "citadel" || world == "" {
		return "", "", nil
	}
	if sig == nil {
		return "", "", ErrUnsigned
	}
	if m.cfg.VerifyDecision == nil {
		return "", "", errors.New("this daemon can't check signatures")
	}
	if err := m.cfg.VerifyDecision(ctx, build(world), *sig); err != nil {
		slog.Warn("session: a decision's signature was refused", "what", what, "approver", sig.Approver, "err", err)
		return "", "", err
	}
	return sig.Approver, sig.Sig, nil
}

// stands reports whether trust or a rule list still stands on who set it:
// the host always does, nothing needs an approver before the world exists,
// and otherwise the approver who signed it must still approve.
func (m *Manager) stands(ctx context.Context, by, approver string, lapsed bool) bool {
	switch {
	case lapsed:
		return false
	case by == "local", m.world() == "":
		return true
	case approver == "" || m.cfg.Valid == nil:
		return false
	}
	return m.cfg.Valid(ctx, approver)
}

// ErrStale is a change to trust or rules made against an older revision
// than the daemon's: something else changed them since.
var ErrStale = approval.ErrStale

// RequestInput is the whole input of a request that is waiting.
func (m *Manager) RequestInput(sessionID, requestID string) (v1.RequestInput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.live[sessionID]
	if l == nil {
		return v1.RequestInput{}, fmt.Errorf("%w: %s", ErrUnknownRequest, requestID)
	}
	input, ok := l.inputs[requestID]
	if !ok {
		return v1.RequestInput{}, fmt.Errorf("%w: %s", ErrUnknownRequest, requestID)
	}
	hash, size, _ := v1.DescribeInput(input)
	return v1.RequestInput{RequestID: requestID, Input: string(input), Hash: hash, Size: size}, nil
}

// permissionRequested is the event for a tool call that waits on a person,
// or that a rule refused, naming its input by hash, size, and preview.
func permissionRequested(sessionID, requestID string, tc agents.ToolCall, req agents.PermissionRequest) v1.PermissionRequested {
	hash, size, preview := v1.DescribeInput(req.Input)
	return v1.PermissionRequested{SessionID: sessionID, RequestID: requestID, ToolCallID: tc.ID, Title: tc.Title, Kind: tc.Kind, Locations: tc.Locations, InputHash: hash, InputSize: size, InputPreview: preview, Options: req.Options}
}

// Deny handles DenyToolCall: it answers a waiting request with a reject
// option and records who did.
func (m *Manager) Deny(ctx context.Context, cmd v1.DenyToolCall, by string) (Session, error) {
	return m.decide(ctx, cmd.ID, cmd.SessionID, cmd.RequestID, by, "", "", cmd.Signature, false)
}

// ErrUnsigned is a decision through the citadel that no approver signed.
var ErrUnsigned = approval.ErrUnsigned

// ErrInputMismatch is an approval through the citadel that names no input,
// or another input than the one the agent proposed.
var ErrInputMismatch = errors.New("the input this approval names isn't the input the agent asked about")

func (m *Manager) decide(ctx context.Context, commandID, sessionID, requestID, by, optionID, inputHash string, sig *v1.DecisionSignature, allow bool) (Session, error) {
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
	if allow && by == "citadel" {
		switch {
		case inputHash == "":
			return *s, fmt.Errorf("%w: an approval through the citadel must name the input it approves", ErrInputMismatch)
		case inputHash != pending.InputHash:
			slog.Warn("session: an approval named another input", "session", sessionID, "request", requestID)
			return *s, ErrInputMismatch
		}
	}
	option := pickOption(pending.Options, allow, optionID)
	approver, signature, err := m.checkSigned(ctx, sessionID, requestID, by, optionID, option, sig, pending, allow)
	if err != nil {
		return *s, err
	}
	if option == "" {
		if allow {
			if optionID != "" {
				return *s, errors.New("an approval must choose the agent's \"allow once\" option")
			}
			return *s, errors.New("the agent offers no \"allow once\" option for this request")
		}
		// No reject option: cancelling the request is the refusal.
		if err := m.record(ctx, sessionID, commandID, v1.EventPermissionDenied, v1.PermissionDenied{SessionID: sessionID, RequestID: requestID, By: by, Approver: approver, Signature: signature}); err != nil {
			return Session{}, err
		}
		m.transcript(sessionID).Line(ToolLine(pending.Title+" (denied)", pending.ToolCallID))
		ask <- agents.Decision{Cancelled: true}
		out, _ := m.Get(sessionID)
		return out, nil
	}
	if allow {
		if err := m.record(ctx, sessionID, commandID, v1.EventPermissionGranted, v1.PermissionGranted{SessionID: sessionID, RequestID: requestID, OptionID: option, By: by, Approver: approver, Signature: signature}); err != nil {
			return Session{}, err
		}
	} else {
		if err := m.record(ctx, sessionID, commandID, v1.EventPermissionDenied, v1.PermissionDenied{SessionID: sessionID, RequestID: requestID, OptionID: option, By: by, Approver: approver, Signature: signature}); err != nil {
			return Session{}, err
		}
		m.transcript(sessionID).Line(ToolLine(pending.Title+" (denied)", pending.ToolCallID))
	}
	slog.Info("session: request decided", "session", sessionID, "request", requestID, "allowed", allow, "by", by)
	ask <- agents.Decision{OptionID: option}
	out, _ := m.Get(sessionID)
	return out, nil
}

// Answer handles AnswerQuestion: it gives a waiting question its answers,
// or the person's refusal, and records who did.
func (m *Manager) Answer(ctx context.Context, cmd v1.AnswerQuestion, by string) (Session, error) {
	m.cmdMu.Lock()
	defer m.cmdMu.Unlock()
	m.mu.Lock()
	if id, ok := m.handled[cmd.ID]; ok && cmd.ID != "" {
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
	var waiting *Question
	for i := range s.Questions {
		if s.Questions[i].QuestionID == cmd.QuestionID {
			waiting = &s.Questions[i]
		}
	}
	var answer chan agents.Answer
	if l != nil {
		answer = l.answers[cmd.QuestionID]
	}
	m.mu.Unlock()
	if waiting == nil || answer == nil {
		return *s, fmt.Errorf("%w: %s", ErrUnknownQuestion, cmd.QuestionID)
	}
	var answers json.RawMessage
	if !cmd.Decline {
		var err error
		if answers, err = CheckAnswers(waiting.Fields, cmd.Answers); err != nil {
			return *s, err
		}
	}
	approver, signature, err := m.signed(ctx, by, cmd.Signature, "question "+cmd.QuestionID, func(world string) approval.Decision {
		d := approval.Answer{V: approval.DecisionVersion, Kind: approval.KindAnswer, World: world, Host: m.cfg.Host, Session: cmd.SessionID, Question: cmd.QuestionID, Declined: cmd.Decline}
		d.Form, _ = approval.FormHash(waiting.Message, waiting.Fields)
		if !cmd.Decline {
			d.Answers, _ = approval.AnswersHash(answers)
		}
		return d
	})
	if err != nil {
		return *s, err
	}
	if err := m.record(ctx, cmd.SessionID, cmd.ID, v1.EventQuestionAnswered, v1.QuestionAnswered{SessionID: cmd.SessionID, QuestionID: cmd.QuestionID, Answers: answers, Declined: cmd.Decline, By: by, Approver: approver, Signature: signature}); err != nil {
		return Session{}, err
	}
	if cmd.Decline {
		m.transcript(cmd.SessionID).Line(QuestionLinePrefix + waiting.Message + " (declined)")
	} else {
		m.transcript(cmd.SessionID).Line(QuestionLinePrefix + waiting.Message + " " + answerText(waiting.Fields, answers))
	}
	slog.Info("session: question answered", "session", cmd.SessionID, "question", cmd.QuestionID, "declined", cmd.Decline, "by", by)
	answer <- agents.Answer{Answers: answers, Declined: cmd.Decline}
	out, _ := m.Get(cmd.SessionID)
	return out, nil
}

// QuestionLinePrefix opens a transcript line that records a question and
// its answer.
const QuestionLinePrefix = "\u2753 "

// answerText renders answers for the transcript: each answered field's
// title and value, in the form's order.
func answerText(fields []v1.QuestionField, answers json.RawMessage) string {
	var values map[string]any
	if json.Unmarshal(answers, &values) != nil {
		return ""
	}
	var parts []string
	for _, f := range fields {
		v, ok := values[f.Key]
		if !ok || v == nil || v == "" {
			continue
		}
		text := fmt.Sprint(v)
		if list, ok := v.([]any); ok {
			var items []string
			for _, it := range list {
				items = append(items, fmt.Sprint(it))
			}
			text = strings.Join(items, ", ")
		}
		if f.Title != "" {
			text = f.Title + ": " + text
		}
		parts = append(parts, text)
	}
	return "\u2192 " + strings.Join(parts, "; ")
}

// Questions lists every question waiting on a person, across sessions.
func (m *Manager) Questions() []WaitingQuestion {
	var out []WaitingQuestion
	for _, s := range m.List() {
		for _, q := range s.Questions {
			out = append(out, WaitingQuestion{Session: s, Question: q})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Question.At.Before(out[j].Question.At) })
	return out
}

// WaitingQuestion is a waiting question with its session.
type WaitingQuestion struct {
	Session  Session
	Question Question
}

// FindQuestion finds the session a waiting question belongs to.
func (m *Manager) FindQuestion(questionID string) (Session, bool) {
	for _, s := range m.List() {
		for _, q := range s.Questions {
			if q.QuestionID == questionID {
				return s, true
			}
		}
	}
	return Session{}, false
}

// pickOption chooses the option to answer with: the one asked for, else
// the agent's first "allow once" or "reject once" option. An "always"
// option is never chosen: it would settle later calls inside the agent,
// where neither a deny rule nor a revocation reaches them, and the daemon
// would never see them.
func pickOption(options []v1.PermissionOption, allow bool, wanted string) string {
	kind := "allow_once"
	if !allow {
		kind = "reject_once"
	}
	for _, o := range options {
		if o.Kind == kind && (wanted == "" || o.ID == wanted) {
			return o.ID
		}
	}
	return ""
}

// Trust handles TrustSession: from now on the session's tool calls are
// allowed without asking, unless a deny rule denies them, or asked about
// again. Trusting a session answers whatever is waiting. By is "citadel"
// or "local"; a change through the citadel names the revision of the
// session's trust it replaces, and once the world exists it must be
// signed.
func (m *Manager) Trust(ctx context.Context, cmd v1.TrustSession, by string) (Session, error) {
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
	revision := s.TrustRevision
	m.mu.Unlock()
	if by == "citadel" && cmd.Revision != revision {
		return *s, fmt.Errorf("%w: the session's trust is at revision %d, not %d", ErrStale, revision, cmd.Revision)
	}
	approver, signature, err := m.signed(ctx, by, cmd.Signature, "trust in "+cmd.SessionID, func(world string) approval.Decision {
		return approval.Trust{V: approval.DecisionVersion, Kind: approval.KindTrust, World: world, Host: m.cfg.Host, Session: cmd.SessionID, Trusted: cmd.Trusted, Revision: revision}
	})
	if err != nil {
		return *s, err
	}
	if err := m.record(ctx, cmd.SessionID, cmd.ID, v1.EventSessionTrusted, v1.SessionTrusted{SessionID: cmd.SessionID, Trusted: cmd.Trusted, By: by, Approver: approver, Signature: signature, Revision: revision}); err != nil {
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

// SetRules handles SetSessionRules: the session's rules become the given
// list. Rules copied from the repository keep their source when sent
// back unchanged; an edited one is the session's own. Whoever sets the
// list stands behind all of it, copied rules included. By and the
// revision and signature are as in Trust.
func (m *Manager) SetRules(ctx context.Context, cmd v1.SetSessionRules, by string) (Session, error) {
	m.cmdMu.Lock()
	defer m.cmdMu.Unlock()
	m.mu.Lock()
	if id, ok := m.handled[cmd.ID]; ok && cmd.ID != "" {
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
	revision := s.RulesRevision
	m.mu.Unlock()
	list, err := rules.Validate(cmd.Rules)
	if err != nil {
		return *s, err
	}
	if by == "citadel" && cmd.Revision != revision {
		return *s, fmt.Errorf("%w: the session's rules are at revision %d, not %d", ErrStale, revision, cmd.Revision)
	}
	approver, signature, err := m.signed(ctx, by, cmd.Signature, "rules of "+cmd.SessionID, func(world string) approval.Decision {
		hash, _ := approval.RulesHash(list)
		return approval.SessionRules{V: approval.DecisionVersion, Kind: approval.KindSessionRules, World: world, Host: m.cfg.Host, Session: cmd.SessionID, Rules: hash, Revision: revision}
	})
	if err != nil {
		return *s, err
	}
	if err := m.record(ctx, cmd.SessionID, cmd.ID, v1.EventSessionRulesSet, v1.SessionRulesSet{SessionID: cmd.SessionID, Rules: list, By: by, Approver: approver, Signature: signature, Revision: revision}); err != nil {
		return Session{}, err
	}
	slog.Info("session: rules set", "session", cmd.SessionID, "rules", len(list))
	out, _ := m.Get(cmd.SessionID)
	return out, nil
}

// Lapse ends what approvers approved in advance once they no longer
// approve: an open session's trust that one of them turned on is turned
// off, and a rule list one of them stands behind lapses, so its allow
// rules ask until an approver sets the list again. valid reports whether
// an approver approves now. Trust and rules set on the host never lapse.
func (m *Manager) Lapse(ctx context.Context, valid func(approver string) bool) {
	if m.world() == "" {
		return
	}
	m.cmdMu.Lock()
	defer m.cmdMu.Unlock()
	for _, s := range m.List() {
		if s.Status == StatusEnded || s.Status == StatusFailed {
			continue
		}
		if s.Trusted && s.TrustedBy != "local" && !valid(s.TrustApprover) {
			if err := m.record(ctx, s.ID, "", v1.EventSessionTrusted, v1.SessionTrusted{SessionID: s.ID, By: "revocation", Approver: s.TrustApprover, Revision: s.TrustRevision}); err != nil {
				slog.Warn("session: end the trust of a revoked approver", "session", s.ID, "err", err)
			} else {
				slog.Info("session: trust ended, its approver no longer approves", "session", s.ID, "approver", s.TrustApprover)
			}
		}
		if !s.RulesLapsed && s.RulesBy != "local" && rules.HasAllow(s.Rules) && !valid(s.RulesApprover) {
			if err := m.record(ctx, s.ID, "", v1.EventSessionRulesLapsed, v1.SessionRulesLapsed{SessionID: s.ID, Approver: s.RulesApprover}); err != nil {
				slog.Warn("session: lapse the rules of a revoked approver", "session", s.ID, "err", err)
			} else {
				slog.Info("session: rules lapsed, their approver no longer approves", "session", s.ID, "approver", s.RulesApprover)
			}
		}
	}
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

// Wait blocks until every running agent has stopped, closing the agents
// of open sessions so that each ends with whatever it started, rather than
// leaving that to the daemon's exit. Cancel the manager's context first:
// a session whose agent stops because the daemon is stopping stays open,
// and is resumed when the daemon starts again.
func (m *Manager) Wait() {
	m.runs.Wait()
	m.mu.Lock()
	runs := make([]*agents.Run, 0, len(m.live))
	for _, l := range m.live {
		runs = append(runs, l.run)
	}
	m.mu.Unlock()
	var wg sync.WaitGroup
	for _, r := range runs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = r.Close()
		}()
	}
	wg.Wait()
}

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
	case s.Status == StatusEnded || s.Status == StatusFailed:
		m.mu.Unlock()
		return *s, ErrOver
	case l == nil && m.resuming[cmd.SessionID]:
		m.mu.Unlock()
		return *s, ErrResuming
	case l == nil:
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
// and records SessionEnded. An agent still on its way back after a restart
// is closed by resume when it arrives and finds the session over.
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
	questions := append([]Question(nil), s.Questions...)
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
		for _, q := range questions {
			_ = m.record(ctx, cmd.SessionID, "", v1.EventQuestionExpired, v1.QuestionExpired{SessionID: cmd.SessionID, QuestionID: q.QuestionID, Reason: "stopped"})
			m.mu.Lock()
			answer := l.answers[q.QuestionID]
			m.mu.Unlock()
			if answer != nil {
				answer <- agents.Answer{Cancelled: true}
			}
		}
		// The turn is given a moment to end before the link closes: a
		// closing link refuses every write, including the answer the agent
		// is still waiting for, and then the turn could never end.
		l.run.Cancel(ctx)
		if l.turnDone != nil {
			select {
			case <-l.turnDone:
			case <-time.After(10 * time.Second):
			}
		}
		_ = l.run.Close()
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

// Cancel handles CancelTurn: it interrupts the turn in progress and leaves
// the session open. Whatever was waiting on a person expires with the
// turn, the agent is told to stop, and TurnEnded follows when it has,
// with the stop reason "cancelled". The next prompt carries on.
func (m *Manager) Cancel(ctx context.Context, cmd v1.CancelTurn, by string) (Session, error) {
	m.cmdMu.Lock()
	defer m.cmdMu.Unlock()
	m.mu.Lock()
	if id, ok := m.handled[cmd.ID]; ok && cmd.ID != "" {
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
	case s.Status == StatusEnded || s.Status == StatusFailed:
		m.mu.Unlock()
		return *s, ErrOver
	case s.Status != StatusRunning:
		m.mu.Unlock()
		return *s, ErrNotRunning
	case l == nil && m.resuming[cmd.SessionID]:
		m.mu.Unlock()
		return *s, ErrResuming
	case l == nil:
		m.mu.Unlock()
		return *s, ErrOver
	}
	pending := append([]Permission(nil), s.Pending...)
	questions := append([]Question(nil), s.Questions...)
	m.mu.Unlock()
	if err := m.record(ctx, cmd.SessionID, cmd.ID, v1.EventTurnCancelled, v1.TurnCancelled{SessionID: cmd.SessionID, By: by}); err != nil {
		return Session{}, err
	}
	// The agent hears the cancel first, so by the time a withdrawn request
	// comes back to it, it knows the turn is over rather than refused.
	l.run.Cancel(ctx)
	for _, p := range pending {
		_ = m.record(ctx, cmd.SessionID, "", v1.EventPermissionExpired, v1.PermissionExpired{SessionID: cmd.SessionID, RequestID: p.RequestID, Reason: "cancelled"})
		m.mu.Lock()
		ask := l.asks[p.RequestID]
		m.mu.Unlock()
		if ask != nil {
			ask <- agents.Decision{Cancelled: true}
		}
	}
	for _, q := range questions {
		_ = m.record(ctx, cmd.SessionID, "", v1.EventQuestionExpired, v1.QuestionExpired{SessionID: cmd.SessionID, QuestionID: q.QuestionID, Reason: "cancelled"})
		m.mu.Lock()
		answer := l.answers[q.QuestionID]
		m.mu.Unlock()
		if answer != nil {
			answer <- agents.Answer{Cancelled: true}
		}
	}
	slog.Info("session: turn cancelled", "session", cmd.SessionID, "by", by)
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
	mode := cmd.Mode
	switch {
	case mode == "" && len(adapter.Modes) > 0:
		mode = adapter.Modes[0]
	case mode != "" && !slices.Contains(adapter.Modes, mode):
		return Session{}, fmt.Errorf("%w: %s", ErrModeNotListed, mode)
	}
	id := cmd.SessionID
	if id == "" {
		id = nuid.Next()
	}
	base, err := currentBranch(ctx, repo.Path)
	if err != nil {
		return Session{}, err
	}
	worktree, branch := filepath.Join(m.cfg.WorktreesDir, id), "skylands/"+id
	if cmd.InCheckout {
		// The checkout is shared, so only one agent at a time works in it.
		for _, other := range m.List() {
			if other.RepoID == repo.ID && other.InCheckout && (other.Status == StatusRunning || other.Status == StatusIdle) {
				return Session{}, fmt.Errorf("%w: %s", ErrCheckoutBusy, other.ID)
			}
		}
		worktree, branch = repo.Path, base
	} else if err := addWorktree(ctx, repo.Path, worktree, branch); err != nil {
		return Session{}, err
	}

	// The copy of the repository's rules stands on the signature of the
	// list it copies.
	started := v1.SessionStarted{SessionID: id, RepoID: repo.ID, Agent: cmd.Agent, Model: cmd.Model, Mode: mode, Worktree: worktree, Branch: branch, Base: base, InCheckout: cmd.InCheckout, Rules: rules.Inherit(repo.Rules),
		RulesBy: repo.RulesBy, RulesApprover: repo.RulesApprover, RulesSignature: repo.RulesSignature, RulesRevision: max(repo.RulesRevision-1, 0), RulesLapsed: repo.RulesLapsed}
	if err := m.record(ctx, id, cmd.ID, v1.EventSessionStarted, started); err != nil {
		return Session{}, err
	}
	if err := m.record(ctx, id, cmd.ID, v1.EventPromptSent, v1.PromptSent{SessionID: id, Text: cmd.Prompt}); err != nil {
		return Session{}, err
	}
	slog.Info("session: started", "session", id, "repo", repo.Name, "agent", cmd.Agent, "model", cmd.Model, "mode", mode, "worktree", worktree, "branch", branch, "base", base)
	m.transcript(id).Append("## You\n\n" + strings.TrimSpace(cmd.Prompt) + "\n\n## Agent\n\n")

	m.runs.Add(1)
	go m.begin(id, adapter, worktree, cmd.Prompt, agents.Settings{Mode: mode, Model: cmd.Model})
	s, _ := m.Get(id)
	return s, nil
}

// begin launches a session's agent and runs its first turn.
func (m *Manager) begin(id string, adapter agents.Adapter, worktree, prompt string, settings agents.Settings) {
	defer m.runs.Done()
	obs := &observer{m: m, id: id, t: m.transcript(id)}
	run, err := agents.Start(m.ctx, adapter, worktree, settings, obs)
	if err != nil {
		if m.ctx.Err() == nil {
			m.failed(id, err)
		}
		return
	}
	l := &live{run: run, turnDone: make(chan struct{}), asks: map[string]chan agents.Decision{}, answers: map[string]chan agents.Answer{}, inputs: map[string][]byte{}}
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
	if stop == "cancelled" {
		m.transcript(id).Append("\n\n---\n\n*Interrupted. The agent stopped before finishing the turn; the next reply steers it.*\n\n")
	} else {
		m.transcript(id).Append("\n\n")
	}
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
		m.sessions[d.SessionID] = &Session{ID: d.SessionID, RepoID: d.RepoID, Agent: d.Agent, Model: d.Model, Mode: d.Mode, Worktree: d.Worktree, Branch: d.Branch, Base: d.Base, InCheckout: d.InCheckout, Rules: d.Rules, RulesBy: d.RulesBy, RulesApprover: d.RulesApprover, RulesSignature: d.RulesSignature, RulesLapsed: d.RulesLapsed, Status: StatusRunning, StartedAt: ev.Time, UpdatedAt: ev.Time}
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
				s.Cancelling = false
				if ev.CommandID != "" {
					m.handled[ev.CommandID] = d.SessionID
				}
			}
		}
	case v1.EventTurnEnded:
		var d v1.TurnEnded
		if ev.Decode(&d) == nil {
			if s := touch(d.SessionID); s != nil && s.Status == StatusRunning {
				s.Status, s.StopReason, s.Cancelling = StatusIdle, d.StopReason, false
			}
		}
	case v1.EventTurnCancelled:
		var d v1.TurnCancelled
		if ev.Decode(&d) == nil {
			if s := touch(d.SessionID); s != nil {
				if s.Status == StatusRunning {
					s.Cancelling = true
				}
				if ev.CommandID != "" {
					m.handled[ev.CommandID] = d.SessionID
				}
			}
		}
	case v1.EventSessionTitled:
		var d v1.SessionTitled
		if ev.Decode(&d) == nil {
			if s := touch(d.SessionID); s != nil {
				s.Title = d.Title
			}
		}
	case v1.EventSessionModeChanged:
		var d v1.SessionModeChanged
		if ev.Decode(&d) == nil {
			if s := touch(d.SessionID); s != nil {
				s.Mode = d.Mode
				if ev.CommandID != "" {
					m.handled[ev.CommandID] = d.SessionID
				}
			}
		}
	case v1.EventSessionModelChanged:
		var d v1.SessionModelChanged
		if ev.Decode(&d) == nil {
			if s := touch(d.SessionID); s != nil {
				s.Model = d.Model
				if ev.CommandID != "" {
					m.handled[ev.CommandID] = d.SessionID
				}
			}
		}
	case v1.EventSessionTrusted:
		var d v1.SessionTrusted
		if ev.Decode(&d) == nil {
			if s := touch(d.SessionID); s != nil {
				s.Trusted, s.TrustedBy, s.TrustApprover = d.Trusted, d.By, d.Approver
				s.TrustRevision++
				if ev.CommandID != "" {
					m.handled[ev.CommandID] = d.SessionID
				}
			}
		}
	case v1.EventSessionRulesSet:
		var d v1.SessionRulesSet
		if ev.Decode(&d) == nil {
			if s := touch(d.SessionID); s != nil {
				s.Rules, s.RulesBy, s.RulesApprover, s.RulesSignature, s.RulesLapsed = d.Rules, d.By, d.Approver, d.Signature, false
				s.RulesRevision++
				if ev.CommandID != "" {
					m.handled[ev.CommandID] = d.SessionID
				}
			}
		}
	case v1.EventSessionRulesLapsed:
		var d v1.SessionRulesLapsed
		if ev.Decode(&d) == nil {
			if s := touch(d.SessionID); s != nil {
				s.RulesLapsed = true
			}
		}
	case v1.EventPermissionRequested:
		var d v1.PermissionRequested
		if ev.Decode(&d) == nil {
			if s := touch(d.SessionID); s != nil {
				s.Pending = append(s.Pending, Permission{RequestID: d.RequestID, ToolCallID: d.ToolCallID, Title: d.Title, Kind: d.Kind, Locations: d.Locations, InputHash: d.InputHash, InputSize: d.InputSize, InputPreview: d.InputPreview, Options: d.Options, At: ev.Time})
			}
		}
	case v1.EventPermissionGranted, v1.EventPermissionDenied, v1.EventPermissionExpired:
		var d struct {
			SessionID string `json:"sessionId"`
			RequestID string `json:"requestId"`
			OptionID  string `json:"optionId"`
			By        string `json:"by"`
			Approver  string `json:"approver"`
			Signature string `json:"signature"`
			Reason    string `json:"reason"`
		}
		if ev.Decode(&d) == nil {
			if s := touch(d.SessionID); s != nil {
				kept := s.Pending[:0]
				for _, p := range s.Pending {
					if p.RequestID != d.RequestID {
						kept = append(kept, p)
						continue
					}
					answer := map[string]string{v1.EventPermissionGranted: "allowed", v1.EventPermissionDenied: "denied", v1.EventPermissionExpired: "expired"}[ev.Type]
					s.Decided = append(s.Decided, Decided{Permission: p, Answer: answer, OptionID: d.OptionID, By: d.By, Approver: d.Approver, Signature: d.Signature, Reason: d.Reason, At: ev.Time})
				}
				s.Pending = kept
				if ev.CommandID != "" {
					m.handled[ev.CommandID] = d.SessionID
				}
			}
		}
	case v1.EventQuestionAsked:
		var d v1.QuestionAsked
		if ev.Decode(&d) == nil {
			if s := touch(d.SessionID); s != nil {
				s.Questions = append(s.Questions, Question{QuestionID: d.QuestionID, ToolCallID: d.ToolCallID, Message: d.Message, Fields: d.Fields, At: ev.Time})
			}
		}
	case v1.EventQuestionAnswered, v1.EventQuestionExpired:
		var d struct {
			SessionID  string `json:"sessionId"`
			QuestionID string `json:"questionId"`
		}
		if ev.Decode(&d) == nil {
			if s := touch(d.SessionID); s != nil {
				kept := s.Questions[:0]
				for _, q := range s.Questions {
					if q.QuestionID != d.QuestionID {
						kept = append(kept, q)
					}
				}
				s.Questions = kept
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
				if d.AgentSessionID != "" {
					s.AgentSessionID = d.AgentSessionID
				}
			}
		}
	case v1.EventSessionResumed:
		var d v1.SessionResumed
		if ev.Decode(&d) == nil {
			if s := touch(d.SessionID); s != nil {
				s.Resumes++
				s.Resumed = Resumed{At: ev.Time, RestartedAt: d.RestartedAt, How: d.How, TurnResumed: d.TurnResumed}
			}
		}
	case v1.EventChangesCommitted:
		var d v1.ChangesCommitted
		if ev.Decode(&d) == nil {
			if s := touch(d.SessionID); s != nil {
				s.Commit = d.Commit
				s.Commits++
				if ev.CommandID != "" {
					m.handled[ev.CommandID] = d.SessionID
				}
			}
		}
	case v1.EventChangesMerged:
		var d v1.ChangesMerged
		if ev.Decode(&d) == nil {
			if s := touch(d.SessionID); s != nil {
				s.MergedInto, s.MergedCommit = d.Into, d.Commit
				if ev.CommandID != "" {
					m.handled[ev.CommandID] = d.SessionID
				}
			}
		}
	case v1.EventPullRequestOpened:
		var d v1.PullRequestOpened
		if ev.Decode(&d) == nil {
			if s := touch(d.SessionID); s != nil {
				s.PullRequest = d.URL
				if ev.CommandID != "" {
					m.handled[ev.CommandID] = d.SessionID
				}
			}
		}
	case v1.EventWorktreeRemoved:
		var d v1.WorktreeRemoved
		if ev.Decode(&d) == nil {
			if s := touch(d.SessionID); s != nil {
				s.WorktreeRemoved = true
				if ev.CommandID != "" {
					m.handled[ev.CommandID] = d.SessionID
				}
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
						if d.Title != "" {
							s.ToolCalls[i].Title = d.Title
						}
						if len(d.Locations) > 0 {
							s.ToolCalls[i].Locations = d.Locations
						}
						s.ToolCalls[i].OutputHash, s.ToolCalls[i].OutputSize = d.OutputHash, d.OutputSize
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
	case v1.EventTokensUsed:
		var d v1.TokensUsed
		if ev.Decode(&d) == nil {
			if s := touch(d.SessionID); s != nil {
				s.Tokens.Add(d.Tokens)
			}
		}
	case v1.EventAgentFailed:
		var d v1.AgentFailed
		if ev.Decode(&d) == nil {
			if s := touch(d.SessionID); s != nil {
				s.Status, s.Error, s.EndedAt, s.Cancelling = StatusFailed, d.Error, ev.Time, false
			}
		}
	case v1.EventSessionEnded:
		var d v1.SessionEnded
		if ev.Decode(&d) == nil {
			if s := touch(d.SessionID); s != nil {
				s.Status, s.StopReason, s.EndedAt, s.Cancelling = StatusEnded, d.StopReason, ev.Time, false
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
	// outputs holds what each unfinished tool call has produced so far,
	// whole, as the agent last reported it.
	outputs map[string]*v1.ToolCallOutput
}

func (o *observer) Spawned(name, version, agentSessionID string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	_ = o.m.record(o.m.ctx, o.id, "", v1.EventAgentSpawned, v1.AgentSpawned{SessionID: o.id, AgentID: o.id, Name: name, Version: version, AgentSessionID: agentSessionID})
}

func (o *observer) ToolCall(tc agents.ToolCall) {
	o.mu.Lock()
	defer o.mu.Unlock()
	now := time.Now().UTC()
	o.m.recordTool(o.id, tc, now)
	o.produced(tc)
	s, _ := o.m.Get(o.id)
	var known *ToolCall
	for i := range s.ToolCalls {
		if s.ToolCalls[i].ID == tc.ID {
			known = &s.ToolCalls[i]
		}
	}
	if known == nil {
		// An agent that streams a call's input reports it first with a
		// placeholder title, such as "Terminal" for a command it has not
		// finished writing. The call starts, for the record and the
		// transcript, once the input is known, so the title is the real
		// one; a call that finishes without ever saying starts then.
		if !tc.InputKnown() && !tc.Finished() {
			return
		}
		_ = o.m.record(o.m.ctx, o.id, "", v1.EventToolCallStarted, v1.ToolCallStarted{SessionID: o.id, ToolCallID: tc.ID, Title: tc.Title, Kind: tc.Kind, Locations: tc.Locations})
		// The transcript notes each tool call on a line of its own, between
		// the agent's words, so a reader sees what it did where it did it.
		if tc.Status != "failed" {
			o.t.Line(ToolLine(tc.Title, tc.ID))
		}
	}
	if tc.Finished() && (known == nil || (known.Status != "completed" && known.Status != "failed")) {
		done := v1.ToolCallCompleted{SessionID: o.id, ToolCallID: tc.ID, Status: tc.Status, Locations: tc.Locations}
		if known != nil && known.Title != tc.Title {
			done.Title = tc.Title
		}
		done.OutputHash, done.OutputSize = o.keepOutput(tc.ID)
		_ = o.m.record(o.m.ctx, o.id, "", v1.EventToolCallCompleted, done)
		if tc.Status == "failed" {
			o.t.Line(ToolLine(tc.Title+" (failed)", tc.ID))
		}
	}
}

// produced notes what a report says the call produced so far: content
// replaces what an earlier report said, and a raw output replaces the
// last one. Unlike the call's record, nothing is cut.
func (o *observer) produced(tc agents.ToolCall) {
	if tc.ID == "" || (tc.Content == nil && tc.Output == "") {
		return
	}
	if o.outputs == nil {
		o.outputs = map[string]*v1.ToolCallOutput{}
	}
	out := o.outputs[tc.ID]
	if out == nil {
		out = &v1.ToolCallOutput{}
		o.outputs[tc.ID] = out
	}
	if tc.Content != nil {
		out.Content = append([]v1.ToolCallContent(nil), tc.Content...)
	}
	if tc.Output != "" {
		out.Output = tc.Output
	}
}

// keepOutput keeps what a finished call produced, whole, as the canonical
// JSON of a ToolCallOutput, and returns its hash and size, or nothing when
// the host keeps no blobs or couldn't keep this one.
func (o *observer) keepOutput(toolCallID string) (string, int) {
	out := o.outputs[toolCallID]
	delete(o.outputs, toolCallID)
	if o.m.cfg.Blobs == nil {
		return "", 0
	}
	if out == nil {
		out = &v1.ToolCallOutput{}
	}
	if out.Content == nil {
		out.Content = []v1.ToolCallContent{}
	}
	data, err := canonical.Marshal(out)
	if err != nil {
		slog.Warn("session: a tool call's output isn't JSON that can be kept", "session", o.id, "toolCall", toolCallID, "err", err)
		return "", 0
	}
	hash, err := o.m.cfg.Blobs.Put(data)
	if err != nil {
		slog.Warn("session: keep a tool call's output", "session", o.id, "toolCall", toolCallID, "err", err)
		return "", 0
	}
	return hash, len(data)
}

// keepInput keeps a request's exact input under its SHA-256, which
// PermissionRequested names and a signed decision covers.
func (m *Manager) keepInput(sessionID, requestID string, input []byte) {
	if m.cfg.Blobs == nil {
		return
	}
	if _, err := m.cfg.Blobs.Put(input); err != nil {
		slog.Warn("session: keep a request's input", "session", sessionID, "request", requestID, "err", err)
	}
}

// Blob returns what the host keeps under a hash: a request's input or a
// tool call's output.
func (m *Manager) Blob(hash string) ([]byte, error) {
	if m.cfg.Blobs == nil {
		return nil, blobs.ErrNotKept
	}
	return m.cfg.Blobs.Get(hash)
}

// started puts a call the agent asks permission for on the record and in
// the transcript before the request is, when its reports have not yet: a
// request is about a call, so the call has begun whatever the agent has
// said of it, and the request arrives alongside those reports rather than
// after them. The request also carries the whole input, which for some
// agents is the first time it is seen, so the call's record gets it.
func (o *observer) started(tc agents.ToolCall, input json.RawMessage) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.m.recordToolInput(o.id, tc, input, time.Now().UTC())
	s, _ := o.m.Get(o.id)
	for _, known := range s.ToolCalls {
		if known.ID == tc.ID {
			return
		}
	}
	_ = o.m.record(o.m.ctx, o.id, "", v1.EventToolCallStarted, v1.ToolCallStarted{SessionID: o.id, ToolCallID: tc.ID, Title: tc.Title, Kind: tc.Kind, Locations: tc.Locations})
	o.t.Line(ToolLine(tc.Title, tc.ID))
}

// ToolLinePrefix opens a transcript line that records a tool call.
const ToolLinePrefix = "\u2699 "

// MaxToolLine is the most of a tool call's title a transcript line shows.
// The record keeps the whole of it.
const MaxToolLine = 160

// ToolLine is the transcript line for a tool call: the prefix, the title
// on one line, and a marker naming the call, which a conversation's reader
// follows to the call's record and which Markdown does not show.
func ToolLine(title, toolCallID string) string {
	line := ToolLinePrefix + lineTitle(title)
	if toolCallID != "" && !strings.ContainsAny(toolCallID, " \t\n>") {
		line += " <!-- tool:" + toolCallID + " -->"
	}
	return line
}

// lineTitle fits a title on one transcript line: whitespace runs, including
// line breaks in a long command, become one space, and the rest is cut.
func lineTitle(title string) string {
	t := strings.Join(strings.Fields(title), " ")
	if utf8.RuneCountInString(t) <= MaxToolLine {
		return t
	}
	return string([]rune(t)[:MaxToolLine]) + "\u2026"
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

// Tokens records what a request cost, which the session's total sums.
func (o *observer) Tokens(purpose string, t v1.TokenUsage) {
	o.mu.Lock()
	defer o.mu.Unlock()
	_ = o.m.record(o.m.ctx, o.id, "", v1.EventTokensUsed, v1.TokensUsed{SessionID: o.id, For: purpose, Tokens: t})
}

// Message keeps agent text on the host, in the session's transcript.
func (o *observer) Message(text string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.t.Append(text)
}

// Permission settles a tool call by the session's trust or its rules,
// and otherwise records it as waiting and blocks until a person answers,
// the request expires, or the agent gives up. A rule's refusal is
// recorded too, so the conversation shows what the agent was not allowed
// to do.
func (o *observer) Permission(ctx context.Context, req agents.PermissionRequest) agents.Decision {
	m := o.m
	tc := req.ToolCall
	o.started(tc, req.Input)
	s, _ := m.Get(o.id)
	// A list stands on the approver who signed it: once that approver no
	// longer approves, its allow rules ask. A deny is final, so trust only
	// settles what the rules would ask about.
	list := s.Rules
	if rules.HasAllow(list) && !m.stands(m.ctx, s.RulesBy, s.RulesApprover, s.RulesLapsed) {
		list = rules.WithoutAllows(list)
	}
	decision := rules.Decide(append(append([]v1.Rule{}, list...), m.hostRules()...), rules.Call{Kind: tc.Kind, Title: tc.Title, Locations: tc.Locations, Input: req.Input})
	if decision == v1.RuleAsk && s.Trusted && m.stands(m.ctx, s.TrustedBy, s.TrustApprover, false) {
		decision = v1.RuleAllow
	}
	switch decision {
	case v1.RuleAllow:
		if opt := pickOption(req.Options, true, ""); opt != "" {
			return agents.Decision{OptionID: opt}
		}
	case v1.RuleDeny:
		requestID := nuid.Next()
		option := pickOption(req.Options, false, "")
		m.keepInput(o.id, requestID, req.Input)
		_ = m.record(m.ctx, o.id, "", v1.EventPermissionRequested, permissionRequested(o.id, requestID, tc, req))
		_ = m.record(m.ctx, o.id, "", v1.EventPermissionDenied, v1.PermissionDenied{SessionID: o.id, RequestID: requestID, OptionID: option, By: "rule"})
		o.t.Line(ToolLine(tc.Title+" (denied by a rule)", tc.ID))
		slog.Info("session: denied by a rule", "session", o.id, "tool", tc.Title)
		if option != "" {
			return agents.Decision{OptionID: option}
		}
		return agents.Decision{Cancelled: true}
	}
	requestID := nuid.Next()
	ask := make(chan agents.Decision, 1)
	m.mu.Lock()
	l := m.live[o.id]
	if l != nil {
		l.asks[requestID] = ask
		l.inputs[requestID] = append([]byte(nil), req.Input...)
	}
	m.mu.Unlock()
	if l == nil {
		return agents.Decision{Cancelled: true}
	}
	defer func() {
		m.mu.Lock()
		delete(l.asks, requestID)
		delete(l.inputs, requestID)
		m.mu.Unlock()
	}()
	m.keepInput(o.id, requestID, req.Input)
	if err := m.record(m.ctx, o.id, "", v1.EventPermissionRequested, permissionRequested(o.id, requestID, tc, req)); err != nil {
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
		// Nobody answered: that is a refusal of this one call, not the end
		// of the turn, so the agent gets its reject option when it has one.
		_ = m.record(m.ctx, o.id, "", v1.EventPermissionExpired, v1.PermissionExpired{SessionID: o.id, RequestID: requestID, Reason: "timeout"})
		o.t.Line(ToolLine(tc.Title+" (nobody answered in time)", tc.ID))
		if opt := pickOption(req.Options, false, ""); opt != "" {
			return agents.Decision{OptionID: opt}
		}
		return agents.Decision{Cancelled: true}
	case <-m.ctx.Done():
		// The daemon is stopping. The request expires at the next Load.
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

// Question records a question the agent asks and blocks until a person
// answers or declines it, the question expires, or the agent gives up.
func (o *observer) Question(ctx context.Context, q agents.Question) agents.Answer {
	m := o.m
	questionID := nuid.Next()
	answer := make(chan agents.Answer, 1)
	m.mu.Lock()
	l := m.live[o.id]
	if l != nil {
		l.answers[questionID] = answer
	}
	m.mu.Unlock()
	if l == nil {
		return agents.Answer{Cancelled: true}
	}
	defer func() {
		m.mu.Lock()
		delete(l.answers, questionID)
		m.mu.Unlock()
	}()
	fields := q.Fields
	if fields == nil {
		fields = []v1.QuestionField{}
	}
	if err := m.record(m.ctx, o.id, "", v1.EventQuestionAsked, v1.QuestionAsked{SessionID: o.id, QuestionID: questionID, ToolCallID: q.ToolCallID, Message: q.Message, Fields: fields}); err != nil {
		return agents.Answer{Cancelled: true}
	}
	slog.Info("session: waiting for an answer", "session", o.id, "question", questionID, "message", q.Message)
	timeout := m.cfg.PermissionTimeout
	if timeout == 0 {
		timeout = 30 * time.Minute
	}
	select {
	case a := <-answer:
		return a
	case <-time.After(timeout):
		_ = m.record(m.ctx, o.id, "", v1.EventQuestionExpired, v1.QuestionExpired{SessionID: o.id, QuestionID: questionID, Reason: "timeout"})
		o.t.Line(QuestionLinePrefix + q.Message + " (nobody answered in time)")
		return agents.Answer{Cancelled: true}
	case <-m.ctx.Done():
		return agents.Answer{Cancelled: true}
	case <-ctx.Done():
		if _, ok := m.Get(o.id); ok {
			m.mu.Lock()
			still := false
			for _, w := range m.sessions[o.id].Questions {
				still = still || w.QuestionID == questionID
			}
			m.mu.Unlock()
			if still {
				_ = m.record(m.ctx, o.id, "", v1.EventQuestionExpired, v1.QuestionExpired{SessionID: o.id, QuestionID: questionID, Reason: "stopped"})
			}
		}
		return agents.Answer{Cancelled: true}
	}
}

// hostRules are the rules that apply when none of the session's do.
func (m *Manager) hostRules() []v1.Rule {
	kinds := m.cfg.AllowKinds
	if kinds == nil {
		kinds = DefaultAllowKinds
	}
	return rules.Defaults(kinds)
}

// addWorktree checks out a new branch of repo into dir.
func addWorktree(ctx context.Context, repo, dir, branch string) error {
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	if _, err := git(ctx, repo, "worktree", "add", "-b", branch, dir); err != nil {
		return fmt.Errorf("create worktree for %s: %w", filepath.Base(repo), err)
	}
	return nil
}
