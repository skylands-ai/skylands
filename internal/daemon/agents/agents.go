// Package agents launches agents as child processes and drives them over
// the Agent Client Protocol on stdio. Each agent type sits behind an
// Adapter, so adding one never changes the daemon core.
package agents

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
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
}

// RunPrompt launches the adapter's agent in cwd, sends one prompt, reports
// what happens to obs, and returns the stop reason when the turn ends. The
// agent process is stopped before it returns, and cancelling ctx stops it
// early. A non-empty model is passed through the adapter's ModelEnv.
func RunPrompt(ctx context.Context, ad Adapter, cwd, prompt, model string, obs Observer) (string, error) {
	if len(ad.Command) == 0 {
		return "", fmt.Errorf("adapter %q has no command", ad.Name)
	}
	cmd := exec.CommandContext(ctx, ad.Command[0], ad.Command[1:]...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), ad.Env...)
	if model != "" && ad.ModelEnv != "" {
		cmd.Env = append(cmd.Env, ad.ModelEnv+"="+model)
	}
	stderr := &tailBuffer{limit: 4096}
	cmd.Stderr = stderr

	h := &handler{cwd: cwd, obs: obs, calls: map[acp.ToolCallId]ToolCall{}}
	h.seq = newSequencer(h.update)
	client, err := acp.Connect(ctx, &processTransport{cmd: cmd}, h)
	if err != nil {
		return "", fmt.Errorf("start %s agent: %w", ad.Name, err)
	}
	defer func() { _ = client.Close() }()
	fail := func(step string, err error) error {
		if msg := stderr.String(); msg != "" {
			return fmt.Errorf("%s: %w (agent said: %s)", step, err, msg)
		}
		return fmt.Errorf("%s: %w", step, err)
	}

	init, err := client.Initialize(ctx, &acp.InitializeRequest{
		ProtocolVersion:    acp.ProtocolVersion(1),
		ClientInfo:         &acp.Implementation{Name: "skylands", Version: "dev"},
		ClientCapabilities: &acp.ClientCapabilities{},
	})
	if err != nil {
		return "", fail("initialize "+ad.Name, err)
	}
	name, version := ad.Name, ""
	if init.AgentInfo != nil {
		name, version = init.AgentInfo.Name, init.AgentInfo.Version
	}
	obs.Spawned(name, version)

	sess, err := client.NewSession(ctx, &acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{}})
	if err != nil {
		return "", fail("open session", err)
	}
	resp, err := client.Prompt(ctx, &acp.PromptRequest{SessionID: sess.SessionID, Prompt: []acp.ContentBlock{acp.TextContentBlock(prompt)}})
	if err != nil {
		return "", fail("prompt", err)
	}
	return string(resp.StopReason), nil
}

// handler answers the agent's calls to the client side of ACP.
type handler struct {
	cwd   string
	obs   Observer
	seq   *sequencer
	mu    sync.Mutex
	calls map[acp.ToolCallId]ToolCall
}

// Update receives session updates, possibly concurrently and out of order,
// and lets the sequencer hand them on in the order the agent sent them.
func (h *handler) Update(_ context.Context, n *acp.SessionNotification) error {
	h.seq.Deliver(n)
	return nil
}

func (h *handler) update(n *acp.SessionNotification) {
	u := n.Update
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

// RequestPermission allows every tool call for now. Rules and signed
// approvals replace this once the Session aggregate records
// PermissionRequested.
func (h *handler) RequestPermission(_ context.Context, req *acp.RequestPermissionRequest) (*acp.RequestPermissionResponse, error) {
	var pick *acp.PermissionOption
	for i := range req.Options {
		o := &req.Options[i]
		if o.Kind == acp.PermissionOptionKindAllowOnce {
			pick = o
			break
		}
		if o.Kind == acp.PermissionOptionKindAllowAlways && pick == nil {
			pick = o
		}
	}
	if pick == nil && len(req.Options) > 0 {
		pick = &req.Options[0]
	}
	if pick == nil {
		return &acp.RequestPermissionResponse{Outcome: acp.CancelledRequestPermissionOutcome()}, nil
	}
	title := ""
	if req.ToolCall.Title != nil {
		title = *req.ToolCall.Title
	}
	slog.Info("agents: permission auto-allowed", "tool", title, "option", pick.Name)
	return &acp.RequestPermissionResponse{Outcome: acp.SelectedRequestPermissionOutcome(pick.OptionID)}, nil
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
var _ = errors.New
