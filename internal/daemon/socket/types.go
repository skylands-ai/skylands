// Package socket is the daemon's local API for the CLI: HTTP over a Unix
// socket that only the OS user owning the daemon can open. The daemon
// serves it, and the CLI dials it.
package socket

import (
	"encoding/json"
	"time"

	v1 "github.com/skylands-ai/skylands/api/v1"
)

// RegisterRepoRequest is the body of POST /v1/repos.
type RegisterRepoRequest struct {
	Path string `json:"path"`
	// CommandID identifies the command. A repeated id gets the original
	// outcome. The client fills it in when empty.
	CommandID string `json:"commandId,omitempty"`
}

// RepoInfo describes one registered repository.
type RepoInfo struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Path   string `json:"path"`
	Commit string `json:"commit,omitempty"`
	// Files counts every tracked file, not only the ones that get buildings.
	Files        int       `json:"files"`
	RegisteredAt time.Time `json:"registeredAt"`
}

// SessionInfo describes one agent session on this host.
type SessionInfo struct {
	ID      string `json:"id"`
	Repo    string `json:"repo"`
	Agent   string `json:"agent"`
	Status  string `json:"status"`
	Turns   int    `json:"turns"`
	Title   string `json:"title,omitempty"`
	Prompt  string `json:"prompt"`
	Pending int    `json:"pending,omitempty"`
	// Questions counts the questions waiting on an answer.
	Questions int `json:"questions,omitempty"`
	// Worktree is where the agent works, and Branch what it works on. A
	// session in the checkout has InCheckout set and no base.
	Worktree   string `json:"worktree,omitempty"`
	Branch     string `json:"branch,omitempty"`
	Base       string `json:"base,omitempty"`
	InCheckout bool   `json:"inCheckout,omitempty"`
	// Commit is the latest commit made through Skylands, MergedInto the
	// branch the session's branch was merged into, and PullRequest the
	// pull request opened from it.
	Commit          string    `json:"commit,omitempty"`
	MergedInto      string    `json:"mergedInto,omitempty"`
	PullRequest     string    `json:"pullRequest,omitempty"`
	WorktreeRemoved bool      `json:"worktreeRemoved,omitempty"`
	StartedAt       time.Time `json:"startedAt"`
	EndedAt         time.Time `json:"endedAt,omitzero"`
}

// SessionCommand is the body of POST /v1/sessions/{id}/stop and DELETE
// /v1/sessions/{id}.
type SessionCommand struct {
	// CommandID identifies the command. The client fills it in when empty.
	CommandID string `json:"commandId,omitempty"`
	// Force removes a worktree with uncommitted changes and a branch with
	// unmerged commits.
	Force bool `json:"force,omitempty"`
}

// RemoveResult is what removing a session's worktree did.
type RemoveResult struct {
	Session       SessionInfo `json:"session"`
	BranchDeleted bool        `json:"branchDeleted"`
}

// PruneResult is what pruning did with one finished session.
type PruneResult struct {
	Session       SessionInfo `json:"session"`
	Removed       bool        `json:"removed"`
	BranchDeleted bool        `json:"branchDeleted,omitempty"`
	// Kept says why the worktree stayed, when it did.
	Kept string `json:"kept,omitempty"`
}

// OptionInfo is one answer a request accepts.
type OptionInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// RequestInfo describes one tool call waiting on a person.
type RequestInfo struct {
	ID           string          `json:"id"`
	Session      string          `json:"session"`
	Conversation string          `json:"conversation"`
	Repo         string          `json:"repo"`
	Agent        string          `json:"agent"`
	Tool         string          `json:"tool"`
	Kind         string          `json:"kind,omitempty"`
	Locations    []string        `json:"locations,omitempty"`
	Input        json.RawMessage `json:"input,omitempty"`
	Options      []OptionInfo    `json:"options"`
	Since        time.Time       `json:"since"`
}

// RepoRulesInfo is one repository's rules, as GET /v1/rules lists them.
type RepoRulesInfo struct {
	RepoID string    `json:"repoId"`
	Repo   string    `json:"repo"`
	Rules  []v1.Rule `json:"rules"`
}

// SetRulesRequest is the body of PUT /v1/repos/{id}/rules.
type SetRulesRequest struct {
	// CommandID identifies the command. The client fills it in when empty.
	CommandID string    `json:"commandId,omitempty"`
	Rules     []v1.Rule `json:"rules"`
}

// QuestionInfo describes one question waiting on a person.
type QuestionInfo struct {
	ID           string             `json:"id"`
	Session      string             `json:"session"`
	Conversation string             `json:"conversation"`
	Repo         string             `json:"repo"`
	Agent        string             `json:"agent"`
	Message      string             `json:"message"`
	Fields       []v1.QuestionField `json:"fields"`
	Since        time.Time          `json:"since"`
}

// AnswerRequest is the body of POST /v1/questions/{id}/answer: values by
// field key, or a refusal.
type AnswerRequest struct {
	CommandID string          `json:"commandId,omitempty"`
	Answers   json.RawMessage `json:"answers,omitempty"`
	Decline   bool            `json:"decline,omitempty"`
}

// DecideRequest is the body of POST /v1/requests/{id}/approve and deny.
type DecideRequest struct {
	// CommandID identifies the decision. The client fills it in when empty.
	CommandID string `json:"commandId,omitempty"`
}

// StatusInfo is what GET /v1/status returns.
type StatusInfo struct {
	Host     string        `json:"host"`
	World    string        `json:"world"`
	Version  string        `json:"version"`
	Started  time.Time     `json:"started"`
	Repos    []RepoInfo    `json:"repos"`
	Sessions []SessionInfo `json:"sessions,omitempty"`
}

// ErrorResponse is the body of every error reply.
type ErrorResponse struct {
	Error string `json:"error"`
}
