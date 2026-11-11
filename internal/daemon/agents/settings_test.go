package agents

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/skylands-ai/skylands/internal/fakeagent"
)

// TestModeAndModelChanges changes a running conversation's mode and model,
// refuses a mode the adapter doesn't list, hears the agent change its own,
// and sets back a mode the adapter doesn't list.
func TestModeAndModelChanges(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd, env := fakeagent.TestCommand()
	ad := Adapter{Name: "fake", Command: cmd, Env: append(append([]string(nil), env...), fakeagent.EnvConfigOptions+"=1"), Modes: []string{"ask", "plan"}}
	rec := &recorder{}
	ask := func(t *testing.T, run *Run, prompt string) string {
		t.Helper()
		before := len(rec.said())
		if _, err := run.Prompt(ctx, prompt); err != nil {
			t.Fatal(err)
		}
		return rec.said()[before:]
	}
	run, err := Start(ctx, ad, t.TempDir(), Settings{}, rec)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = run.Close() }()
	if got := ask(t, run, fakeagent.SettingsQuestion); got != "mode=ask model=fake-small" {
		t.Errorf("a new session = %s, want the first listed mode", got)
	}

	// Changes the run makes are not taken for the agent's own, though the
	// agent announces each one before it answers.
	if err := run.SetMode(ctx, "plan"); err != nil {
		t.Fatal(err)
	}
	if err := run.SetModel(ctx, "fake-large"); err != nil {
		t.Fatal(err)
	}
	if got := ask(t, run, fakeagent.SettingsQuestion); got != "mode=plan model=fake-large" {
		t.Errorf("after the changes = %s", got)
	}
	if err := run.SetMode(ctx, "auto"); !errors.Is(err, ErrModeNotListed) {
		t.Errorf("set an unlisted mode: err = %v", err)
	}
	if err := run.SetModel(ctx, "fake-huge"); err == nil || !strings.Contains(err.Error(), "it offers fake-small, fake-large") {
		t.Errorf("set a model the agent lacks: err = %v", err)
	}
	if got := rec.changes(); len(got) != 0 {
		t.Errorf("the run's own changes were reported as the agent's: %q", got)
	}

	// The agent changes its own model, and its mode to a listed one.
	ask(t, run, fakeagent.SwitchModelTo+"fake-small")
	ask(t, run, fakeagent.SwitchModeTo+"ask")
	if got, want := rec.changes(), []string{"model=fake-small", "mode=ask by=agent"}; !reflect.DeepEqual(got, want) {
		t.Errorf("the agent's own changes = %q, want %q", got, want)
	}

	// A mode the adapter doesn't list is set back to the last listed one.
	ask(t, run, fakeagent.SwitchModeTo+"auto")
	want := []string{"model=fake-small", "mode=ask by=agent", "mode=auto by=agent", "mode=ask by=daemon"}
	waitFor(t, func() bool { return reflect.DeepEqual(rec.changes(), want) }, func() string { return strings.Join(rec.changes(), "; ") })
	if got := ask(t, run, fakeagent.SettingsQuestion); got != "mode=ask model=fake-small" {
		t.Errorf("after the guard = %s, want the mode set back", got)
	}

	// A session is started and resumed in a listed mode only.
	if _, err := Start(ctx, ad, t.TempDir(), Settings{Mode: "auto"}, &recorder{}); !errors.Is(err, ErrModeNotListed) {
		t.Errorf("start in an unlisted mode: err = %v", err)
	}
	rec = &recorder{}
	resumed, how, err := Resume(ctx, ad, t.TempDir(), Settings{Mode: "plan", Model: "fake-large"}, "sess-before", "", rec)
	if err != nil || how != ResumedByLoad {
		t.Fatalf("Resume = %q, %v", how, err)
	}
	defer func() { _ = resumed.Close() }()
	if got := ask(t, resumed, fakeagent.SettingsQuestion); got != "mode=plan model=fake-large" {
		t.Errorf("a resumed session = %s, want its latest settings", got)
	}
}

// TestModeThatCannotBeSetBack reports a mode the agent left the listed ones
// for and would not leave again.
func TestModeThatCannotBeSetBack(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd, env := fakeagent.TestCommand()
	// A stuck agent takes no changes, but starts in auto, the one mode
	// listed here, so the session starts.
	ad := Adapter{Name: "fake", Command: cmd, Env: append(append([]string(nil), env...), fakeagent.EnvConfigOptions+"=stuck"), Modes: []string{"auto"}}
	rec := &recorder{}
	run, err := Start(ctx, ad, t.TempDir(), Settings{}, rec)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = run.Close() }()
	if _, err := run.Prompt(ctx, fakeagent.SwitchModeTo+"plan"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		got := rec.changes()
		return len(got) == 2 && got[0] == "mode=plan by=agent" && strings.HasPrefix(got[1], "lost: set the agent's mode back to auto")
	}, func() string { return strings.Join(rec.changes(), "; ") })
}

// TestSessionModes settles, changes, and guards the mode of an agent that
// offers it through ACP's session modes and announces each change in its
// reply, as Gemini CLI does, the same way as one with a config option.
func TestSessionModes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	const marker = "[MODE_UPDATE] "
	cmd, env := fakeagent.TestCommand()
	env = append(append([]string(nil), env...), fakeagent.EnvConfigOptions+"=modes", fakeagent.EnvModeMarker+"="+marker)
	ad := Adapter{Name: "fake", Command: cmd, Env: env, Modes: []string{"ask", "plan"}, ModeMarker: marker}
	rec := &recorder{}
	ask := func(t *testing.T, run *Run, prompt string) string {
		t.Helper()
		before := len(rec.said())
		if _, err := run.Prompt(ctx, prompt); err != nil {
			t.Fatal(err)
		}
		return rec.said()[before:]
	}
	run, err := Start(ctx, ad, t.TempDir(), Settings{}, rec)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = run.Close() }()
	if got := ask(t, run, fakeagent.SettingsQuestion); got != "mode=ask model=" {
		t.Errorf("a new session = %s, want the first listed mode, set from the agent's first", got)
	}
	if err := run.SetMode(ctx, "plan"); err != nil {
		t.Fatal(err)
	}
	if got := ask(t, run, fakeagent.SettingsQuestion); got != "mode=plan model=" {
		t.Errorf("after the change = %s", got)
	}
	if err := run.SetModel(ctx, "fake-large"); err == nil || !strings.Contains(err.Error(), "no model setting") {
		t.Errorf("set a model on an agent without a model setting: err = %v", err)
	}
	if got := rec.changes(); len(got) != 0 {
		t.Errorf("the run's own changes were reported as the agent's: %q", got)
	}

	// The agent's own change to a listed mode is heard, and one to a mode
	// the adapter doesn't list is set back.
	ask(t, run, fakeagent.SwitchModeTo+"ask")
	ask(t, run, fakeagent.SwitchModeTo+"auto")
	want := []string{"mode=ask by=agent", "mode=auto by=agent", "mode=ask by=daemon"}
	waitFor(t, func() bool { return reflect.DeepEqual(rec.changes(), want) }, func() string { return strings.Join(rec.changes(), "; ") })
	if got := ask(t, run, fakeagent.SettingsQuestion); got != "mode=ask model=" {
		t.Errorf("after the guard = %s, want the mode set back", got)
	}

	// A title is asked for in a side session, whose mode change stays out
	// of the title.
	title, err := run.Title(ctx, "make it faster", 10*time.Second)
	if err != nil || title == "" || strings.Contains(title, "MODE_UPDATE") {
		t.Errorf("Title = %q, %v", title, err)
	}
	if strings.Contains(rec.said(), marker) {
		t.Errorf("the conversation got the agent's mode announcements: %q", rec.said())
	}

	// A loaded session is set to the conversation's mode again.
	resumed, how, err := Resume(ctx, ad, t.TempDir(), Settings{Mode: "plan"}, "sess-before", "", &recorder{})
	if err != nil || how != ResumedByLoad {
		t.Fatalf("Resume = %q, %v", how, err)
	}
	defer func() { _ = resumed.Close() }()
	if err := resumed.SetMode(ctx, "ask"); err != nil {
		t.Errorf("change a resumed session's mode: %v", err)
	}
}

// waitFor waits up to ten seconds for ok, and fails saying what was seen.
func waitFor(t *testing.T, ok func() bool, seen func() string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("gave up waiting; saw %s", seen())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestTitleInTheFirstMode asks for a session's title in the first mode the
// adapter lists, whatever mode the conversation is in: in plan mode an
// agent like Claude Code names a plan instead.
func TestTitleInTheFirstMode(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd, env := fakeagent.TestCommand()
	ad := Adapter{Name: "fake", Command: cmd, Env: append(append([]string(nil), env...), fakeagent.EnvConfigOptions+"=1"), Modes: []string{"ask", "plan"}}
	run, err := Start(ctx, ad, t.TempDir(), Settings{Mode: "plan"}, &recorder{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = run.Close() }()
	title, err := run.Title(ctx, "make it faster", 10*time.Second)
	if err != nil || title == fakeagent.PlanTitle || title == "" {
		t.Errorf("Title = %q, %v; want a title for the request, not a plan's name", title, err)
	}
}
