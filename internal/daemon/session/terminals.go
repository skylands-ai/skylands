package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"sync"
	"time"

	"github.com/nats-io/nuid"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/approval"
	"github.com/skylands-ai/skylands/internal/daemon/agents"
	"github.com/skylands-ai/skylands/internal/daemon/terminal"
)

// Terminal is a shell a person has open in a session's worktree.
type Terminal struct {
	ID       string
	Shell    string
	OpenedAt time.Time
	// Approver is the approver who signed the opening, if one did.
	Approver string
}

// MaxTerminalInput is the most one input to a terminal carries. A browser
// sends a longer paste in pieces.
const MaxTerminalInput = 16 * 1024

var (
	ErrUnknownTerminal = errors.New("no such terminal is open in this session")
	ErrTerminalExists  = errors.New("a terminal with this id has been opened before")
	ErrStaleInput      = errors.New("this input was taken already, or came after a later one")
	ErrInputTooLong    = fmt.Errorf("a terminal input carries at most %d bytes", MaxTerminalInput)
)

// terminalIDPattern is a terminal id a browser may pick, and an attach id:
// letters and digits, as they go in subjects.
var terminalIDPattern = regexp.MustCompile(`^[A-Za-z0-9]{8,64}$`)

// openTerminal is a terminal's running shell and what the daemon tracks
// about the input it has taken.
type openTerminal struct {
	session string
	term    *terminal.Terminal

	mu sync.Mutex
	// seqs holds, by attach id, the last input taken from that view.
	seqs map[string]int
	// reason, by, and commandID say why the shell is going, once someone
	// closes it; a shell that exits on its own has none.
	reason, by, commandID string
	// recorded is closed once TerminalClosed is recorded, or the daemon
	// stopped before it could be.
	recorded chan struct{}
}

// OpenTerminal handles OpenTerminal: it starts a shell in an open
// session's worktree and records TerminalOpened. Through the citadel, once
// the world exists, an approver must have signed the opening, which names
// the terminal's id, so it opens that terminal only.
func (m *Manager) OpenTerminal(ctx context.Context, cmd v1.OpenTerminal, by string) (Terminal, error) {
	m.cmdMu.Lock()
	defer m.cmdMu.Unlock()

	m.mu.Lock()
	if id, ok := m.handled[cmd.ID]; ok && cmd.ID != "" {
		for _, t := range m.sessions[id].Terminals {
			if t.ID == cmd.TerminalID {
				m.mu.Unlock()
				return t, nil
			}
		}
	}
	cur, ok := m.sessions[cmd.SessionID]
	if !ok {
		m.mu.Unlock()
		return Terminal{}, fmt.Errorf("%w: %s", ErrUnknownSession, cmd.SessionID)
	}
	s := cur.copy()
	tid := cmd.TerminalID
	if tid == "" {
		tid = nuid.Next()
	}
	seen := m.terminalIDs[tid]
	m.mu.Unlock()

	switch {
	case s.Status == StatusEnded || s.Status == StatusFailed:
		return Terminal{}, ErrOver
	case s.WorktreeRemoved:
		return Terminal{}, ErrNoWorktree
	case !terminalIDPattern.MatchString(tid):
		return Terminal{}, errors.New("a terminal id is 8 to 64 letters and digits")
	case seen:
		return Terminal{}, fmt.Errorf("%w: %s", ErrTerminalExists, tid)
	}
	if _, err := os.Stat(s.Worktree); err != nil {
		return Terminal{}, fmt.Errorf("the worktree is gone: %s", s.Worktree)
	}
	approver, signature, err := m.signed(ctx, by, cmd.Signature, "terminal "+tid, func(world string) approval.Decision {
		return approval.Terminal{V: approval.DecisionVersion, Kind: approval.KindTerminal, World: world, Host: m.cfg.Host, Session: cmd.SessionID, Terminal: tid}
	})
	if err != nil {
		return Terminal{}, err
	}

	ot := &openTerminal{session: s.ID, seqs: map[string]int{}, recorded: make(chan struct{})}
	term, err := terminal.Start(terminal.Config{Shell: m.cfg.Shell, Dir: s.Worktree, Cols: cmd.Cols, Rows: cmd.Rows, Output: m.terminalOutput(s.ID, tid), Watch: agents.WatchFrom(m.ctx)})
	if err != nil {
		return Terminal{}, fmt.Errorf("start a shell: %w", err)
	}
	ot.term = term
	// It is found from the moment it is recorded, so a close that follows
	// at once closes it.
	m.mu.Lock()
	m.terminals[tid] = ot
	m.mu.Unlock()
	if err := m.record(ctx, s.ID, cmd.ID, v1.EventTerminalOpened, v1.TerminalOpened{SessionID: s.ID, TerminalID: tid, Shell: term.Shell(), Approver: approver, Signature: signature}); err != nil {
		m.mu.Lock()
		delete(m.terminals, tid)
		m.mu.Unlock()
		term.Close()
		close(ot.recorded)
		return Terminal{}, err
	}
	m.terms.Add(1)
	go m.watchTerminal(tid, ot)
	slog.Info("session: terminal opened", "session", s.ID, "terminal", tid, "shell", term.Shell(), "dir", s.Worktree)
	out, _ := m.terminal(s.ID, tid)
	return out, nil
}

// terminalOutput publishes what a terminal's shell writes on the
// terminal's output subject, for whoever is watching it.
func (m *Manager) terminalOutput(sessionID, terminalID string) func(v1.TerminalOutput) {
	if m.cfg.Publish == nil {
		return nil
	}
	subject := v1.TerminalOutputSubject(m.cfg.Host, sessionID, terminalID)
	publish := m.cfg.Publish
	return func(out v1.TerminalOutput) {
		if data, err := json.Marshal(out); err == nil {
			_ = publish(subject, data)
		}
	}
}

// watchTerminal waits for a terminal's shell to end, or for the daemon to
// stop, which ends it, and records TerminalClosed with why.
func (m *Manager) watchTerminal(tid string, ot *openTerminal) {
	defer m.terms.Done()
	defer close(ot.recorded)
	select {
	case <-ot.term.Done():
	case <-m.ctx.Done():
		// The session stays open across a restart; the next daemon records
		// the terminal closed as it loads.
		ot.term.Close()
		return
	}
	m.mu.Lock()
	delete(m.terminals, tid)
	m.mu.Unlock()
	ot.mu.Lock()
	closed := v1.TerminalClosed{SessionID: ot.session, TerminalID: tid, Reason: ot.reason, By: ot.by}
	commandID := ot.commandID
	ot.mu.Unlock()
	if closed.Reason == "" {
		code := ot.term.ExitCode()
		closed.Reason, closed.ExitCode = v1.TerminalExited, &code
	}
	if err := m.record(m.ctx, ot.session, commandID, v1.EventTerminalClosed, closed); err != nil && m.ctx.Err() == nil {
		slog.Warn("session: record a terminal's end", "session", ot.session, "terminal", tid, "err", err)
	}
	slog.Info("session: terminal closed", "session", ot.session, "terminal", tid, "reason", closed.Reason)
}

// CloseTerminal handles CloseTerminal: it hangs the terminal up and returns
// once TerminalClosed is recorded. Closing makes nothing act, so it isn't
// signed. A terminal that has closed already is no error.
func (m *Manager) CloseTerminal(ctx context.Context, cmd v1.CloseTerminal, by string) (Session, error) {
	m.mu.Lock()
	s, ok := m.sessions[cmd.SessionID]
	if !ok {
		m.mu.Unlock()
		return Session{}, fmt.Errorf("%w: %s", ErrUnknownSession, cmd.SessionID)
	}
	ot := m.terminals[cmd.TerminalID]
	seen := m.terminalIDs[cmd.TerminalID]
	m.mu.Unlock()
	if ot == nil || ot.session != s.ID {
		if seen && ot == nil {
			out, _ := m.Get(cmd.SessionID)
			return out, nil
		}
		return Session{}, fmt.Errorf("%w: %s", ErrUnknownTerminal, cmd.TerminalID)
	}
	if err := m.closeTerminal(ctx, ot, v1.TerminalClosedByPerson, by, cmd.ID); err != nil {
		return Session{}, err
	}
	out, _ := m.Get(cmd.SessionID)
	return out, nil
}

// closeTerminal hangs a terminal up for reason and waits until its end is
// recorded. Whoever closes it first names the reason.
func (m *Manager) closeTerminal(ctx context.Context, ot *openTerminal, reason, by, commandID string) error {
	ot.mu.Lock()
	if ot.reason == "" {
		ot.reason, ot.by, ot.commandID = reason, by, commandID
	}
	ot.mu.Unlock()
	go ot.term.Close()
	select {
	case <-ot.recorded:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// closeTerminals closes every terminal open in a session, as ending the
// session or removing its worktree does.
func (m *Manager) closeTerminals(ctx context.Context, sessionID, reason string) {
	m.mu.Lock()
	var open []*openTerminal
	for _, ot := range m.terminals {
		if ot.session == sessionID {
			open = append(open, ot)
		}
	}
	m.mu.Unlock()
	var wg sync.WaitGroup
	for _, ot := range open {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = m.closeTerminal(ctx, ot, reason, "", "")
		}()
	}
	wg.Wait()
}

// closeAllTerminals closes every terminal, as a daemon that is stopping
// does, and waits until each one's end is recorded, or the daemon stopped
// before it could be.
func (m *Manager) closeAllTerminals() {
	m.mu.Lock()
	open := make([]*openTerminal, 0, len(m.terminals))
	for _, ot := range m.terminals {
		open = append(open, ot)
	}
	m.mu.Unlock()
	var wg sync.WaitGroup
	for _, ot := range open {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = m.closeTerminal(context.Background(), ot, v1.TerminalRestart, "", "")
		}()
	}
	wg.Wait()
	m.terms.Wait()
}

// TerminalInput writes what a person typed into a terminal. Through the
// citadel, once the world exists, each input must be signed by an approver
// who still approves, over the terminal, the browser view it came from,
// its place in that view's count, and its bytes, and the daemon takes each
// view's inputs only in increasing order, so none can be sent twice.
func (m *Manager) TerminalInput(ctx context.Context, sessionID, terminalID string, in v1.TerminalInput, by string) error {
	ot, err := m.openTerminal(sessionID, terminalID)
	if err != nil {
		return err
	}
	if len(in.Data) > MaxTerminalInput {
		return ErrInputTooLong
	}
	if !terminalIDPattern.MatchString(in.Attach) || in.Seq < 1 {
		return errors.New("a terminal input names its view with 8 to 64 letters and digits, and counts from one")
	}
	if world := m.world(); by == "citadel" && world != "" {
		if in.Signature == nil {
			return ErrUnsigned
		}
		d := approval.TerminalInput{V: approval.DecisionVersion, Kind: approval.KindTerminalIn, World: world, Host: m.cfg.Host, Session: sessionID, Terminal: terminalID, Attach: in.Attach, Seq: in.Seq, Data: approval.BytesHash(in.Data)}
		// Every keystroke is checked against the registry as the daemon
		// holds it, without waiting to read any more of it: the opening
		// was checked against all of it, and a revocation the daemon reads
		// stops the next keystroke.
		if err := approval.VerifyDecision(d, *in.Signature); err != nil {
			return err
		}
		if m.cfg.Valid == nil || !m.cfg.Valid(ctx, in.Signature.Approver) {
			return errors.New("the input's signer doesn't approve for this world")
		}
	}
	ot.mu.Lock()
	if in.Seq <= ot.seqs[in.Attach] {
		ot.mu.Unlock()
		return ErrStaleInput
	}
	ot.seqs[in.Attach] = in.Seq
	ot.mu.Unlock()
	return ot.term.Write(in.Data)
}

// ResizeTerminal tells a terminal's shell its new size.
func (m *Manager) ResizeTerminal(sessionID, terminalID string, size v1.TerminalSize) error {
	ot, err := m.openTerminal(sessionID, terminalID)
	if err != nil {
		return err
	}
	return ot.term.Resize(size.Cols, size.Rows)
}

// TerminalScreen is the newest of what a terminal showed, as much as the
// daemon keeps.
func (m *Manager) TerminalScreen(sessionID, terminalID string) (v1.TerminalOutput, error) {
	ot, err := m.openTerminal(sessionID, terminalID)
	if err != nil {
		return v1.TerminalOutput{}, err
	}
	return ot.term.Screen(), nil
}

func (m *Manager) openTerminal(sessionID, terminalID string) (*openTerminal, error) {
	m.mu.Lock()
	ot := m.terminals[terminalID]
	m.mu.Unlock()
	if ot == nil || ot.session != sessionID {
		return nil, fmt.Errorf("%w: %s", ErrUnknownTerminal, terminalID)
	}
	return ot, nil
}

// terminal is one of a session's open terminals as the session records it.
func (m *Manager) terminal(sessionID, terminalID string) (Terminal, bool) {
	s, ok := m.Get(sessionID)
	if !ok {
		return Terminal{}, false
	}
	for _, t := range s.Terminals {
		if t.ID == terminalID {
			return t, true
		}
	}
	return Terminal{}, false
}

// applyTerminal folds a terminal event into the state. The caller holds mu.
func (m *Manager) applyTerminal(ev v1.Event, touch func(id string) *Session) {
	switch ev.Type {
	case v1.EventTerminalOpened:
		var d v1.TerminalOpened
		if ev.Decode(&d) != nil {
			return
		}
		m.terminalIDs[d.TerminalID] = true
		if s := touch(d.SessionID); s != nil {
			s.Terminals = append(s.Terminals, Terminal{ID: d.TerminalID, Shell: d.Shell, OpenedAt: ev.Time, Approver: d.Approver})
			if ev.CommandID != "" {
				m.handled[ev.CommandID] = d.SessionID
			}
		}
	case v1.EventTerminalClosed:
		var d v1.TerminalClosed
		if ev.Decode(&d) != nil {
			return
		}
		if s := touch(d.SessionID); s != nil {
			kept := s.Terminals[:0]
			for _, t := range s.Terminals {
				if t.ID != d.TerminalID {
					kept = append(kept, t)
				}
			}
			s.Terminals = kept
			if ev.CommandID != "" {
				m.handled[ev.CommandID] = d.SessionID
			}
		}
	}
}
