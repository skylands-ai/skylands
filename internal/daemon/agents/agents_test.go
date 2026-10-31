package agents

import (
	"context"
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
	mu       sync.Mutex
	name     string
	calls    []ToolCall
	plans    int
	usage    []uint64
	messages []string
}

func (r *recorder) Spawned(name, _ string) { r.mu.Lock(); r.name = name; r.mu.Unlock() }
func (r *recorder) ToolCall(tc ToolCall)   { r.mu.Lock(); r.calls = append(r.calls, tc); r.mu.Unlock() }
func (r *recorder) Plan(e []v1.PlanEntry)  { r.mu.Lock(); r.plans++; r.mu.Unlock() }
func (r *recorder) Usage(used, _ uint64)   { r.mu.Lock(); r.usage = append(r.usage, used); r.mu.Unlock() }
func (r *recorder) Message(text string) {
	r.mu.Lock()
	r.messages = append(r.messages, text)
	r.mu.Unlock()
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
	want := "Done. I read 2 files and edited README.md. Every tracked file was counted, every hidden one was skipped, and nothing was changed on disk."
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
