// Package fakeagent is a scripted agent that speaks ACP over stdio. It reads
// a few files in its working directory, edits one, reports a plan and its
// context use, and ends its turn. Tests and demos use it in place of a real
// agent, so nothing needs API keys.
package fakeagent

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nuid"
	"github.com/skylands-ai/skylands/internal/titles"
	"github.com/spachava753/acp-sdk/acp"
)

// EnvFlag, when set to 1, makes TestMainHook run the fake agent instead of
// the tests.
const EnvFlag = "SKYLANDS_FAKE_AGENT"

// EnvDelay overrides the pause between the agent's steps, as a Go duration.
const EnvDelay = "SKYLANDS_FAKE_AGENT_DELAY"

// EnvNoLoad, when set to 1, makes the fake agent one that cannot load a
// session, so a daemon restart has to seed a new one with the transcript.
const EnvNoLoad = "SKYLANDS_FAKE_AGENT_NOLOAD"

// ReplayedLine is what the fake agent says while loading a session: the
// history it replays, which the daemon must keep out of the transcript.
const ReplayedLine = "(history replayed by the agent)"

// Run serves the fake agent over stdio until the client closes the link.
func Run(ctx context.Context) error {
	delay := 150 * time.Millisecond
	if d, err := time.ParseDuration(os.Getenv(EnvDelay)); err == nil {
		delay = d
	}
	return acp.RunAgent(ctx, &acp.StdioTransport{}, func(conn *acp.AgentConnection) any {
		return &agent{conn: conn, delay: delay, noLoad: os.Getenv(EnvNoLoad) == "1", sessions: map[acp.SessionId]*session{}}
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
	// turns numbers the prompts so far, so each turn's tool calls get fresh ids.
	turns int
}

type agent struct {
	conn   *acp.AgentConnection
	delay  time.Duration
	noLoad bool
	// canAsk means the client fills in forms, so the agent may ask a person
	// a question.
	canAsk   bool
	mu       sync.Mutex
	sessions map[acp.SessionId]*session
}

func (a *agent) Initialize(_ context.Context, req *acp.InitializeRequest) (*acp.InitializeResponse, error) {
	a.mu.Lock()
	a.canAsk = req.ClientCapabilities != nil && req.ClientCapabilities.Elicitation != nil && req.ClientCapabilities.Elicitation.Form != nil
	a.mu.Unlock()
	return &acp.InitializeResponse{
		ProtocolVersion:   acp.ProtocolVersion(1),
		AgentInfo:         &acp.Implementation{Name: "skylands-fakeagent", Version: "dev"},
		AgentCapabilities: &acp.AgentCapabilities{LoadSession: !a.noLoad},
	}, nil
}

// LoadSession brings back a session from a previous process. The fake
// agent keeps nothing on disk, so any id is accepted, and it replays a
// line of history the way a real agent streams the conversation back.
func (a *agent) LoadSession(ctx context.Context, req *acp.LoadSessionRequest) (*acp.LoadSessionResponse, error) {
	if a.noLoad {
		return nil, fmt.Errorf("this agent cannot load sessions")
	}
	a.mu.Lock()
	a.sessions[req.SessionID] = &session{cwd: req.Cwd}
	a.mu.Unlock()
	if err := a.conn.SessionUpdate(ctx, &acp.SessionNotification{SessionID: req.SessionID, Update: acp.AgentMessageChunkSessionUpdate(acp.TextContentBlock(ReplayedLine))}); err != nil {
		return nil, err
	}
	return &acp.LoadSessionResponse{}, nil
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
func promptText(req *acp.PromptRequest) string {
	asked := ""
	for _, c := range req.Prompt {
		asked += c.Text
	}
	return asked
}

func (a *agent) Prompt(ctx context.Context, req *acp.PromptRequest) (*acp.PromptResponse, error) {
	a.mu.Lock()
	s, ok := a.sessions[req.SessionID]
	if ok {
		s.turns++
	}
	turn := 0
	if ok {
		turn = s.turns
	}
	a.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("unknown session %s", req.SessionID)
	}
	send := func(u acp.SessionUpdate) error {
		return a.conn.SessionUpdate(ctx, &acp.SessionNotification{SessionID: req.SessionID, Update: u})
	}
	if asked := promptText(req); titles.IsRequest(asked) {
		// A title request gets a title and nothing else, like a well-behaved agent.
		if err := send(acp.AgentMessageChunkSessionUpdate(acp.TextContentBlock(titles.Fake(titles.RequestedFor(asked))))); err != nil {
			return nil, err
		}
		return &acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
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
		id := acp.ToolCallId(fmt.Sprintf("t%d-read-%d", turn, i))
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
	// Asked to, the agent asks which file to edit, the way a real agent
	// asks a clarifying question: a choice, and a box for anything else.
	chosen, note, declined := "", "", false
	a.mu.Lock()
	canAsk := a.canAsk
	a.mu.Unlock()
	if canAsk && len(files) > 0 && strings.Contains(strings.ToLower(promptText(req)), "ask me") {
		options := make([]acp.EnumOption, 0, len(files))
		for _, f := range files {
			options = append(options, acp.EnumOption{Const: f, Title: f})
		}
		fileTitle, otherTitle, otherDesc := "File", "Other", "Type your own answer, or add a note to the option you chose above (optional)."
		resp, err := a.conn.CreateElicitation(ctx, &acp.CreateElicitationRequest{
			Mode: acp.CreateElicitationRequestTypeForm, SessionID: req.SessionID, Message: "Which file should I edit?",
			RequestedSchema: acp.ElicitationSchema{Type: acp.ElicitationSchemaTypeObject, Properties: map[string]acp.ElicitationPropertySchema{
				"question_0":        {Type: acp.ElicitationPropertySchemaTypeString, Title: &fileTitle, OneOf: &options},
				"question_0_custom": {Type: acp.ElicitationPropertySchemaTypeString, Title: &otherTitle, Description: &otherDesc},
			}},
		})
		if err != nil {
			return nil, err
		}
		switch resp.Action {
		case acp.CreateElicitationResponseTypeAccept:
			if resp.Content != nil {
				var pick string
				_ = json.Unmarshal((*resp.Content)["question_0"], &pick)
				for _, f := range files {
					if f == pick {
						chosen = f
					}
				}
				_ = json.Unmarshal((*resp.Content)["question_0_custom"], &note)
			}
		case acp.CreateElicitationResponseTypeDecline:
			declined = true
		default:
			return done()
		}
	}
	edited := ""
	refused := false
	if len(files) > 0 {
		if !pause() {
			return done()
		}
		edited = files[0]
		if chosen != "" {
			edited = chosen
		}
		id := acp.ToolCallId(fmt.Sprintf("t%d-edit-0", turn))
		if err := send(toolCall(id, "Edit "+edited, acp.ToolKindEdit, filepath.Join(s.cwd, edited), acp.ToolCallStatusPending)); err != nil {
			return nil, err
		}
		// An edit needs permission, like a real agent's would.
		title := "Edit " + edited
		kind := acp.ToolKindEdit
		locs := []acp.ToolCallLocation{{Path: filepath.Join(s.cwd, edited)}}
		perm, err := a.conn.RequestPermission(ctx, &acp.RequestPermissionRequest{
			SessionID: req.SessionID,
			ToolCall:  acp.ToolCallUpdate{ToolCallID: id, Title: &title, Kind: &kind, Locations: &locs, RawInput: map[string]any{"path": edited, "change": "rewrite"}},
			Options: []acp.PermissionOption{
				{OptionID: "allow", Name: "Allow", Kind: acp.PermissionOptionKindAllowOnce},
				{OptionID: "always", Name: "Always allow", Kind: acp.PermissionOptionKindAllowAlways},
				{OptionID: "deny", Name: "Deny", Kind: acp.PermissionOptionKindRejectOnce},
			},
		})
		if err != nil {
			return nil, err
		}
		allowed := perm.Outcome.Outcome == acp.RequestPermissionOutcomeTypeSelected && (perm.Outcome.OptionID == "allow" || perm.Outcome.OptionID == "always")
		if !allowed {
			if err := send(toolCallUpdate(id, acp.ToolCallStatusFailed)); err != nil {
				return nil, err
			}
			edited = ""
			refused = true
		} else {
			if err := send(toolCallUpdate(id, acp.ToolCallStatusInProgress)); err != nil {
				return nil, err
			}
			if !pause() {
				return done()
			}
			if err := send(toolCallUpdate(id, acp.ToolCallStatusCompleted)); err != nil {
				return nil, err
			}
		}
	}

	plan[1].Status = acp.PlanEntryStatusCompleted
	plan[2].Status = acp.PlanEntryStatusCompleted
	if err := send(acp.PlanSessionUpdate(plan)); err != nil {
		return nil, err
	}
	request, context := requestOf(promptText(req))
	msg := fmt.Sprintf("Done with %q. I read %d files", request, n)
	if edited != "" {
		msg += " and edited " + edited
	} else if refused {
		msg += " and was not allowed to edit"
	}
	msg += ". Every tracked file was counted, every hidden one was skipped, and nothing was changed on disk."
	if context > 0 {
		msg += fmt.Sprintf(" The request came with %d lines of context.", context)
	}
	switch {
	case declined && len(files) > 0:
		msg += " You declined to choose, so I took " + files[0] + "."
	case chosen != "" && note != "":
		msg += " You chose " + chosen + " and added: " + note + "."
	case chosen != "":
		msg += " You chose " + chosen + "."
	}
	// Stream the answer in small chunks with no pause, as real agents do.
	for _, chunk := range chunks(msg, 6) {
		if err := send(acp.AgentMessageChunkSessionUpdate(acp.TextContentBlock(chunk))); err != nil {
			return nil, err
		}
	}
	return &acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
}

// requestOf reads a prompt the way the fake agent does: the request is its
// last line, and whatever came before is context, such as the transcript a
// daemon seeds a new session with after a restart.
func requestOf(prompt string) (request string, contextLines int) {
	lines := strings.Split(strings.TrimSpace(prompt), "\n")
	request = strings.TrimSpace(lines[len(lines)-1])
	for _, l := range lines[:len(lines)-1] {
		if strings.TrimSpace(l) != "" {
			contextLines++
		}
	}
	return request, contextLines
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
