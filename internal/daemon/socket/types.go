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
	ID    string `json:"id"`
	Repo  string `json:"repo"`
	Agent string `json:"agent"`
	// Mode is the mode the session is in, and Model the model it runs on,
	// empty for the agent's own default.
	Mode    string `json:"mode,omitempty"`
	Model   string `json:"model,omitempty"`
	Status  string `json:"status"`
	Turns   int    `json:"turns"`
	Title   string `json:"title,omitempty"`
	Prompt  string `json:"prompt"`
	Pending int    `json:"pending,omitempty"`
	// Questions counts the questions waiting on an answer.
	Questions int `json:"questions,omitempty"`
	// Cancelling means the turn was interrupted and the agent has not
	// stopped yet.
	Cancelling bool `json:"cancelling,omitempty"`
	// Trusted means the session's tool calls are allowed without asking,
	// unless a deny rule denies them. RulesLapsed means the approver
	// behind the session's rules no longer approves, so their allow rules
	// ask.
	Trusted     bool `json:"trusted,omitempty"`
	RulesLapsed bool `json:"rulesLapsed,omitempty"`
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
	// Value is the mode or model to set, for those commands.
	Value string `json:"value,omitempty"`
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

// DecisionInfo is one request that was answered or expired, as GET
// /v1/requests/history lists them.
type DecisionInfo struct {
	Request      string `json:"request"`
	Session      string `json:"session"`
	Conversation string `json:"conversation"`
	Repo         string `json:"repo"`
	Agent        string `json:"agent"`
	Tool         string `json:"tool"`
	Kind         string `json:"kind,omitempty"`
	// Answer is "allowed", "denied", or "expired".
	Answer string `json:"answer"`
	Option string `json:"option,omitempty"`
	// By is "local" for the host's CLI, "citadel" for an approver through
	// a citadel, "trust", or "rule"; empty when the request expired.
	By string `json:"by,omitempty"`
	// Approver and ApproverName are the approver that signed a decision
	// through the citadel.
	Approver     string `json:"approver,omitempty"`
	ApproverName string `json:"approverName,omitempty"`
	// Signature says whether a decision through the citadel is signed and
	// verifies: "verified", "verified, but the approver no longer
	// approves", "doesn't verify", "unsigned", or "not checked" before the
	// daemon has read the world's approvers. It is empty for a decision
	// that needs no signature.
	Signature string    `json:"signature,omitempty"`
	Reason    string    `json:"reason,omitempty"`
	Asked     time.Time `json:"asked"`
	At        time.Time `json:"at"`
}

// DecisionDetail is one decision with what it covered, as GET
// /v1/requests/{id}/decision shows it: the input the request named, which
// a signature covers, and what the tool call produced, each as this host
// kept it. The bytes come as the host has them, and whoever reads them
// checks them against their hashes.
type DecisionDetail struct {
	DecisionInfo
	// Sig is the approver's signature over the decision, in unpadded
	// base64url, and World the world's owner key it names.
	Sig   string `json:"sig,omitempty"`
	World string `json:"world,omitempty"`
	// ToolCall is the call the request was about.
	ToolCall string `json:"toolCall,omitempty"`
	// InputHash and InputSize are the input as PermissionRequested names
	// it, and Input the bytes kept under that hash. InputKept is false
	// when the host kept none, as before inputs were kept.
	InputHash string `json:"inputHash"`
	InputSize int    `json:"inputSize"`
	Input     []byte `json:"input,omitempty"`
	InputKept bool   `json:"inputKept"`
	// CallStatus is how the tool call ended, "completed" or "failed", and
	// empty while it hasn't. OutputHash and OutputSize name what it
	// produced, as ToolCallCompleted records it, and Output is the bytes
	// kept under that hash: a v1.ToolCallOutput as canonical JSON.
	// OutputKept is false when the host kept none.
	CallStatus string `json:"callStatus,omitempty"`
	OutputHash string `json:"outputHash,omitempty"`
	OutputSize int    `json:"outputSize,omitempty"`
	Output     []byte `json:"output,omitempty"`
	OutputKept bool   `json:"outputKept"`
}

// RepoRulesInfo is one repository's rules, as GET /v1/rules lists them.
type RepoRulesInfo struct {
	RepoID string    `json:"repoId"`
	Repo   string    `json:"repo"`
	Rules  []v1.Rule `json:"rules"`
	// By is who set the rules, "local" or "citadel", and Approver the
	// approver key that signed them through the citadel. Lapsed means that
	// approver no longer approves, so the rules' allow rules ask until
	// they are set again.
	By       string `json:"by,omitempty"`
	Approver string `json:"approver,omitempty"`
	Lapsed   bool   `json:"lapsed,omitempty"`
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
	Host string `json:"host"`
	// World is the NATS URL the daemon joins the world through.
	World string `json:"world"`
	// WorldOwner is the owner key of the world this island belongs to, or
	// empty before a devworld's daemon has pinned it.
	WorldOwner string `json:"worldOwner,omitempty"`
	// SetupURL is the link to set the world up, while there is no world
	// and the daemon knows it, as devworld's daemon does.
	SetupURL string `json:"setupUrl,omitempty"`
	// Approvers is how many approvers approve for the world, as the daemon
	// reads them from the world's registry, once it has.
	Approvers *int          `json:"approvers,omitempty"`
	Version   string        `json:"version"`
	Started   time.Time     `json:"started"`
	Repos     []RepoInfo    `json:"repos"`
	Sessions  []SessionInfo `json:"sessions,omitempty"`
	// Agents is what the daemon last found about the agents it can
	// launch, or empty before its first check.
	Agents []v1.AgentAvailability `json:"agents,omitempty"`
}

// ErrorResponse is the body of every error reply.
type ErrorResponse struct {
	Error string `json:"error"`
}
