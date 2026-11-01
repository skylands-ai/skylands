package agents

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spachava753/acp-sdk/acp"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/fakeagent"
)

func TestMain(m *testing.M) {
	fakeagent.TestMainHook()
	os.Exit(m.Run())
}

type recorder struct {
	mu        sync.Mutex
	name      string
	session   string
	calls     []ToolCall
	plans     int
	usage     []uint64
	messages  []string
	asked     []PermissionRequest
	questions []Question
	// answer, when set, answers every question; otherwise the first choice of
	// each field is picked.
	answer func(Question) Answer
}

func (r *recorder) Question(_ context.Context, q Question) Answer {
	r.mu.Lock()
	r.questions = append(r.questions, q)
	answer := r.answer
	r.mu.Unlock()
	if answer != nil {
		return answer(q)
	}
	values := map[string]any{}
	for _, f := range q.Fields {
		if len(f.Options) > 0 {
			values[f.Key] = f.Options[0].Value
		}
	}
	data, _ := json.Marshal(values)
	return Answer{Answers: data}
}

func TestQuestionsReachTheObserver(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cwd := t.TempDir()
	for _, f := range []string{"main.go", "README.md"} {
		if err := os.WriteFile(filepath.Join(cwd, f), []byte(f), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd, env := fakeagent.TestCommand()
	ad := Adapter{Name: "fake", Command: cmd, Env: env}

	// The fake agent asks which file to edit; the form has the choice and
	// an "other" box, and the answer steers the edit.
	rec := &recorder{answer: func(q Question) Answer {
		return Answer{Answers: json.RawMessage(`{"question_0":"main.go","question_0_custom":"just the top"}`)}
	}}
	if _, err := RunPrompt(ctx, ad, cwd, "ask me which file", "", rec); err != nil {
		t.Fatalf("RunPrompt: %v", err)
	}
	if len(rec.questions) != 1 || rec.questions[0].Message != "Which file should I edit?" || len(rec.questions[0].Fields) != 2 {
		t.Fatalf("questions = %+v", rec.questions)
	}
	if f := rec.questions[0].Fields[0]; f.Key != "question_0" || f.Title != "File" || len(f.Options) != 2 || f.Options[0].Value != "README.md" || f.Options[1].Value != "main.go" {
		t.Errorf("choice field = %+v", f)
	}
	if f := rec.questions[0].Fields[1]; f.Key != "question_0_custom" || f.Title != "Other" || len(f.Options) != 0 {
		t.Errorf("free text field = %+v", f)
	}
	if said := rec.said(); !strings.Contains(said, "edited main.go") || !strings.Contains(said, "You chose main.go and added: just the top.") {
		t.Errorf("after an answer the agent said %q", said)
	}

	// A refusal is told apart from an answer.
	rec2 := &recorder{answer: func(Question) Answer { return Answer{Declined: true} }}
	if _, err := RunPrompt(ctx, ad, cwd, "ask me which file", "", rec2); err != nil {
		t.Fatalf("RunPrompt: %v", err)
	}
	if said := rec2.said(); !strings.Contains(said, "You declined to choose, so I took README.md.") {
		t.Errorf("after a refusal the agent said %q", said)
	}

	// Without the words that make it ask, nothing is asked.
	rec3 := &recorder{}
	if _, err := RunPrompt(ctx, ad, cwd, "do the thing", "", rec3); err != nil {
		t.Fatal(err)
	}
	if len(rec3.questions) != 0 {
		t.Errorf("an unprompted question: %+v", rec3.questions)
	}
}

func (r *recorder) Spawned(name, _, session string) {
	r.mu.Lock()
	r.name, r.session = name, session
	r.mu.Unlock()
}
func (r *recorder) ToolCall(tc ToolCall)  { r.mu.Lock(); r.calls = append(r.calls, tc); r.mu.Unlock() }
func (r *recorder) Plan(e []v1.PlanEntry) { r.mu.Lock(); r.plans++; r.mu.Unlock() }
func (r *recorder) Usage(used, _ uint64)  { r.mu.Lock(); r.usage = append(r.usage, used); r.mu.Unlock() }
func (r *recorder) Permission(_ context.Context, req PermissionRequest) Decision {
	r.mu.Lock()
	r.asked = append(r.asked, req)
	r.mu.Unlock()
	for _, o := range req.Options {
		if o.Kind == "allow_once" {
			return Decision{OptionID: o.ID}
		}
	}
	return Decision{Cancelled: true}
}

func (r *recorder) Message(text string) {
	r.mu.Lock()
	r.messages = append(r.messages, text)
	r.mu.Unlock()
}

// said is everything the agent has said so far.
func (r *recorder) said() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.messages, "")
}

func TestResumeLoadsOrSeedsASession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "main.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd, env := fakeagent.TestCommand()
	ad := Adapter{Name: "fake", Command: cmd, Env: env}
	transcript := "## You\n\nearlier\n\n## Agent\n\nDone with it.\n"

	// An agent that loads sessions comes back under the same session id,
	// and the history it replays while loading stays out of the observer.
	rec := &recorder{}
	run, how, err := Resume(ctx, ad, cwd, "", "sess-before", transcript, rec)
	if err != nil || how != ResumedByLoad {
		t.Fatalf("Resume = %q, %v", how, err)
	}
	defer func() { _ = run.Close() }()
	if rec.session != "sess-before" || rec.name != "skylands-fakeagent" {
		t.Errorf("spawned as %q with session %q", rec.name, rec.session)
	}
	if said := rec.said(); said != "" {
		t.Errorf("the replayed history leaked: %q", said)
	}
	if stop, err := run.Prompt(ctx, "carry on"); err != nil || stop != "end_turn" {
		t.Fatalf("Prompt = %q, %v", stop, err)
	}
	if said := rec.said(); !strings.HasPrefix(said, "Done with \"carry on\".") || strings.Contains(said, "lines of context") || strings.Contains(said, fakeagent.ReplayedLine) {
		t.Errorf("after a load the agent said %q", said)
	}

	// An agent that cannot load gets a new session, and the transcript
	// goes in front of its first prompt only.
	forgetful := Adapter{Name: "fake", Command: cmd, Env: append(append([]string(nil), env...), fakeagent.EnvNoLoad+"=1")}
	rec2 := &recorder{}
	run2, how, err := Resume(ctx, forgetful, cwd, "", "sess-before", transcript, rec2)
	if err != nil || how != ResumedBySeed {
		t.Fatalf("Resume of a forgetful agent = %q, %v", how, err)
	}
	defer func() { _ = run2.Close() }()
	if rec2.session == "" || rec2.session == "sess-before" {
		t.Errorf("seeded session id = %q, want a new one", rec2.session)
	}
	if _, err := run2.Prompt(ctx, "carry on"); err != nil {
		t.Fatal(err)
	}
	first := rec2.said()
	if !strings.HasPrefix(first, "Done with \"carry on\".") || !strings.Contains(first, "lines of context") {
		t.Errorf("after a seed the agent said %q", first)
	}
	if _, err := run2.Prompt(ctx, "once more"); err != nil {
		t.Fatal(err)
	}
	if second := strings.TrimPrefix(rec2.said(), first); !strings.HasPrefix(second, "Done with \"once more\".") || strings.Contains(second, "lines of context") {
		t.Errorf("the seed was sent twice: %q", second)
	}

	// With nothing to load and nothing to seed, Resume is a plain start.
	rec3 := &recorder{}
	run3, how, err := Resume(ctx, ad, cwd, "", "", "", rec3)
	if err != nil || how != ResumedBySeed {
		t.Fatalf("Resume with nothing = %q, %v", how, err)
	}
	defer func() { _ = run3.Close() }()
	if _, err := run3.Prompt(ctx, "fresh"); err != nil {
		t.Fatal(err)
	}
	if said := rec3.said(); !strings.HasPrefix(said, "Done with \"fresh\".") || strings.Contains(said, "lines of context") {
		t.Errorf("a fresh agent said %q", said)
	}
}

func TestRunPromptDrivesTheFakeAgent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cwd := t.TempDir()
	for _, f := range []string{"main.go", "README.md"} {
		if err := os.WriteFile(filepath.Join(cwd, f), []byte(f), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd, env := fakeagent.TestCommand()
	rec := &recorder{}
	stop, err := RunPrompt(ctx, Adapter{Name: "fake", Command: cmd, Env: env}, cwd, "do the thing", "", rec)
	if err != nil {
		t.Fatalf("RunPrompt: %v", err)
	}
	if stop != "end_turn" {
		t.Errorf("stop reason = %q, want end_turn", stop)
	}
	if rec.name != "skylands-fakeagent" {
		t.Errorf("spawned name = %q", rec.name)
	}
	var started, completed int
	var paths []string
	for _, c := range rec.calls {
		switch c.Status {
		case "in_progress":
			started++
			paths = append(paths, c.Locations...)
		case "completed":
			completed++
		}
	}
	if started != 3 || completed != 3 {
		t.Errorf("tool calls started=%d completed=%d, want 3 and 3: %+v", started, completed, rec.calls)
	}
	if strings.Join(paths, ",") != "README.md,main.go,README.md" {
		t.Errorf("locations = %v, want repo-relative paths in script order", paths)
	}
	if rec.plans < 2 || len(rec.usage) < 2 || len(rec.messages) < 10 {
		t.Errorf("plans=%d usage=%v messages=%d", rec.plans, rec.usage, len(rec.messages))
	}
	// The agent streams its answer in rapid chunks; they must land in order.
	want := "Done with \"do the thing\". I read 2 files and edited README.md. Every tracked file was counted, every hidden one was skipped, and nothing was changed on disk."
	if got := strings.Join(rec.messages, ""); got != want {
		t.Errorf("message chunks arrived out of order:\n got %q\nwant %q", got, want)
	}
	// Tool calls start before they complete, every time.
	seen := map[string]bool{}
	for _, c := range rec.calls {
		if c.Status == "completed" && !seen[c.ID] {
			t.Errorf("tool call %s completed before it started", c.ID)
		}
		seen[c.ID] = true
	}
}

func TestTitleUsesASideSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cwd := t.TempDir()
	cmd, env := fakeagent.TestCommand()
	rec := &recorder{}
	run, err := Start(ctx, Adapter{Name: "fake", Command: cmd, Env: env}, cwd, "", rec)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = run.Close() }()
	title, err := run.Title(ctx, "please tidy the readme and the docs", 10*time.Second)
	if err != nil || title != "Please tidy the readme" {
		t.Fatalf("Title = %q, %v", title, err)
	}
	if len(rec.messages) != 0 || len(rec.calls) != 0 {
		t.Errorf("the title request leaked into the observer: messages %d, calls %d", len(rec.messages), len(rec.calls))
	}
}

func TestSequencerRestoresOrder(t *testing.T) {
	var got []string
	s := newSequencer(func(n *acp.SessionNotification) { got = append(got, string(n.SessionID)) })
	stamped := func(seq float64, id string) *acp.SessionNotification {
		return &acp.SessionNotification{SessionID: acp.SessionId(id), Meta: acp.Meta{seqKey: seq}}
	}
	s.Deliver(stamped(2, "b"))
	s.Deliver(stamped(3, "c"))
	s.Deliver(&acp.SessionNotification{SessionID: "unstamped"})
	s.Deliver(stamped(1, "a"))
	s.Deliver(stamped(4, "d"))
	if strings.Join(got, "") != "unstampedabcd" {
		t.Errorf("delivered %v", got)
	}
}

func TestStampNumbersSessionUpdates(t *testing.T) {
	var seq uint64
	line := []byte(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"agent_message_chunk"}}}` + "\n")
	first := stamp(line, &seq)
	second := stamp(line, &seq)
	if !strings.Contains(string(first), `"skylands_seq":1`) || !strings.Contains(string(second), `"skylands_seq":2`) {
		t.Errorf("stamps = %s %s", first, second)
	}
	other := []byte(`{"jsonrpc":"2.0","id":1,"method":"session/request_permission","params":{}}` + "\n")
	if out := stamp(other, &seq); string(out) != string(other) || seq != 2 {
		t.Errorf("a request was changed or counted: %s (seq %d)", out, seq)
	}
}

func TestRunPromptReportsAMissingAgent(t *testing.T) {
	_, err := RunPrompt(context.Background(), Adapter{Name: "nope", Command: []string{"/definitely/not/here"}}, t.TempDir(), "x", "", &recorder{})
	if err == nil {
		t.Fatal("a missing agent command started")
	}
}
