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

	// A stop while a request waits cancels it. The first session is ended
	// first, so the second manager has nothing to bring back.
	if _, err := m.Stop(f.ctx, v1.StopAgent{ID: "c1-stop", SessionID: "s1"}); err != nil {
		t.Fatal(err)
	}
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

func TestLoadFailsASessionWhoseWorktreeIsGone(t *testing.T) {
	f := setup(t)
	ev, _ := v1.NewEvent(v1.EventSessionStarted, "c9", v1.SessionStarted{SessionID: "left", RepoID: f.repo.ID, Agent: "fake", Worktree: "/x", Branch: "skylands/left"})
	if _, err := f.store.Append(f.ctx, v1.SessionEventSubject("ws", "left", ev.Type), ev); err != nil {
		t.Fatal(err)
	}
	m, err := Load(f.ctx, f.store, f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	// The agent cannot come back without its worktree: that is a failure
	// of its own, and it says so.
	s := waitStatus(t, m, "left", StatusFailed)
	if !strings.Contains(s.Error, "worktree is gone") || s.Resumes != 0 {
		t.Errorf("session without a worktree = %+v", s)
	}
	if _, err := m.Prompt(f.ctx, v1.SendPrompt{ID: "c10", SessionID: "nope", Text: "x"}); !errors.Is(err, ErrUnknownSession) {
		t.Errorf("prompt to an unknown session: err = %v", err)
	}
}

// waitResumed blocks until the session has lived through n restarts.
func waitResumed(t *testing.T, m *Manager, id string, n int) Session {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		s, ok := m.Get(id)
		if ok && (s.Resumes == n || s.Status == StatusFailed) {
			if s.Status == StatusFailed {
				t.Fatalf("session %s failed instead of resuming: %s", id, s.Error)
			}
			return s
		}
		if time.Now().After(deadline) {
			t.Fatalf("session %s never resumed %d times: %+v", id, n, s)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestLoadResumesOpenSessions(t *testing.T) {
	f := setup(t)
	// Each manager is a daemon of its own: cancelling its context is the
	// daemon stopping, and its agents die with it.
	ctx1, stop1 := context.WithCancel(f.ctx)
	defer stop1()
	m1, err := Load(ctx1, f.store, f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m1.Start(f.ctx, v1.StartSession{ID: "c1", SessionID: "s1", RepoID: f.repo.ID, Agent: "fake", Prompt: "keep going"}); err != nil {
		t.Fatal(err)
	}
	before := waitPending(t, m1, "s1", 1)
	first := before.Pending[0].RequestID
	if before.AgentSessionID == "" {
		t.Fatalf("no agent session id recorded: %+v", before)
	}
	stop1()
	m1.Wait()

	// The daemon comes back mid-turn. The session is still running, what
	// was waiting on a person has expired, and the agent is loaded again
	// under its own session and continues the turn, which asks about the
	// edit again.
	ctx2, stop2 := context.WithCancel(f.ctx)
	defer stop2()
	m2, err := Load(ctx2, f.store, f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := m2.Get("s1"); s.Status != StatusRunning || len(s.Pending) != 0 {
		t.Errorf("right after the restart = %+v", s)
	}
	if _, err := m2.Prompt(f.ctx, v1.SendPrompt{ID: "c2", SessionID: "s1", Text: "x"}); !errors.Is(err, ErrResuming) && !errors.Is(err, ErrNotIdle) {
		t.Errorf("prompt during the resume: err = %v", err)
	}
	resumed := waitResumed(t, m2, "s1", 1)
	if resumed.Resumed.How != v1.ResumedByLoad || !resumed.Resumed.TurnResumed || resumed.Resumed.RestartedAt.IsZero() || resumed.AgentSessionID != before.AgentSessionID || resumed.Status != StatusRunning {
		t.Errorf("resumed session = %+v", resumed)
	}
	asked := waitPending(t, m2, "s1", 1)
	if asked.Pending[0].RequestID == first || asked.Pending[0].Title != "Edit a.go" {
		t.Errorf("the edit was not asked again: %+v", asked.Pending)
	}
	if _, err := m2.Approve(f.ctx, v1.ApproveToolCall{ID: "c3", SessionID: "s1", RequestID: asked.Pending[0].RequestID}, "local"); err != nil {
		t.Fatal(err)
	}
	if idle := waitStatus(t, m2, "s1", StatusIdle); idle.Turns != 1 || idle.Error != "" {
		t.Errorf("after the resumed turn = %+v", idle)
	}
	b, err := os.ReadFile(filepath.Join(f.cfg.TranscriptsDir, "s1.md"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(b); !strings.Contains(got, "\n---\n\n*The daemon restarted at ") || !strings.Contains(got, "continues the interrupted turn.*\n\n## Agent\n\n") || strings.Contains(got, fakeagent.ReplayedLine) || strings.Count(got, "## You") != 1 || !strings.Contains(got, "Done with \"The daemon that runs this session restarted") {
		t.Errorf("transcript after the resume = %q", got)
	}

	// Across a restart an idle session stays idle, and the next prompt runs
	// on the agent that was brought back.
	stop2()
	m2.Wait()
	ctx3, stop3 := context.WithCancel(f.ctx)
	defer stop3()
	m3, err := Load(ctx3, f.store, f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if s := waitResumed(t, m3, "s1", 2); s.Status != StatusIdle || s.Resumed.TurnResumed || s.Resumed.How != v1.ResumedByLoad {
		t.Errorf("idle session after a restart = %+v", s)
	}
	if _, err := m3.Prompt(f.ctx, v1.SendPrompt{ID: "c4", SessionID: "s1", Text: "and again"}); err != nil {
		t.Fatal(err)
	}
	asked = waitPending(t, m3, "s1", 1)
	if _, err := m3.Approve(f.ctx, v1.ApproveToolCall{ID: "c5", SessionID: "s1", RequestID: asked.Pending[0].RequestID}, "local"); err != nil {
		t.Fatal(err)
	}
	second := waitStatus(t, m3, "s1", StatusIdle)
	for second.Turns < 2 {
		time.Sleep(20 * time.Millisecond)
		second = waitStatus(t, m3, "s1", StatusIdle)
	}

	// An agent that cannot load its session gets a new one, seeded with
	// the transcript.
	stop3()
	m3.Wait()
	cmd, env := fakeagent.TestCommand()
	seeded := f.cfg
	seeded.Registry = agents.Registry{"fake": {Name: "fake", Provider: "local", Command: cmd, Env: append(append([]string(nil), env...), fakeagent.EnvNoLoad+"=1")}}
	ctx4, stop4 := context.WithCancel(f.ctx)
	defer stop4()
	m4, err := Load(ctx4, f.store, seeded)
	if err != nil {
		t.Fatal(err)
	}
	if s := waitResumed(t, m4, "s1", 3); s.Status != StatusIdle || s.Resumed.How != v1.ResumedBySeed || s.AgentSessionID == before.AgentSessionID {
		t.Errorf("seeded session = %+v", s)
	}
	if _, err := m4.Prompt(f.ctx, v1.SendPrompt{ID: "c6", SessionID: "s1", Text: "after a seed"}); err != nil {
		t.Fatal(err)
	}
	asked = waitPending(t, m4, "s1", 1)
	if _, err := m4.Approve(f.ctx, v1.ApproveToolCall{ID: "c7", SessionID: "s1", RequestID: asked.Pending[0].RequestID}, "local"); err != nil {
		t.Fatal(err)
	}
	third := waitStatus(t, m4, "s1", StatusIdle)
	for third.Turns < 3 {
		time.Sleep(20 * time.Millisecond)
		third = waitStatus(t, m4, "s1", StatusIdle)
	}
	b, _ = os.ReadFile(filepath.Join(f.cfg.TranscriptsDir, "s1.md"))
	if got := string(b); !strings.Contains(got, "a new agent will be given the conversation so far.*") || !strings.Contains(got, "Done with \"after a seed\".") || !strings.Contains(got, "lines of context.") || !strings.Contains(got, "Done with \"and again\".") {
		t.Errorf("transcript after the seed = %q", got)
	}

	// The history says what happened: three resumes, one expiry by the
	// restart, and no failure.
	counts := map[string]int{}
	var expired []string
	err = f.store.Replay(f.ctx, v1.IslandStreamName("ws"), []string{v1.SessionEventFilter("ws")}, func(_ uint64, ev v1.Event) error {
		counts[ev.Type]++
		if ev.Type == v1.EventPermissionExpired {
			var d v1.PermissionExpired
			_ = ev.Decode(&d)
			expired = append(expired, d.Reason)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if counts[v1.EventSessionResumed] != 3 || counts[v1.EventAgentFailed] != 0 || counts[v1.EventAgentSpawned] != 4 || strings.Join(expired, ",") != "restart" {
		t.Errorf("event counts = %v, expiries = %v", counts, expired)
	}

	// Once stopped, a session stays stopped through a restart.
	if _, err := m4.Stop(f.ctx, v1.StopAgent{ID: "c8", SessionID: "s1"}); err != nil {
		t.Fatal(err)
	}
	stop4()
	m4.Wait()
	m5, err := Load(f.ctx, f.store, f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := m5.Get("s1"); s.Status != StatusEnded || s.Resumes != 3 || s.Turns != 3 {
		t.Errorf("ended session after a restart = %+v", s)
	}
}

// gitIdentity gives the daemon's git commands an author and committer, as
// a host's global config would.
func gitIdentity(t *testing.T) {
	t.Helper()
	for _, kv := range [][2]string{{"GIT_AUTHOR_NAME", "t"}, {"GIT_AUTHOR_EMAIL", "t@t"}, {"GIT_COMMITTER_NAME", "t"}, {"GIT_COMMITTER_EMAIL", "t@t"}} {
		t.Setenv(kv[0], kv[1])
	}
}

// fakeGH puts a gh on the PATH that records its arguments and prints a
// pull request URL, so opening a pull request needs no GitHub.
func fakeGH(t *testing.T) (argsFile string) {
	t.Helper()
	dir := t.TempDir()
	argsFile = filepath.Join(dir, "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsFile + "\necho https://example.com/pr/7\n"
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return argsFile
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestChangesAreCommittedMergedSentAndRemoved(t *testing.T) {
	f := setup(t)
	gitIdentity(t)
	ghArgs := fakeGH(t)
	// The repository has an origin to push to: a bare clone beside it.
	bare := t.TempDir()
	gitOut(t, bare, "init", "-q", "--bare")
	gitOut(t, f.repo.Path, "remote", "add", "origin", bare)
	base := gitOut(t, f.repo.Path, "rev-parse", "--abbrev-ref", "HEAD")

	m, err := Load(f.ctx, f.store, f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	s, err := m.Start(f.ctx, v1.StartSession{ID: "c1", SessionID: "s1", RepoID: f.repo.ID, Agent: "fake", Prompt: "add a file"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Base != base || s.InCheckout || s.Branch != "skylands/s1" {
		t.Errorf("session = base %q, inCheckout %v, branch %q", s.Base, s.InCheckout, s.Branch)
	}
	// Nothing can be done with the changes while the agent is mid-turn.
	if _, err := m.Commit(f.ctx, v1.CommitChanges{ID: "x0", SessionID: "s1"}); !errors.Is(err, ErrBusy) {
		t.Errorf("commit during a turn: err = %v", err)
	}
	asked := waitPending(t, m, "s1", 1)
	if _, err := m.Approve(f.ctx, v1.ApproveToolCall{ID: "c2", SessionID: "s1", RequestID: asked.Pending[0].RequestID}, "local"); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, m, "s1", StatusIdle)

	// The fake agent changes nothing, so the test plays the agent's edit.
	if _, err := m.Commit(f.ctx, v1.CommitChanges{ID: "x1", SessionID: "s1"}); !errors.Is(err, ErrNothingToCommit) {
		t.Errorf("commit with nothing changed: err = %v", err)
	}
	if _, err := m.Merge(f.ctx, v1.MergeChanges{ID: "x2", SessionID: "s1"}); !errors.Is(err, ErrNothingToMerge) {
		t.Errorf("merge with nothing to merge: err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(s.Worktree, "new.go"), []byte("package new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Worktree, "a.go"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changes, err := m.Changes(f.ctx, "s1")
	if err != nil || strings.Join(changes.Changed, ",") != "a.go,new.go" || changes.Ahead != 0 || changes.Base != base {
		t.Errorf("Changes = %+v, %v", changes, err)
	}
	if _, err := m.Merge(f.ctx, v1.MergeChanges{ID: "x3", SessionID: "s1"}); !errors.Is(err, ErrUncommitted) {
		t.Errorf("merge with uncommitted changes: err = %v", err)
	}

	// A commit takes everything in the worktree, with the title as the
	// message when none is given; the same command id commits nothing more.
	for titled, _ := m.Get("s1"); titled.Title == ""; titled, _ = m.Get("s1") {
		time.Sleep(20 * time.Millisecond)
	}
	committed, err := m.Commit(f.ctx, v1.CommitChanges{ID: "x4", SessionID: "s1"})
	if err != nil || committed.Commits != 1 || committed.Commit == "" {
		t.Fatalf("Commit = %+v, %v", committed, err)
	}
	if again, err := m.Commit(f.ctx, v1.CommitChanges{ID: "x4", SessionID: "s1"}); err != nil || again.Commits != 1 {
		t.Errorf("repeated commit = %+v, %v", again, err)
	}
	if msg := gitOut(t, s.Worktree, "log", "-1", "--format=%s"); msg != "Add a file" {
		t.Errorf("commit message = %q, want the title", msg)
	}
	if changes, _ := m.Changes(f.ctx, "s1"); len(changes.Changed) != 0 || changes.Ahead != 1 {
		t.Errorf("after the commit = %+v", changes)
	}

	// A merge lands the branch in the checkout, on its base.
	merged, err := m.Merge(f.ctx, v1.MergeChanges{ID: "x5", SessionID: "s1"})
	if err != nil || merged.MergedInto != base || merged.MergedCommit != committed.Commit {
		t.Fatalf("Merge = %+v, %v", merged, err)
	}
	if _, err := os.Stat(filepath.Join(f.repo.Path, "new.go")); err != nil {
		t.Errorf("the checkout lacks the merged file: %v", err)
	}
	if changes, _ := m.Changes(f.ctx, "s1"); changes.Ahead != 0 {
		t.Errorf("after the merge = %+v", changes)
	}

	// Another commit, pushed as a pull request through gh.
	if err := os.WriteFile(filepath.Join(s.Worktree, "b.go"), []byte("more\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Commit(f.ctx, v1.CommitChanges{ID: "x6", SessionID: "s1", Message: "Second change"}); err != nil {
		t.Fatal(err)
	}
	pr, err := m.OpenPullRequest(f.ctx, v1.OpenPullRequest{ID: "x7", SessionID: "s1"})
	if err != nil || pr.PullRequest != "https://example.com/pr/7" {
		t.Fatalf("OpenPullRequest = %+v, %v", pr, err)
	}
	if args, _ := os.ReadFile(ghArgs); !strings.Contains(string(args), "pr\ncreate\n--head\nskylands/s1\n--base\n"+base+"\n--title\nAdd a file\n--body\nadd a file") {
		t.Errorf("gh was called with %q", args)
	}
	if pushed := gitOut(t, bare, "branch", "--list", "skylands/s1"); !strings.Contains(pushed, "skylands/s1") {
		t.Errorf("the branch was not pushed: %q", pushed)
	}

	// The worktree stays until the session is over and someone removes it.
	if _, _, err := m.Remove(f.ctx, "x8", "s1", false); !errors.Is(err, ErrOpen) {
		t.Errorf("remove an open session: err = %v", err)
	}
	if _, err := m.Stop(f.ctx, v1.StopAgent{ID: "x9", SessionID: "s1"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Worktree, "dirty.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Prune keeps a dirty worktree, and an unforced removal refuses it.
	if pruned := m.Prune(f.ctx); len(pruned) != 1 || pruned[0].Removed || pruned[0].Kept == "" {
		t.Errorf("Prune of a dirty worktree = %+v", pruned)
	}
	if _, _, err := m.Remove(f.ctx, "x10", "s1", false); err == nil {
		t.Error("a dirty worktree was removed without force")
	}
	// Forced, the worktree goes, and the branch with it even though the
	// second commit was never merged.
	removed, branchDeleted, err := m.Remove(f.ctx, "x11", "s1", true)
	if err != nil || !removed.WorktreeRemoved || !branchDeleted {
		t.Fatalf("Remove = %+v, %v, %v", removed, branchDeleted, err)
	}
	if _, err := os.Stat(s.Worktree); err == nil {
		t.Error("the worktree directory is still there")
	}
	if branches := gitOut(t, f.repo.Path, "branch", "--list", "skylands/s1"); branches != "" {
		t.Errorf("the branch is still there: %q", branches)
	}
	if _, err := m.Commit(f.ctx, v1.CommitChanges{ID: "x12", SessionID: "s1"}); !errors.Is(err, ErrNoWorktree) {
		t.Errorf("commit after removal: err = %v", err)
	}
	if changes, err := m.Changes(f.ctx, "s1"); err != nil || !changes.WorktreeRemoved {
		t.Errorf("Changes after removal = %+v, %v", changes, err)
	}
	if pruned := m.Prune(f.ctx); len(pruned) != 0 {
		t.Errorf("Prune after removal = %+v", pruned)
	}
	if b, _ := os.ReadFile(filepath.Join(f.cfg.TranscriptsDir, "s1.md")); !strings.Contains(string(b), "*Committed "+committed.Commit[:7]+": Add a file.*") || !strings.Contains(string(b), "*Merged into "+base+" as ") || !strings.Contains(string(b), "*Opened pull request https://example.com/pr/7.*") || !strings.Contains(string(b), "*Worktree and branch removed.*") {
		t.Errorf("transcript = %q", b)
	}

	// A fresh manager knows all of it.
	m2, err := Load(f.ctx, f.store, f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if s2, _ := m2.Get("s1"); s2.Commits != 2 || s2.MergedInto != base || s2.PullRequest == "" || !s2.WorktreeRemoved || s2.Base != base {
		t.Errorf("reloaded session = %+v", s2)
	}
}

func TestSessionInTheCheckout(t *testing.T) {
	f := setup(t)
	gitIdentity(t)
	base := gitOut(t, f.repo.Path, "rev-parse", "--abbrev-ref", "HEAD")
	m, err := Load(f.ctx, f.store, f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	s, err := m.Start(f.ctx, v1.StartSession{ID: "c1", SessionID: "s1", RepoID: f.repo.ID, Agent: "fake", Prompt: "work here", InCheckout: true})
	if err != nil {
		t.Fatal(err)
	}
	if !s.InCheckout || s.Worktree != f.repo.Path || s.Branch != base || s.Base != base {
		t.Errorf("checkout session = %+v", s)
	}
	// The checkout is shared, so a second session there must wait.
	if _, err := m.Start(f.ctx, v1.StartSession{ID: "c2", SessionID: "s2", RepoID: f.repo.ID, Agent: "fake", Prompt: "me too", InCheckout: true}); !errors.Is(err, ErrCheckoutBusy) {
		t.Errorf("second checkout session: err = %v", err)
	}
	asked := waitPending(t, m, "s1", 1)
	if _, err := m.Approve(f.ctx, v1.ApproveToolCall{ID: "c3", SessionID: "s1", RequestID: asked.Pending[0].RequestID}, "local"); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, m, "s1", StatusIdle)

	// A commit lands on the checkout's own branch; there is no branch to
	// merge, send, or remove.
	if err := os.WriteFile(filepath.Join(f.repo.Path, "here.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if changes, err := m.Changes(f.ctx, "s1"); err != nil || !changes.InCheckout || changes.Ahead != 0 || strings.Join(changes.Changed, ",") != "here.go" {
		t.Errorf("Changes = %+v, %v", changes, err)
	}
	if c, err := m.Commit(f.ctx, v1.CommitChanges{ID: "c4", SessionID: "s1", Message: "In place"}); err != nil || c.Commits != 1 {
		t.Fatalf("Commit = %+v, %v", c, err)
	}
	if msg := gitOut(t, f.repo.Path, "log", "-1", "--format=%s"); msg != "In place" {
		t.Errorf("checkout's last commit = %q", msg)
	}
	if _, err := m.Merge(f.ctx, v1.MergeChanges{ID: "c5", SessionID: "s1"}); !errors.Is(err, ErrInCheckout) {
		t.Errorf("merge a checkout session: err = %v", err)
	}
	if _, err := m.OpenPullRequest(f.ctx, v1.OpenPullRequest{ID: "c6", SessionID: "s1"}); !errors.Is(err, ErrInCheckout) {
		t.Errorf("pull request from a checkout session: err = %v", err)
	}
	if _, err := m.Stop(f.ctx, v1.StopAgent{ID: "c7", SessionID: "s1"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Remove(f.ctx, "c8", "s1", true); !errors.Is(err, ErrInCheckout) {
		t.Errorf("remove a checkout session: err = %v", err)
	}
	if pruned := m.Prune(f.ctx); len(pruned) != 0 {
		t.Errorf("Prune touched a checkout session: %+v", pruned)
	}
	// With the first one over, the checkout is free again.
	if _, err := m.Start(f.ctx, v1.StartSession{ID: "c9", SessionID: "s3", RepoID: f.repo.ID, Agent: "fake", Prompt: "now me", InCheckout: true}); err != nil {
		t.Errorf("checkout session after the first ended: %v", err)
	}
}

func TestRulesSettleToolCallsBeforeAnyoneIsAsked(t *testing.T) {
	f := setup(t)
	// The repository allows edits inside the worktree, so a session that
	// starts on it copies that rule and its edit never waits.
	if _, err := f.agg.SetRules(f.ctx, v1.SetRepoRules{ID: "r1", RepoID: f.repo.ID, Rules: []v1.Rule{{Decision: "allow", Kind: "edit", Pattern: "worktree"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.agg.SetRules(f.ctx, v1.SetRepoRules{ID: "r2", RepoID: f.repo.ID, Rules: []v1.Rule{{Decision: "maybe", Kind: "edit"}}}); err == nil {
		t.Error("a bad decision was accepted")
	}
	m, err := Load(f.ctx, f.store, f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	s, err := m.Start(f.ctx, v1.StartSession{ID: "c1", SessionID: "s1", RepoID: f.repo.ID, Agent: "fake", Prompt: "tidy up"})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Rules) != 1 || s.Rules[0].Source != v1.RuleFromRepo || s.Rules[0].Pattern != "worktree" {
		t.Errorf("inherited rules = %+v", s.Rules)
	}
	idle := waitStatus(t, m, "s1", StatusIdle)
	if len(idle.Pending) != 0 || idle.ToolCalls[2].Status != "completed" {
		t.Errorf("after the first turn = pending %d, tool calls %+v", len(idle.Pending), idle.ToolCalls)
	}

	// A rule set on the session itself beats the copied one: the edit is
	// refused by the rule, recorded, and never waits on anyone.
	if _, err := m.SetRules(f.ctx, v1.SetSessionRules{ID: "c2", SessionID: "s1", Rules: append(idle.Rules, v1.Rule{Decision: "deny", Kind: "edit", Pattern: "worktree"})}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetRules(f.ctx, v1.SetSessionRules{ID: "c3", SessionID: "s1", Rules: []v1.Rule{{Decision: "allow", Kind: "shout"}}}); err == nil {
		t.Error("a bad kind was accepted")
	}
	if _, err := m.Prompt(f.ctx, v1.SendPrompt{ID: "c4", SessionID: "s1", Text: "and again"}); err != nil {
		t.Fatal(err)
	}
	second := waitStatus(t, m, "s1", StatusIdle)
	for second.Turns < 2 {
		time.Sleep(20 * time.Millisecond)
		second = waitStatus(t, m, "s1", StatusIdle)
	}
	if len(second.Pending) != 0 || second.ToolCalls[5].Status != "failed" || len(second.Rules) != 2 {
		t.Errorf("after the denied turn = pending %d, tool calls %+v, rules %+v", len(second.Pending), second.ToolCalls, second.Rules)
	}
	var denied []string
	var requested int
	_ = f.store.Replay(f.ctx, v1.IslandStreamName("ws"), []string{v1.SessionEventFilter("ws")}, func(_ uint64, ev v1.Event) error {
		switch ev.Type {
		case v1.EventPermissionRequested:
			requested++
		case v1.EventPermissionDenied:
			var d v1.PermissionDenied
			_ = ev.Decode(&d)
			denied = append(denied, d.By)
		}
		return nil
	})
	if requested != 1 || strings.Join(denied, ",") != "rule" {
		t.Errorf("requested %d, denied by %v; want one request denied by the rule", requested, denied)
	}
	if b, _ := os.ReadFile(filepath.Join(f.cfg.TranscriptsDir, "s1.md")); !strings.Contains(string(b), ToolLinePrefix+"Edit a.go (denied by a rule)") {
		t.Errorf("transcript lacks the refusal: %q", b)
	}

	// Trust still allows everything, rules or not.
	if _, err := m.Trust(f.ctx, v1.TrustSession{ID: "c5", SessionID: "s1", Trusted: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Prompt(f.ctx, v1.SendPrompt{ID: "c6", SessionID: "s1", Text: "once more"}); err != nil {
		t.Fatal(err)
	}
	third := waitStatus(t, m, "s1", StatusIdle)
	for third.Turns < 3 {
		time.Sleep(20 * time.Millisecond)
		third = waitStatus(t, m, "s1", StatusIdle)
	}
	if third.ToolCalls[8].Status != "completed" {
		t.Errorf("trusted turn = %+v", third.ToolCalls[6:])
	}

	// Rules survive a reload, and a changed repository rule reaches only
	// new sessions.
	if _, err := f.agg.SetRules(f.ctx, v1.SetRepoRules{ID: "r3", RepoID: f.repo.ID, Rules: nil}); err != nil {
		t.Fatal(err)
	}
	m2, err := Load(f.ctx, f.store, f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if s2, _ := m2.Get("s1"); len(s2.Rules) != 2 {
		t.Errorf("reloaded rules = %+v", s2.Rules)
	}
	if _, err := m2.Stop(f.ctx, v1.StopAgent{ID: "c7", SessionID: "s1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m2.SetRules(f.ctx, v1.SetSessionRules{ID: "c8", SessionID: "s1", Rules: nil}); !errors.Is(err, ErrOver) {
		t.Errorf("rules after the end: err = %v", err)
	}
	fresh, err := m2.Start(f.ctx, v1.StartSession{ID: "c9", SessionID: "s2", RepoID: f.repo.ID, Agent: "fake", Prompt: "later"})
	if err != nil || len(fresh.Rules) != 0 {
		t.Errorf("session after the repo's rules were cleared = %+v, %v", fresh.Rules, err)
	}
	waitPending(t, m2, "s2", 1)
}
