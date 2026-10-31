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
	"strings"
	"sync"

	"github.com/spachava753/acp-sdk/acp"

	v1 "github.com/skylands-ai/skylands/api/v1"
)

// Adapter describes how to launch one agent type.
type Adapter struct {
	// Name is what a StartSession command asks for, such as "claude".
	Name string
	// Provider colors the airship: claude, codex, gemini, or local.
	Provider string
	// Command runs the ACP agent, with the worktree as its working directory.
	Command []string
	// Env adds to the daemon's environment for the agent process.
	Env []string
	// ModelEnv is the environment variable that picks the agent's model,
	// such as ANTHROPIC_MODEL for Claude Code. Empty means the adapter has
	// no model choice.
	ModelEnv string
}

// Registry is the adapters a daemon can launch, by name.
type Registry map[string]Adapter

// DefaultRegistry holds the fake agent built into the skylands binary and
// the Claude Code adapter from npm.
func DefaultRegistry() Registry {
	exe, err := os.Executable()
	if err != nil {
		exe = "skylands"
	}
	return Registry{
		"fake":   {Name: "fake", Provider: "local", Command: []string{exe, "fakeagent"}},
		"claude": {Name: "claude", Provider: "claude", Command: []string{"npx", "-y", "@agentclientprotocol/claude-agent-acp"}, ModelEnv: "ANTHROPIC_MODEL"},
	}
}

// ToolCall is a tool call as last reported by the agent.
type ToolCall struct {
	ID     string
	Title  string
	Kind   string
	Status string
	// Locations are the files the call touches, relative to the worktree
	// when inside it.
	Locations []string
}

// Observer receives what the agent reports during a prompt.
type Observer interface {
	// Spawned runs once the agent has identified itself.
	Spawned(name, version string)
	// ToolCall runs for a new tool call and for each update to one. Status
	// tells which: pending or in_progress at the start, then completed or
	// failed.
	ToolCall(tc ToolCall)
	Plan(entries []v1.PlanEntry)
	Usage(used, size uint64)
	// Message receives agent text, which stays on the host.
	Message(text string)
	// Permission decides a tool call the agent asks about. It may block
	// until a person answers; ctx ends if the agent gives up waiting.
	Permission(ctx context.Context, req PermissionRequest) Decision
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
		h.calls[u.ToolCallID] = tc
		h.mu.Unlock()
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
func (h *handler) RequestPermission(ctx context.Context, req *acp.RequestPermissionRequest) (*acp.RequestPermissionResponse, error) {
	h.mu.Lock()
	_, side := h.side[req.SessionID]
	h.mu.Unlock()
	if side {
		for _, o := range req.Options {
			if o.Kind == acp.PermissionOptionKindRejectOnce || o.Kind == acp.PermissionOptionKindRejectAlways {
				return &acp.RequestPermissionResponse{Outcome: acp.SelectedRequestPermissionOutcome(o.OptionID)}, nil
			}
		}
		return &acp.RequestPermissionResponse{Outcome: acp.CancelledRequestPermissionOutcome()}, nil
	}
	u := req.ToolCall
	tc := ToolCall{ID: string(u.ToolCallID), Status: "pending"}
	if u.Title != nil {
		tc.Title = *u.Title
	}
	if u.Kind != nil {
		tc.Kind = string(*u.Kind)
	}
	if u.Locations != nil {
		tc.Locations = h.relative(*u.Locations)
	}
	var input json.RawMessage
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
