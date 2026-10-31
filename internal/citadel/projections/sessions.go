package projections

import (
	"encoding/json"
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
	RequestID  string                `json:"requestId"`
	ToolCallID string                `json:"toolCallId"`
	Title      string                `json:"title"`
	Kind       string                `json:"kind,omitempty"`
	Locations  []string              `json:"locations,omitempty"`
	Input      json.RawMessage       `json:"input,omitempty"`
	Options    []v1.PermissionOption `json:"options"`
	At         time.Time             `json:"at"`
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
	Host      string `json:"host"`
	ID        string `json:"id"`
	RepoID    string `json:"repoId"`
	Agent     string `json:"agent"`
	Model     string `json:"model,omitempty"`
	AgentName string `json:"agentName,omitempty"`
	Prompt    string `json:"prompt"`
	Branch    string `json:"branch"`
	// Status is running, idle (waiting for a person), ended, or failed.
	Status     string `json:"status"`
	StopReason string `json:"stopReason,omitempty"`
	Error      string `json:"error,omitempty"`
	Turns      int    `json:"turns"`
	// Title is what the agent named the conversation, if it has yet.
	Title string `json:"title,omitempty"`
	// Trusted means the session's tool calls are allowed without asking.
	Trusted bool `json:"trusted,omitempty"`
	// Pending holds the tool calls waiting on a person, oldest first.
	Pending   []Permission `json:"pending"`
	StartedAt time.Time    `json:"startedAt"`
	EndedAt   time.Time    `json:"endedAt,omitzero"`
	// UpdatedAt is the time of the latest event, which is what "something
	// new happened" means.
	UpdatedAt time.Time `json:"updatedAt"`
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
	Used  uint64            `json:"used,omitempty"`
	Size  uint64            `json:"size,omitempty"`
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
			s := &Session{Host: host, ID: d.SessionID, RepoID: d.RepoID, Agent: d.Agent, Model: d.Model, Branch: d.Branch, Status: "running", StartedAt: ev.Time, Recent: []ToolCall{}, Files: map[string]string{}, Pending: []Permission{}}
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
				s.Status = "running"
				changed = s
			}
		}
	case v1.EventTurnEnded:
		var d v1.TurnEnded
		if ev.Decode(&d) == nil {
			if s, ok := p.byID[d.SessionID]; ok && s.Status == "running" {
				s.Status, s.StopReason = "idle", d.StopReason
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
	case v1.EventSessionTrusted:
		var d v1.SessionTrusted
		if ev.Decode(&d) == nil {
			if s, ok := p.byID[d.SessionID]; ok {
				s.Trusted = d.Trusted
				changed = s
			}
		}
	case v1.EventPermissionRequested:
		var d v1.PermissionRequested
		if ev.Decode(&d) == nil {
			if s, ok := p.byID[d.SessionID]; ok {
				s.Pending = append(s.Pending, Permission{RequestID: d.RequestID, ToolCallID: d.ToolCallID, Title: d.Title, Kind: d.Kind, Locations: d.Locations, Input: d.Input, Options: d.Options, At: ev.Time})
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
	case v1.EventAgentSpawned:
		var d v1.AgentSpawned
		if ev.Decode(&d) == nil {
			if s, ok := p.byID[d.SessionID]; ok {
				s.AgentName = d.Name
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
	case v1.EventAgentFailed:
		var d v1.AgentFailed
		if ev.Decode(&d) == nil {
			if s, ok := p.byID[d.SessionID]; ok {
				s.Status, s.Error, s.EndedAt = "failed", d.Error, ev.Time
				changed = s
			}
		}
	case v1.EventSessionEnded:
		var d v1.SessionEnded
		if ev.Decode(&d) == nil {
			if s, ok := p.byID[d.SessionID]; ok {
				s.Status, s.StopReason, s.EndedAt = "ended", d.StopReason, ev.Time
				changed = s
			}
		}
	}
	var copy Session
	if changed != nil {
		if ev.Time.After(changed.UpdatedAt) {
			changed.UpdatedAt = ev.Time
		}
		copy = cloneSession(changed)
	}
	p.mu.Unlock()
	if changed != nil {
		p.onChange(copy)
	}
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
	return c
}
