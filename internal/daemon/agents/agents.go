// Package agents launches agents as child processes and drives them over
// the Agent Client Protocol on stdio. Each agent type sits behind an
// Adapter, so adding one never changes the daemon core.
package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/spachava753/acp-sdk/acp"

	v1 "github.com/skylands-ai/skylands/api/v1"
)

// Adapter describes how to launch one agent type.
type Adapter struct {
	// Name is what a StartSession command asks for, such as "claude".
	Name string
	// Provider colors the airship: claude, codex, gemini, goose, or local.
	Provider string
	// Command runs the ACP agent, with the worktree as its working directory.
	Command []string
	// Env adds to the daemon's environment for the agent process.
	Env []string
	// Mode is the value every session is set to in the agent's mode
	// setting: the ACP session config option in the "mode" category. Empty
	// leaves the agent's own. A session the agent will not set it for does
	// not start.
	Mode string
	// Meta goes in the _meta of every session the agent opens or loads,
	// for an agent that takes settings there that ACP has no field for.
	Meta acp.Meta
	// Login tells a person how to log the agent in, for an agent that
	// reports its login.
	Login string
	// Install tells a person how to install the agent when its command is
	// not on PATH. Empty for an agent that comes with its command, such as
	// one that npx fetches.
	Install string
	// BuiltIn marks the fake agent inside the skylands binary, which needs
	// nothing installed, so it does not count as an agent the host has.
	BuiltIn bool
	// Instructions are added to the agent's own instructions for every
	// session it opens or loads, through ACP's session system prompt in
	// append mode. An agent that does not advertise the system prompt
	// does not get them.
	Instructions string
}

// Registry is the adapters a daemon can launch, by name.
type Registry map[string]Adapter

// ClaudeAdapterPackage is the npm package, at an exact version, that runs
// Claude Code over ACP. It is pinned because the adapter publishes several
// preview builds a day, and an unpinned npx would start every new session,
// and every resume after a daemon restart, on whatever was published last.
// Move it on purpose, after a session with the new version works.
const ClaudeAdapterPackage = "@agentclientprotocol/claude-agent-acp@0.100.0"

// DefaultRegistry holds the fake agent built into the skylands binary, the
// Claude Code and Codex adapters from npm, and goose as installed on the
// host. Each real agent is set to a mode in which it asks before acting.
func DefaultRegistry() Registry {
	exe, err := os.Executable()
	if err != nil {
		exe = "skylands"
	}
	return Registry{
		"fake": {Name: "fake", Provider: "local", Command: []string{exe, "fakeagent"}, BuiltIn: true},
		"claude": {Name: "claude", Provider: "claude", Command: []string{"npx", "-y", ClaudeAdapterPackage},
			Mode: claudeMode, Meta: claudeMeta(),
			Login: "run claude and log in with /login, or set ANTHROPIC_API_KEY"},
		"codex": {Name: "codex", Provider: "codex", Command: []string{"npx", "-y", CodexAdapterPackage},
			Mode: codexMode, Instructions: codexInstructions,
			Login: "run npx -y " + CodexAdapterPackage + " login, or codex login if the Codex CLI is installed"},
		"goose": {Name: "goose", Provider: "goose", Command: []string{"goose", "acp"}, Mode: gooseMode, Install: gooseInstall},
	}
}

// ToolCall is a tool call as last reported by the agent, plus what the
// latest report said about its result.
type ToolCall struct {
	ID     string
	Title  string
	Kind   string
	Status string
	// Locations are the files the call touches, relative to the worktree
	// when inside it.
	Locations []string
	// Input is the exact tool input as JSON, as the agent last reported it.
	// It is nil until the agent has said, and an agent that streams the
	// input reports an empty object first and the rest once it knows it.
	Input json.RawMessage
	// Content is what this report says the call produced, which replaces
	// what an earlier report said. It is nil when the report says nothing
	// about the result, which is not the same as an empty result.
	Content []v1.ToolCallContent
	// Output is the raw result this report carries, as text, when the agent
	// gave one.
	Output string
}

// InputKnown reports whether the agent has said what the call's input is:
// either it never reports inputs, or it reported one that is not the empty
// object an agent sends while the input still streams.
func (tc ToolCall) InputKnown() bool {
	if tc.Input == nil {
		return true
	}
	trimmed := bytes.TrimSpace(tc.Input)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return false
	}
	if trimmed[0] == '{' {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(trimmed, &fields); err == nil && len(fields) == 0 {
			return false
		}
	}
	return true
}

// Finished reports whether the call is over, well or badly.
func (tc ToolCall) Finished() bool {
	return tc.Status == string(acp.ToolCallStatusCompleted) || tc.Status == string(acp.ToolCallStatusFailed)
}

// Observer receives what the agent reports during a prompt.
type Observer interface {
	// Spawned runs once the agent has identified itself and opened its
	// session. agentSessionID is the agent's own id for it, which Resume
	// asks the agent to load again after a restart.
	Spawned(name, version, agentSessionID string)
	// ToolCall runs for a new tool call and for each update to one. Status
	// tells which: pending or in_progress at the start, then completed or
	// failed. The call's identity, input, and locations are as last
	// reported; its Content and Output are what this report carried.
	ToolCall(tc ToolCall)
	Plan(entries []v1.PlanEntry)
	// Usage is a reading of the context window: used tokens of size.
	Usage(used, size uint64)
	// Tokens is what one request cost, once the agent has answered it: a
	// turn's, or the title request's. purpose is v1.TokensForTurn or
	// v1.TokensForTitle. Agents that count nothing never call it.
	Tokens(purpose string, t v1.TokenUsage)
	// Message receives agent text, which stays on the host.
	Message(text string)
	// Permission decides a tool call the agent asks about. It may block
	// until a person answers; ctx ends if the agent gives up waiting.
	Permission(ctx context.Context, req PermissionRequest) Decision
	// Question answers a question the agent asks a person. It may block
	// until someone answers; ctx ends if the agent gives up waiting.
	Question(ctx context.Context, q Question) Answer
}

// Question is what an agent asks a person: a message and the form it
// wants filled in.
type Question struct {
	// ToolCallID is the tool call the question belongs to, when the agent
	// says.
	ToolCallID string
	Message    string
	Fields     []v1.QuestionField
}

// Answer is a person's reply to a question: values by field key as JSON,
// or a refusal, or nothing because nobody answered in time.
type Answer struct {
	Answers   json.RawMessage
	Declined  bool
	Cancelled bool
}

// PermissionRequest is a tool call the agent will not make without an
// answer, with the options it offers.
type PermissionRequest struct {
	ToolCall ToolCall
	// Input is the exact tool input the agent proposed, as JSON.
	Input   json.RawMessage
	Options []v1.PermissionOption
}

// Decision answers a permission request: the chosen option, or cancelled.
type Decision struct {
	OptionID  string
	Cancelled bool
}

// handler answers the agent's calls to the client side of ACP.
type handler struct {
	// ExtensionMux receives the agent's underscore-prefixed notifications,
	// such as its login report.
	*acp.ExtensionMux
	// auth gets each login the agent reports, newest kept.
	auth chan AuthStatus
	// read counts the session updates the transport has read from the
	// agent, which catchUp waits for the sequencer to deliver.
	read  *atomic.Uint64
	cwd   string
	obs   Observer
	seq   *sequencer
	mu    sync.Mutex
	calls map[acp.ToolCallId]ToolCall
	// side collects the text of side sessions, such as a title request,
	// which the observer never sees.
	side map[acp.SessionId]*strings.Builder
}

// aside starts collecting the text of a session instead of reporting it.
func (h *handler) aside(id acp.SessionId) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.side == nil {
		h.side = map[acp.SessionId]*strings.Builder{}
	}
	h.side[id] = &strings.Builder{}
}

// collected returns a side session's text and stops collecting.
func (h *handler) collected(id acp.SessionId) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	b := h.side[id]
	delete(h.side, id)
	if b == nil {
		return ""
	}
	return b.String()
}

// Update receives session updates, possibly concurrently and out of order,
// and lets the sequencer hand them on in the order the agent sent them.
func (h *handler) Update(_ context.Context, n *acp.SessionNotification) error {
	h.seq.Deliver(n)
	return nil
}

func (h *handler) update(n *acp.SessionNotification) {
	u := n.Update
	h.mu.Lock()
	b := h.side[n.SessionID]
	h.mu.Unlock()
	if b != nil {
		if u.SessionUpdate == acp.SessionUpdateTypeAgentMessageChunk {
			h.mu.Lock()
			b.WriteString(contentText(u.Content))
			h.mu.Unlock()
		}
		return
	}
	switch u.SessionUpdate {
	case acp.SessionUpdateTypeToolCall, acp.SessionUpdateTypeToolCallUpdate:
		h.mu.Lock()
		tc := h.calls[u.ToolCallID]
		tc.ID = string(u.ToolCallID)
		if u.Title != nil {
			tc.Title = *u.Title
		}
		if u.Kind != nil {
			tc.Kind = string(*u.Kind)
		}
		if u.Status != nil {
			tc.Status = string(*u.Status)
		} else if tc.Status == "" {
			tc.Status = string(acp.ToolCallStatusPending)
		}
		if u.Locations != nil {
			tc.Locations = h.relative(*u.Locations)
		}
		if u.RawInput != nil {
			if data, err := json.Marshal(u.RawInput); err == nil {
				tc.Input = data
			}
		}
		// What the call produced is handed on with this report and not
		// kept, so a long session's outputs do not pile up in memory.
		h.calls[u.ToolCallID] = tc
		h.mu.Unlock()
		tc.Content = h.toolContent(u.Content)
		tc.Output = rawText(u.RawOutput)
		h.obs.ToolCall(tc)
	case acp.SessionUpdateTypePlan:
		entries := make([]v1.PlanEntry, 0, len(u.Entries))
		for _, e := range u.Entries {
			entries = append(entries, v1.PlanEntry{Content: e.Content, Priority: string(e.Priority), Status: string(e.Status)})
		}
		h.obs.Plan(entries)
	case acp.SessionUpdateTypeUsageUpdate:
		h.obs.Usage(u.Used, u.Size)
	case acp.SessionUpdateTypeAgentMessageChunk:
		if text := contentText(u.Content); text != "" {
			h.obs.Message(text)
		}
	}
}

// RequestPermission hands a tool call the agent asks about to the
// observer, which applies the host's rules or waits for a person. A side
// session, such as a title request, is refused: it was told not to use
// tools.
// catchUp waits until every session update read from the agent so far has
// been handled. A request from the agent, such as one for permission,
// follows on the wire the updates it sent before it, but the SDK handles
// requests and updates concurrently, so without this a request can
// overtake the reports that came first: an edit asked about would go on
// the record before a read the agent had already reported.
func (h *handler) catchUp() {
	if h.read == nil {
		return
	}
	deadline := time.Now().Add(3 * time.Second)
	for h.seq.Delivered() < h.read.Load() && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
}

func (h *handler) RequestPermission(ctx context.Context, req *acp.RequestPermissionRequest) (*acp.RequestPermissionResponse, error) {
	h.mu.Lock()
	_, side := h.side[req.SessionID]
	h.mu.Unlock()
	h.catchUp()
	if side {
		for _, o := range req.Options {
			if o.Kind == acp.PermissionOptionKindRejectOnce || o.Kind == acp.PermissionOptionKindRejectAlways {
				return &acp.RequestPermissionResponse{Outcome: acp.SelectedRequestPermissionOutcome(o.OptionID)}, nil
			}
		}
		return &acp.RequestPermissionResponse{Outcome: acp.CancelledRequestPermissionOutcome()}, nil
	}
	u := req.ToolCall
	// A request about a call the agent already reported may say only what
	// changed, as Codex does when asking to run a command it has started,
	// so what it leaves out is what the agent last said about the call.
	h.mu.Lock()
	known := h.calls[u.ToolCallID]
	h.mu.Unlock()
	tc := ToolCall{ID: string(u.ToolCallID), Status: "pending", Title: known.Title, Kind: known.Kind, Locations: known.Locations}
	if u.Title != nil {
		tc.Title = *u.Title
	}
	if u.Kind != nil {
		tc.Kind = string(*u.Kind)
	}
	if u.Locations != nil {
		tc.Locations = h.relative(*u.Locations)
	}
	input := known.Input
	if u.RawInput != nil {
		if data, err := json.Marshal(u.RawInput); err == nil {
			input = data
		}
	}
	options := make([]v1.PermissionOption, 0, len(req.Options))
	for _, o := range req.Options {
		options = append(options, v1.PermissionOption{ID: string(o.OptionID), Name: o.Name, Kind: string(o.Kind)})
	}
	d := h.obs.Permission(ctx, PermissionRequest{ToolCall: tc, Input: input, Options: options})
	if d.Cancelled || d.OptionID == "" {
		return &acp.RequestPermissionResponse{Outcome: acp.CancelledRequestPermissionOutcome()}, nil
	}
	return &acp.RequestPermissionResponse{Outcome: acp.SelectedRequestPermissionOutcome(acp.PermissionOptionId(d.OptionID))}, nil
}

// CreateElicitation hands a question the agent asks to the observer, which
// waits for a person. A side session, such as a title request, is refused,
// and so is a URL elicitation, which Skylands cannot open for anyone.
func (h *handler) CreateElicitation(ctx context.Context, req *acp.CreateElicitationRequest) (*acp.CreateElicitationResponse, error) {
	h.mu.Lock()
	_, side := h.side[req.SessionID]
	h.mu.Unlock()
	h.catchUp()
	if side || req.Mode != acp.CreateElicitationRequestTypeForm {
		return &acp.CreateElicitationResponse{Action: acp.CreateElicitationResponseTypeDecline}, nil
	}
	q := Question{Message: req.Message, Fields: formFields(req.RequestedSchema)}
	if req.ToolCallID != nil {
		q.ToolCallID = string(*req.ToolCallID)
	}
	a := h.obs.Question(ctx, q)
	switch {
	case a.Cancelled:
		return &acp.CreateElicitationResponse{Action: acp.CreateElicitationResponseTypeCancel}, nil
	case a.Declined:
		return &acp.CreateElicitationResponse{Action: acp.CreateElicitationResponseTypeDecline}, nil
	}
	content := map[string]acp.ElicitationContentValue{}
	if err := json.Unmarshal(a.Answers, &content); err != nil {
		return &acp.CreateElicitationResponse{Action: acp.CreateElicitationResponseTypeCancel}, nil
	}
	return &acp.CreateElicitationResponse{Action: acp.CreateElicitationResponseTypeAccept, Content: &content}, nil
}

// formFields reads a form's schema into fields. The schema's properties
// are a map, so the fields come out in key order, which keeps a question
// and its "other" box together.
func formFields(schema acp.ElicitationSchema) []v1.QuestionField {
	keys := make([]string, 0, len(schema.Properties))
	for k := range schema.Properties {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	required := map[string]bool{}
	if schema.Required != nil {
		for _, k := range *schema.Required {
			required[k] = true
		}
	}
	fields := make([]v1.QuestionField, 0, len(keys))
	for _, k := range keys {
		p := schema.Properties[k]
		f := v1.QuestionField{Key: k, Type: string(p.Type), Required: required[k]}
		if f.Type == "" {
			f.Type = "string"
		}
		if p.Title != nil {
			f.Title = *p.Title
		}
		if p.Description != nil {
			f.Description = *p.Description
		}
		switch {
		case p.OneOf != nil:
			f.Options = enumOptions(*p.OneOf)
		case p.Enum != nil:
			f.Options = plainOptions(*p.Enum)
		case len(p.Items.AnyOf) > 0:
			f.Options = enumOptions(p.Items.AnyOf)
		case len(p.Items.Enum) > 0:
			f.Options = plainOptions(p.Items.Enum)
		}
		fields = append(fields, f)
	}
	return fields
}

func enumOptions(opts []acp.EnumOption) []v1.QuestionOption {
	out := make([]v1.QuestionOption, 0, len(opts))
	for _, o := range opts {
		q := v1.QuestionOption{Value: o.Const, Title: o.Title}
		if o.Description != nil {
			q.Description = *o.Description
		}
		out = append(out, q)
	}
	return out
}

func plainOptions(values []string) []v1.QuestionOption {
	out := make([]v1.QuestionOption, 0, len(values))
	for _, v := range values {
		out = append(out, v1.QuestionOption{Value: v})
	}
	return out
}

// relative turns absolute paths inside the worktree into repo-relative
// ones, which is how buildings are named.
func (h *handler) relative(locs []acp.ToolCallLocation) []string {
	out := make([]string, 0, len(locs))
	for _, l := range locs {
		p := l.Path
		if filepath.IsAbs(p) {
			if rel, err := filepath.Rel(h.cwd, p); err == nil && !filepath.IsAbs(rel) && rel != ".." && !hasPrefix(rel, ".."+string(filepath.Separator)) {
				p = rel
			}
		}
		out = append(out, filepath.ToSlash(p))
	}
	return out
}

func hasPrefix(s, prefix string) bool { return len(s) >= len(prefix) && s[:len(prefix)] == prefix }

// toolContent reads what a tool call report says the call produced. A
// first report carries a slice, an update a pointer to one; nil means the
// report said nothing about it.
func (h *handler) toolContent(c any) []v1.ToolCallContent {
	var items []acp.ToolCallContent
	switch v := c.(type) {
	case []acp.ToolCallContent:
		items = v
	case *[]acp.ToolCallContent:
		if v == nil {
			return nil
		}
		items = *v
	default:
		return nil
	}
	out := make([]v1.ToolCallContent, 0, len(items))
	for _, it := range items {
		switch it.Type {
		case acp.ToolCallContentTypeDiff:
			out = append(out, v1.ToolCallContent{Type: "diff", Path: h.relative([]acp.ToolCallLocation{{Path: it.Path}})[0], OldText: it.OldText, NewText: it.NewText})
		case acp.ToolCallContentTypeTerminal:
			out = append(out, v1.ToolCallContent{Type: "terminal", TerminalID: string(it.TerminalID)})
		default:
			b := it.Content
			switch b.Type {
			case acp.ContentBlockTypeText, "":
				out = append(out, v1.ToolCallContent{Type: "text", Text: b.Text})
			default:
				// An image, audio, or resource: say what it was, not what
				// it held.
				desc := string(b.Type)
				if b.MimeType != nil {
					desc += " " + *b.MimeType
				}
				if b.URI != nil {
					desc += " " + *b.URI
				} else if b.Name != "" {
					desc += " " + b.Name
				}
				out = append(out, v1.ToolCallContent{Type: string(b.Type), Text: desc})
			}
		}
	}
	return out
}

// rawText renders an agent's raw tool output as text: a string as itself,
// anything else as JSON.
func rawText(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return ""
	}
	return string(data)
}

func contentText(c any) string {
	switch v := c.(type) {
	case acp.ContentBlock:
		return v.Text
	case *acp.ContentBlock:
		if v != nil {
			return v.Text
		}
	case map[string]any:
		if t, ok := v["text"].(string); ok {
			return t
		}
	}
	return ""
}

// tailBuffer keeps the last limit bytes written to it.
type tailBuffer struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	limit int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf.Write(p)
	if t.buf.Len() > t.limit {
		t.buf.Next(t.buf.Len() - t.limit)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(bytes.TrimSpace(t.buf.Bytes()))
}

var _ io.Writer = (*tailBuffer)(nil)
