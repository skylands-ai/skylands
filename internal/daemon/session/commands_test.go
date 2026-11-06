package session

import (
	"errors"
	"strings"
	"testing"

	v1 "github.com/skylands-ai/skylands/api/v1"
)

// TestSlashCommands runs a command the agent announced, refuses one its
// adapter withholds or the agent didn't announce, before or after the
// session starts, and lists the announced ones for a person to pick from.
func TestSlashCommands(t *testing.T) {
	f := setup(t)
	fake := f.cfg.Registry["fake"]
	fake.Withheld = []string{"config"}
	f.cfg.Registry["fake"] = fake
	m, err := Load(f.ctx, f.store, f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Wait()

	// A withheld command is refused before a session starts.
	if _, err := m.Start(f.ctx, v1.StartSession{ID: "c0", SessionID: "s0", RepoID: f.repo.ID, Agent: "fake", Prompt: "/config x=1"}); !errors.Is(err, ErrCommandWithheld) {
		t.Errorf("start with a withheld command: err = %v", err)
	}
	if _, ok := m.Get("s0"); ok {
		t.Error("a session was started for a withheld command")
	}

	// One the agent didn't announce ends the session once the agent says so.
	if _, err := m.Start(f.ctx, v1.StartSession{ID: "c1", SessionID: "s1", RepoID: f.repo.ID, Agent: "fake", Prompt: "/nope"}); err != nil {
		t.Fatal(err)
	}
	if s := waitStatus(t, m, "s1", StatusFailed); !strings.Contains(s.Error, "isn't one of the commands the agent announced") {
		t.Errorf("an unannounced first command: error %q", s.Error)
	}

	// An announced command runs, as a first prompt and as a reply.
	if _, err := m.Start(f.ctx, v1.StartSession{ID: "c2", SessionID: "s2", RepoID: f.repo.ID, Agent: "fake", Prompt: "/review the diff"}); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, m, "s2", StatusIdle)
	if got := said(t, f, "s2"); got != "ran /review the diff" {
		t.Errorf("the agent said %q", got)
	}
	res, err := m.Commands("s2")
	if err != nil || !res.Announced || len(res.Commands) != 1 || res.Commands[0].Name != "review" {
		t.Errorf("Commands = %+v, %v; want review alone", res, err)
	}
	for text, want := range map[string]error{"/config x=1": ErrCommandWithheld, "/Config": ErrCommandWithheld, "/clear": ErrCommandUnknown} {
		if _, err := m.Prompt(f.ctx, v1.SendPrompt{ID: "p" + text, SessionID: "s2", Text: text}); !errors.Is(err, want) {
			t.Errorf("prompt %q: err = %v, want %v", text, err, want)
		}
	}
	if s, _ := m.Get("s2"); s.Turns != 1 || s.Status != StatusIdle {
		t.Errorf("refused prompts were recorded: %d turns, %s", s.Turns, s.Status)
	}
	if _, err := m.Prompt(f.ctx, v1.SendPrompt{ID: "p-review", SessionID: "s2", Text: "/review again"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, m, "s2", func(s Session) bool { return s.Status == StatusIdle && s.Turns == 2 })
	if got := said(t, f, "s2"); got != "ran /review again" {
		t.Errorf("the agent said %q", got)
	}
	if _, err := m.Commands("nope"); !errors.Is(err, ErrUnknownSession) {
		t.Errorf("commands of an unknown session: err = %v", err)
	}
	if _, err := m.Stop(f.ctx, v1.StopAgent{ID: "c3", SessionID: "s2"}); err != nil {
		t.Fatal(err)
	}
	if res, _ := m.Commands("s2"); res.Announced || len(res.Commands) != 0 {
		t.Errorf("an ended session's commands = %+v", res)
	}
}
