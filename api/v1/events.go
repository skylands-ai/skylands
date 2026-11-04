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

// Rule decisions, the kind that matches every tool call, the path pattern
// that matches everything inside a session's worktree, and the source a
// session's rule carries when it was copied from the repository.
const (
	RuleAllow      = "allow"
	RuleAsk        = "ask"
	RuleDeny       = "deny"
	RuleAnyKind    = "*"
	RuleInWorktree = "worktree"
	RuleFromRepo   = "repo"
)

// Rule settles tool calls before they reach a person. Kind is a tool call
// kind as ACP reports it, such as read, edit, execute, or fetch, or
// RuleAnyKind. Pattern narrows the rule to some calls of that kind: a
// command pattern for execute, a URL pattern for fetch, and a path pattern
// for the rest, where RuleInWorktree means every location inside the
// session's worktree and * stands for anything. A session's rules are
// copied from its repository when it starts, with Source RuleFromRepo;
// a rule set on the session itself has no source.
type Rule struct {
	Decision string `json:"decision"`
	Kind     string `json:"kind"`
	Pattern  string `json:"pattern,omitempty"`
	Source   string `json:"source,omitempty"`
}

// EventRepoRulesSet records a repository's rules, which every session
// started on it from then on copies.
const EventRepoRulesSet = "RepoRulesSet"

// RepoRulesSet carries the whole list, so the event is order-free.
type RepoRulesSet struct {
	RepoID string `json:"repoId"`
	Rules  []Rule `json:"rules"`
}

// SetRepoRules replaces a repository's rules.
type SetRepoRules struct {
	ID     string `json:"id"`
	RepoID string `json:"repoId"`
	Rules  []Rule `json:"rules"`
}

// EventAdapterAvailabilityChanged records which agents the host can
// launch, each time the daemon's check of them comes out differently.
const EventAdapterAvailabilityChanged = "AdapterAvailabilityChanged"

// AdapterAvailabilityChanged lists every agent type the daemon knows, by
// name, as the daemon found it when it last checked. The daemon checks by
// starting each agent without a session, in the daemon's own environment,
// and reading the login an agent reports.
type AdapterAvailabilityChanged struct {
	Agents []AgentAvailability `json:"agents"`
}

// AgentAvailability is whether the host can launch one agent type.
type AgentAvailability struct {
	// Name is the agent type a session asks for, such as "claude".
	Name string `json:"name"`
	// Available means a session with this agent can start.
	Available bool `json:"available"`
	// Agent is what the agent calls itself, with its version, when it
	// started.
	Agent string `json:"agent,omitempty"`
	// Login is the login the agent reported, such as "Claude Max".
	Login string `json:"login,omitempty"`
	// Notes are the checks that failed or warned for this agent. A failed
	// one is why the agent is not available.
	Notes []AvailabilityNote `json:"notes,omitempty"`
	// ModelsRead says the daemon read which models the agent offers this
	// host's login, from the agent's ACP model setting in a session it
	// opened without prompting. With no Models, the agent has no model
	// setting. False means nobody knows yet.
	ModelsRead bool `json:"modelsRead,omitempty"`
	// Models are the models the agent offered, in its order.
	Models []AgentModel `json:"models,omitempty"`
	// DefaultModel is the model a new session started on.
	DefaultModel string `json:"defaultModel,omitempty"`
}

// AgentModel is one model an agent offers: the value a session's model is
// set to, and what the agent calls it.
type AgentModel struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}

// AvailabilityNote is one check that failed or warned, with what to do
// about it.
type AvailabilityNote struct {
	// Check names what was checked, such as "node" or "login claude".
	Check  string `json:"check"`
	Failed bool   `json:"failed,omitempty"`
	Found  string `json:"found"`
	Fix    string `json:"fix,omitempty"`
}

// CommandSetRepoRules is sent to a host by request-reply, or over its
// socket.
const CommandSetRepoRules = "SetRepoRules"

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
		EventPlanUpdated, EventUsageReported, EventAgentFailed, EventSessionEnded, EventTurnEnded, EventSessionTitled,
		EventPermissionRequested, EventPermissionGranted, EventPermissionDenied, EventPermissionExpired, EventSessionTrusted,
		EventSessionResumed, EventChangesCommitted, EventChangesMerged, EventPullRequestOpened, EventWorktreeRemoved,
		EventSessionRulesSet, EventQuestionAsked, EventQuestionAnswered, EventQuestionExpired, EventTurnCancelled,
		EventTokensUsed:
		return true
	}
	return false
}

// SessionStarted records a new session: in its own git worktree on its own
// branch, or in the registered checkout itself.
type SessionStarted struct {
	SessionID string `json:"sessionId"`
	RepoID    string `json:"repoId"`
	// Agent names the adapter the session runs on, such as "claude".
	Agent string `json:"agent"`
	// Model is the model the agent was asked to use, or empty for the
	// agent's own default.
	Model string `json:"model,omitempty"`
	// Worktree is the directory the agent works in: a worktree of its own,
	// or the checkout when InCheckout is set.
	Worktree string `json:"worktree"`
	// Branch is the branch the agent works on. For a worktree session it
	// was cut from Base; for a checkout session it is the checkout's own.
	Branch string `json:"branch"`
	// Base is the branch a worktree session's changes merge into, which
	// is what the checkout was on when the session started.
	Base       string `json:"base,omitempty"`
	InCheckout bool   `json:"inCheckout,omitempty"`
	// Rules are the repository's rules as the session copied them.
	Rules []Rule `json:"rules,omitempty"`
}

// PromptSent records what a person asked the session to do.
type PromptSent struct {
	SessionID string `json:"sessionId"`
	Text      string `json:"text"`
}

// AgentSpawned records that the agent process is up, identified itself,
// and opened its session. It is recorded again when a daemon restart
// brings the agent back.
type AgentSpawned struct {
	SessionID string `json:"sessionId"`
	// AgentID identifies the agent within the session. The lead agent's id
	// is the session id.
	AgentID string `json:"agentId"`
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	// AgentSessionID is the agent's own id for the conversation, which is
	// what the daemon asks it to load again after a restart.
	AgentSessionID string `json:"agentSessionId,omitempty"`
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
// "failed". Title is set when the agent renamed the call since it started,
// as one does once it knows the whole input.
type ToolCallCompleted struct {
	SessionID  string   `json:"sessionId"`
	ToolCallID string   `json:"toolCallId"`
	Status     string   `json:"status"`
	Title      string   `json:"title,omitempty"`
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
// It is a reading, not a cost; what the session spends is in TokensUsed.
type UsageReported struct {
	SessionID string `json:"sessionId"`
	Used      uint64 `json:"used"`
	Size      uint64 `json:"size"`
}

// EventTokensUsed records what one request to the agent's model cost in
// tokens: a turn, or a side request such as the one for the title. A
// session's spend is the sum of its TokensUsed events, and a repository's
// the sum over its sessions.
const EventTokensUsed = "TokensUsed"

// What a TokensUsed event paid for.
const (
	TokensForTurn  = "turn"
	TokensForTitle = "title"
)

// TokenUsage counts tokens by kind, for one request or summed over many.
// Input leaves out the cached counts, as providers report them, and Total
// is the agent's own sum: input, output, and the cache counts, with thought
// tokens where the provider bills them apart from output.
type TokenUsage struct {
	Input       uint64 `json:"input"`
	Output      uint64 `json:"output"`
	CachedRead  uint64 `json:"cachedRead,omitempty"`
	CachedWrite uint64 `json:"cachedWrite,omitempty"`
	Thought     uint64 `json:"thought,omitempty"`
	Total       uint64 `json:"total"`
}

// Add adds o's counts to t's.
func (t *TokenUsage) Add(o TokenUsage) {
	t.Input += o.Input
	t.Output += o.Output
	t.CachedRead += o.CachedRead
	t.CachedWrite += o.CachedWrite
	t.Thought += o.Thought
	t.Total += o.Total
}

// IsZero reports whether nothing was counted.
func (t TokenUsage) IsZero() bool { return t == TokenUsage{} }

// TokensUsed records the tokens one request cost. For is TokensForTurn or
// TokensForTitle.
type TokensUsed struct {
	SessionID string     `json:"sessionId"`
	For       string     `json:"for,omitempty"`
	Tokens    TokenUsage `json:"tokens"`
}

// AgentFailed records that the agent stopped on an error. A daemon restart
// is not one: that records SessionResumed.
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
	// InCheckout runs the agent in the registered checkout itself instead
	// of a worktree of its own. One session at a time may work there.
	InCheckout bool `json:"inCheckout,omitempty"`
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
	// FetchChanges returns where a session's changes stand: what is
	// uncommitted and how far its branch is ahead of its base.
	FetchChanges = "changes"
	// FetchToolCall returns one tool call's record: its exact input, what
	// it produced, and when. The request names the call by ToolCallID.
	FetchToolCall = "toolcall"
)

// ToolCallDetail is everything the host kept about one tool call: what the
// agent asked for, what came back, and when. It stays on the host's disk
// beside the transcript until a conversation's reader opens the call.
type ToolCallDetail struct {
	ToolCallID string   `json:"toolCallId"`
	Title      string   `json:"title"`
	Kind       string   `json:"kind,omitempty"`
	Status     string   `json:"status"`
	Locations  []string `json:"locations,omitempty"`
	// Input is the exact tool input as the agent last reported it, as JSON.
	Input json.RawMessage `json:"input,omitempty"`
	// Content is what the call produced, as the agent last reported it:
	// text, such as a command's output, a diff, or a terminal reference.
	Content []ToolCallContent `json:"content,omitempty"`
	// Output is the agent's raw result as text, when it gave one and no
	// content carried it.
	Output string `json:"output,omitempty"`
	// Truncated says some text was cut at the host's size limit.
	Truncated bool      `json:"truncated,omitempty"`
	StartedAt time.Time `json:"startedAt"`
	// UpdatedAt is when the agent last said anything about the call.
	UpdatedAt time.Time  `json:"updatedAt"`
	EndedAt   *time.Time `json:"endedAt,omitempty"`
}

// ToolCallContent is one piece of what a tool call produced, as ACP
// reports it: "text" with Text, "diff" with Path and the texts, "terminal"
// with TerminalID, or another content block's type with a short Text
// describing it.
type ToolCallContent struct {
	Type       string  `json:"type"`
	Text       string  `json:"text,omitempty"`
	Path       string  `json:"path,omitempty"`
	OldText    *string `json:"oldText,omitempty"`
	NewText    string  `json:"newText,omitempty"`
	TerminalID string  `json:"terminalId,omitempty"`
}

// ChangesResult is the state of a session's changes on the host's disk.
// What was committed, merged, or sent as a pull request through Skylands
// is in the session's events; this is what git says right now.
type ChangesResult struct {
	Branch     string `json:"branch"`
	Base       string `json:"base,omitempty"`
	InCheckout bool   `json:"inCheckout,omitempty"`
	// Changed lists the files with uncommitted changes.
	Changed []string `json:"changed"`
	// Ahead counts the commits on Branch that Base lacks. It is zero for
	// a checkout session, which has no branch of its own.
	Ahead int `json:"ahead"`
	// WorktreeRemoved means the worktree and its branch are gone, so
	// nothing above applies.
	WorktreeRemoved bool `json:"worktreeRemoved,omitempty"`
}

// FetchSessionRequest asks for one session's transcript, diff, or changes,
// or with ToolCallID for one of its tool calls.
type FetchSessionRequest struct {
	SessionID  string `json:"sessionId"`
	ToolCallID string `json:"toolCallId,omitempty"`
}

// TextResult is a fetched text, cut off at the daemon's size limit when
// Truncated is set. Length is the whole text's size in bytes, which is
// where live appends continue from.
type TextResult struct {
	Text      string `json:"text"`
	Truncated bool   `json:"truncated,omitempty"`
	Length    int64  `json:"length,omitempty"`
}

// Live is one piece of text appended to a session's transcript, published
// on the session's live subject as it is written. Off is the byte offset
// in the transcript where Text begins, so a reader can tell what it
// already has from what is new.
type Live struct {
	Off  int64  `json:"off"`
	Text string `json:"text"`
}

// EventTurnEnded records that the agent finished one turn and the session
// now waits for a person. SessionEnded is the end of the whole session.
const EventTurnEnded = "TurnEnded"

// TurnEnded records the end of one turn.
type TurnEnded struct {
	SessionID  string `json:"sessionId"`
	StopReason string `json:"stopReason"`
}

// EventSessionTitled records the name an agent gave its conversation.
const EventSessionTitled = "SessionTitled"

// SessionTitled carries a conversation's title.
type SessionTitled struct {
	SessionID string `json:"sessionId"`
	Title     string `json:"title"`
}

// Permission events. A tool call no rule settles becomes PermissionRequested
// and waits for a person; a decision records PermissionGranted or
// PermissionDenied, and PermissionExpired records that none came in time,
// or that the session stopped or the daemon restarted while it waited.
const (
	EventPermissionRequested = "PermissionRequested"
	EventPermissionGranted   = "PermissionGranted"
	EventPermissionDenied    = "PermissionDenied"
	EventPermissionExpired   = "PermissionExpired"
	EventSessionTrusted      = "SessionTrusted"
)

// PermissionOption is one answer the agent offers for a permission request,
// as ACP defines them: allow_once, allow_always, reject_once, reject_always.
type PermissionOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// PermissionRequested records a tool call waiting on a person, with the
// exact input the agent proposed.
type PermissionRequested struct {
	SessionID  string             `json:"sessionId"`
	RequestID  string             `json:"requestId"`
	ToolCallID string             `json:"toolCallId"`
	Title      string             `json:"title"`
	Kind       string             `json:"kind,omitempty"`
	Locations  []string           `json:"locations,omitempty"`
	Input      json.RawMessage    `json:"input,omitempty"`
	Options    []PermissionOption `json:"options"`
}

// PermissionGranted records who allowed a request and which option they chose.
type PermissionGranted struct {
	SessionID string `json:"sessionId"`
	RequestID string `json:"requestId"`
	OptionID  string `json:"optionId"`
	// By is who decided: "local" for the host's CLI, "citadel" for a browser
	// through a citadel, or "trust" for a trusted session.
	By string `json:"by"`
}

// PermissionDenied records who refused a request.
type PermissionDenied struct {
	SessionID string `json:"sessionId"`
	RequestID string `json:"requestId"`
	OptionID  string `json:"optionId,omitempty"`
	By        string `json:"by"`
}

// PermissionExpired records a request nobody answered: Reason is "timeout",
// "stopped", "cancelled", or "restart".
type PermissionExpired struct {
	SessionID string `json:"sessionId"`
	RequestID string `json:"requestId"`
	Reason    string `json:"reason"`
}

// SessionTrusted records whether the session's tool calls are allowed
// without asking from now on.
type SessionTrusted struct {
	SessionID string `json:"sessionId"`
	Trusted   bool   `json:"trusted"`
}

// EventSessionResumed records that the daemon restarted while the session
// was open and brought its agent back. The session's history was never in
// danger: the events are in the host's stream and the transcript is on its
// disk. What the restart cost was the agent process, and this says how it
// came back.
const EventSessionResumed = "SessionResumed"

// How an agent came back after a restart.
const (
	// ResumedByLoad means the agent loaded its own session, so it remembers
	// the conversation itself.
	ResumedByLoad = "load"
	// ResumedBySeed means the agent could not load the session, so a new
	// one was opened and its first prompt carries the transcript so far.
	ResumedBySeed = "seed"
)

// SessionResumed records a session's agent coming back after a daemon
// restart.
type SessionResumed struct {
	SessionID string `json:"sessionId"`
	// RestartedAt is when the daemon came back up.
	RestartedAt time.Time `json:"restartedAt"`
	// How is ResumedByLoad or ResumedBySeed.
	How string `json:"how"`
	// TurnResumed means the daemon stopped in the middle of a turn, so the
	// agent was asked to pick that turn up again. Anything it had asked
	// permission for, it asks again.
	TurnResumed bool `json:"turnResumed,omitempty"`
}

// More session commands.
const (
	CommandSendPrompt      = "SendPrompt"
	CommandStopAgent       = "StopAgent"
	CommandApproveToolCall = "ApproveToolCall"
	CommandDenyToolCall    = "DenyToolCall"
	CommandTrustSession    = "TrustSession"
)

// ApproveToolCall answers a waiting request with an allow option. An empty
// OptionID picks the agent's first allow option.
type ApproveToolCall struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	RequestID string `json:"requestId"`
	OptionID  string `json:"optionId,omitempty"`
}

// DenyToolCall answers a waiting request with a reject option.
type DenyToolCall struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	RequestID string `json:"requestId"`
}

// TrustSession allows every tool call in a session without asking, or
// stops doing so.
type TrustSession struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	Trusted   bool   `json:"trusted"`
}

// SendPrompt continues an idle session with another prompt.
type SendPrompt struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	Text      string `json:"text"`
}

// StopAgent ends a session, cancelling any turn in progress.
type StopAgent struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
}

// Changes events. A session's changes sit in its worktree until a person
// commits them, merges the branch into its base, or opens a pull request
// from it, each from the conversation or the host's CLI, and the worktree
// and branch stay until removed.
const (
	EventChangesCommitted  = "ChangesCommitted"
	EventChangesMerged     = "ChangesMerged"
	EventPullRequestOpened = "PullRequestOpened"
	EventWorktreeRemoved   = "WorktreeRemoved"
)

// ChangesCommitted records a commit of everything in the session's
// worktree, on its branch.
type ChangesCommitted struct {
	SessionID string   `json:"sessionId"`
	Commit    string   `json:"commit"`
	Message   string   `json:"message"`
	Files     []string `json:"files,omitempty"`
}

// ChangesMerged records the session's branch merged into its base in the
// registered checkout. Commit is the checkout's new tip.
type ChangesMerged struct {
	SessionID string `json:"sessionId"`
	Into      string `json:"into"`
	Commit    string `json:"commit"`
}

// PullRequestOpened records the session's branch pushed and a pull
// request opened from it.
type PullRequestOpened struct {
	SessionID string `json:"sessionId"`
	URL       string `json:"url"`
	Branch    string `json:"branch"`
}

// WorktreeRemoved records that the session's worktree is gone, and whether
// its branch went with it. A branch with commits its base lacks is kept
// unless the removal was forced.
type WorktreeRemoved struct {
	SessionID     string `json:"sessionId"`
	BranchDeleted bool   `json:"branchDeleted"`
}

// Changes commands.
const (
	CommandCommitChanges   = "CommitChanges"
	CommandMergeChanges    = "MergeChanges"
	CommandOpenPullRequest = "OpenPullRequest"
)

// CommitChanges commits everything in the session's worktree. An empty
// Message uses the conversation's title.
type CommitChanges struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	Message   string `json:"message,omitempty"`
}

// MergeChanges merges the session's branch into its base in the registered
// checkout, which must be on that base. The worktree must be committed.
type MergeChanges struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
}

// OpenPullRequest pushes the session's branch to the repository's origin
// and opens a pull request against its base with gh. Empty Title and Body
// use the conversation's title and first prompt.
type OpenPullRequest struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	Title     string `json:"title,omitempty"`
	Body      string `json:"body,omitempty"`
}

// EventSessionRulesSet records a session's rules replaced, from the
// conversation or the host.
const EventSessionRulesSet = "SessionRulesSet"

// SessionRulesSet carries the whole list.
type SessionRulesSet struct {
	SessionID string `json:"sessionId"`
	Rules     []Rule `json:"rules"`
}

// SetSessionRules replaces a session's rules.
type SetSessionRules struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	Rules     []Rule `json:"rules"`
}

// CommandSetSessionRules is sent to a session by request-reply.
const CommandSetSessionRules = "SetSessionRules"

// Question events. An agent that needs something only a person can tell
// it asks a question: a message and a form of fields, each a choice or
// free text. The question waits like a permission request does, and a
// person answers it from the conversation, the inbox, or the host's CLI,
// or declines it, or it expires.
const (
	EventQuestionAsked    = "QuestionAsked"
	EventQuestionAnswered = "QuestionAnswered"
	EventQuestionExpired  = "QuestionExpired"
)

// QuestionOption is one choice a field offers.
type QuestionOption struct {
	Value       string `json:"value"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
}

// QuestionField is one field of a question's form. Type is string,
// number, integer, boolean, or array for a choice of several. A field
// with options is a choice; one without is free text.
type QuestionField struct {
	Key         string           `json:"key"`
	Title       string           `json:"title,omitempty"`
	Description string           `json:"description,omitempty"`
	Type        string           `json:"type"`
	Options     []QuestionOption `json:"options,omitempty"`
	Required    bool             `json:"required,omitempty"`
}

// QuestionAsked records a question waiting on a person.
type QuestionAsked struct {
	SessionID  string          `json:"sessionId"`
	QuestionID string          `json:"questionId"`
	ToolCallID string          `json:"toolCallId,omitempty"`
	Message    string          `json:"message"`
	Fields     []QuestionField `json:"fields"`
}

// QuestionAnswered records the answers, by field key, or that the person
// declined to answer. By is "local" or "citadel".
type QuestionAnswered struct {
	SessionID  string          `json:"sessionId"`
	QuestionID string          `json:"questionId"`
	Answers    json.RawMessage `json:"answers,omitempty"`
	Declined   bool            `json:"declined,omitempty"`
	By         string          `json:"by"`
}

// QuestionExpired records a question nobody answered: Reason is
// "timeout", "stopped", "cancelled", or "restart".
type QuestionExpired struct {
	SessionID  string `json:"sessionId"`
	QuestionID string `json:"questionId"`
	Reason     string `json:"reason"`
}

// CommandAnswerQuestion is sent to a session by request-reply.
const CommandAnswerQuestion = "AnswerQuestion"

// AnswerQuestion answers a waiting question with values by field key, or
// declines it.
type AnswerQuestion struct {
	ID         string          `json:"id"`
	SessionID  string          `json:"sessionId"`
	QuestionID string          `json:"questionId"`
	Answers    json.RawMessage `json:"answers,omitempty"`
	Decline    bool            `json:"decline,omitempty"`
}

// EventTurnCancelled records that a person interrupted the turn in
// progress. The session stays open: the agent stops, TurnEnded follows
// with the stop reason "cancelled", and the next prompt steers it. By is
// "local" or "citadel".
const EventTurnCancelled = "TurnCancelled"

// TurnCancelled carries who interrupted the turn.
type TurnCancelled struct {
	SessionID string `json:"sessionId"`
	By        string `json:"by"`
}

// CommandCancelTurn is sent to a session by request-reply, or over the
// host's socket.
const CommandCancelTurn = "CancelTurn"

// CancelTurn interrupts the turn in progress without ending the session.
type CancelTurn struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
}
