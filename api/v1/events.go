package v1

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nuid"
)

// Event is the envelope every stored event uses. Type names the payload in
// Data, and CommandID records the command that produced the event, which is
// how an aggregate recognizes a command it has already handled.
type Event struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	CommandID string          `json:"commandId,omitempty"`
	Time      time.Time       `json:"time"`
	Data      json.RawMessage `json:"data"`
}

// NewEvent wraps data as an event of the given type produced by commandID.
func NewEvent(eventType, commandID string, data any) (Event, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return Event{}, fmt.Errorf("encode %s: %w", eventType, err)
	}
	return Event{Type: eventType, ID: nuid.Next(), CommandID: commandID, Time: time.Now().UTC(), Data: raw}, nil
}

// Decode unmarshals the event's payload into v.
func (e Event) Decode(v any) error {
	if err := json.Unmarshal(e.Data, v); err != nil {
		return fmt.Errorf("decode %s: %w", e.Type, err)
	}
	return nil
}

// Host event types, produced by the Host aggregate on each daemon.
const (
	EventRepoRegistered     = "RepoRegistered"
	EventTerrainSnapshotted = "TerrainSnapshotted"
)

// RepoRegistered records that a repository on the host is now a terrace.
type RepoRegistered struct {
	RepoID string `json:"repoId"`
	Name   string `json:"name"`
	// Path is the repository's top-level directory on the host.
	Path string `json:"path"`
}

// MaxBuildings is the most files a TerrainSnapshotted event carries, and so
// the most buildings a terrace shows: an 8 by 8 grid, which keeps each
// building large enough to click at terrace level. The daemon keeps the
// largest files, until recently touched files can take priority.
const MaxBuildings = 64

// TerrainSnapshotted records a repository's tracked files and their sizes,
// which become the buildings on its terrace. Files holds at most
// MaxBuildings entries, largest first, and TotalFiles counts them all.
type TerrainSnapshotted struct {
	RepoID     string        `json:"repoId"`
	Commit     string        `json:"commit,omitempty"`
	TotalFiles int           `json:"totalFiles"`
	Files      []TerrainFile `json:"files"`
}

// TerrainFile is one tracked file.
type TerrainFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// RegisterRepo asks a host to register the repository at Path. ID is chosen
// by the sender, and a repeated ID gets the original outcome.
type RegisterRepo struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

// Session event types, produced by the Session aggregate on each daemon.
const (
	EventSessionStarted    = "SessionStarted"
	EventPromptSent        = "PromptSent"
	EventAgentSpawned      = "AgentSpawned"
	EventToolCallStarted   = "ToolCallStarted"
	EventToolCallCompleted = "ToolCallCompleted"
	EventPlanUpdated       = "PlanUpdated"
	EventUsageReported     = "UsageReported"
	EventAgentFailed       = "AgentFailed"
	EventSessionEnded      = "SessionEnded"
)

// IsSessionEvent reports whether an event type belongs to a session.
func IsSessionEvent(eventType string) bool {
	switch eventType {
	case EventSessionStarted, EventPromptSent, EventAgentSpawned, EventToolCallStarted, EventToolCallCompleted,
		EventPlanUpdated, EventUsageReported, EventAgentFailed, EventSessionEnded:
		return true
	}
	return false
}

// SessionStarted records a new session in its own git worktree.
type SessionStarted struct {
	SessionID string `json:"sessionId"`
	RepoID    string `json:"repoId"`
	// Agent names the adapter the session runs on, such as "claude".
	Agent string `json:"agent"`
	// Model is the model the agent was asked to use, or empty for the
	// agent's own default.
	Model    string `json:"model,omitempty"`
	Worktree string `json:"worktree"`
	Branch   string `json:"branch"`
}

// PromptSent records what a person asked the session to do.
type PromptSent struct {
	SessionID string `json:"sessionId"`
	Text      string `json:"text"`
}

// AgentSpawned records that the agent process is up and identified itself.
type AgentSpawned struct {
	SessionID string `json:"sessionId"`
	// AgentID identifies the agent within the session. The lead agent's id
	// is the session id.
	AgentID string `json:"agentId"`
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// ToolCallStarted records a tool call the agent began, with the files it
// touches relative to the worktree.
type ToolCallStarted struct {
	SessionID  string   `json:"sessionId"`
	ToolCallID string   `json:"toolCallId"`
	Title      string   `json:"title"`
	Kind       string   `json:"kind,omitempty"`
	Locations  []string `json:"locations,omitempty"`
}

// ToolCallCompleted records a tool call's end. Status is "completed" or
// "failed".
type ToolCallCompleted struct {
	SessionID  string   `json:"sessionId"`
	ToolCallID string   `json:"toolCallId"`
	Status     string   `json:"status"`
	Locations  []string `json:"locations,omitempty"`
}

// PlanEntry is one step of an agent's plan.
type PlanEntry struct {
	Content  string `json:"content"`
	Priority string `json:"priority,omitempty"`
	Status   string `json:"status"`
}

// PlanUpdated records the agent's whole plan as it stands.
type PlanUpdated struct {
	SessionID string      `json:"sessionId"`
	Entries   []PlanEntry `json:"entries"`
}

// UsageReported records context use: Used tokens of a Size token window.
type UsageReported struct {
	SessionID string `json:"sessionId"`
	Used      uint64 `json:"used"`
	Size      uint64 `json:"size"`
}

// AgentFailed records that the agent stopped on an error.
type AgentFailed struct {
	SessionID string `json:"sessionId"`
	Error     string `json:"error"`
}

// SessionEnded records that the agent finished its turn.
type SessionEnded struct {
	SessionID  string `json:"sessionId"`
	StopReason string `json:"stopReason"`
}

// Session commands, sent to a daemon by request-reply.
const CommandStartSession = "StartSession"

// StartSession asks a host to run an agent on a repository with a prompt.
// The sender chooses SessionID so it can address the session right away.
type StartSession struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	RepoID    string `json:"repoId"`
	Agent     string `json:"agent"`
	// Model overrides the agent's default model when set.
	Model  string `json:"model,omitempty"`
	Prompt string `json:"prompt"`
}

// Reply is the answer to any command: an error, or the handler's result.
type Reply struct {
	Error  string          `json:"error,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
}

// StartSessionResult is the result of a StartSession command.
type StartSessionResult struct {
	SessionID string `json:"sessionId"`
	Worktree  string `json:"worktree"`
	Branch    string `json:"branch"`
}

// Fetch kinds a daemon answers.
const (
	// FetchTranscript returns a session's prompt and agent messages.
	FetchTranscript = "transcript"
	// FetchDiff returns the changes in a session's worktree.
	FetchDiff = "diff"
)

// FetchSessionRequest asks for one session's transcript or diff.
type FetchSessionRequest struct {
	SessionID string `json:"sessionId"`
}

// TextResult is a fetched text, cut off at the daemon's size limit when
// Truncated is set.
type TextResult struct {
	Text      string `json:"text"`
	Truncated bool   `json:"truncated,omitempty"`
}
