package projections

import (
	"sort"
	"sync"
	"time"

	v1 "github.com/skylands-ai/skylands/api/v1"
)

// RecentToolCalls is how many tool calls a session keeps for display.
const RecentToolCalls = 20

// Permission is a tool call waiting on a person, with the options the
// agent accepts as answers.
type Permission struct {
	RequestID  string   `json:"requestId"`
	ToolCallID string   `json:"toolCallId"`
	Title      string   `json:"title"`
	Kind       string   `json:"kind,omitempty"`
	Locations  []string `json:"locations,omitempty"`
	// InputHash, InputSize, and InputPreview describe the input the agent
	// proposed. A browser shows the whole input, from the preview or
	// fetched from the host, and approves it by its hash.
	InputHash    string                `json:"inputHash"`
	InputSize    int                   `json:"inputSize"`
	InputPreview string                `json:"inputPreview,omitempty"`
	Options      []v1.PermissionOption `json:"options"`
	At           time.Time             `json:"at"`
}

// Resumed says how a session's agent came back after a daemon restart.
type Resumed struct {
	At          time.Time `json:"at"`
	RestartedAt time.Time `json:"restartedAt"`
	// How is "load" when the agent loaded its own session and "seed" when
	// a new one was given the transcript.
	How string `json:"how"`
	// TurnResumed means the restart cut a turn short and the agent picked
	// it up again.
	TurnResumed bool `json:"turnResumed,omitempty"`
}

// Question is a question the agent is waiting on a person to answer.
type Question struct {
	QuestionID string             `json:"questionId"`
	ToolCallID string             `json:"toolCallId,omitempty"`
	Message    string             `json:"message"`
	Fields     []v1.QuestionField `json:"fields"`
	At         time.Time          `json:"at"`
}

// Terminal is a shell a person has open in the session's worktree.
type Terminal struct {
	ID       string    `json:"id"`
	Shell    string    `json:"shell"`
	OpenedAt time.Time `json:"openedAt"`
}

// ToolCall is one tool call as the session last reported it.
type ToolCall struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Kind      string    `json:"kind,omitempty"`
	Status    string    `json:"status"`
	Locations []string  `json:"locations,omitempty"`
	At        time.Time `json:"at"`
}

// Session is one agent run on one repository, as the citadel projects it
// from session events.
type Session struct {
	Host   string `json:"host"`
	ID     string `json:"id"`
	RepoID string `json:"repoId"`
	Agent  string `json:"agent"`
	// Model is the model the session runs on, empty for the agent's own
	// default, and Mode the mode it is in, empty for the first its
	// adapter lists. Both follow the session's changes.
	Model     string `json:"model,omitempty"`
	Mode      string `json:"mode,omitempty"`
	AgentName string `json:"agentName,omitempty"`
	Prompt    string `json:"prompt"`
	// Branch is what the agent works on: its own, cut from Base, or the
	// checkout's own when InCheckout is set.
	Branch     string `json:"branch"`
	Base       string `json:"base,omitempty"`
	InCheckout bool   `json:"inCheckout,omitempty"`
	// Commit is the latest commit made through Skylands and Commits counts
	// them; MergedInto and MergedCommit record the latest merge into the
	// checkout, PullRequest the pull request opened from the branch, and
	// WorktreeRemoved that the worktree is gone, and BranchKept that its
	// branch stayed, as an unforced removal keeps one with unmerged
	// commits.
	Commit          string `json:"commit,omitempty"`
	Commits         int    `json:"commits,omitempty"`
	MergedInto      string `json:"mergedInto,omitempty"`
	MergedCommit    string `json:"mergedCommit,omitempty"`
	PullRequest     string `json:"pullRequest,omitempty"`
	WorktreeRemoved bool   `json:"worktreeRemoved,omitempty"`
	BranchKept      bool   `json:"branchKept,omitempty"`
	// Status is running, idle (waiting for a person), ended, or failed.
	Status     string `json:"status"`
	StopReason string `json:"stopReason,omitempty"`
	Error      string `json:"error,omitempty"`
	Turns      int    `json:"turns"`
	// Revision counts the session's prompts, commits, merges, and pull
	// requests, as its daemon does. A signed prompt or change names it.
	Revision int `json:"revision"`
	// Title is what the agent named the conversation, if it has yet.
	Title string `json:"title,omitempty"`
	// Trusted means the session's tool calls are allowed without asking,
	// unless a deny rule denies them. It is always sent, so a browser sees
	// it turn back off. TrustedBy is who last set it ("citadel", "local",
	// or "revocation" when the approver who turned it on was revoked),
	// TrustApprover the approver key behind that, and TrustRevision how
	// many times it has been set, which a change names.
	Trusted       bool   `json:"trusted"`
	TrustedBy     string `json:"trustedBy,omitempty"`
	TrustApprover string `json:"trustApprover,omitempty"`
	TrustRevision int    `json:"trustRevision"`
	// Cancelling means a person interrupted the turn and the agent has not
	// stopped yet.
	Cancelling bool `json:"cancelling,omitempty"`
	// Rules settle tool calls before they reach a person: the repository's
	// as copied at the start, and any set on the session since. RulesBy and
	// RulesApprover are who stands behind the list, RulesRevision how many
	// times the session's rules were set, which a change names, and
	// RulesLapsed that the approver behind them no longer approves, so
	// their allow rules ask.
	Rules         []v1.Rule `json:"rules"`
	RulesBy       string    `json:"rulesBy,omitempty"`
	RulesApprover string    `json:"rulesApprover,omitempty"`
	RulesRevision int       `json:"rulesRevision"`
	RulesLapsed   bool      `json:"rulesLapsed,omitempty"`
	// Pending holds the tool calls waiting on a person, oldest first, and
	// Questions the questions waiting on an answer.
	Pending   []Permission `json:"pending"`
	Questions []Question   `json:"questions"`
	// Resumes counts the daemon restarts the session has lived through, and
	// Resumed says how its agent last came back.
	Resumes   int       `json:"resumes,omitempty"`
	Resumed   Resumed   `json:"resumed,omitzero"`
	StartedAt time.Time `json:"startedAt"`
	EndedAt   time.Time `json:"endedAt,omitzero"`
	// UpdatedAt is the time of the latest event of any kind, which is when
	// what a browser fetched about the session went stale.
	UpdatedAt time.Time `json:"updatedAt"`
	// ActiveAt is the time of the latest activity in the conversation: the
	// agent or a person did something. Bookkeeping such as a daemon restart,
	// a title, a trust switch, or a rule change leaves it alone, so none of
	// those reads as something waiting on a person.
	ActiveAt time.Time `json:"activeAt"`
	// CurrentFile is the file of the latest tool call that named one, and
	// CurrentAction that call's title.
	CurrentFile   string `json:"currentFile,omitempty"`
	CurrentAction string `json:"currentAction,omitempty"`
	// Recent holds the last RecentToolCalls tool calls, oldest first.
	Recent []ToolCall `json:"recent"`
	// Files maps each file the session touched to the kind of its last
	// touch, such as read or edit.
	Files map[string]string `json:"files"`
	Plan  []v1.PlanEntry    `json:"plan"`
	// Used and Size are the latest reading of the context window.
	Used uint64 `json:"used,omitempty"`
	Size uint64 `json:"size,omitempty"`
	// Tokens is what the session has cost so far: every turn's tokens and
	// the title request's, summed. Always sent, so a browser can add
	// sessions up without checking.
	Tokens v1.TokenUsage `json:"tokens"`
	// Terminals are the shells people have open in the worktree, oldest
	// first.
	Terminals []Terminal `json:"terminals"`
}

// Sessions is the projection of every host's sessions. It is safe for
// concurrent use.
type Sessions struct {
	mu       sync.Mutex
	byID     map[string]*Session
	onChange func(Session)
}

// NewSessions returns an empty projection. onChange, if set, gets a copy of
// each session whose state changed.
func NewSessions(onChange func(Session)) *Sessions {
	if onChange == nil {
		onChange = func(Session) {}
	}
	return &Sessions{byID: map[string]*Session{}, onChange: onChange}
}

// Apply folds one session event into the projection.
func (p *Sessions) Apply(host string, ev v1.Event) {
	var changed *Session
	p.mu.Lock()
	switch ev.Type {
	case v1.EventSessionStarted:
		var d v1.SessionStarted
		if ev.Decode(&d) == nil {
			s := &Session{Host: host, ID: d.SessionID, RepoID: d.RepoID, Agent: d.Agent, Model: d.Model, Mode: d.Mode, Branch: d.Branch, Base: d.Base, InCheckout: d.InCheckout, Rules: append([]v1.Rule{}, d.Rules...), RulesBy: d.RulesBy, RulesApprover: d.RulesApprover, RulesLapsed: d.RulesLapsed, Status: "running", StartedAt: ev.Time, ActiveAt: ev.Time, Recent: []ToolCall{}, Files: map[string]string{}, Pending: []Permission{}, Questions: []Question{}, Terminals: []Terminal{}}
			p.byID[d.SessionID] = s
			changed = s
		}
	case v1.EventPromptSent:
		var d v1.PromptSent
		if ev.Decode(&d) == nil {
			if s, ok := p.byID[d.SessionID]; ok {
				if s.Turns == 0 {
					s.Prompt = d.Text
				}
				s.Turns++
				s.Revision++
				s.Status = "running"
				s.Cancelling = false
				changed = s
			}
		}
	case v1.EventTurnEnded:
		var d v1.TurnEnded
		if ev.Decode(&d) == nil {
			if s, ok := p.byID[d.SessionID]; ok && s.Status == "running" {
				s.Status, s.StopReason, s.Cancelling = "idle", d.StopReason, false
				changed = s
			}
		}
	case v1.EventTurnCancelled:
		var d v1.TurnCancelled
		if ev.Decode(&d) == nil {
			if s, ok := p.byID[d.SessionID]; ok && s.Status == "running" {
				s.Cancelling = true
				changed = s
			}
		}
	case v1.EventSessionTitled:
		var d v1.SessionTitled
		if ev.Decode(&d) == nil {
			if s, ok := p.byID[d.SessionID]; ok {
				s.Title = d.Title
				changed = s
			}
		}
	case v1.EventSessionModeChanged:
		var d v1.SessionModeChanged
		if ev.Decode(&d) == nil {
			if s, ok := p.byID[d.SessionID]; ok {
				s.Mode = d.Mode
				changed = s
			}
		}
	case v1.EventSessionModelChanged:
		var d v1.SessionModelChanged
		if ev.Decode(&d) == nil {
			if s, ok := p.byID[d.SessionID]; ok {
				s.Model = d.Model
				changed = s
			}
		}
	case v1.EventSessionTrusted:
		var d v1.SessionTrusted
		if ev.Decode(&d) == nil {
			if s, ok := p.byID[d.SessionID]; ok {
				s.Trusted, s.TrustedBy, s.TrustApprover = d.Trusted, d.By, d.Approver
				s.TrustRevision++
				changed = s
			}
		}
	case v1.EventSessionRulesSet:
		var d v1.SessionRulesSet
		if ev.Decode(&d) == nil {
			if s, ok := p.byID[d.SessionID]; ok {
				s.Rules = append([]v1.Rule{}, d.Rules...)
				s.RulesBy, s.RulesApprover, s.RulesLapsed = d.By, d.Approver, false
				s.RulesRevision++
				changed = s
			}
		}
	case v1.EventSessionRulesLapsed:
		var d v1.SessionRulesLapsed
		if ev.Decode(&d) == nil {
			if s, ok := p.byID[d.SessionID]; ok {
				s.RulesLapsed = true
				changed = s
			}
		}
	case v1.EventPermissionRequested:
		var d v1.PermissionRequested
		if ev.Decode(&d) == nil {
			if s, ok := p.byID[d.SessionID]; ok {
				s.Pending = append(s.Pending, Permission{RequestID: d.RequestID, ToolCallID: d.ToolCallID, Title: d.Title, Kind: d.Kind, Locations: d.Locations, InputHash: d.InputHash, InputSize: d.InputSize, InputPreview: d.InputPreview, Options: d.Options, At: ev.Time})
				changed = s
			}
		}
	case v1.EventPermissionGranted, v1.EventPermissionDenied, v1.EventPermissionExpired:
		var d struct {
			SessionID string `json:"sessionId"`
			RequestID string `json:"requestId"`
		}
		if ev.Decode(&d) == nil {
			if s, ok := p.byID[d.SessionID]; ok {
				kept := make([]Permission, 0, len(s.Pending))
				for _, q := range s.Pending {
					if q.RequestID != d.RequestID {
						kept = append(kept, q)
					}
				}
				s.Pending = kept
				changed = s
			}
		}
	case v1.EventQuestionAsked:
		var d v1.QuestionAsked
		if ev.Decode(&d) == nil {
			if s, ok := p.byID[d.SessionID]; ok {
				fields := d.Fields
				if fields == nil {
					fields = []v1.QuestionField{}
				}
				s.Questions = append(s.Questions, Question{QuestionID: d.QuestionID, ToolCallID: d.ToolCallID, Message: d.Message, Fields: fields, At: ev.Time})
				changed = s
			}
		}
	case v1.EventQuestionAnswered, v1.EventQuestionExpired:
		var d struct {
			SessionID  string `json:"sessionId"`
			QuestionID string `json:"questionId"`
		}
		if ev.Decode(&d) == nil {
			if s, ok := p.byID[d.SessionID]; ok {
				kept := make([]Question, 0, len(s.Questions))
				for _, q := range s.Questions {
					if q.QuestionID != d.QuestionID {
						kept = append(kept, q)
					}
				}
				s.Questions = kept
				changed = s
			}
		}
	case v1.EventAgentSpawned:
		var d v1.AgentSpawned
		if ev.Decode(&d) == nil {
			if s, ok := p.byID[d.SessionID]; ok {
				s.AgentName = d.Name
				changed = s
			}
		}
	case v1.EventSessionResumed:
		var d v1.SessionResumed
		if ev.Decode(&d) == nil {
			if s, ok := p.byID[d.SessionID]; ok {
				s.Resumes++
				s.Resumed = Resumed{At: ev.Time, RestartedAt: d.RestartedAt, How: d.How, TurnResumed: d.TurnResumed}
				changed = s
			}
		}
	case v1.EventChangesCommitted:
		var d v1.ChangesCommitted
		if ev.Decode(&d) == nil {
			if s, ok := p.byID[d.SessionID]; ok {
				s.Commit = d.Commit
				s.Commits++
				s.Revision++
				changed = s
			}
		}
	case v1.EventChangesMerged:
		var d v1.ChangesMerged
		if ev.Decode(&d) == nil {
			if s, ok := p.byID[d.SessionID]; ok {
				s.MergedInto, s.MergedCommit = d.Into, d.Commit
				s.Revision++
				changed = s
			}
		}
	case v1.EventPullRequestOpened:
		var d v1.PullRequestOpened
		if ev.Decode(&d) == nil {
			if s, ok := p.byID[d.SessionID]; ok {
				s.PullRequest = d.URL
				s.Revision++
				changed = s
			}
		}
	case v1.EventWorktreeRemoved:
		var d v1.WorktreeRemoved
		if ev.Decode(&d) == nil {
			if s, ok := p.byID[d.SessionID]; ok {
				s.WorktreeRemoved, s.BranchKept = true, !d.BranchDeleted
				changed = s
			}
		}
	case v1.EventToolCallStarted:
		var d v1.ToolCallStarted
		if ev.Decode(&d) == nil {
			if s, ok := p.byID[d.SessionID]; ok {
				s.Recent = append(s.Recent, ToolCall{ID: d.ToolCallID, Title: d.Title, Kind: d.Kind, Status: "in_progress", Locations: d.Locations, At: ev.Time})
				if len(s.Recent) > RecentToolCalls {
					s.Recent = s.Recent[len(s.Recent)-RecentToolCalls:]
				}
				s.CurrentAction = d.Title
				if len(d.Locations) > 0 {
					s.CurrentFile = d.Locations[0]
					for _, l := range d.Locations {
						s.Files[l] = d.Kind
					}
				}
				changed = s
			}
		}
	case v1.EventToolCallCompleted:
		var d v1.ToolCallCompleted
		if ev.Decode(&d) == nil {
			if s, ok := p.byID[d.SessionID]; ok {
				for i := range s.Recent {
					if s.Recent[i].ID == d.ToolCallID {
						s.Recent[i].Status = d.Status
						if d.Title != "" {
							if s.CurrentAction == s.Recent[i].Title {
								s.CurrentAction = d.Title
							}
							s.Recent[i].Title = d.Title
						}
						if len(d.Locations) > 0 {
							s.Recent[i].Locations = d.Locations
						}
					}
				}
				changed = s
			}
		}
	case v1.EventPlanUpdated:
		var d v1.PlanUpdated
		if ev.Decode(&d) == nil {
			if s, ok := p.byID[d.SessionID]; ok {
				s.Plan = d.Entries
				changed = s
			}
		}
	case v1.EventUsageReported:
		var d v1.UsageReported
		if ev.Decode(&d) == nil {
			if s, ok := p.byID[d.SessionID]; ok {
				s.Used, s.Size = d.Used, d.Size
				changed = s
			}
		}
	case v1.EventTokensUsed:
		var d v1.TokensUsed
		if ev.Decode(&d) == nil {
			if s, ok := p.byID[d.SessionID]; ok {
				s.Tokens.Add(d.Tokens)
				changed = s
			}
		}
	case v1.EventAgentFailed:
		var d v1.AgentFailed
		if ev.Decode(&d) == nil {
			if s, ok := p.byID[d.SessionID]; ok {
				s.Status, s.Error, s.EndedAt, s.Cancelling = "failed", d.Error, ev.Time, false
				changed = s
			}
		}
	case v1.EventSessionEnded:
		var d v1.SessionEnded
		if ev.Decode(&d) == nil {
			if s, ok := p.byID[d.SessionID]; ok {
				s.Status, s.StopReason, s.EndedAt, s.Cancelling = "ended", d.StopReason, ev.Time, false
				changed = s
			}
		}
	case v1.EventTerminalOpened:
		var d v1.TerminalOpened
		if ev.Decode(&d) == nil {
			if s, ok := p.byID[d.SessionID]; ok {
				s.Terminals = append(s.Terminals, Terminal{ID: d.TerminalID, Shell: d.Shell, OpenedAt: ev.Time})
				changed = s
			}
		}
	case v1.EventTerminalClosed:
		var d v1.TerminalClosed
		if ev.Decode(&d) == nil {
			if s, ok := p.byID[d.SessionID]; ok {
				kept := make([]Terminal, 0, len(s.Terminals))
				for _, t := range s.Terminals {
					if t.ID != d.TerminalID {
						kept = append(kept, t)
					}
				}
				s.Terminals = kept
				changed = s
			}
		}
	}
	var copy Session
	if changed != nil {
		if ev.Time.After(changed.UpdatedAt) {
			changed.UpdatedAt = ev.Time
		}
		if isActivity(ev.Type) && ev.Time.After(changed.ActiveAt) {
			changed.ActiveAt = ev.Time
		}
		copy = cloneSession(changed)
	}
	p.mu.Unlock()
	if changed != nil {
		p.onChange(copy)
	}
}

// isActivity says whether an event is the agent or a person doing
// something in the conversation, as opposed to bookkeeping about it. A
// turn's token count arrives with the turn's end, and the title's with the
// title, so TokensUsed is bookkeeping too.
func isActivity(eventType string) bool {
	switch eventType {
	case v1.EventSessionStarted, v1.EventPromptSent, v1.EventTurnEnded, v1.EventToolCallStarted, v1.EventToolCallCompleted,
		v1.EventPlanUpdated, v1.EventUsageReported, v1.EventPermissionRequested, v1.EventPermissionGranted, v1.EventPermissionDenied,
		v1.EventQuestionAsked, v1.EventQuestionAnswered, v1.EventTurnCancelled,
		v1.EventAgentFailed, v1.EventSessionEnded, v1.EventChangesCommitted, v1.EventChangesMerged, v1.EventPullRequestOpened:
		return true
	}
	return false
}

// Get returns the session id, if the projection has it.
func (p *Sessions) Get(id string) (Session, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.byID[id]
	if !ok {
		return Session{}, false
	}
	return cloneSession(s), true
}

// List returns every session, newest first.
func (p *Sessions) List() []Session {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]Session, 0, len(p.byID))
	for _, s := range p.byID {
		out = append(out, cloneSession(s))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out
}

// cloneSession copies a session for callers. Its slices and map are never
// nil, so they encode as [] and {} rather than null, which browsers can
// index without checking.
func cloneSession(s *Session) Session {
	c := *s
	c.Recent = append(make([]ToolCall, 0, len(s.Recent)), s.Recent...)
	c.Files = make(map[string]string, len(s.Files))
	for k, v := range s.Files {
		c.Files[k] = v
	}
	c.Plan = append(make([]v1.PlanEntry, 0, len(s.Plan)), s.Plan...)
	c.Pending = append(make([]Permission, 0, len(s.Pending)), s.Pending...)
	c.Questions = append(make([]Question, 0, len(s.Questions)), s.Questions...)
	c.Rules = append(make([]v1.Rule, 0, len(s.Rules)), s.Rules...)
	c.Terminals = append(make([]Terminal, 0, len(s.Terminals)), s.Terminals...)
	return c
}
