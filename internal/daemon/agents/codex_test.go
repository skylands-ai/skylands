package agents

import (
	"context"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/spachava753/acp-sdk/acp"

	"github.com/skylands-ai/skylands/internal/fakeagent"
)

// TestCodexAdapter keeps the Codex adapter at an exact version and in the
// mode where its edits and escaping commands wait on the daemon.
func TestCodexAdapter(t *testing.T) {
	exact := regexp.MustCompile(`^@agentclientprotocol/codex-acp@\d+\.\d+\.\d+$`)
	if !exact.MatchString(CodexAdapterPackage) {
		t.Errorf("CodexAdapterPackage = %q, want the package at an exact x.y.z version", CodexAdapterPackage)
	}
	ad := DefaultRegistry()["codex"]
	if cmd := ad.Command; len(cmd) == 0 || cmd[len(cmd)-1] != CodexAdapterPackage {
		t.Errorf("codex adapter command = %q, want it to run %s", cmd, CodexAdapterPackage)
	}
	if ad.Provider != "codex" {
		t.Errorf("codex provider = %q", ad.Provider)
	}
	if ad.Mode != "read-only" || len(ad.Env) != 0 {
		t.Errorf("codex mode = %q and env = %q, want read-only sessions set over ACP alone", ad.Mode, ad.Env)
	}
	if ad.Login == "" || ad.Instructions == "" {
		t.Error("codex adapter has no login hint or instructions")
	}
}

// TestGooseAdapter runs the goose on PATH over ACP, in the mode where it
// asks before every tool call, and says how to install it.
func TestGooseAdapter(t *testing.T) {
	ad := DefaultRegistry()["goose"]
	if !reflect.DeepEqual(ad.Command, []string{"goose", "acp"}) || ad.Provider != "goose" {
		t.Errorf("goose adapter = %+v", ad)
	}
	if ad.Mode != "approve" || len(ad.Env) != 0 {
		t.Errorf("goose mode = %q and env = %q, want approve sessions set over ACP alone", ad.Mode, ad.Env)
	}
	if ad.Install == "" {
		t.Error("goose adapter has no install hint")
	}
}

// TestSettingsGoThroughConfigOptions sets a session's mode and model
// through the agent's ACP config options, found by category, on every
// session the agent opens or loads, and refuses to start one whose mode
// does not take.
func TestSettingsGoThroughConfigOptions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd, env := fakeagent.TestCommand()
	agent := func(config string, more ...string) Adapter {
		e := append(append([]string(nil), env...), more...)
		if config != "" {
			e = append(e, fakeagent.EnvConfigOptions+"="+config)
		}
		return Adapter{Name: "fake", Command: cmd, Env: e, Mode: "ask"}
	}
	settings := func(t *testing.T, run *Run, rec *recorder) string {
		t.Helper()
		before := len(rec.said())
		if _, err := run.Prompt(ctx, fakeagent.SettingsQuestion); err != nil {
			t.Fatal(err)
		}
		return rec.said()[before:]
	}

	rec := &recorder{}
	run, err := Start(ctx, agent("1"), t.TempDir(), "fake-large", rec)
	if err != nil {
		t.Fatal(err)
	}
	if got := settings(t, run, rec); got != "mode=ask model=fake-large" {
		t.Errorf("new session: %s", got)
	}
	// The side session for a title is set the same way.
	if _, err := run.Title(ctx, "do the thing", 10*time.Second); err != nil {
		t.Errorf("title: %v", err)
	}
	_ = run.Close()

	// A loaded session comes back at the agent's own values and is set again.
	rec = &recorder{}
	run, how, err := Resume(ctx, agent("1"), t.TempDir(), "fake-large", "sess-before", "", rec)
	if err != nil || how != ResumedByLoad {
		t.Fatalf("Resume = %q, %v", how, err)
	}
	if got := settings(t, run, rec); got != "mode=ask model=fake-large" {
		t.Errorf("loaded session: %s", got)
	}
	_ = run.Close()

	// So is the new session that stands in for one that could not be loaded.
	rec = &recorder{}
	run, how, err = Resume(ctx, agent("1", fakeagent.EnvNoLoad+"=1"), t.TempDir(), "", "sess-before", "", rec)
	if err != nil || how != ResumedBySeed {
		t.Fatalf("Resume = %q, %v", how, err)
	}
	if got := settings(t, run, rec); got != "mode=ask model=fake-small" {
		t.Errorf("seeded session: %s", got)
	}
	_ = run.Close()

	for _, tc := range []struct {
		name, config, model, want string
	}{
		{"a model the agent lacks", "1", "fake-huge", "it offers fake-small, fake-large"},
		{"no mode setting", "", "", "the agent has no mode setting"},
		{"a mode that does not take", "stuck", "", "did not confirm its mode is ask"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Start(ctx, agent(tc.config), t.TempDir(), tc.model, &recorder{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Start = %v, want an error saying %q", err, tc.want)
			}
		})
	}

	// An agent without a model setting cannot be given a model.
	plain := Adapter{Name: "fake", Command: cmd, Env: env}
	if _, err := Start(ctx, plain, t.TempDir(), "fake-large", &recorder{}); err == nil || !strings.Contains(err.Error(), "no model setting") {
		t.Errorf("Start with a model = %v", err)
	}
}

// TestPermissionFillsInWhatTheAgentReported covers a request about a call
// the agent already reported that leaves out the call's kind and title, as
// Codex's does for a command it has started: the rules and the person
// still see what the call is.
func TestPermissionFillsInWhatTheAgentReported(t *testing.T) {
	ctx := context.Background()
	rec := &recorder{}
	h := &handler{ExtensionMux: acp.NewExtensionMux(), auth: make(chan AuthStatus, 1), cwd: t.TempDir(), obs: rec, calls: map[acp.ToolCallId]ToolCall{}}
	h.seq = newSequencer(h.update)
	title, kind := "go test ./...", acp.ToolKindExecute
	_ = h.Update(ctx, &acp.SessionNotification{SessionID: "s", Update: acp.SessionUpdate{
		SessionUpdate: acp.SessionUpdateTypeToolCall, ToolCallID: "c1", Title: &title, Kind: &kind,
		RawInput: map[string]any{"command": "go test ./..."},
	}})
	allow := []acp.PermissionOption{{OptionID: "allow_once", Name: "Allow", Kind: acp.PermissionOptionKindAllowOnce}}
	if _, err := h.RequestPermission(ctx, &acp.RequestPermissionRequest{SessionID: "s", Options: allow, ToolCall: acp.ToolCallUpdate{
		ToolCallID: "c1", Locations: &[]acp.ToolCallLocation{},
		RawInput: map[string]any{"command": "go test ./...", "cwd": "/w"},
	}}); err != nil {
		t.Fatal(err)
	}
	// A request that says what the call is wins over the earlier report.
	editTitle, edit := "Edit main.go", acp.ToolKindEdit
	if _, err := h.RequestPermission(ctx, &acp.RequestPermissionRequest{SessionID: "s", Options: allow, ToolCall: acp.ToolCallUpdate{
		ToolCallID: "c1", Title: &editTitle, Kind: &edit,
	}}); err != nil {
		t.Fatal(err)
	}
	if len(rec.asked) != 2 {
		t.Fatalf("asked %d times", len(rec.asked))
	}
	first, second := rec.asked[0], rec.asked[1]
	if first.ToolCall.Kind != "execute" || first.ToolCall.Title != title || first.ToolCall.Status != "pending" {
		t.Errorf("first request = %+v, want the reported execute call", first.ToolCall)
	}
	if !strings.Contains(string(first.Input), `"cwd":"/w"`) {
		t.Errorf("first input = %s, want the request's own", first.Input)
	}
	if second.ToolCall.Kind != "edit" || second.ToolCall.Title != editTitle {
		t.Errorf("second request = %+v, want what the request said", second.ToolCall)
	}
	if !strings.Contains(string(second.Input), `"command":"go test ./..."`) {
		t.Errorf("second input = %s, want the reported input when the request has none", second.Input)
	}
}

// TestInstructionsGoToAgentsThatTakeThem sends an adapter's instructions
// as the session system prompt, on opening and on loading a session, but
// only to an agent that advertises it.
func TestInstructionsGoToAgentsThatTakeThem(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd, env := fakeagent.TestCommand()
	const instructions = "Ask before writing."
	ask := func(t *testing.T, run *Run, rec *recorder, want string) {
		t.Helper()
		before := len(rec.said())
		if _, err := run.Prompt(ctx, fakeagent.InstructionsQuestion); err != nil {
			t.Fatal(err)
		}
		if got := rec.said()[before:]; got != want {
			t.Errorf("instructions = %q, want %q", got, want)
		}
	}

	taker := Adapter{Name: "fake", Command: cmd, Env: append(append([]string(nil), env...), fakeagent.EnvSystemPrompt+"=1"), Instructions: instructions}
	rec := &recorder{}
	run, err := Start(ctx, taker, t.TempDir(), "", rec)
	if err != nil {
		t.Fatal(err)
	}
	ask(t, run, rec, instructions)
	_ = run.Close()

	rec = &recorder{}
	run, how, err := Resume(ctx, taker, t.TempDir(), "", "sess-before", "", rec)
	if err != nil || how != ResumedByLoad {
		t.Fatalf("Resume = %q, %v", how, err)
	}
	ask(t, run, rec, instructions)
	_ = run.Close()

	// An agent that does not advertise the system prompt is not sent one.
	rec = &recorder{}
	run, err = Start(ctx, Adapter{Name: "fake", Command: cmd, Env: env, Instructions: instructions}, t.TempDir(), "", rec)
	if err != nil {
		t.Fatal(err)
	}
	ask(t, run, rec, fakeagent.NoInstructions)
	_ = run.Close()
}
