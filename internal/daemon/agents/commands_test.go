package agents

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/skylands-ai/skylands/internal/fakeagent"
)

func TestCommandOf(t *testing.T) {
	for prompt, want := range map[string]string{
		"/review":                     "review",
		"  /review the diff":          "review",
		"/mcp:unison:assistant x":     "mcp:unison:assistant",
		"/design-consent":             "design-consent",
		"/usr/bin is missing":         "",
		"/ review":                    "",
		"please run /review":          "",
		"/":                           "",
		"":                            "",
		"/a.b":                        "",
		"/review\nand then something": "review",
	} {
		got, ok := CommandOf(prompt)
		if got != want || ok != (want != "") {
			t.Errorf("CommandOf(%q) = %q, %v; want %q", prompt, got, ok, want)
		}
	}
}

// TestCommandsAreAnnouncedOrRefused keeps the commands the agent announces,
// less the withheld ones, and refuses a prompt that starts with a
// withheld command or one the agent didn't announce.
func TestCommandsAreAnnouncedOrRefused(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd, env := fakeagent.TestCommand()
	ad := Adapter{Name: "fake", Command: cmd, Env: env, Withheld: []string{"config"}}
	if err := ad.CheckPrompt("/config x=1"); !errors.Is(err, ErrCommandWithheld) {
		t.Errorf("a withheld command, before any agent: err = %v", err)
	}
	if err := ad.CheckPrompt("/review"); err != nil {
		t.Errorf("an unwithheld command, before any agent: err = %v", err)
	}
	rec := &recorder{}
	run, err := Start(ctx, ad, t.TempDir(), Settings{}, rec)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = run.Close() }()

	// A prompt that comes before the commands waits for them.
	if err := run.CheckPrompt(ctx, "/review the diff", 10*time.Second); err != nil {
		t.Errorf("an announced command: err = %v", err)
	}
	commands, announced := run.Commands()
	if !announced || len(commands) != 1 || commands[0].Name != "review" || commands[0].Hint != "what to look at" {
		t.Errorf("Commands = %+v, %v; want review alone, config and config show being withheld", commands, announced)
	}
	for prompt, want := range map[string]error{
		"/config x=1":            ErrCommandWithheld,
		"/CONFIG x=1":            ErrCommandWithheld,
		"/config show":           ErrCommandWithheld,
		"/clear":                 ErrCommandUnknown,
		"/usr/bin is missing":    nil,
		"review the diff please": nil,
	} {
		if err := run.CheckPrompt(ctx, prompt, time.Second); !errors.Is(err, want) && (err != nil || want != nil) {
			t.Errorf("CheckPrompt(%q) = %v, want %v", prompt, err, want)
		}
	}
	before := len(rec.said())
	if _, err := run.Prompt(ctx, "/review the diff"); err != nil {
		t.Fatal(err)
	}
	if got := rec.said()[before:]; got != "ran /review the diff" {
		t.Errorf("the agent said %q", got)
	}

	// A loaded session's commands are kept too, though the rest of what
	// the agent says while loading is history.
	resumed, how, err := Resume(ctx, ad, t.TempDir(), Settings{}, "sess-before", "", &recorder{})
	if err != nil || how != ResumedByLoad {
		t.Fatalf("Resume = %q, %v", how, err)
	}
	defer func() { _ = resumed.Close() }()
	if err := resumed.CheckPrompt(ctx, "/review", 10*time.Second); err != nil {
		t.Errorf("an announced command after a load: err = %v", err)
	}
}
