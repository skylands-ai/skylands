// Package fetch answers on-demand reads from the host's disk: a session's
// transcript, the diff in its worktree, where its changes stand, the
// record of one of its tool calls, and what one of its terminals shows. The citadel asks through the island's
// fetch subjects, and the daemon decides what leaves the host.
package fetch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/nats-io/nats.go"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/daemon/session"
)

// MaxBytes is the most text one answer carries.
const MaxBytes = 512 * 1024

// Sessions finds sessions and reports their changes and their tool calls,
// which the session manager provides.
type Sessions interface {
	Get(id string) (session.Session, bool)
	Changes(ctx context.Context, id string) (v1.ChangesResult, error)
	ToolCall(sessionID, toolCallID string) (v1.ToolCallDetail, error)
	RequestInput(sessionID, requestID string) (v1.RequestInput, error)
	Commands(id string) (v1.CommandsResult, error)
	TerminalScreen(sessionID, terminalID string) (v1.TerminalOutput, error)
}

// Responder answers fetch requests for one host.
type Responder struct {
	host           string
	sessions       Sessions
	transcriptsDir string
}

// New returns a responder for host whose transcripts live in dir.
func New(host string, sessions Sessions, transcriptsDir string) *Responder {
	return &Responder{host: host, sessions: sessions, transcriptsDir: transcriptsDir}
}

// Handle answers one request. The kind is the subject's last token.
func (r *Responder) Handle(ctx context.Context, msg *nats.Msg) {
	kind := msg.Subject[strings.LastIndex(msg.Subject, ".")+1:]
	var req v1.FetchSessionRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil || req.SessionID == "" {
		r.reply(msg, v1.Reply{Error: "a sessionId is required"})
		return
	}
	s, ok := r.sessions.Get(req.SessionID)
	if !ok {
		r.reply(msg, v1.Reply{Error: "no session " + req.SessionID + " on " + r.host})
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var text string
	var err error
	switch kind {
	case v1.FetchTranscript:
		text, err = r.transcript(s)
	case v1.FetchDiff:
		if s.WorktreeRemoved {
			err = errors.New("the worktree has been removed")
		} else {
			text, err = diff(ctx, s.Worktree)
		}
	case v1.FetchChanges:
		changes, err := r.sessions.Changes(ctx, s.ID)
		if err != nil {
			r.reply(msg, v1.Reply{Error: err.Error()})
			return
		}
		data, _ := json.Marshal(changes)
		r.reply(msg, v1.Reply{Result: data})
		return
	case v1.FetchRequestInput:
		// The whole input, however long: the browser hashes all of it to
		// approve it.
		in, err := r.sessions.RequestInput(s.ID, req.RequestID)
		if err != nil {
			r.reply(msg, v1.Reply{Error: "that request isn't waiting any more"})
			return
		}
		data, _ := json.Marshal(in)
		r.reply(msg, v1.Reply{Result: data})
		return
	case v1.FetchCommands:
		commands, err := r.sessions.Commands(s.ID)
		if err != nil {
			r.reply(msg, v1.Reply{Error: err.Error()})
			return
		}
		data, _ := json.Marshal(commands)
		r.reply(msg, v1.Reply{Result: data})
		return
	case v1.FetchTerminal:
		screen, err := r.sessions.TerminalScreen(s.ID, req.TerminalID)
		if err != nil {
			r.reply(msg, v1.Reply{Error: err.Error()})
			return
		}
		data, _ := json.Marshal(screen)
		r.reply(msg, v1.Reply{Result: data})
		return
	case v1.FetchToolCall:
		detail, err := r.sessions.ToolCall(s.ID, req.ToolCallID)
		if err != nil {
			r.reply(msg, v1.Reply{Error: err.Error()})
			return
		}
		data, _ := json.Marshal(detail)
		r.reply(msg, v1.Reply{Result: data})
		return
	default:
		err = errors.New("unknown fetch kind " + kind)
	}
	if err != nil {
		r.reply(msg, v1.Reply{Error: err.Error()})
		return
	}
	res := v1.TextResult{Text: text, Length: int64(len(text))}
	if len(res.Text) > MaxBytes {
		res.Text, res.Truncated = res.Text[:MaxBytes], true
	}
	data, _ := json.Marshal(res)
	r.reply(msg, v1.Reply{Result: data})
}

func (r *Responder) transcript(s session.Session) (string, error) {
	data, err := os.ReadFile(filepath.Join(r.transcriptsDir, s.ID+".md"))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return string(data), err
}

// diff reports what a session changed in its worktree: tracked changes
// against the branch point, then any untracked files.
func diff(ctx context.Context, worktree string) (string, error) {
	if _, err := os.Stat(worktree); err != nil {
		return "", fmt.Errorf("the worktree is gone: %s", worktree)
	}
	run := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", worktree}, args...)...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("git %s: %s", args[0], strings.TrimSpace(stderr.String()))
		}
		return string(out), nil
	}
	changes, err := run("diff", "HEAD")
	if err != nil {
		return "", err
	}
	untracked, err := run("ls-files", "--others", "--exclude-standard")
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(changes)
	if u := strings.TrimSpace(untracked); u != "" {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("Untracked files:\n")
		for _, f := range strings.Split(u, "\n") {
			b.WriteString("  " + f + "\n")
		}
	}
	return b.String(), nil
}

func (r *Responder) reply(msg *nats.Msg, reply v1.Reply) {
	data, _ := json.Marshal(reply)
	if err := msg.Respond(data); err != nil {
		slog.Warn("fetch: reply", "subject", msg.Subject, "err", err)
	}
}
