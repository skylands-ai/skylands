// Package fakeagent is a scripted agent that speaks ACP over stdio. It reads
// a few files in its working directory, edits one, reports a plan and its
// context use, and ends its turn. Tests and demos use it in place of a real
// agent, so nothing needs API keys.
package fakeagent

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nuid"
	"github.com/spachava753/acp-sdk/acp"
)

// EnvFlag, when set to 1, makes TestMainHook run the fake agent instead of
// the tests.
const EnvFlag = "SKYLANDS_FAKE_AGENT"

// EnvDelay overrides the pause between the agent's steps, as a Go duration.
const EnvDelay = "SKYLANDS_FAKE_AGENT_DELAY"

// Run serves the fake agent over stdio until the client closes the link.
func Run(ctx context.Context) error {
	delay := 150 * time.Millisecond
	if d, err := time.ParseDuration(os.Getenv(EnvDelay)); err == nil {
		delay = d
	}
	return acp.RunAgent(ctx, &acp.StdioTransport{}, func(conn *acp.AgentConnection) any {
		return &agent{conn: conn, delay: delay, sessions: map[acp.SessionId]*session{}}
	})
}

// TestMainHook runs the fake agent and exits when the process was started
// as one, so a test binary can play the agent for its own tests.
func TestMainHook() {
	if os.Getenv(EnvFlag) != "1" {
		return
	}
	if err := Run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "fakeagent:", err)
		os.Exit(1)
	}
	os.Exit(0)
}

// TestCommand is the command and environment that run the current binary
// as the fake agent, with short pauses.
func TestCommand() (command []string, env []string) {
	return []string{os.Args[0]}, []string{EnvFlag + "=1", EnvDelay + "=5ms"}
}

type session struct {
	cwd       string
	cancelled bool
}

type agent struct {
	conn     *acp.AgentConnection
	delay    time.Duration
	mu       sync.Mutex
	sessions map[acp.SessionId]*session
}

func (*agent) Initialize(context.Context, *acp.InitializeRequest) (*acp.InitializeResponse, error) {
	return &acp.InitializeResponse{
		ProtocolVersion: acp.ProtocolVersion(1),
		AgentInfo:       &acp.Implementation{Name: "skylands-fakeagent", Version: "dev"},
	}, nil
}

func (a *agent) NewSession(_ context.Context, req *acp.NewSessionRequest) (*acp.NewSessionResponse, error) {
	id := acp.SessionId(nuid.Next())
	a.mu.Lock()
	a.sessions[id] = &session{cwd: req.Cwd}
	a.mu.Unlock()
	return &acp.NewSessionResponse{SessionID: id}, nil
}

func (a *agent) Cancel(_ context.Context, n *acp.CancelNotification) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if s, ok := a.sessions[n.SessionID]; ok {
		s.cancelled = true
	}
	return nil
}

func (a *agent) cancelled(id acp.SessionId) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.sessions[id]
	return ok && s.cancelled
}

// Prompt plays the script: plan, read up to four files, edit the first,
// report usage, say what it did, and end the turn.
func (a *agent) Prompt(ctx context.Context, req *acp.PromptRequest) (*acp.PromptResponse, error) {
	a.mu.Lock()
	s, ok := a.sessions[req.SessionID]
	a.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("unknown session %s", req.SessionID)
	}
	send := func(u acp.SessionUpdate) error {
		return a.conn.SessionUpdate(ctx, &acp.SessionNotification{SessionID: req.SessionID, Update: u})
	}
	pause := func() bool {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(a.delay):
			return !a.cancelled(req.SessionID)
		}
	}
	done := func() (*acp.PromptResponse, error) {
		return &acp.PromptResponse{StopReason: acp.StopReasonCancelled}, nil
	}

	files := someFiles(s.cwd, 4)
	plan := []acp.PlanEntry{
		{Content: "Look at the files that matter", Priority: acp.PlanEntryPriorityHigh, Status: acp.PlanEntryStatusInProgress},
		{Content: "Make the change", Priority: acp.PlanEntryPriorityHigh, Status: acp.PlanEntryStatusPending},
		{Content: "Report back", Priority: acp.PlanEntryPriorityLow, Status: acp.PlanEntryStatusPending},
	}
	if err := send(acp.PlanSessionUpdate(plan)); err != nil {
		return nil, err
	}
	if err := send(acp.UsageUpdateSessionUpdate(12_000, 200_000)); err != nil {
		return nil, err
	}

	n := 0
	for i, f := range files {
		if !pause() {
			return done()
		}
		id := acp.ToolCallId(fmt.Sprintf("read-%d", i))
		if err := send(toolCall(id, "Read "+f, acp.ToolKindRead, filepath.Join(s.cwd, f), acp.ToolCallStatusInProgress)); err != nil {
			return nil, err
		}
		if !pause() {
			return done()
		}
		if err := send(toolCallUpdate(id, acp.ToolCallStatusCompleted)); err != nil {
			return nil, err
		}
		n++
		if err := send(acp.UsageUpdateSessionUpdate(uint64(12_000+(i+1)*9_000), 200_000)); err != nil {
			return nil, err
		}
	}

	plan[0].Status = acp.PlanEntryStatusCompleted
	plan[1].Status = acp.PlanEntryStatusInProgress
	if err := send(acp.PlanSessionUpdate(plan)); err != nil {
		return nil, err
	}
	edited := ""
	if len(files) > 0 {
		if !pause() {
			return done()
		}
		edited = files[0]
		id := acp.ToolCallId("edit-0")
		if err := send(toolCall(id, "Edit "+edited, acp.ToolKindEdit, filepath.Join(s.cwd, edited), acp.ToolCallStatusInProgress)); err != nil {
			return nil, err
		}
		if !pause() {
			return done()
		}
		if err := send(toolCallUpdate(id, acp.ToolCallStatusCompleted)); err != nil {
			return nil, err
		}
	}

	plan[1].Status = acp.PlanEntryStatusCompleted
	plan[2].Status = acp.PlanEntryStatusCompleted
	if err := send(acp.PlanSessionUpdate(plan)); err != nil {
		return nil, err
	}
	msg := fmt.Sprintf("Done. I read %d files", n)
	if edited != "" {
		msg += " and edited " + edited
	}
	msg += ". Every tracked file was counted, every hidden one was skipped, and nothing was changed on disk."
	// Stream the answer in small chunks with no pause, as real agents do.
	for _, chunk := range chunks(msg, 6) {
		if err := send(acp.AgentMessageChunkSessionUpdate(acp.TextContentBlock(chunk))); err != nil {
			return nil, err
		}
	}
	return &acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
}

// chunks splits s into pieces of about n bytes.
func chunks(s string, n int) []string {
	var out []string
	for len(s) > n {
		out = append(out, s[:n])
		s = s[n:]
	}
	return append(out, s)
}

func toolCall(id acp.ToolCallId, title string, kind acp.ToolKind, path string, status acp.ToolCallStatus) acp.SessionUpdate {
	u := acp.ToolCallSessionUpdate(id, title)
	u.Kind = &kind
	u.Status = &status
	locs := []acp.ToolCallLocation{{Path: path}}
	u.Locations = &locs
	return u
}

func toolCallUpdate(id acp.ToolCallId, status acp.ToolCallStatus) acp.SessionUpdate {
	u := acp.ToolCallUpdateSessionUpdate(id)
	u.Status = &status
	return u
}

// someFiles lists up to n regular files under dir, outside .git, sorted by
// path, as paths relative to dir.
func someFiles(dir string, n int) []string {
	var out []string
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		// Hidden entries stay out, including the .git file a worktree has.
		if path != dir && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if d.Type().IsRegular() {
			if rel, err := filepath.Rel(dir, path); err == nil {
				out = append(out, rel)
			}
		}
		return nil
	})
	sort.Strings(out)
	if len(out) > n {
		out = out[:n]
	}
	return out
}
