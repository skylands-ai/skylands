package session

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

	done := waitStatus(t, m, "s1", StatusEnded)
	if done.StopReason != "end_turn" || done.AgentName != "skylands-fakeagent" || done.Used == 0 || len(done.Plan) != 3 {
		t.Errorf("ended session = %+v", done)
	}
	var started, completed int
	for _, tc := range done.ToolCalls {
		if tc.Status == "completed" {
			completed++
		}
		started++
	}
	if started != 3 || completed != 3 {
		t.Errorf("tool calls = %+v", done.ToolCalls)
	}
	if done.ToolCalls[0].Locations[0] != "a.go" || done.ToolCalls[0].Kind != "read" {
		t.Errorf("first tool call = %+v, want a read of a.go", done.ToolCalls[0])
	}
	if b, err := os.ReadFile(filepath.Join(f.cfg.TranscriptsDir, "s1.md")); err != nil || !strings.Contains(string(b), "Done.") {
		t.Errorf("transcript = %q, %v", b, err)
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
	for _, want := range []string{"SessionStarted PromptSent AgentSpawned", "ToolCallStarted", "ToolCallCompleted", "PlanUpdated", "UsageReported", "SessionEnded"} {
		if !strings.Contains(got, want) {
			t.Errorf("event types %q lack %q", got, want)
		}
	}
	if !strings.HasSuffix(got, "SessionEnded") || strings.Count(got, "SessionStarted") != 1 {
		t.Errorf("event types = %q", got)
	}

	// A fresh manager rebuilt from the stream knows the session as ended.
	m2, err := Load(f.ctx, f.store, f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if s2, ok := m2.Get("s1"); !ok || s2.Status != StatusEnded || len(s2.ToolCalls) != 3 {
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
}
