package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nkeys"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/approval"
	"github.com/skylands-ai/skylands/internal/fakeagent"
)

// screens collects what terminals publish, by subject.
type screens struct {
	mu  sync.Mutex
	out map[string]*bytes.Buffer
}

func (s *screens) publish(subject string, data []byte) error {
	if !strings.Contains(subject, ".term.") {
		return nil
	}
	var o v1.TerminalOutput
	if err := json.Unmarshal(data, &o); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.out == nil {
		s.out = map[string]*bytes.Buffer{}
	}
	b := s.out[subject]
	if b == nil {
		b = &bytes.Buffer{}
		s.out[subject] = b
	}
	b.Write(o.Data)
	return nil
}

func (s *screens) waitFor(t *testing.T, subject, text string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		s.mu.Lock()
		var got string
		if b := s.out[subject]; b != nil {
			got = b.String()
		}
		s.mu.Unlock()
		if strings.Contains(got, text) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never showed %q; it showed %q", subject, text, got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// terminalEvents lists the session's terminal events in the stream.
func terminalEvents(t *testing.T, f *fixture) []v1.Event {
	t.Helper()
	var out []v1.Event
	_ = f.store.Replay(f.ctx, v1.IslandStreamName("ws"), []string{v1.SessionEventFilter("ws")}, func(_ uint64, ev v1.Event) error {
		if ev.Type == v1.EventTerminalOpened || ev.Type == v1.EventTerminalClosed || ev.Type == v1.EventSessionEnded {
			out = append(out, ev)
		}
		return nil
	})
	return out
}

func closedReason(t *testing.T, ev v1.Event) v1.TerminalClosed {
	t.Helper()
	var d v1.TerminalClosed
	if ev.Type != v1.EventTerminalClosed || ev.Decode(&d) != nil {
		t.Fatalf("event %s isn't TerminalClosed", ev.Type)
	}
	return d
}

// TestTerminalOpensInTheWorktreeAndEndsWithTheSession opens terminals in
// a session's worktree, types into one, and checks how each one's end is
// recorded: its shell exiting, a person closing it, and the session ending.
func TestTerminalOpensInTheWorktreeAndEndsWithTheSession(t *testing.T) {
	f := setup(t)
	out := &screens{}
	f.cfg.Publish = out.publish
	f.cfg.Shell = "/bin/sh"
	m, err := Load(f.ctx, f.store, f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Wait()
	if _, err := m.Start(f.ctx, v1.StartSession{ID: "c1", SessionID: "s1", RepoID: f.repo.ID, Agent: "fake", Prompt: fakeagent.SettingsQuestion}); err != nil {
		t.Fatal(err)
	}
	s := waitStatus(t, m, "s1", StatusIdle)
	if err := os.WriteFile(filepath.Join(s.Worktree, "only-here.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	term, err := m.OpenTerminal(f.ctx, v1.OpenTerminal{ID: "t1", SessionID: "s1", TerminalID: "Term0001", Cols: 90, Rows: 20}, "citadel")
	if err != nil {
		t.Fatal(err)
	}
	if term.ID != "Term0001" || term.Shell != "/bin/sh" {
		t.Errorf("terminal = %+v", term)
	}
	if again, err := m.OpenTerminal(f.ctx, v1.OpenTerminal{ID: "t1", SessionID: "s1", TerminalID: "Term0001"}, "citadel"); err != nil || again.ID != "Term0001" {
		t.Errorf("the same open sent again = %+v, %v", again, err)
	}
	if _, err := m.OpenTerminal(f.ctx, v1.OpenTerminal{ID: "t2", SessionID: "s1", TerminalID: "Term0001"}, "citadel"); !errors.Is(err, ErrTerminalExists) {
		t.Errorf("another open of the same terminal: err = %v", err)
	}
	if s, _ := m.Get("s1"); len(s.Terminals) != 1 {
		t.Errorf("terminals = %+v", s.Terminals)
	}

	// It runs where the agent works, and its output is published.
	subject := v1.TerminalOutputSubject("ws", "s1", "Term0001")
	type_ := func(seq int, text string) {
		t.Helper()
		if err := m.TerminalInput(f.ctx, "s1", "Term0001", v1.TerminalInput{Attach: "Attach01", Seq: seq, Data: []byte(text)}, "citadel"); err != nil {
			t.Fatalf("input %d: %v", seq, err)
		}
	}
	type_(1, "ls; stty size\n")
	out.waitFor(t, subject, "only-here.txt")
	out.waitFor(t, subject, "20 90")
	if err := m.ResizeTerminal("s1", "Term0001", v1.TerminalSize{Cols: 132, Rows: 43}); err != nil {
		t.Fatal(err)
	}
	type_(2, "stty size\n")
	out.waitFor(t, subject, "43 132")
	if err := m.TerminalInput(f.ctx, "s1", "Term0001", v1.TerminalInput{Attach: "Attach01", Seq: 2, Data: []byte("echo twice\n")}, "citadel"); !errors.Is(err, ErrStaleInput) {
		t.Errorf("an input taken already: err = %v", err)
	}
	if screen, err := m.TerminalScreen("s1", "Term0001"); err != nil || !bytes.Contains(screen.Data, []byte("only-here.txt")) {
		t.Errorf("screen = %q, %v", screen.Data, err)
	}
	if _, err := m.TerminalScreen("s2", "Term0001"); !errors.Is(err, ErrUnknownTerminal) {
		t.Errorf("another session's terminal: err = %v", err)
	}

	// Its shell exiting closes it, with the shell's status.
	type_(3, "exit 4\n")
	waitFor(t, m, "s1", func(s Session) bool { return len(s.Terminals) == 0 })
	evs := terminalEvents(t, f)
	if d := closedReason(t, evs[len(evs)-1]); d.Reason != v1.TerminalExited || d.ExitCode == nil || *d.ExitCode != 4 {
		t.Errorf("closed = %+v", d)
	}

	// A person closing one, and the session ending, close the rest.
	if _, err := m.OpenTerminal(f.ctx, v1.OpenTerminal{ID: "t3", SessionID: "s1", TerminalID: "Term0002"}, "citadel"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.OpenTerminal(f.ctx, v1.OpenTerminal{ID: "t4", SessionID: "s1", TerminalID: "Term0003"}, "citadel"); err != nil {
		t.Fatal(err)
	}
	if s, err := m.CloseTerminal(f.ctx, v1.CloseTerminal{ID: "t5", SessionID: "s1", TerminalID: "Term0002"}, "citadel"); err != nil || len(s.Terminals) != 1 {
		t.Fatalf("close = %+v, %v", s.Terminals, err)
	}
	if _, err := m.CloseTerminal(f.ctx, v1.CloseTerminal{ID: "t6", SessionID: "s1", TerminalID: "Term0002"}, "citadel"); err != nil {
		t.Errorf("closing a closed terminal: err = %v", err)
	}
	if _, err := m.Stop(f.ctx, v1.StopAgent{ID: "c2", SessionID: "s1"}); err != nil {
		t.Fatal(err)
	}
	evs = terminalEvents(t, f)
	last := evs[len(evs)-3:]
	if d := closedReason(t, last[0]); d.TerminalID != "Term0002" || d.Reason != v1.TerminalClosedByPerson || d.By != "citadel" {
		t.Errorf("closed by a person = %+v", d)
	}
	if d := closedReason(t, last[1]); d.TerminalID != "Term0003" || d.Reason != v1.TerminalSessionEnded {
		t.Errorf("closed with the session = %+v", d)
	}
	if last[2].Type != v1.EventSessionEnded {
		t.Errorf("the session ended at %s, before its terminals closed", last[2].Type)
	}
	if _, err := m.OpenTerminal(f.ctx, v1.OpenTerminal{ID: "t7", SessionID: "s1", TerminalID: "Term0004"}, "citadel"); !errors.Is(err, ErrOver) {
		t.Errorf("a terminal in an ended session: err = %v", err)
	}
}

// TestTerminalsMustBeSigned opens a terminal and types into it in a world,
// where an approver must sign the opening and every input, over exactly
// that terminal and input, in order.
func TestTerminalsMustBeSigned(t *testing.T) {
	f := setup(t)
	out := &screens{}
	f.cfg.Publish = out.publish
	f.cfg.Shell = "/bin/sh"
	owner, _ := nkeys.CreateOperator()
	world, _ := owner.PublicKey()
	kp, _ := nkeys.CreateUser()
	pub, _ := kp.PublicKey()
	var revoked sync.Map
	f.cfg.World = func() string { return world }
	f.cfg.VerifyDecision = func(_ context.Context, d approval.Decision, s v1.DecisionSignature) error {
		return approval.VerifyDecision(d, s)
	}
	f.cfg.Valid = func(_ context.Context, key string) bool {
		_, gone := revoked.Load(key)
		return !gone
	}
	m, err := Load(f.ctx, f.store, f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Wait()
	sign := func(d approval.Decision) *v1.DecisionSignature {
		t.Helper()
		s, err := approval.SignDecision(kp, d)
		if err != nil {
			t.Fatal(err)
		}
		return &s
	}
	start := v1.StartSession{ID: "c1", SessionID: "s1", RepoID: f.repo.ID, Agent: "fake", Prompt: fakeagent.SettingsQuestion}
	start.Signature = sign(approval.Start{V: approval.DecisionVersion, Kind: approval.KindStart, World: world, Host: "ws", Session: "s1", Repo: f.repo.ID, Agent: "fake", Prompt: approval.TextHash(start.Prompt)})
	if _, err := m.Start(f.ctx, start); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, m, "s1", StatusIdle)

	opening := func(terminal string) approval.Decision {
		return approval.Terminal{V: approval.DecisionVersion, Kind: approval.KindTerminal, World: world, Host: "ws", Session: "s1", Terminal: terminal}
	}
	open := v1.OpenTerminal{ID: "t1", SessionID: "s1", TerminalID: "Term0001"}
	if _, err := m.OpenTerminal(f.ctx, open, "citadel"); !errors.Is(err, approval.ErrUnsigned) {
		t.Errorf("an unsigned opening: err = %v", err)
	}
	open.Signature = sign(opening("Term0002"))
	if _, err := m.OpenTerminal(f.ctx, open, "citadel"); !errors.Is(err, approval.ErrBadSignature) {
		t.Errorf("an opening signed for another terminal: err = %v", err)
	}
	open.Signature = sign(opening("Term0001"))
	if _, err := m.OpenTerminal(f.ctx, open, "citadel"); err != nil {
		t.Fatal(err)
	}
	var opened v1.TerminalOpened
	if evs := terminalEvents(t, f); evs[len(evs)-1].Decode(&opened) != nil || opened.Approver != pub || opened.Signature != open.Signature.Sig {
		t.Errorf("opened = %+v", opened)
	}

	input := func(attach string, seq int, data string) approval.Decision {
		return approval.TerminalInput{V: approval.DecisionVersion, Kind: approval.KindTerminalIn, World: world, Host: "ws", Session: "s1", Terminal: "Term0001", Attach: attach, Seq: seq, Data: approval.BytesHash([]byte(data))}
	}
	send := func(attach string, seq int, data string, sig *v1.DecisionSignature) error {
		return m.TerminalInput(f.ctx, "s1", "Term0001", v1.TerminalInput{Attach: attach, Seq: seq, Data: []byte(data), Signature: sig}, "citadel")
	}
	const typed = "echo signed-\"\"input\n"
	for _, tc := range []struct {
		name string
		sig  *v1.DecisionSignature
		want error
	}{
		{"unsigned", nil, approval.ErrUnsigned},
		{"signed over other bytes", sign(input("Attach01", 1, "rm -rf /\n")), approval.ErrBadSignature},
		{"signed at another place in the count", sign(input("Attach01", 2, typed)), approval.ErrBadSignature},
		{"signed for another view", sign(input("Attach02", 1, typed)), approval.ErrBadSignature},
	} {
		if err := send("Attach01", 1, typed, tc.sig); !errors.Is(err, tc.want) {
			t.Errorf("an input %s: err = %v, want %v", tc.name, err, tc.want)
		}
	}
	signed := sign(input("Attach01", 1, typed))
	if err := send("Attach01", 1, typed, signed); err != nil {
		t.Fatal(err)
	}
	out.waitFor(t, v1.TerminalOutputSubject("ws", "s1", "Term0001"), "signed-input")
	if err := send("Attach01", 1, typed, signed); !errors.Is(err, ErrStaleInput) {
		t.Errorf("the signed input sent again: err = %v", err)
	}
	// Another view of the terminal counts its own inputs.
	if err := send("Attach02", 1, typed, sign(input("Attach02", 1, typed))); err != nil {
		t.Errorf("another view's first input: %v", err)
	}
	// An approver revoked since types nothing more.
	revoked.Store(pub, true)
	if err := send("Attach01", 2, typed, sign(input("Attach01", 2, typed))); err == nil {
		t.Error("a revoked approver's input was taken")
	}
}

// TestTerminalsCloseOnRestart checks that a daemon restart, which ends
// every shell, records the terminals it ended as closed.
func TestTerminalsCloseOnRestart(t *testing.T) {
	f := setup(t)
	f.cfg.Shell = "/bin/sh"
	ctx, cancel := context.WithCancel(f.ctx)
	m, err := Load(ctx, f.store, f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(f.ctx, v1.StartSession{ID: "c1", SessionID: "s1", RepoID: f.repo.ID, Agent: "fake", Prompt: fakeagent.SettingsQuestion}); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, m, "s1", StatusIdle)
	if _, err := m.OpenTerminal(f.ctx, v1.OpenTerminal{ID: "t1", SessionID: "s1", TerminalID: "Term0001"}, "citadel"); err != nil {
		t.Fatal(err)
	}
	cancel()
	m.Wait()

	m2, err := Load(f.ctx, f.store, f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer m2.Wait()
	if s, _ := m2.Get("s1"); len(s.Terminals) != 0 {
		t.Errorf("terminals after a restart = %+v", s.Terminals)
	}
	evs := terminalEvents(t, f)
	if d := closedReason(t, evs[len(evs)-1]); d.Reason != v1.TerminalRestart {
		t.Errorf("closed = %+v", d)
	}
	if _, err := m2.OpenTerminal(f.ctx, v1.OpenTerminal{ID: "t2", SessionID: "s1", TerminalID: "Term0001"}, "citadel"); !errors.Is(err, ErrTerminalExists) {
		t.Errorf("reopening a terminal from before the restart: err = %v", err)
	}
	_, _ = m2.Stop(f.ctx, v1.StopAgent{ID: "c2", SessionID: "s1"})
}
