package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/daemon/agents"
	"github.com/skylands-ai/skylands/internal/fakeagent"
)

// withModes adds an agent to the fixture that offers a mode and a model
// setting, and lists ask and plan as its modes.
func withModes(f *fixture, config string) {
	fake := f.cfg.Registry["fake"]
	f.cfg.Registry = agents.Registry{
		"fake": fake,
		"moded": {Name: "moded", Provider: "local", Command: fake.Command,
			Env: append(append([]string(nil), fake.Env...), fakeagent.EnvConfigOptions+"="+config), Modes: []string{"ask", "plan"}},
	}
}

// said is the transcript's text after the last "## Agent" heading.
func said(t *testing.T, f *fixture, id string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.cfg.TranscriptsDir, id+".md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	return strings.TrimSpace(text[strings.LastIndex(text, "## Agent")+len("## Agent"):])
}

// waitFor polls the session until ok says it is as wanted.
func waitFor(t *testing.T, m *Manager, id string, ok func(Session) bool) Session {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		s, _ := m.Get(id)
		if ok(s) {
			return s
		}
		if time.Now().After(deadline) {
			t.Fatalf("gave up waiting; the session is %+v", s)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestSessionModeAndModel starts a session in a listed mode, changes its
// mode and model while it's idle, records the agent's own changes and the
// daemon setting back a mode its adapter doesn't list, and brings the
// session back after a restart on its latest mode and model.
func TestSessionModeAndModel(t *testing.T) {
	f := setup(t)
	withModes(f, "1")
	ctx1, stop1 := context.WithCancel(f.ctx)
	defer stop1()
	m, err := Load(ctx1, f.store, f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(f.ctx, v1.StartSession{ID: "c0", SessionID: "s0", RepoID: f.repo.ID, Agent: "moded", Mode: "auto", Prompt: "x"}); !errors.Is(err, ErrModeNotListed) {
		t.Errorf("start in an unlisted mode: err = %v", err)
	}
	if _, err := m.Start(f.ctx, v1.StartSession{ID: "c1", SessionID: "s1", RepoID: f.repo.ID, Agent: "moded", Mode: "plan", Prompt: fakeagent.SettingsQuestion}); err != nil {
		t.Fatal(err)
	}
	if s := waitStatus(t, m, "s1", StatusIdle); s.Mode != "plan" || s.Model != "" {
		t.Errorf("started session = mode %q model %q", s.Mode, s.Model)
	}
	if got := said(t, f, "s1"); got != "mode=plan model=fake-small" {
		t.Errorf("the agent says %q", got)
	}

	// A person changes the mode and the model; a command seen before gets
	// the session back unchanged.
	if s, err := m.SetMode(f.ctx, v1.SetSessionMode{ID: "c2", SessionID: "s1", Mode: "ask"}, "citadel"); err != nil || s.Mode != "ask" {
		t.Fatalf("SetMode = %q, %v", s.Mode, err)
	}
	if s, err := m.SetMode(f.ctx, v1.SetSessionMode{ID: "c2", SessionID: "s1", Mode: "ask"}, "citadel"); err != nil || s.Mode != "ask" {
		t.Errorf("SetMode again = %q, %v", s.Mode, err)
	}
	if _, err := m.SetMode(f.ctx, v1.SetSessionMode{ID: "c3", SessionID: "s1", Mode: "auto"}, "local"); !errors.Is(err, ErrModeNotListed) {
		t.Errorf("set an unlisted mode: err = %v", err)
	}
	if s, err := m.SetModel(f.ctx, v1.SetSessionModel{ID: "c4", SessionID: "s1", Model: "fake-large"}, "local"); err != nil || s.Model != "fake-large" {
		t.Fatalf("SetModel = %q, %v", s.Model, err)
	}
	if _, err := m.SetModel(f.ctx, v1.SetSessionModel{ID: "c5", SessionID: "s1", Model: "fake-huge"}, "local"); err == nil || !strings.Contains(err.Error(), "it offers fake-small, fake-large") {
		t.Errorf("set a model the agent lacks: err = %v", err)
	}
	if _, err := m.SetModel(f.ctx, v1.SetSessionModel{ID: "c6", SessionID: "nope", Model: "fake-large"}, "local"); !errors.Is(err, ErrUnknownSession) {
		t.Errorf("set the model of an unknown session: err = %v", err)
	}
	if _, err := m.Prompt(f.ctx, v1.SendPrompt{ID: "c7", SessionID: "s1", Text: fakeagent.SettingsQuestion}); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, m, "s1", StatusIdle)
	if got := said(t, f, "s1"); got != "mode=ask model=fake-large" {
		t.Errorf("after the changes the agent says %q", got)
	}

	// The agent changes its model and leaves the listed modes; the daemon
	// sets the mode back.
	if _, err := m.Prompt(f.ctx, v1.SendPrompt{ID: "c8", SessionID: "s1", Text: fakeagent.SwitchModelTo + "fake-small"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, m, "s1", func(s Session) bool { return s.Status == StatusIdle && s.Model == "fake-small" })
	if _, err := m.Prompt(f.ctx, v1.SendPrompt{ID: "c9", SessionID: "s1", Text: fakeagent.SwitchModeTo + "auto"}); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, m, "s1", StatusIdle)
	b, _ := os.ReadFile(filepath.Join(f.cfg.TranscriptsDir, "s1.md"))
	waitFor(t, m, "s1", func(s Session) bool {
		b, _ = os.ReadFile(filepath.Join(f.cfg.TranscriptsDir, "s1.md"))
		return s.Mode == "ask" && strings.Contains(string(b), "Set the mode back to ask.")
	})
	for _, note := range []string{"Mode set to ask.", "Model set to fake-large.", "The agent switched its model to fake-small.", "The agent switched to mode auto, in which it may act without asking. Setting it back."} {
		if !strings.Contains(string(b), note) {
			t.Errorf("the transcript has no %q", note)
		}
	}
	var changes []string
	if err := f.store.Replay(f.ctx, v1.IslandStreamName("ws"), []string{v1.SessionEventFilter("ws")}, func(_ uint64, ev v1.Event) error {
		switch ev.Type {
		case v1.EventSessionModeChanged:
			var d v1.SessionModeChanged
			_ = ev.Decode(&d)
			changes = append(changes, "mode "+d.Mode+" by "+d.By)
		case v1.EventSessionModelChanged:
			var d v1.SessionModelChanged
			_ = ev.Decode(&d)
			changes = append(changes, "model "+d.Model+" by "+d.By)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(changes, "; "), "mode ask by citadel; model fake-large by local; model fake-small by agent; mode auto by agent; mode ask by daemon"; got != want {
		t.Errorf("recorded changes = %s\nwant %s", got, want)
	}

	// After a restart the session comes back on its latest mode and model.
	stop1()
	m.Wait()
	ctx2, stop2 := context.WithCancel(f.ctx)
	defer stop2()
	m2, err := Load(ctx2, f.store, f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if s := waitResumed(t, m2, "s1", 1); s.Mode != "ask" || s.Model != "fake-small" || s.Status != StatusIdle {
		t.Errorf("after the restart = mode %q model %q %s", s.Mode, s.Model, s.Status)
	}
	if _, err := m2.Prompt(f.ctx, v1.SendPrompt{ID: "c10", SessionID: "s1", Text: fakeagent.SettingsQuestion}); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, m2, "s1", StatusIdle)
	if got := said(t, f, "s1"); got != "mode=ask model=fake-small" {
		t.Errorf("after the restart the agent says %q", got)
	}
	if _, err := m2.Stop(f.ctx, v1.StopAgent{ID: "c11", SessionID: "s1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m2.SetMode(f.ctx, v1.SetSessionMode{ID: "c12", SessionID: "s1", Mode: "plan"}, "local"); !errors.Is(err, ErrOver) {
		t.Errorf("set the mode of an ended session: err = %v", err)
	}
	m2.Wait()
}

// TestSessionStopsWhenItsModeIsLost stops a session whose agent left the
// listed modes and would not be set back.
func TestSessionStopsWhenItsModeIsLost(t *testing.T) {
	f := setup(t)
	withModes(f, "stuck")
	// A stuck agent takes no changes, but starts in auto, so listing auto
	// alone lets the session start.
	moded := f.cfg.Registry["moded"]
	moded.Modes = []string{"auto"}
	f.cfg.Registry["moded"] = moded
	m, err := Load(f.ctx, f.store, f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Wait()
	if _, err := m.Start(f.ctx, v1.StartSession{ID: "c1", SessionID: "s1", RepoID: f.repo.ID, Agent: "moded", Prompt: fakeagent.SwitchModeTo + "plan"}); err != nil {
		t.Fatal(err)
	}
	s := waitStatus(t, m, "s1", StatusEnded)
	b, _ := os.ReadFile(filepath.Join(f.cfg.TranscriptsDir, "s1.md"))
	if !strings.Contains(string(b), "could not be set back, so the session is stopped") || s.Mode != "plan" {
		t.Errorf("session = mode %q %s; transcript %q", s.Mode, s.Status, b)
	}
}
