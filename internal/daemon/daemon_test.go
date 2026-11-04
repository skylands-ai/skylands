package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/micro"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/daemon/agents"
	"github.com/skylands-ai/skylands/internal/daemon/socket"
	"github.com/skylands-ai/skylands/internal/fakeagent"
	"github.com/skylands-ai/skylands/internal/natsembed"
)

func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func TestRunAnswersPingsFromTheWorld(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	hub, err := natsembed.Start(ctx, natsembed.Config{Name: "hub", ClientAddr: "127.0.0.1:0", LeafAddr: freePort(t), StoreDir: t.TempDir(), Domain: "world"})
	if err != nil {
		t.Fatalf("hub: %v", err)
	}
	defer hub.Shutdown()

	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		fakeCmd, fakeEnv := fakeagent.TestCommand()
		done <- Run(runCtx, Config{Host: "Test.Host", NATSURL: hub.LeafURL(), DataDir: t.TempDir(), Version: "v1.2.3-rc1", TrustFirstWorld: true,
			Adapters: agents.Registry{"fake": {Name: "fake", Provider: "local", Command: fakeCmd, Env: fakeEnv}}})
	}()

	nc, err := nats.Connect(hub.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()

	var ping micro.Ping
	deadline := time.Now().Add(10 * time.Second)
	for {
		msg, err := nc.Request("$SRV.PING."+ServiceName, nil, 300*time.Millisecond)
		if err == nil {
			if err := json.Unmarshal(msg.Data, &ping); err != nil {
				t.Fatalf("decode ping reply %q: %v", msg.Data, err)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no daemon answered a ping: %v", err)
		}
	}
	if ping.Name != ServiceName || ping.Version != "1.2.3-rc1" {
		t.Errorf("ping = %+v, want service %s at version 1.2.3-rc1", ping, ServiceName)
	}
	if got := ping.Metadata["host"]; got != "test-host" {
		t.Errorf("host metadata = %q, want test-host", got)
	}
	for _, k := range []string{"os", "arch", "started", "skylands_version"} {
		if ping.Metadata[k] == "" {
			t.Errorf("metadata %q is empty", k)
		}
	}

	stop()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned %v after cancel, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not stop after cancel")
	}
}

func TestHostNames(t *testing.T) {
	cases := map[string]string{"Kevins-MacBook-Pro.local": "kevins-macbook-pro-local", "build 01": "build-01", "nuc": "nuc"}
	for in, want := range cases {
		if got := cleanHost(in); got != want {
			t.Errorf("cleanHost(%q) = %q, want %q", in, got, want)
		}
	}
	if got := DefaultHost(); got == "" || got != cleanHost(got) {
		t.Errorf("DefaultHost() = %q, want a clean non-empty name", got)
	}
	for in, want := range map[string]string{"v1.2.3": "1.2.3", "1.2.3+build.4": "1.2.3+build.4", "a7ab8e1-dirty": "0.0.0", "": "0.0.0"} {
		if got := semver(in); got != want {
			t.Errorf("semver(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStartSessionOverNATS(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	hub, err := natsembed.Start(ctx, natsembed.Config{Name: "hub", ClientAddr: "127.0.0.1:0", LeafAddr: freePort(t), StoreDir: t.TempDir(), Domain: "world"})
	if err != nil {
		t.Fatalf("hub: %v", err)
	}
	defer hub.Shutdown()

	sockDir, err := os.MkdirTemp("", "sk")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(sockDir) }()
	sock := filepath.Join(sockDir, "d.sock")
	fakeCmd, fakeEnv := fakeagent.TestCommand()
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- Run(runCtx, Config{
			TrustFirstWorld: true,
			Host:            "ws", NATSURL: hub.LeafURL(), DataDir: t.TempDir(), SocketPath: sock, Version: "v0.1.0",
			Adapters: agents.Registry{"fake": {Name: "fake", Provider: "local", Command: fakeCmd, Env: fakeEnv}},
		})
	}()

	// Register a repo through the socket once the daemon is up.
	repoDir := gitRepo(t, "main.go")
	cli := socket.Dial(sock)
	var repo socket.RepoInfo
	deadline := time.Now().Add(10 * time.Second)
	for {
		if repo, err = cli.RegisterRepo(ctx, repoDir); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("register repo: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// The daemon checked its agents when it started, and checks again when
	// asked over the socket.
	var st socket.StatusInfo
	for deadline := time.Now().Add(10 * time.Second); ; {
		if st, err = cli.Status(ctx); err == nil && len(st.Agents) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no agents recorded after startup: %+v, %v", st.Agents, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(st.Agents) != 1 || st.Agents[0].Name != "fake" || !st.Agents[0].Available || st.Agents[0].Agent != "skylands-fakeagent dev" {
		t.Errorf("agents after startup = %+v, want the fake agent available", st.Agents)
	}
	if checked, err := cli.CheckAgents(ctx); err != nil || len(checked) != 1 || !checked[0].Available {
		t.Errorf("CheckAgents = %+v, %v", checked, err)
	}

	// The world sends StartSession to the host and gets the worktree back.
	nc, err := nats.Connect(hub.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	cmd, _ := json.Marshal(v1.StartSession{ID: "cmd-1", SessionID: "sess-1", RepoID: repo.ID, Agent: "fake", Prompt: "fix it"})
	var reply v1.Reply
	for {
		msg, err := nc.Request(v1.SessionCommandSubject("ws", "sess-1", v1.CommandStartSession), cmd, 5*time.Second)
		if err == nil {
			if err := json.Unmarshal(msg.Data, &reply); err != nil {
				t.Fatalf("decode reply %q: %v", msg.Data, err)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no reply to StartSession: %v", err)
		}
	}
	if reply.Error != "" {
		t.Fatalf("StartSession failed: %s", reply.Error)
	}
	var result v1.StartSessionResult
	if err := json.Unmarshal(reply.Result, &result); err != nil || result.SessionID != "sess-1" || result.Branch != "skylands/sess-1" {
		t.Fatalf("result = %+v, %v", result, err)
	}

	// A bad command gets an error, not silence.
	bad, _ := json.Marshal(v1.StartSession{ID: "cmd-2", SessionID: "sess-2", RepoID: "nope", Agent: "fake", Prompt: "x"})
	msg, err := nc.Request(v1.SessionCommandSubject("ws", "sess-2", v1.CommandStartSession), bad, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(msg.Data, &reply)
	if reply.Error == "" {
		t.Error("an unknown repo was accepted")
	}

	// The first turn ends and the session waits, as the daemon's status shows.
	waitSession := func(status string, turns int) {
		t.Helper()
		for {
			st, err := cli.Status(ctx)
			if err == nil && len(st.Sessions) == 1 && st.Sessions[0].Status == status && st.Sessions[0].Turns == turns {
				if st.Sessions[0].Repo != repo.Name || st.Sessions[0].Agent != "fake" {
					t.Errorf("session = %+v", st.Sessions[0])
				}
				return
			}
			if time.Now().After(deadline.Add(30 * time.Second)) {
				t.Fatalf("session never reached %s with %d turns: %+v, %v", status, turns, st, err)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	// The edit waits on a person, and the local CLI's path answers it.
	for {
		reqs, err := cli.Requests(ctx)
		if err == nil && len(reqs) == 1 {
			if reqs[0].Tool != "Edit "+reqs[0].Locations[0] || reqs[0].Repo != repo.Name || reqs[0].Agent != "fake" || len(reqs[0].Options) != 3 {
				t.Errorf("request = %+v", reqs[0])
			}
			if _, err := cli.Deny(ctx, "nope"); err == nil || !strings.Contains(err.Error(), "no such request") {
				t.Errorf("deny an unknown request: err = %v", err)
			}
			if r, err := cli.Approve(ctx, reqs[0].ID); err != nil || r.ID != reqs[0].ID {
				t.Fatalf("approve: %+v, %v", r, err)
			}
			break
		}
		if time.Now().After(deadline.Add(30 * time.Second)) {
			t.Fatalf("no request ever waited: %+v, %v", reqs, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	waitSession("idle", 1)
	if reqs, err := cli.Requests(ctx); err != nil || len(reqs) != 0 {
		t.Errorf("requests after approval = %+v, %v", reqs, err)
	}

	// SendPrompt runs another turn, whose edit is approved over NATS, and
	// StopAgent ends the session.
	command := func(typ string, body any) v1.Reply {
		t.Helper()
		data, _ := json.Marshal(body)
		msg, err := nc.Request(v1.SessionCommandSubject("ws", "sess-1", typ), data, 10*time.Second)
		if err != nil {
			t.Fatalf("%s: %v", typ, err)
		}
		var r v1.Reply
		_ = json.Unmarshal(msg.Data, &r)
		return r
	}
	if r := command(v1.CommandSendPrompt, v1.SendPrompt{ID: "cmd-p", Text: "once more"}); r.Error != "" {
		t.Fatalf("SendPrompt failed: %s", r.Error)
	}
	for {
		st, err := cli.Status(ctx)
		if err == nil && len(st.Sessions) == 1 && st.Sessions[0].Pending == 1 {
			reqs, _ := cli.Requests(ctx)
			if r := command(v1.CommandApproveToolCall, v1.ApproveToolCall{ID: "cmd-ok", RequestID: reqs[0].ID}); r.Error != "" {
				t.Fatalf("ApproveToolCall failed: %s", r.Error)
			}
			break
		}
		if time.Now().After(deadline.Add(30 * time.Second)) {
			t.Fatalf("the second turn never waited: %+v, %v", st, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	waitSession("idle", 2)
	if r := command(v1.CommandStopAgent, v1.StopAgent{ID: "cmd-stop"}); r.Error != "" {
		t.Fatalf("StopAgent failed: %s", r.Error)
	}
	waitSession("ended", 2)

	// The transcript and the worktree's diff come from the host on request.
	fetchText := func(kind, sessionID string) (v1.TextResult, string) {
		t.Helper()
		body, _ := json.Marshal(v1.FetchSessionRequest{SessionID: sessionID})
		msg, err := nc.Request(v1.IslandFetchSubject("ws", kind), body, 5*time.Second)
		if err != nil {
			t.Fatalf("fetch %s: %v", kind, err)
		}
		var reply v1.Reply
		_ = json.Unmarshal(msg.Data, &reply)
		var res v1.TextResult
		_ = json.Unmarshal(reply.Result, &res)
		return res, reply.Error
	}
	if res, errText := fetchText(v1.FetchTranscript, "sess-1"); errText != "" || !strings.Contains(res.Text, "## You\n\nfix it") || !strings.Contains(res.Text, "Done with \"fix it\"") || !strings.Contains(res.Text, "## You\n\nonce more") || !strings.Contains(res.Text, "*Stopped*") {
		t.Errorf("transcript = %q, err %q", res.Text, errText)
	}
	if err := os.WriteFile(filepath.Join(result.Worktree, "main.go"), []byte("changed by a test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(result.Worktree, "new.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if res, errText := fetchText(v1.FetchDiff, "sess-1"); errText != "" || !strings.Contains(res.Text, "+changed by a test") || !strings.Contains(res.Text, "Untracked files:\n  new.txt") {
		t.Errorf("diff = %q, err %q", res.Text, errText)
	}
	if _, errText := fetchText(v1.FetchTranscript, "nope"); errText == "" {
		t.Error("an unknown session's transcript was served")
	}
	// So does one tool call's record, named by its id.
	body, _ := json.Marshal(v1.FetchSessionRequest{SessionID: "sess-1", ToolCallID: "t1-read-0"})
	if msg, err := nc.Request(v1.IslandFetchSubject("ws", v1.FetchToolCall), body, 5*time.Second); err != nil {
		t.Fatalf("fetch tool call: %v", err)
	} else {
		var reply v1.Reply
		_ = json.Unmarshal(msg.Data, &reply)
		var detail v1.ToolCallDetail
		_ = json.Unmarshal(reply.Result, &detail)
		if reply.Error != "" || detail.Title != "Read main.go" || detail.Status != "completed" || len(detail.Content) != 1 || detail.Content[0].Text != "main.go" {
			t.Errorf("tool call = %+v, err %q", detail, reply.Error)
		}
	}

	// The local CLI's path lists the session and cleans up after it: the
	// dirty worktree is kept until the removal is forced.
	sessions, err := cli.Sessions(ctx)
	if err != nil || len(sessions) != 1 || sessions[0].Branch != "skylands/sess-1" || sessions[0].Worktree != result.Worktree || sessions[0].Status != "ended" {
		t.Fatalf("Sessions = %+v, %v", sessions, err)
	}
	if _, err := cli.StopSession(ctx, "nope"); err == nil || !strings.Contains(err.Error(), "no such session") {
		t.Errorf("stop an unknown session: err = %v", err)
	}
	if pruned, err := cli.PruneSessions(ctx); err != nil || len(pruned) != 1 || pruned[0].Removed {
		t.Errorf("PruneSessions with a dirty worktree = %+v, %v", pruned, err)
	}
	if _, err := cli.RemoveSession(ctx, "sess-1", false); err == nil {
		t.Error("a dirty worktree was removed without force")
	}
	if res, err := cli.RemoveSession(ctx, "sess-1", true); err != nil || !res.Session.WorktreeRemoved || !res.BranchDeleted {
		t.Fatalf("RemoveSession = %+v, %v", res, err)
	}
	if _, err := os.Stat(result.Worktree); err == nil {
		t.Error("the worktree is still there")
	}
	if _, errText := fetchText(v1.FetchDiff, "sess-1"); errText == "" {
		t.Error("a removed worktree's diff was served")
	}

	stop()
	if err := <-done; err != nil {
		t.Errorf("Run returned %v", err)
	}
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

func TestASecondDaemonForAnIslandStops(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	hub, err := natsembed.Start(ctx, natsembed.Config{Name: "hub", ClientAddr: "127.0.0.1:0", LeafAddr: freePort(t), StoreDir: t.TempDir(), Domain: "world"})
	if err != nil {
		t.Fatalf("hub: %v", err)
	}
	defer hub.Shutdown()
	sockDir, err := os.MkdirTemp("", "sk")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(sockDir) }()
	fakeCmd, fakeEnv := fakeagent.TestCommand()
	cfg := Config{
		TrustFirstWorld: true,
		Host:            "ws", NATSURL: hub.LeafURL(), DataDir: t.TempDir(), SocketPath: filepath.Join(sockDir, "d.sock"), Version: "v0.1.0",
		Adapters: agents.Registry{"fake": {Name: "fake", Provider: "local", Command: fakeCmd, Env: fakeEnv}},
	}

	runCtx, stop := context.WithCancel(ctx)
	first := make(chan error, 1)
	go func() { first <- Run(runCtx, cfg) }()
	defer func() {
		stop()
		if err := <-first; err != nil {
			t.Errorf("first daemon: %v", err)
		}
	}()
	cli := socket.Dial(cfg.SocketPath)
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if _, err := cli.Status(ctx); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the first daemon never answered: %v", err)
		}
	}

	// The same command again, as a second "daemon up" would run it, stops
	// before it starts anything.
	refused := func(c Config, want string) {
		t.Helper()
		done := make(chan error, 1)
		go func() { done <- Run(runCtx, c) }()
		select {
		case err := <-done:
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("err = %v, want it to say %q", err, want)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("a second daemon started, where it should have said %q", want)
		}
	}
	refused(cfg, fmt.Sprintf("the daemon for island ws (process %d) is already running on %s", os.Getpid(), cfg.SocketPath))
	// So does one on a socket of its own: the island's store and sessions
	// in the data directory are the first daemon's.
	other := cfg
	other.SocketPath = filepath.Join(sockDir, "e.sock")
	refused(other, fmt.Sprintf("the daemon for island ws (process %d) is already running with data directory %s", os.Getpid(), cfg.DataDir))
	if st, err := cli.Status(ctx); err != nil || st.Host != "ws" {
		t.Errorf("after the refusals, Status = %+v, %v; want the first daemon", st, err)
	}

	// A daemon for another island can share the data directory.
	third := cfg
	third.Host = "ws2"
	third.SocketPath = filepath.Join(sockDir, "f.sock")
	thirdDone := make(chan error, 1)
	go func() { thirdDone <- Run(runCtx, third) }()
	defer func() {
		stop()
		if err := <-thirdDone; err != nil {
			t.Errorf("third daemon: %v", err)
		}
	}()
	var st socket.StatusInfo
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if st, err = socket.Dial(third.SocketPath).Status(ctx); err == nil {
			break
		}
		select {
		case err := <-thirdDone:
			thirdDone <- nil
			t.Fatalf("a daemon for another island in the same data directory stopped: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("a daemon for another island in the same data directory never answered: %v", err)
		}
	}
	if st.Host != "ws2" {
		t.Errorf("third daemon's Status = %+v", st)
	}
}

func TestMain(m *testing.M) {
	fakeagent.TestMainHook()
	os.Exit(m.Run())
}
