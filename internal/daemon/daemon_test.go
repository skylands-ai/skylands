package daemon

import (
	"context"
	"encoding/json"
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
		done <- Run(runCtx, Config{Host: "Test.Host", NATSURL: hub.LeafURL(), DataDir: t.TempDir(), Version: "v1.2.3-rc1"})
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
			Host: "ws", NATSURL: hub.LeafURL(), DataDir: t.TempDir(), SocketPath: sock, Version: "v0.1.0",
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

	// The session runs to the end and shows in the daemon's status.
	for {
		st, err := cli.Status(ctx)
		if err == nil && len(st.Sessions) == 1 && st.Sessions[0].Status == "ended" {
			if st.Sessions[0].Repo != repo.Name || st.Sessions[0].Agent != "fake" {
				t.Errorf("session = %+v", st.Sessions[0])
			}
			break
		}
		if time.Now().After(deadline.Add(20 * time.Second)) {
			t.Fatalf("session never ended: %+v, %v", st, err)
		}
		time.Sleep(100 * time.Millisecond)
	}

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
	if res, errText := fetchText(v1.FetchTranscript, "sess-1"); errText != "" || !strings.Contains(res.Text, "# Prompt") || !strings.Contains(res.Text, "fix it") || !strings.Contains(res.Text, "Done.") || !strings.Contains(res.Text, "Finished: end_turn") {
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

func TestMain(m *testing.M) {
	fakeagent.TestMainHook()
	os.Exit(m.Run())
}
