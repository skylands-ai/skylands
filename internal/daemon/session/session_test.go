package session

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/daemon/agents"
	"github.com/skylands-ai/skylands/internal/daemon/host"
	"github.com/skylands-ai/skylands/internal/es"
	"github.com/skylands-ai/skylands/internal/fakeagent"
	"github.com/skylands-ai/skylands/internal/natsembed"
)

func TestMain(m *testing.M) {
	fakeagent.TestMainHook()
	os.Exit(m.Run())
}

func gitRepo(t *testing.T, files ...string) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f), []byte(f), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("add", ".")
	run("commit", "-q", "-m", "init")
	return dir
}

type fixture struct {
	ctx   context.Context
	store *es.Store
	agg   *host.Host
	repo  host.Repo
	cfg   Config
}

func setup(t *testing.T) *fixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	srv, err := natsembed.Start(ctx, natsembed.Config{Name: "s", ClientAddr: "127.0.0.1:0", StoreDir: t.TempDir(), Domain: "ws"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Shutdown)
	nc, err := nats.Connect(srv.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	store, err := es.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureStream(ctx, v1.IslandStreamName("ws"), v1.IslandSubjects("ws")); err != nil {
		t.Fatal(err)
	}
	agg, err := host.Load(ctx, store, "ws")
	if err != nil {
		t.Fatal(err)
	}
	repo, err := agg.RegisterRepo(ctx, v1.RegisterRepo{ID: "reg-1", Path: gitRepo(t, "a.go", "b.go")})
	if err != nil {
		t.Fatal(err)
	}
	cmd, env := fakeagent.TestCommand()
	base := t.TempDir()
	cfg := Config{
		Host:           "ws",
		WorktreesDir:   filepath.Join(base, "worktrees"),
		TranscriptsDir: filepath.Join(base, "transcripts"),
		Registry:       agents.Registry{"fake": {Name: "fake", Provider: "local", Command: cmd, Env: env}},
		Repos:          agg,
	}
	return &fixture{ctx: ctx, store: store, agg: agg, repo: repo, cfg: cfg}
}

// waitPending blocks until the session has n requests waiting on a person.
func waitPending(t *testing.T, m *Manager, id string, n int) Session {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		s, ok := m.Get(id)
		if ok && len(s.Pending) == n {
			return s
		}
		if time.Now().After(deadline) {
			t.Fatalf("session %s never had %d pending requests: %+v", id, n, s)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestPermissionExpiresAndStopCancels(t *testing.T) {
	f := setup(t)
	f.cfg.PermissionTimeout = 300 * time.Millisecond
	m, err := Load(f.ctx, f.store, f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(f.ctx, v1.StartSession{ID: "c1", SessionID: "s1", RepoID: f.repo.ID, Agent: "fake", Prompt: "tidy up"}); err != nil {
		t.Fatal(err)
	}
	// Nobody answers: the request expires, the edit fails, and the turn
	// still ends.
	idle := waitStatus(t, m, "s1", StatusIdle)
	if len(idle.Pending) != 0 || idle.ToolCalls[2].Status != "failed" {
		t.Errorf("after an expiry = pending %d, tool calls %+v", len(idle.Pending), idle.ToolCalls)
	}
	var expired int
	_ = f.store.Replay(f.ctx, v1.IslandStreamName("ws"), []string{v1.SessionEventFilter("ws")}, func(_ uint64, ev v1.Event) error {
		if ev.Type == v1.EventPermissionExpired {
			var d v1.PermissionExpired
			_ = ev.Decode(&d)
			if d.Reason == "timeout" {
				expired++
			}
		}
		return nil
	})
	if expired != 1 {
		t.Errorf("expired events = %d, want 1 timeout", expired)
	}

	// A stop while a request waits cancels it.
	f.cfg.PermissionTimeout = time.Hour
	m2, err := Load(f.ctx, f.store, f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m2.Start(f.ctx, v1.StartSession{ID: "c2", SessionID: "s2", RepoID: f.repo.ID, Agent: "fake", Prompt: "tidy up"}); err != nil {
		t.Fatal(err)
	}
	waitPending(t, m2, "s2", 1)
	if s, err := m2.Stop(f.ctx, v1.StopAgent{ID: "c3", SessionID: "s2"}); err != nil || s.Status != StatusEnded || len(s.Pending) != 0 {
		t.Fatalf("Stop = %+v, %v", s, err)
	}
	if _, err := m2.Trust(f.ctx, v1.TrustSession{ID: "c4", SessionID: "s2", Trusted: true}); !errors.Is(err, ErrOver) {
		t.Errorf("trust after the end: err = %v", err)
	}
}

func waitStatus(t *testing.T, m *Manager, id string, want Status) Session {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		s, ok := m.Get(id)
		if ok && s.Status == want {
			return s
		}
		if time.Now().After(deadline) {
			t.Fatalf("session %s never reached %s: %+v", id, want, s)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestStartRunsAnAgentAndRecordsItsEvents(t *testing.T) {
	f := setup(t)
	// Everything appended to the transcript is published live, in order,
	// with the offset it landed at.
	var liveMu sync.Mutex
	var lives []v1.Live
	f.cfg.Publish = func(subject string, data []byte) error {
		var l v1.Live
		if err := json.Unmarshal(data, &l); err != nil {
			return err
		}
		if subject != v1.SessionLiveSubject("ws", "s1") {
			t.Errorf("live subject = %q", subject)
		}
		liveMu.Lock()
		lives = append(lives, l)
		liveMu.Unlock()
		return nil
	}
	m, err := Load(f.ctx, f.store, f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	s, err := m.Start(f.ctx, v1.StartSession{ID: "cmd-1", SessionID: "s1", RepoID: f.repo.ID, Agent: "fake", Prompt: "tidy up"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if s.Status != StatusRunning || s.Prompt != "tidy up" || s.Branch != "skylands/s1" {
		t.Errorf("session = %+v", s)
	}
	if _, err := os.Stat(filepath.Join(s.Worktree, "a.go")); err != nil {
		t.Errorf("worktree not checked out: %v", err)
	}

	// The same command gets the same session, and nothing else starts.
	again, err := m.Start(f.ctx, v1.StartSession{ID: "cmd-1", SessionID: "other", RepoID: f.repo.ID, Agent: "fake", Prompt: "x"})
	if err != nil || again.ID != "s1" {
		t.Errorf("repeat = %+v, %v", again, err)
	}

	// The edit needs a person: the request waits with the exact input, and
	// the turn goes on once it is approved.
	asked := waitPending(t, m, "s1", 1)
	req := asked.Pending[0]
	if req.Title != "Edit a.go" || req.Kind != "edit" || len(req.Options) != 3 || !strings.Contains(string(req.Input), "rewrite") || req.Locations[0] != "a.go" {
		t.Errorf("pending request = %+v", req)
	}
	if _, err := m.Approve(f.ctx, v1.ApproveToolCall{ID: "cmd-a1", SessionID: "s1", RequestID: "nope"}, "local"); !errors.Is(err, ErrUnknownRequest) {
		t.Errorf("approve an unknown request: err = %v", err)
	}
	if s, err := m.Approve(f.ctx, v1.ApproveToolCall{ID: "cmd-a2", SessionID: "s1", RequestID: req.RequestID}, "local"); err != nil || len(s.Pending) != 0 {
		t.Fatalf("Approve = %+v, %v", s, err)
	}
	if s, err := m.Approve(f.ctx, v1.ApproveToolCall{ID: "cmd-a2", SessionID: "s1", RequestID: req.RequestID}, "local"); err != nil || len(s.Pending) != 0 {
		t.Errorf("a repeated approve = %+v, %v", s, err)
	}

	// The first turn ends and the session waits for a person.
	idle := waitStatus(t, m, "s1", StatusIdle)
	if idle.StopReason != "end_turn" || idle.AgentName != "skylands-fakeagent" || idle.Used == 0 || len(idle.Plan) != 3 || idle.Turns != 1 {
		t.Errorf("idle session = %+v", idle)
	}
	var started, completed int
	for _, tc := range idle.ToolCalls {
		if tc.Status == "completed" {
			completed++
		}
		started++
	}
	if started != 3 || completed != 3 {
		t.Errorf("tool calls = %+v", idle.ToolCalls)
	}
	if idle.ToolCalls[0].Locations[0] != "a.go" || idle.ToolCalls[0].Kind != "read" {
		t.Errorf("first tool call = %+v, want a read of a.go", idle.ToolCalls[0])
	}

	// The agent named the conversation in a side session, which the
	// transcript and the tool calls never saw.
	for titled := idle; titled.Title == ""; titled, _ = m.Get("s1") {
		time.Sleep(20 * time.Millisecond)
	}
	if titled, _ := m.Get("s1"); titled.Title != "Tidy up" {
		t.Errorf("title = %q, want \"Tidy up\"", titled.Title)
	}

	// A second prompt runs another turn on the same agent. This time the
	// edit is denied, and the agent says so.
	if _, err := m.Prompt(f.ctx, v1.SendPrompt{ID: "cmd-p1", SessionID: "s1", Text: "and again"}); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if _, err := m.Prompt(f.ctx, v1.SendPrompt{ID: "cmd-p2", SessionID: "s1", Text: "too soon"}); !errors.Is(err, ErrNotIdle) {
		t.Errorf("prompt during a turn: err = %v, want ErrNotIdle", err)
	}
	asked = waitPending(t, m, "s1", 1)
	if s, err := m.Deny(f.ctx, v1.DenyToolCall{ID: "cmd-d1", SessionID: "s1", RequestID: asked.Pending[0].RequestID}, "citadel"); err != nil || len(s.Pending) != 0 {
		t.Fatalf("Deny = %+v, %v", s, err)
	}
	second := waitStatus(t, m, "s1", StatusIdle)
	for second.Turns < 2 {
		time.Sleep(20 * time.Millisecond)
		second = waitStatus(t, m, "s1", StatusIdle)
	}
	if second.Turns != 2 || len(second.ToolCalls) != 6 || second.Prompt != "tidy up" || second.ToolCalls[5].Status != "failed" {
		t.Errorf("after the second turn = turns %d, tool calls %+v, prompt %q", second.Turns, second.ToolCalls, second.Prompt)
	}

	// A trusted session asks no one.
	if s, err := m.Trust(f.ctx, v1.TrustSession{ID: "cmd-t1", SessionID: "s1", Trusted: true}); err != nil || !s.Trusted {
		t.Fatalf("Trust = %+v, %v", s, err)
	}
	if _, err := m.Prompt(f.ctx, v1.SendPrompt{ID: "cmd-p3", SessionID: "s1", Text: "once more"}); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	third := waitStatus(t, m, "s1", StatusIdle)
	for third.Turns < 3 {
		time.Sleep(20 * time.Millisecond)
		third = waitStatus(t, m, "s1", StatusIdle)
	}
	if len(third.Pending) != 0 || third.ToolCalls[8].Status != "completed" {
		t.Errorf("trusted turn = pending %d, tool calls %+v", len(third.Pending), third.ToolCalls[6:])
	}
	if b, err := os.ReadFile(filepath.Join(f.cfg.TranscriptsDir, "s1.md")); err != nil || !strings.Contains(string(b), "Done with \"tidy up\"") || !strings.Contains(string(b), "## You\n\nand again") || !strings.Contains(string(b), "Done with \"and again\". I read 2 files and was not allowed to edit") || !strings.Contains(string(b), ToolLinePrefix+"Edit a.go (denied)") {
		t.Errorf("transcript = %q, %v", b, err)
	} else {
		// Tool calls sit on their own lines between the agent's words.
		if !strings.Contains(string(b), "\n"+ToolLinePrefix+"Read a.go\n") {
			t.Errorf("transcript lacks a tool line: %q", b)
		}
		// The live feed is the file, piece by piece.
		liveMu.Lock()
		var joined strings.Builder
		var next int64
		for _, l := range lives {
			if l.Off != next {
				t.Errorf("live offset %d, want %d", l.Off, next)
			}
			joined.WriteString(l.Text)
			next = l.Off + int64(len(l.Text))
		}
		liveMu.Unlock()
		if joined.String() != string(b) {
			t.Errorf("live feed differs from the transcript:\n%q\n%q", joined.String(), b)
		}
	}

	// Stop ends the session; a repeated stop is harmless.
	done, err := m.Stop(f.ctx, v1.StopAgent{ID: "cmd-s1", SessionID: "s1"})
	if err != nil || done.Status != StatusEnded || done.StopReason != "stopped" {
		t.Fatalf("Stop = %+v, %v", done, err)
	}
	if again, err := m.Stop(f.ctx, v1.StopAgent{ID: "cmd-s2", SessionID: "s1"}); err != nil || again.Status != StatusEnded {
		t.Errorf("second Stop = %+v, %v", again, err)
	}
	if _, err := m.Prompt(f.ctx, v1.SendPrompt{ID: "cmd-p9", SessionID: "s1", Text: "x"}); !errors.Is(err, ErrOver) {
		t.Errorf("prompt after stop: err = %v, want ErrOver", err)
	}

	var types []string
	err = f.store.Replay(f.ctx, v1.IslandStreamName("ws"), []string{v1.SessionEventFilter("ws")}, func(seq uint64, ev v1.Event) error {
		types = append(types, ev.Type)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(types, " ")
	for _, want := range []string{"SessionStarted PromptSent AgentSpawned", "ToolCallStarted", "ToolCallCompleted", "PlanUpdated", "UsageReported", "TurnEnded PromptSent", "TurnEnded SessionEnded", "PermissionRequested PermissionGranted", "PermissionRequested PermissionDenied", "SessionTrusted"} {
		if !strings.Contains(got, want) {
			t.Errorf("event types %q lack %q", got, want)
		}
	}
	if !strings.HasSuffix(got, "SessionEnded") || strings.Count(got, "SessionStarted") != 1 || strings.Count(got, "TurnEnded") != 3 || strings.Count(got, "PermissionRequested") != 2 {
		t.Errorf("event types = %q", got)
	}

	// A fresh manager rebuilt from the stream knows the session as ended.
	m2, err := Load(f.ctx, f.store, f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if s2, ok := m2.Get("s1"); !ok || s2.Status != StatusEnded || len(s2.ToolCalls) != 9 || s2.Turns != 3 || s2.Title != "Tidy up" || !s2.Trusted || len(s2.Pending) != 0 {
		t.Errorf("reloaded session = %+v", s2)
	}
}

func TestStartRejectsBadCommands(t *testing.T) {
	f := setup(t)
	m, err := Load(f.ctx, f.store, f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(f.ctx, v1.StartSession{ID: "c1", RepoID: "nope", Agent: "fake", Prompt: "x"}); !errors.Is(err, ErrUnknownRepo) {
		t.Errorf("unknown repo err = %v", err)
	}
	if _, err := m.Start(f.ctx, v1.StartSession{ID: "c2", RepoID: f.repo.ID, Agent: "nope", Prompt: "x"}); !errors.Is(err, ErrUnknownAgent) {
		t.Errorf("unknown agent err = %v", err)
	}
	if _, err := m.Start(f.ctx, v1.StartSession{ID: "c3", RepoID: f.repo.ID, Agent: "fake", Prompt: "  "}); !errors.Is(err, ErrEmptyPrompt) {
		t.Errorf("empty prompt err = %v", err)
	}
	if len(m.List()) != 0 {
		t.Errorf("rejected commands left sessions: %+v", m.List())
	}
}

func TestLoadFailsSessionsLeftRunning(t *testing.T) {
	f := setup(t)
	ev, _ := v1.NewEvent(v1.EventSessionStarted, "c9", v1.SessionStarted{SessionID: "left", RepoID: f.repo.ID, Agent: "fake", Worktree: "/x", Branch: "skylands/left"})
	if _, err := f.store.Append(f.ctx, v1.SessionEventSubject("ws", "left", ev.Type), ev); err != nil {
		t.Fatal(err)
	}
	m, err := Load(f.ctx, f.store, f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	s, ok := m.Get("left")
	if !ok || s.Status != StatusFailed || !strings.Contains(s.Error, "restarted") {
		t.Errorf("session left running after a restart = %+v", s)
	}
	if _, err := m.Prompt(f.ctx, v1.SendPrompt{ID: "c10", SessionID: "nope", Text: "x"}); !errors.Is(err, ErrUnknownSession) {
		t.Errorf("prompt to an unknown session: err = %v", err)
	}
}
