package agents

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/skylands-ai/skylands/internal/titles"
	"github.com/spachava753/acp-sdk/acp"
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
}

// Start launches the adapter's agent in cwd, initializes it, and opens a
// session. The process lives until Close, or until ctx is cancelled. A
// non-empty model is passed through the adapter's ModelEnv.
func Start(ctx context.Context, ad Adapter, cwd, model string, obs Observer) (*Run, error) {
	if len(ad.Command) == 0 {
		return nil, fmt.Errorf("adapter %q has no command", ad.Name)
	}
	ctx, cancel := context.WithCancel(ctx)
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
	transport := &processTransport{cmd: cmd}
	client, err := acp.Connect(ctx, transport, h)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("start %s agent: %w", ad.Name, err)
	}
	r := &Run{name: ad.Name, cwd: cwd, client: client, cancel: cancel, stderr: stderr, transport: transport, seq: h.seq, handler: h}

	init, err := client.Initialize(ctx, &acp.InitializeRequest{
		ProtocolVersion:    acp.ProtocolVersion(1),
		ClientInfo:         &acp.Implementation{Name: "skylands", Version: "dev"},
		ClientCapabilities: &acp.ClientCapabilities{},
	})
	if err != nil {
		_ = r.Close()
		return nil, r.fail("initialize "+ad.Name, err)
	}
	name, version := ad.Name, ""
	if init.AgentInfo != nil {
		name, version = init.AgentInfo.Name, init.AgentInfo.Version
	}
	obs.Spawned(name, version)

	sess, err := client.NewSession(ctx, &acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{}})
	if err != nil {
		_ = r.Close()
		return nil, r.fail("open session", err)
	}
	r.session = sess.SessionID
	return r, nil
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
	return string(resp.StopReason), nil
}

// drain waits until every update the transport read before the prompt's
// response has been handled. The response line follows those updates on
// the wire, but their handlers run concurrently, so without this the turn
// could be recorded as over while its last words were still arriving.
func (r *Run) drain() {
	deadline := time.Now().Add(3 * time.Second)
	for r.seq.Delivered() < r.transport.read.Load() && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
}

// Title asks the agent, in a side session of its own, to name the
// conversation that begins with the given request. Nothing of it reaches
// the observer or the conversation. It gives up after the timeout.
func (r *Run) Title(ctx context.Context, request string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	sess, err := r.client.NewSession(ctx, &acp.NewSessionRequest{Cwd: r.cwd, McpServers: []acp.McpServer{}})
	if err != nil {
		return "", fmt.Errorf("open a side session: %w", err)
	}
	r.handler.aside(sess.SessionID)
	defer r.handler.collected(sess.SessionID)
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = r.client.Cancel(context.Background(), &acp.CancelNotification{SessionID: sess.SessionID})
		case <-done:
		}
	}()
	_, err = r.client.Prompt(ctx, &acp.PromptRequest{SessionID: sess.SessionID, Prompt: []acp.ContentBlock{acp.TextContentBlock(titles.Request(request))}})
	close(done)
	if err != nil {
		return "", fmt.Errorf("ask for a title: %w", err)
	}
	r.drain()
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

// Close stops the agent process.
func (r *Run) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	r.mu.Unlock()
	err := r.client.Close()
	r.cancel()
	return err
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
