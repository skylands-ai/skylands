package agents

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spachava753/acp-sdk/acp"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/titles"
)

// Run is one agent process with one open ACP session. It takes any number
// of prompts, one at a time, until it is closed.
type Run struct {
	name      string
	cwd       string
	client    *acp.Client
	session   acp.SessionId
	cancel    context.CancelFunc
	stderr    *tailBuffer
	transport *processTransport
	seq       *sequencer
	handler   *handler

	mu     sync.Mutex
	turn   context.CancelFunc
	closed bool
	// seed, when set, goes in front of the next prompt: the conversation
	// so far, for an agent that could not load its session.
	seed string
	// system is the adapter's instructions as ACP's session system prompt,
	// or nil when there are none or the agent does not take them.
	system *acp.SystemPrompt
	// mode and model are what every session the agent opens or loads is
	// set to through its config options; empty leaves the agent's own.
	mode, model string
	// meta is the adapter's Meta, which goes with every session the agent
	// opens or loads.
	meta acp.Meta
}

// Start launches the adapter's agent in cwd, initializes it, and opens a
// session. The process lives until Close, or until ctx is cancelled. The
// session is set to the adapter's mode and to model, when either is set.
func Start(ctx context.Context, ad Adapter, cwd, model string, obs Observer) (*Run, error) {
	r, init, err := launch(ctx, ad, cwd, model, obs)
	if err != nil {
		return nil, err
	}
	sess, err := r.client.NewSession(ctx, &acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{}, SystemPrompt: r.system, Meta: r.meta})
	if err != nil {
		_ = r.Close()
		return nil, r.fail("open session", err)
	}
	if err := r.settle(ctx, sess.SessionID, sess.ConfigOptions); err != nil {
		_ = r.Close()
		return nil, r.fail("set up the session", err)
	}
	r.session = sess.SessionID
	name, version := identity(ad, init)
	obs.Spawned(name, version, string(sess.SessionID))
	return r, nil
}

// Resume launches the adapter's agent in cwd and brings a conversation
// back after the previous process died with its daemon. When the agent can
// load sessions and still knows agentSessionID, the agent's own memory of
// the conversation comes back with it, and how is ResumedByLoad. Otherwise
// a new session opens and transcript goes in front of its first prompt,
// and how is ResumedBySeed. Whatever the agent replays while loading stays
// out of the observer: it is history the daemon already has.
func Resume(ctx context.Context, ad Adapter, cwd, model, agentSessionID, transcript string, obs Observer) (r *Run, how string, err error) {
	r, init, err := launch(ctx, ad, cwd, model, obs)
	if err != nil {
		return nil, "", err
	}
	name, version := identity(ad, init)
	if agentSessionID != "" && init.AgentCapabilities != nil && init.AgentCapabilities.LoadSession {
		id := acp.SessionId(agentSessionID)
		r.handler.aside(id)
		loaded, err := r.client.LoadSession(ctx, &acp.LoadSessionRequest{SessionID: id, Cwd: cwd, McpServers: []acp.McpServer{}, SystemPrompt: r.system, Meta: r.meta})
		r.drain()
		r.handler.collected(id)
		if err == nil {
			// A loaded session comes back in the agent's own mode and model,
			// so they are set again before anything is asked of it.
			var options *[]acp.SessionConfigOption
			if loaded != nil {
				options = loaded.ConfigOptions
			}
			if err := r.settle(ctx, id, options); err != nil {
				_ = r.Close()
				return nil, "", r.fail("set up the loaded session", err)
			}
			r.session = id
			obs.Spawned(name, version, agentSessionID)
			return r, ResumedByLoad, nil
		}
		if ctx.Err() != nil {
			_ = r.Close()
			return nil, "", r.fail("load session", err)
		}
		slog.Info("agents: the agent could not load its session, seeding a new one", "agent", ad.Name, "err", err)
	}
	sess, err := r.client.NewSession(ctx, &acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{}, SystemPrompt: r.system, Meta: r.meta})
	if err != nil {
		_ = r.Close()
		return nil, "", r.fail("open session", err)
	}
	if err := r.settle(ctx, sess.SessionID, sess.ConfigOptions); err != nil {
		_ = r.Close()
		return nil, "", r.fail("set up the session", err)
	}
	r.session = sess.SessionID
	r.seed = seedText(transcript)
	obs.Spawned(name, version, string(sess.SessionID))
	return r, ResumedBySeed, nil
}

// How an agent came back in Resume. The values are the ones the
// SessionResumed event records.
const (
	ResumedByLoad = "load"
	ResumedBySeed = "seed"
)

// MaxSeed is the most of a transcript that goes in front of a seeded
// session's first prompt. A longer one keeps its end.
const MaxSeed = 256 * 1024

// toolMarkers matches the marker a transcript's tool call lines end with,
// which names the call for a reader's browser and means nothing to an agent.
var toolMarkers = regexp.MustCompile(` <!-- tool:[^ >]* -->`)

// seedText is what a new agent session is told before its first prompt
// when it stands in for one that could not be loaded: the conversation so
// far, as the transcript recorded it.
func seedText(transcript string) string {
	transcript = strings.TrimSpace(toolMarkers.ReplaceAllString(transcript, ""))
	if transcript == "" {
		return ""
	}
	omitted := ""
	if len(transcript) > MaxSeed {
		cut := transcript[len(transcript)-MaxSeed:]
		if i := strings.IndexByte(cut, '\n'); i >= 0 {
			cut = cut[i+1:]
		}
		transcript = cut
		omitted = "The beginning is left out for length.\n\n"
	}
	return "The daemon that runs you restarted, and this conversation continues in a new session that has no memory of it. " +
		"Between the two rules below is the conversation so far, as it was recorded: \"## You\" opens what the person wrote, " +
		"\"## Agent\" opens what you answered, and a line that starts with ⚙ is a tool call you made. " +
		"Carry on from the end of it, and do not repeat what was already said.\n\n" + omitted +
		"-----\n\n" + transcript + "\n\n-----\n\n"
}

// launch starts the agent process and initializes it, leaving the session
// to the caller.
func launch(ctx context.Context, ad Adapter, cwd, model string, obs Observer) (*Run, *acp.InitializeResponse, error) {
	if len(ad.Command) == 0 {
		return nil, nil, fmt.Errorf("adapter %q has no command", ad.Name)
	}
	ctx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(ctx, ad.Command[0], ad.Command[1:]...)
	ownGroup(cmd)
	// When ctx ends, as it does when the daemon stops, the agent's whole
	// group is asked to stop, and killed if it has not within stopWait.
	// Without this, only the agent's first process would be killed, and
	// the rest of it left running.
	cmd.Cancel = func() error {
		_ = signalGroup(cmd, syscall.SIGTERM)
		time.AfterFunc(stopWait, func() { _ = signalGroup(cmd, syscall.SIGKILL) })
		return nil
	}
	cmd.WaitDelay = stopWait
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), ad.Env...)
	stderr := &tailBuffer{limit: 4096}
	cmd.Stderr = stderr

	h := &handler{ExtensionMux: acp.NewExtensionMux(), auth: make(chan AuthStatus, 1), cwd: cwd, obs: obs, calls: map[acp.ToolCallId]ToolCall{}}
	h.seq = newSequencer(h.update)
	if err := acp.AddExtensionNotification(h.ExtensionMux, authStatusMethod, h.authStatus); err != nil {
		cancel()
		return nil, nil, err
	}
	transport := &processTransport{cmd: cmd, watch: watchFrom(ctx)}
	h.read = &transport.read
	client, err := acp.Connect(ctx, transport, h)
	if err != nil {
		cancel()
		return nil, nil, fmt.Errorf("start %s agent: %w", ad.Name, err)
	}
	r := &Run{name: ad.Name, cwd: cwd, client: client, cancel: cancel, stderr: stderr, transport: transport, seq: h.seq, handler: h, mode: ad.Mode, model: model, meta: ad.Meta}

	init, err := client.Initialize(ctx, &acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersion(1),
		ClientInfo:      &acp.Implementation{Name: "skylands", Version: "dev"},
		// Forms are how an agent asks a person something: the daemon turns
		// one into a question that waits like a permission request does.
		ClientCapabilities: &acp.ClientCapabilities{Elicitation: &acp.ElicitationCapabilities{Form: &acp.ElicitationFormCapabilities{}}},
	})
	if err != nil {
		_ = r.Close()
		return nil, nil, r.fail("initialize "+ad.Name, err)
	}
	r.system = systemPrompt(ad, init)
	return r, init, nil
}

// systemPrompt is the adapter's instructions for the agent's sessions, in
// append mode, which every agent that takes a system prompt supports. ACP
// lets a client send one only to an agent that advertises it.
func systemPrompt(ad Adapter, init *acp.InitializeResponse) *acp.SystemPrompt {
	if ad.Instructions == "" || init == nil || init.AgentCapabilities == nil ||
		init.AgentCapabilities.SessionCapabilities == nil || init.AgentCapabilities.SessionCapabilities.SystemPrompt == nil {
		return nil
	}
	return &acp.SystemPrompt{Mode: acp.SystemPromptModeAppend, Content: []acp.ContentBlock{acp.TextContentBlock(ad.Instructions)}}
}

// settle sets a new or loaded session to the adapter's mode and the chosen
// model, through the config options the agent gave for the session. Each
// is found by its ACP category, so no agent's own name for it is needed.
// The mode is what keeps an agent asking before it acts, so a session
// whose mode cannot be set, or does not read back as set, fails.
func (r *Run) settle(ctx context.Context, id acp.SessionId, given *[]acp.SessionConfigOption) error {
	var options []acp.SessionConfigOption
	if given != nil {
		options = *given
	}
	if r.mode != "" {
		after, err := r.setOption(ctx, id, options, acp.SessionConfigOptionCategoryMode, r.mode)
		if err != nil {
			return fmt.Errorf("set the agent's mode to %s: %w", r.mode, err)
		}
		if o, ok := optionFor(after, acp.SessionConfigOptionCategoryMode); !ok || optionValue(o) != r.mode {
			return fmt.Errorf("the agent did not confirm its mode is %s", r.mode)
		}
		options = after
	}
	if r.model != "" {
		if _, err := r.setOption(ctx, id, options, acp.SessionConfigOptionCategoryModel, r.model); err != nil {
			return fmt.Errorf("set the agent's model to %s: %w", r.model, err)
		}
	}
	return nil
}

// setOption sets the session's option in category to value, unless it is
// already that, and returns the options as the agent says they stand.
func (r *Run) setOption(ctx context.Context, id acp.SessionId, options []acp.SessionConfigOption, category acp.SessionConfigOptionCategory, value string) ([]acp.SessionConfigOption, error) {
	o, ok := optionFor(options, category)
	if !ok {
		return nil, fmt.Errorf("the agent has no %s setting", category)
	}
	if optionValue(o) == value {
		return options, nil
	}
	resp, err := r.client.SetSessionConfigOption(ctx, &acp.SetSessionConfigOptionRequest{SessionID: id, ConfigID: o.ID, Value: acp.SessionConfigValueId(value)})
	if err != nil {
		// An agent may take a value it does not list, such as a model's
		// full id for one it lists by alias, so the list is only a hint.
		if offered := optionChoices(o); len(offered) > 0 && !slices.Contains(offered, value) {
			return nil, fmt.Errorf("%w (it offers %s)", err, strings.Join(offered, ", "))
		}
		return nil, err
	}
	return resp.ConfigOptions, nil
}

// optionFor finds the first option in an ACP category.
func optionFor(options []acp.SessionConfigOption, category acp.SessionConfigOptionCategory) (acp.SessionConfigOption, bool) {
	for _, o := range options {
		if o.Category != nil && *o.Category == category {
			return o, true
		}
	}
	return acp.SessionConfigOption{}, false
}

// optionValue is an option's current value as text.
func optionValue(o acp.SessionConfigOption) string {
	if o.CurrentValue == nil {
		return ""
	}
	return fmt.Sprint(o.CurrentValue)
}

// optionChoices are the values a select option lists, in or out of groups.
func optionChoices(o acp.SessionConfigOption) []string {
	var out []string
	for _, m := range optionModels(o) {
		out = append(out, m.ID)
	}
	return out
}

// identity is what the agent calls itself, or the adapter's name until it
// says.
func identity(ad Adapter, init *acp.InitializeResponse) (name, version string) {
	if init != nil && init.AgentInfo != nil {
		return init.AgentInfo.Name, init.AgentInfo.Version
	}
	return ad.Name, ""
}

// Prompt sends one prompt and returns the stop reason when the turn ends.
// Cancel ends the turn early with the cancelled stop reason.
func (r *Run) Prompt(ctx context.Context, text string) (string, error) {
	ctx, cancel := context.WithCancel(ctx)
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		cancel()
		return "", errors.New("the agent is closed")
	}
	r.turn = cancel
	text, r.seed = r.seed+text, ""
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.turn = nil
		r.mu.Unlock()
		cancel()
	}()
	resp, err := r.client.Prompt(ctx, &acp.PromptRequest{SessionID: r.session, Prompt: []acp.ContentBlock{acp.TextContentBlock(text)}})
	if err != nil {
		return "", r.fail("prompt", err)
	}
	r.drain()
	// The turn's cost comes back with its end, after everything the turn
	// reported, so it is handed on last.
	if t, ok := tokenUsage(resp.Usage); ok {
		r.handler.obs.Tokens(v1.TokensForTurn, t)
	}
	return string(resp.StopReason), nil
}

// tokenUsage reads what a prompt response says the request cost. ACP's
// usage is a turn's own figure, not a running total: Claude Code's adapter
// resets its tally as each turn starts, and the fake agent counts each turn
// alone. An agent that gives no total gets the sum of the counts it gave.
func tokenUsage(u *acp.Usage) (v1.TokenUsage, bool) {
	if u == nil {
		return v1.TokenUsage{}, false
	}
	t := v1.TokenUsage{Input: u.InputTokens, Output: u.OutputTokens, Total: u.TotalTokens}
	if u.CachedReadTokens != nil {
		t.CachedRead = *u.CachedReadTokens
	}
	if u.CachedWriteTokens != nil {
		t.CachedWrite = *u.CachedWriteTokens
	}
	if u.ThoughtTokens != nil {
		t.Thought = *u.ThoughtTokens
	}
	if t.Total == 0 {
		t.Total = t.Input + t.Output + t.CachedRead + t.CachedWrite
	}
	return t, !t.IsZero()
}

// drain waits until every update the transport read before the last
// response has been handled. The response line follows those updates on
// the wire, but their handlers run concurrently, so without this a turn
// could be recorded as over while its last words were still arriving, or
// a loaded session's history could leak out after the load.
func (r *Run) drain() { r.handler.catchUp() }

// Title asks the agent, in a side session of its own, to name the
// conversation that begins with the given request. Nothing it says reaches
// the observer or the conversation; what it costs does, since the session
// pays for it. It gives up after the timeout.
func (r *Run) Title(ctx context.Context, request string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	sess, err := r.client.NewSession(ctx, &acp.NewSessionRequest{Cwd: r.cwd, McpServers: []acp.McpServer{}, Meta: r.meta})
	if err != nil {
		return "", fmt.Errorf("open a side session: %w", err)
	}
	r.handler.aside(sess.SessionID)
	defer r.handler.collected(sess.SessionID)
	// The side session asks before acting, and runs on the chosen model,
	// the same as the conversation.
	if err := r.settle(ctx, sess.SessionID, sess.ConfigOptions); err != nil {
		return "", fmt.Errorf("set up a side session: %w", err)
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = r.client.Cancel(context.Background(), &acp.CancelNotification{SessionID: sess.SessionID})
		case <-done:
		}
	}()
	resp, err := r.client.Prompt(ctx, &acp.PromptRequest{SessionID: sess.SessionID, Prompt: []acp.ContentBlock{acp.TextContentBlock(titles.Request(request))}})
	close(done)
	if err != nil {
		return "", fmt.Errorf("ask for a title: %w", err)
	}
	r.drain()
	if t, ok := tokenUsage(resp.Usage); ok {
		r.handler.obs.Tokens(v1.TokensForTitle, t)
	}
	title := titles.Clean(r.handler.collected(sess.SessionID))
	if title == "" {
		return "", errors.New("the agent gave no usable title")
	}
	return title, nil
}

// Cancel asks the agent to end the turn in progress, if any.
func (r *Run) Cancel(ctx context.Context) {
	r.mu.Lock()
	turn := r.turn
	r.mu.Unlock()
	if turn == nil {
		return
	}
	_ = r.client.Cancel(ctx, &acp.CancelNotification{SessionID: r.session})
}

// CloseGrace is how long Close gives the agent to finish what it is doing
// and exit on its own before its process is killed.
const CloseGrace = 15 * time.Second

// Close stops the agent process: the link closes once nothing is in flight
// and the agent gets CloseGrace to exit, after which it is killed.
func (r *Run) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	r.mu.Unlock()
	done := make(chan error, 1)
	go func() { done <- r.client.Close() }()
	select {
	case err := <-done:
		r.cancel()
		return err
	case <-time.After(CloseGrace):
		// Killing the process breaks the link, which fails whatever was in
		// flight and lets the close finish.
		r.cancel()
		return errors.Join(errors.New("the agent did not stop in time and was killed"), <-done)
	}
}

func (r *Run) fail(step string, err error) error {
	if msg := r.stderr.String(); msg != "" {
		return fmt.Errorf("%s: %w (agent said: %s)", step, err, msg)
	}
	return fmt.Errorf("%s: %w", step, err)
}

// RunPrompt launches the adapter's agent, sends one prompt, and stops the
// agent when the turn ends.
func RunPrompt(ctx context.Context, ad Adapter, cwd, prompt, model string, obs Observer) (string, error) {
	r, err := Start(ctx, ad, cwd, model, obs)
	if err != nil {
		return "", err
	}
	defer func() { _ = r.Close() }()
	return r.Prompt(ctx, prompt)
}
