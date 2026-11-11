// Package fakeagent is a scripted agent that speaks ACP over stdio. It reads
// a few files in its working directory, edits one, reports a plan and its
// context use, and ends its turn with a made-up token count. Tests and
// demos use it in place of a real agent, so nothing needs API keys.
package fakeagent

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/nats-io/nuid"
	"github.com/skylands-ai/skylands/internal/titles"
	"github.com/skylands-ai/skylands/internal/watchdog"
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

// EnvAuth, when set, makes the fake agent report a login the way Claude
// Code's adapter does, with this as its kind, such as "account" or "none"
// for logged out. Unset, it reports no login, like most agents.
const EnvAuth = "SKYLANDS_FAKE_AGENT_AUTH"

// EnvChild, when set to a file path, makes the fake agent start a child
// process that sleeps, the way a real agent's shell tool leaves a command
// running, and write the child's PID to that file. Tests use it to see
// that stopping an agent ends what it started.
const EnvChild = "SKYLANDS_FAKE_AGENT_CHILD"

// EnvSystemPrompt, when set to 1, makes the fake agent advertise ACP's
// session system prompt, and keep the one each session is opened or
// loaded with, which it gives when asked InstructionsQuestion.
const EnvSystemPrompt = "SKYLANDS_FAKE_AGENT_SYSTEM_PROMPT"

// InstructionsQuestion, as a whole prompt, makes the fake agent answer
// with the system prompt its session got, or NoInstructions, and nothing
// else.
const InstructionsQuestion = "what are your instructions?"

// NoInstructions is the fake agent's answer to InstructionsQuestion for a
// session that got no system prompt.
const NoInstructions = "(none)"

// EnvConfigOptions, when set to 1, makes the fake agent offer two ACP
// session config options, a mode and a model, which every session opens
// or loads with at their first values, and which it gives when asked
// SettingsQuestion. Set to "stuck", it takes every change and makes none,
// like an agent that ignores what it is told. Set to "modes", it offers
// the mode alone, through ACP's session modes and session/set_mode rather
// than a config option, as Gemini CLI does, and its sessions have no model.
const EnvConfigOptions = "SKYLANDS_FAKE_AGENT_CONFIG_OPTIONS"

// EnvNoRawInput, when set to 1, makes the fake agent give no raw input
// for its edit, saying what the edit will do in a diff in the request's
// content instead, as Gemini CLI does.
const EnvNoRawInput = "SKYLANDS_FAKE_AGENT_NO_RAW_INPUT"

// EnvModeMarker, when set, makes the fake agent announce each change of
// its mode, its own or one it was asked for, as a message chunk of this
// text followed by the mode, as Gemini CLI does, in place of
// current_mode_update.
const EnvModeMarker = "SKYLANDS_FAKE_AGENT_MODE_MARKER"

// The values the fake agent's config options take, the first of each
// being what a session starts with.
var (
	Modes  = []string{"auto", "ask", "plan"}
	Models = []string{"fake-small", "fake-large"}
)

// Commands are the slash commands the fake agent announces for every
// session, a moment after it opens or loads, as real agents do. As a
// prompt, a command gets the answer "ran /<name>" and the text after it.
// "config show" is announced as Gemini CLI announces a subcommand, by its
// words, and runs as "/config show".
var Commands = []acp.AvailableCommand{
	{Name: "review", Description: "Review the changes", Input: &acp.AvailableCommandInput{Hint: "what to look at"}},
	{Name: "config", Description: "Change a setting directly"},
	{Name: "config show", Description: "Show the settings"},
}

// SwitchModeTo and SwitchModelTo, followed by a value and as a whole
// prompt, make the fake agent change its own mode or model to it, as an
// agent does through one of its commands, and announce the change with
// current_mode_update, for a mode, and config_option_update. It answers
// with its settings, as for SettingsQuestion.
const (
	SwitchModeTo  = "switch your mode to "
	SwitchModelTo = "switch your model to "
)

// BigEdit, anywhere in a prompt, makes the fake agent's edit carry a long
// input, about 20 KB, as an agent writing a whole file would.
const BigEdit = "big edit"

// BigOutput, anywhere in a prompt, makes what the fake agent's edit
// produces long, a diff whose new text is about 200 KB, past what a tool
// call's record keeps of any text.
const BigOutput = "big output"

// SettingsQuestion, as a whole prompt, makes the fake agent answer with
// its session's mode and model, as "mode=<mode> model=<model>".
const SettingsQuestion = "what are your settings?"

// PlanTitle is the fake agent's answer to a title request in plan mode,
// like the names Claude Code gives its plans there.
const PlanTitle = "give this conversation a tingly parrot"

// EnvRequireMeta, when set to 1, makes the fake agent refuse to open or
// load a session that comes without _meta, as an agent would that needs
// a setting only _meta carries.
const EnvRequireMeta = "SKYLANDS_FAKE_AGENT_REQUIRE_META"

// MetaQuestion, as a whole prompt, makes the fake agent answer with the
// _meta its session was opened or loaded with, as JSON, or NoMeta.
const MetaQuestion = "what was your session's meta?"

// NoMeta is the fake agent's answer to MetaQuestion for a session that
// came without _meta.
const NoMeta = "(none)"

// EnvHang, when set to 1, makes the fake agent one that will not stop: it
// ignores SIGTERM and SIGINT and keeps running after its stdin closes.
const EnvHang = "SKYLANDS_FAKE_AGENT_HANG"

// ReplayedLine is what the fake agent says while loading a session: the
// history it replays, which the daemon must keep out of the transcript.
const ReplayedLine = "(history replayed by the agent)"

// Run serves the fake agent over stdio until the client closes the link.
func Run(ctx context.Context) error {
	if path := os.Getenv(EnvChild); path != "" {
		child := exec.Command("sleep", "300")
		if err := child.Start(); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(strconv.Itoa(child.Process.Pid)), 0o644); err != nil {
			return err
		}
	}
	if os.Getenv(EnvHang) == "1" {
		signal.Ignore(syscall.SIGTERM, syscall.SIGINT)
		// Never returns, on purpose: this mode stands in for an agent that
		// ignores its stdin closing, SIGTERM, and SIGINT, so tests can see
		// the daemon end it with SIGKILL. Nothing cancels the context it
		// runs under, and honouring it would defeat the mode. It sleeps
		// rather than waiting in an empty select, which Go would end as a
		// deadlock.
		defer func() {
			for {
				time.Sleep(time.Hour)
			}
		}()
	}
	delay := 150 * time.Millisecond
	if d, err := time.ParseDuration(os.Getenv(EnvDelay)); err == nil {
		delay = d
	}
	return acp.RunAgent(ctx, &acp.StdioTransport{}, func(conn *acp.AgentConnection) any {
		return &agent{conn: conn, delay: delay, noLoad: os.Getenv(EnvNoLoad) == "1", auth: os.Getenv(EnvAuth),
			systemPrompt: os.Getenv(EnvSystemPrompt) == "1", config: os.Getenv(EnvConfigOptions), requireMeta: os.Getenv(EnvRequireMeta) == "1",
			modeMarker: os.Getenv(EnvModeMarker), noRawInput: os.Getenv(EnvNoRawInput) == "1", sessions: map[acp.SessionId]*session{}}
	})
}

// TestMainHook runs the fake agent and exits when the process was started
// as one, so a test binary can play the agent for its own tests. It runs
// the watchdog the same way, which a daemon started in a test starts from
// the test binary.
func TestMainHook() {
	watchdog.MainHook()
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
	// instructions is the system prompt the session was opened or loaded
	// with, as text.
	instructions string
	// mode and model are the session's config options, when it has them.
	mode, model string
	// meta is the _meta the session was opened or loaded with, as JSON,
	// or empty for none.
	meta string
}

type agent struct {
	conn   *acp.AgentConnection
	delay  time.Duration
	noLoad bool
	// auth is the kind of login to report, or empty to report none.
	auth string
	// systemPrompt means the agent advertises ACP's session system prompt.
	systemPrompt bool
	// config is EnvConfigOptions: empty for no config options.
	config string
	// requireMeta refuses a session that comes without _meta.
	requireMeta bool
	// modeMarker is EnvModeMarker: empty to announce modes with
	// current_mode_update.
	modeMarker string
	// noRawInput is EnvNoRawInput.
	noRawInput bool
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
	caps := &acp.AgentCapabilities{LoadSession: !a.noLoad}
	if a.systemPrompt {
		caps.SessionCapabilities = &acp.SessionCapabilities{SystemPrompt: &acp.SessionSystemPromptCapabilities{}}
	}
	if a.auth != "" {
		// Like Claude Code's adapter: advertise the report, then send it
		// once initialize has been answered.
		caps.Meta = acp.Meta{"authStatus": map[string]any{}}
		go func() {
			time.Sleep(a.delay)
			_ = a.conn.NotifyExtension(context.Background(), "_auth/status_update",
				map[string]any{"authStatus": map[string]any{"kind": a.auth, "label": "Fake " + a.auth}})
		}()
	}
	return &acp.InitializeResponse{
		ProtocolVersion:   acp.ProtocolVersion(1),
		AgentInfo:         &acp.Implementation{Name: "skylands-fakeagent", Version: "dev"},
		AgentCapabilities: caps,
	}, nil
}

// LoadSession brings back a session from a previous process. The fake
// agent keeps nothing on disk, so any id is accepted, and it replays a
// line of history the way a real agent streams the conversation back.
func (a *agent) LoadSession(ctx context.Context, req *acp.LoadSessionRequest) (*acp.LoadSessionResponse, error) {
	if a.noLoad {
		return nil, fmt.Errorf("this agent cannot load sessions")
	}
	instructions, err := systemPromptText(req.SystemPrompt)
	if err != nil {
		return nil, err
	}
	s, err := a.newSession(req.Cwd, instructions, req.Meta)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	a.sessions[req.SessionID] = s
	options, modes := a.configOptions(s), a.sessionModes(s)
	a.mu.Unlock()
	if err := a.conn.SessionUpdate(ctx, &acp.SessionNotification{SessionID: req.SessionID, Update: acp.AgentMessageChunkSessionUpdate(acp.TextContentBlock(ReplayedLine))}); err != nil {
		return nil, err
	}
	go a.announceCommands(req.SessionID)
	return &acp.LoadSessionResponse{ConfigOptions: options, Modes: modes}, nil
}

func (a *agent) NewSession(_ context.Context, req *acp.NewSessionRequest) (*acp.NewSessionResponse, error) {
	instructions, err := systemPromptText(req.SystemPrompt)
	if err != nil {
		return nil, err
	}
	id := acp.SessionId(nuid.Next())
	s, err := a.newSession(req.Cwd, instructions, req.Meta)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	a.sessions[id] = s
	options, modes := a.configOptions(s), a.sessionModes(s)
	a.mu.Unlock()
	go a.announceCommands(id)
	return &acp.NewSessionResponse{SessionID: id, ConfigOptions: options, Modes: modes}, nil
}

// announceCommands tells the client the session's slash commands, a
// moment after the session opens.
func (a *agent) announceCommands(id acp.SessionId) {
	time.Sleep(a.delay)
	_ = a.conn.SessionUpdate(context.Background(), &acp.SessionNotification{SessionID: id, Update: acp.AvailableCommandsUpdateSessionUpdate(Commands)})
}

// newSession is a session at the first value of each config option, which
// is also where a loaded one comes back.
func (a *agent) newSession(cwd, instructions string, meta acp.Meta) (*session, error) {
	s := &session{cwd: cwd, instructions: instructions}
	switch a.config {
	case "":
	case "modes":
		s.mode = Modes[0]
	default:
		s.mode, s.model = Modes[0], Models[0]
	}
	if len(meta) > 0 {
		b, err := json.Marshal(meta)
		if err != nil {
			return nil, err
		}
		s.meta = string(b)
	} else if a.requireMeta {
		return nil, fmt.Errorf("this agent needs _meta to open or load a session")
	}
	return s, nil
}

// configOptions are a session's config options as ACP describes them, or
// nil for an agent that offers none. The caller holds a.mu.
func (a *agent) configOptions(s *session) *[]acp.SessionConfigOption {
	if a.config == "" || a.config == "modes" {
		return nil
	}
	sel := func(id, name string, category acp.SessionConfigOptionCategory, current string, values []string) acp.SessionConfigOption {
		choices := acp.UngroupedSessionConfigSelectOptions{}
		for _, v := range values {
			choices = append(choices, acp.SessionConfigSelectOption{Name: v, Value: acp.SessionConfigValueId(v)})
		}
		o := acp.SelectSessionConfigOption(acp.SessionConfigId(id), name, acp.SessionConfigValueId(current), acp.SessionConfigSelectOptions{Ungrouped: &choices})
		o.Category = &category
		return o
	}
	return &[]acp.SessionConfigOption{
		sel("fake-mode", "Mode", acp.SessionConfigOptionCategoryMode, s.mode, Modes),
		sel("fake-model", "Model", acp.SessionConfigOptionCategoryModel, s.model, Models),
	}
}

// sessionModes are a session's modes as ACP's session modes describe
// them, for an agent that offers its mode that way, or nil. The caller
// holds a.mu.
func (a *agent) sessionModes(s *session) *acp.SessionModeState {
	if a.config != "modes" {
		return nil
	}
	modes := make([]acp.SessionMode, 0, len(Modes))
	for _, m := range Modes {
		modes = append(modes, acp.SessionMode{ID: acp.SessionModeId(m), Name: strings.ToUpper(m[:1]) + m[1:]})
	}
	return &acp.SessionModeState{AvailableModes: modes, CurrentModeID: acp.SessionModeId(s.mode)}
}

// SetSessionMode changes the mode of a session that offers its modes
// through session modes, and announces it before answering.
func (a *agent) SetSessionMode(ctx context.Context, req *acp.SetSessionModeRequest) (*acp.SetSessionModeResponse, error) {
	a.mu.Lock()
	s, ok := a.sessions[req.SessionID]
	if !ok || a.config != "modes" {
		a.mu.Unlock()
		return nil, fmt.Errorf("no session modes for session %s", req.SessionID)
	}
	if !slices.Contains(Modes, string(req.ModeID)) {
		a.mu.Unlock()
		return nil, fmt.Errorf("invalid mode %q", req.ModeID)
	}
	mode := string(req.ModeID)
	s.mode = mode
	a.mu.Unlock()
	if err := a.announceMode(ctx, req.SessionID, mode); err != nil {
		return nil, err
	}
	return &acp.SetSessionModeResponse{}, nil
}

// announceMode tells the client a session's mode is now mode, with
// current_mode_update or, given EnvModeMarker, in a message chunk.
func (a *agent) announceMode(ctx context.Context, id acp.SessionId, mode string) error {
	update := acp.CurrentModeUpdateSessionUpdate(acp.SessionModeId(mode))
	if a.modeMarker != "" {
		update = acp.AgentMessageChunkSessionUpdate(acp.TextContentBlock(a.modeMarker + mode))
	}
	return a.conn.SessionUpdate(ctx, &acp.SessionNotification{SessionID: id, Update: update})
}

// SetSessionConfigOption changes a session's mode or model to a value the
// agent offers, and announces the options it then has before answering,
// as real agents do.
func (a *agent) SetSessionConfigOption(ctx context.Context, req *acp.SetSessionConfigOptionRequest) (*acp.SetSessionConfigOptionResponse, error) {
	resp, err := a.setConfigOption(req)
	if err != nil {
		return nil, err
	}
	if err := a.conn.SessionUpdate(ctx, &acp.SessionNotification{SessionID: req.SessionID, Update: acp.ConfigOptionUpdateSessionUpdate(resp.ConfigOptions)}); err != nil {
		return nil, err
	}
	return resp, nil
}

func (a *agent) setConfigOption(req *acp.SetSessionConfigOptionRequest) (*acp.SetSessionConfigOptionResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.sessions[req.SessionID]
	if !ok || a.config == "" {
		return nil, fmt.Errorf("no config options for session %s", req.SessionID)
	}
	value := fmt.Sprint(req.Value)
	var field *string
	var values []string
	switch req.ConfigID {
	case "fake-mode":
		field, values = &s.mode, Modes
	case "fake-model":
		field, values = &s.model, Models
	default:
		return nil, fmt.Errorf("unknown config option %q", req.ConfigID)
	}
	if !slices.Contains(values, value) {
		return nil, fmt.Errorf("invalid value %q for %s", value, req.ConfigID)
	}
	if a.config != "stuck" {
		*field = value
	}
	return &acp.SetSessionConfigOptionResponse{ConfigOptions: *a.configOptions(s)}, nil
}

// switchOwn changes one of a session's settings on the agent's own
// account and announces it: a mode first as announceMode does, then
// config_option_update for an agent with config options.
func (a *agent) switchOwn(ctx context.Context, id acp.SessionId, s *session, field *string, values []string, value string, mode bool) error {
	a.mu.Lock()
	if a.config == "" || (a.config == "modes" && !mode) || !slices.Contains(values, value) {
		a.mu.Unlock()
		return fmt.Errorf("cannot switch to %q", value)
	}
	*field = value
	options := a.configOptions(s)
	a.mu.Unlock()
	if mode {
		if err := a.announceMode(ctx, id, value); err != nil {
			return err
		}
	}
	if options == nil {
		return nil
	}
	return a.conn.SessionUpdate(ctx, &acp.SessionNotification{SessionID: id, Update: acp.ConfigOptionUpdateSessionUpdate(*options)})
}

// systemPromptText is a session's system prompt as text. Only append mode
// is taken, which is what an agent that advertises the prompt must take.
func systemPromptText(p *acp.SystemPrompt) (string, error) {
	if p == nil {
		return "", nil
	}
	if p.Mode != acp.SystemPromptModeAppend {
		return "", fmt.Errorf("system prompt mode %q is not supported", p.Mode)
	}
	text := ""
	for _, c := range p.Content {
		text += c.Text
	}
	return text, nil
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
		// A cancel ends one turn, not every turn after it.
		s.cancelled = false
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
	if text := strings.TrimSpace(promptText(req)); strings.HasPrefix(text, "/") {
		name, rest, _ := strings.Cut(text[1:], " ")
		for _, c := range Commands {
			if c.Name == name {
				answer := strings.TrimSpace("ran /" + name + " " + rest)
				if err := send(acp.AgentMessageChunkSessionUpdate(acp.TextContentBlock(answer))); err != nil {
					return nil, err
				}
				return &acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
			}
		}
	}
	if mode, ok := strings.CutPrefix(promptText(req), SwitchModeTo); ok {
		if err := a.switchOwn(ctx, req.SessionID, s, &s.mode, Modes, mode, true); err != nil {
			return nil, err
		}
	}
	if model, ok := strings.CutPrefix(promptText(req), SwitchModelTo); ok {
		if err := a.switchOwn(ctx, req.SessionID, s, &s.model, Models, model, false); err != nil {
			return nil, err
		}
	}
	if text := promptText(req); text == SettingsQuestion || strings.HasPrefix(text, SwitchModeTo) || strings.HasPrefix(text, SwitchModelTo) {
		a.mu.Lock()
		answer := "mode=" + s.mode + " model=" + s.model
		a.mu.Unlock()
		if err := send(acp.AgentMessageChunkSessionUpdate(acp.TextContentBlock(answer))); err != nil {
			return nil, err
		}
		return &acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
	}
	if promptText(req) == InstructionsQuestion {
		a.mu.Lock()
		answer := s.instructions
		a.mu.Unlock()
		if answer == "" {
			answer = NoInstructions
		}
		if err := send(acp.AgentMessageChunkSessionUpdate(acp.TextContentBlock(answer))); err != nil {
			return nil, err
		}
		return &acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
	}
	if promptText(req) == MetaQuestion {
		a.mu.Lock()
		answer := s.meta
		a.mu.Unlock()
		if answer == "" {
			answer = NoMeta
		}
		if err := send(acp.AgentMessageChunkSessionUpdate(acp.TextContentBlock(answer))); err != nil {
			return nil, err
		}
		return &acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
	}
	if asked := promptText(req); titles.IsRequest(asked) {
		// A title request gets a title and nothing else, like a well-behaved
		// agent, except in plan mode, where it names a plan the way Claude
		// Code does.
		title := titles.Fake(titles.RequestedFor(asked))
		a.mu.Lock()
		if s.mode == "plan" {
			title = PlanTitle
		}
		a.mu.Unlock()
		if err := send(acp.AgentMessageChunkSessionUpdate(acp.TextContentBlock(title))); err != nil {
			return nil, err
		}
		return &acp.PromptResponse{StopReason: acp.StopReasonEndTurn, Usage: usage(TitleInputTokens, uint64(len(title)))}, nil
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
		if err := send(toolCall(id, "Read "+f, acp.ToolKindRead, filepath.Join(s.cwd, f), acp.ToolCallStatusInProgress, map[string]any{"file_path": f})); err != nil {
			return nil, err
		}
		if !pause() {
			return done()
		}
		// What a read produces is the file's text, as a real agent reports it.
		if err := send(toolResult(id, acp.ToolCallStatusCompleted, acp.ContentToolCallContent(acp.TextContentBlock(fileText(s.cwd, f))))); err != nil {
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
		// Like an agent whose tool input streams in, the edit is announced
		// before its input is known, under a placeholder title with an empty
		// input, and named once the input is complete.
		var streaming any = map[string]any{}
		if a.noRawInput {
			streaming = nil
		}
		if err := send(toolCall(id, "Edit", acp.ToolKindEdit, "", acp.ToolCallStatusPending, streaming)); err != nil {
			return nil, err
		}
		title := "Edit " + edited
		kind := acp.ToolKindEdit
		locs := []acp.ToolCallLocation{{Path: filepath.Join(s.cwd, edited)}}
		input := map[string]any{"path": edited, "change": "rewrite"}
		// "big edit" in the prompt makes the edit's input long, as a whole
		// file's new content would be, past what a request's event carries.
		if strings.Contains(strings.ToLower(promptText(req)), BigEdit) {
			input["content"] = strings.Repeat("// a long line of the file's new content, éh, ✓\n", 400)
		}
		named := acp.ToolCallUpdateSessionUpdate(id)
		named.Title, named.Kind, named.Locations = &title, &kind, &locs
		asked := acp.ToolCallUpdate{ToolCallID: id, Title: &title, Kind: &kind, Locations: &locs}
		if a.noRawInput {
			diff := []acp.ToolCallContent{acp.DiffToolCallContent(filepath.Join(s.cwd, edited), "rewritten\n")}
			named.Content, asked.Content = &diff, &diff
		} else {
			named.RawInput, asked.RawInput = input, input
		}
		if err := send(named); err != nil {
			return nil, err
		}
		// An edit needs permission, like a real agent's would.
		perm, err := a.conn.RequestPermission(ctx, &acp.RequestPermissionRequest{
			SessionID: req.SessionID,
			ToolCall:  asked,
			Options: []acp.PermissionOption{
				{OptionID: "allow", Name: "Allow", Kind: acp.PermissionOptionKindAllowOnce},
				{OptionID: "always", Name: "Always allow", Kind: acp.PermissionOptionKindAllowAlways},
				{OptionID: "deny", Name: "Deny", Kind: acp.PermissionOptionKindRejectOnce},
			},
		})
		if err != nil {
			return nil, err
		}
		// A cancelled outcome means the client ended the turn while the
		// request waited, as ACP defines it; a refusal is a reject option.
		if perm.Outcome.Outcome == acp.RequestPermissionOutcomeTypeCancelled || a.cancelled(req.SessionID) {
			return done()
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
			// What an edit produces is a diff of the file, as a real agent
			// reports it, though the fake agent changes nothing on disk.
			old := fileText(s.cwd, edited)
			newText := old + "// rewritten by the fake agent\n"
			if strings.Contains(strings.ToLower(promptText(req)), BigOutput) {
				newText += strings.Repeat("// a long line the fake agent wrote, past what a record keeps, \u00e9h\n", 3000)
			}
			diff := acp.DiffToolCallContent(filepath.Join(s.cwd, edited), newText)
			diff.OldText = &old
			if err := send(toolResult(id, acp.ToolCallStatusCompleted, diff)); err != nil {
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
	// The turn's cost comes back with its end, as it does from a real
	// agent: a share per file read, and the answer as output.
	return &acp.PromptResponse{StopReason: acp.StopReasonEndTurn, Usage: usage(TurnInputTokens+uint64(n)*FileInputTokens, uint64(len(msg)))}, nil
}

// What the fake agent says its requests cost, in tokens. The figures are
// made up but fixed, so a test can add them up.
const (
	// TurnInputTokens is what every turn costs before its files are read.
	TurnInputTokens uint64 = 2_000
	// FileInputTokens is what each file read adds.
	FileInputTokens uint64 = 1_000
	// TitleInputTokens is what the title request costs.
	TitleInputTokens uint64 = 50
)

// usage is a turn's token count as ACP carries it: the given input, the
// output, and the total of the two.
func usage(input, output uint64) *acp.Usage {
	return &acp.Usage{InputTokens: input, OutputTokens: output, TotalTokens: input + output}
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

// toolCall is the first report of a tool call. An empty path means the
// call names no file yet.
func toolCall(id acp.ToolCallId, title string, kind acp.ToolKind, path string, status acp.ToolCallStatus, input any) acp.SessionUpdate {
	u := acp.ToolCallSessionUpdate(id, title)
	u.Kind = &kind
	u.Status = &status
	u.RawInput = input
	if path != "" {
		locs := []acp.ToolCallLocation{{Path: path}}
		u.Locations = &locs
	}
	return u
}

func toolCallUpdate(id acp.ToolCallId, status acp.ToolCallStatus) acp.SessionUpdate {
	u := acp.ToolCallUpdateSessionUpdate(id)
	u.Status = &status
	return u
}

// toolResult ends a tool call with what it produced.
func toolResult(id acp.ToolCallId, status acp.ToolCallStatus, content ...acp.ToolCallContent) acp.SessionUpdate {
	u := toolCallUpdate(id, status)
	u.Content = &content
	return u
}

// MaxFileText is the most of a file the fake agent reports having read.
const MaxFileText = 2048

// fileText is a file's text as the fake agent reports it, cut short for a
// long one.
func fileText(dir, rel string) string {
	data, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		return "(could not read " + rel + ": " + err.Error() + ")"
	}
	if len(data) > MaxFileText {
		return string(data[:MaxFileText]) + "\n[…]"
	}
	return string(data)
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
