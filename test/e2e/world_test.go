// Package e2e runs a whole world in one test process: embedded NATS, a
// citadel, and real daemons, observed the way a browser observes it, over
// the citadel's WebSocket.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/skylands-ai/skylands/internal/citadel"
	"github.com/skylands-ai/skylands/internal/citadel/projections"
	"github.com/skylands-ai/skylands/internal/citadel/web"
	"github.com/skylands-ai/skylands/internal/daemon"
	"github.com/skylands-ai/skylands/internal/daemon/agents"
	"github.com/skylands-ai/skylands/internal/daemon/socket"
	"github.com/skylands-ai/skylands/internal/fakeagent"
	"github.com/skylands-ai/skylands/internal/natsembed"
)

func TestMain(m *testing.M) {
	fakeagent.TestMainHook()
	os.Exit(m.Run())
}

// freeAddr picks a port that is free right now. Whoever listens on it does
// so a moment later, which is a small race these tests accept.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

// world is one running world: the hub, one citadel, and whatever daemons a
// test starts. The hub keeps fixed ports so it can be restarted in place.
type world struct {
	t      *testing.T
	ctx    context.Context
	hubCfg natsembed.Config
	hub    *natsembed.Server
	webURL string
}

func startWorld(t *testing.T, ctx context.Context) *world {
	t.Helper()
	w := &world{t: t, ctx: ctx}
	w.hubCfg = natsembed.Config{Name: "e2e", ClientAddr: freeAddr(t), LeafAddr: freeAddr(t), StoreDir: t.TempDir(), Domain: "world"}
	w.startHub()
	t.Cleanup(func() { w.hub.Shutdown() })

	webAddr := freeAddr(t)
	w.webURL = "http://" + webAddr
	cctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- citadel.Run(cctx, citadel.Config{NATSURL: w.hub.ClientURL(), HTTPAddr: webAddr, PollInterval: 200 * time.Millisecond, PollWindow: 100 * time.Millisecond})
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("citadel.Run returned %v", err)
		}
	})
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, err := http.Get(w.webURL + "/healthz")
		if err == nil {
			_ = resp.Body.Close()
			return w
		}
		if time.Now().After(deadline) {
			t.Fatalf("citadel never answered: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (w *world) startHub() {
	w.t.Helper()
	hub, err := natsembed.Start(w.ctx, w.hubCfg)
	if err != nil {
		w.t.Fatalf("start hub: %v", err)
	}
	w.hub = hub
}

// startDaemon runs a real daemon for host and returns a function that stops
// it and waits for it to exit.
func (w *world) startDaemon(host string) (stop func()) {
	return w.startDaemonWithSocket(host, "")
}

// startDaemonWithSocket is startDaemon with a local API on socketPath.
func (w *world) startDaemonWithSocket(host, socketPath string) (stop func()) {
	w.t.Helper()
	ctx, cancel := context.WithCancel(w.ctx)
	done := make(chan error, 1)
	fakeCmd, fakeEnv := fakeagent.TestCommand()
	go func() {
		done <- daemon.Run(ctx, daemon.Config{
			Host: host, NATSURL: w.hub.LeafURL(), DataDir: w.t.TempDir(), SocketPath: socketPath, Version: "v0.1.0",
			Adapters: agents.Registry{"fake": {Name: "fake", Provider: "local", Command: fakeCmd, Env: fakeEnv}},
		})
	}()
	return func() {
		cancel()
		if err := <-done; err != nil {
			w.t.Errorf("daemon %s returned %v", host, err)
		}
	}
}

// browser is a WebSocket client that keeps the same state a page would.
type browser struct {
	mu        sync.Mutex
	server    web.ServerInfo
	islands   map[string]projections.Island
	repos     map[string]projections.Repo
	sessions  map[string]projections.Session
	snapshots int
	err       error
}

func (w *world) openBrowser() *browser {
	w.t.Helper()
	c, _, err := websocket.Dial(w.ctx, "ws"+w.webURL[len("http"):]+"/ws", nil)
	if err != nil {
		w.t.Fatalf("dial: %v", err)
	}
	w.t.Cleanup(func() { _ = c.CloseNow() })
	b := &browser{islands: map[string]projections.Island{}, repos: map[string]projections.Repo{}, sessions: map[string]projections.Session{}}
	go func() {
		for {
			_, data, err := c.Read(w.ctx)
			if err != nil {
				b.mu.Lock()
				b.err = err
				b.mu.Unlock()
				return
			}
			var m web.Message
			if err := json.Unmarshal(data, &m); err != nil {
				continue
			}
			b.mu.Lock()
			switch m.Type {
			case "snapshot":
				b.snapshots++
				b.islands = map[string]projections.Island{}
				for _, isl := range m.Islands {
					b.islands[isl.Host] = isl
				}
				b.repos = map[string]projections.Repo{}
				for _, r := range m.Repos {
					b.repos[r.ID] = r
				}
				b.sessions = map[string]projections.Session{}
				for _, s := range m.Sessions {
					b.sessions[s.ID] = s
				}
				if m.Server != nil {
					b.server = *m.Server
				}
			case "server":
				if m.Server != nil {
					b.server = *m.Server
				}
			case "island":
				if m.Island != nil {
					b.islands[m.Island.Host] = *m.Island
				}
			case "repo":
				if m.Repo != nil {
					b.repos[m.Repo.ID] = *m.Repo
				}
			case "session":
				if m.Session != nil {
					b.sessions[m.Session.ID] = *m.Session
				}
			}
			b.mu.Unlock()
		}
	}()
	return b
}

type view struct {
	server   web.ServerInfo
	islands  map[string]projections.Island
	repos    map[string]projections.Repo
	sessions map[string]projections.Session
}

// wait blocks until pred holds for the browser's state, or fails the test
// after timeout.
func (b *browser) wait(t *testing.T, what string, timeout time.Duration, pred func(v view) bool) view {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		b.mu.Lock()
		v := view{server: b.server, islands: make(map[string]projections.Island, len(b.islands)), repos: make(map[string]projections.Repo, len(b.repos))}
		for k, isl := range b.islands {
			v.islands[k] = isl
		}
		for k, r := range b.repos {
			v.repos[k] = r
		}
		v.sessions = make(map[string]projections.Session, len(b.sessions))
		for k, s := range b.sessions {
			v.sessions[k] = s
		}
		err := b.err
		b.mu.Unlock()
		if pred(v) {
			return v
		}
		if err != nil {
			t.Fatalf("waiting for %s: socket closed: %v", what, err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("waiting for %s: timed out; islands=%+v server=%+v", what, v.islands, v.server)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func reachable(v view, host string, want bool) bool {
	isl, ok := v.islands[host]
	return ok && isl.Reachable == want
}

func TestHostsComeAndGo(t *testing.T) {
	if testing.Short() {
		t.Skip("whole-world test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	w := startWorld(t, ctx)
	b := w.openBrowser()
	b.wait(t, "the opening snapshot", 5*time.Second, func(v view) bool { return v.server.Connected })

	hosts := []string{"alpha", "beta", "gamma"}
	stops := map[string]func(){}
	for _, h := range hosts {
		stops[h] = w.startDaemon(h)
	}
	v := b.wait(t, "three islands", 15*time.Second, func(v view) bool {
		for _, h := range hosts {
			if !reachable(v, h, true) {
				return false
			}
		}
		return true
	})
	for _, h := range hosts {
		if d := v.islands[h].Daemon; d.Version != "v0.1.0" || d.OS == "" || d.ID == "" || d.Started.IsZero() {
			t.Errorf("%s daemon facts = %+v", h, d)
		}
	}
	betaID := v.islands["beta"].Daemon.ID

	// One host leaves. The others are untouched, and a fresh browser's
	// snapshot agrees with what the first one saw happen.
	stops["beta"]()
	b.wait(t, "beta unreachable", 10*time.Second, func(v view) bool {
		return reachable(v, "beta", false) && reachable(v, "alpha", true) && reachable(v, "gamma", true)
	})
	b2 := w.openBrowser()
	b2.wait(t, "a snapshot with beta unreachable", 5*time.Second, func(v view) bool {
		return len(v.islands) == 3 && reachable(v, "beta", false) && reachable(v, "alpha", true) && reachable(v, "gamma", true)
	})

	// The host comes back with a restarted daemon, which has a new id.
	stops["beta"] = w.startDaemon("beta")
	v = b.wait(t, "beta back", 15*time.Second, func(v view) bool { return reachable(v, "beta", true) })
	if v.islands["beta"].Daemon.ID == betaID {
		t.Errorf("restarted beta kept daemon id %q", betaID)
	}
	if v.islands["beta"].FirstSeen.IsZero() || !v.islands["beta"].LastSeen.After(v.islands["beta"].FirstSeen) {
		t.Errorf("beta times not kept across its restart: %+v", v.islands["beta"])
	}

	for _, h := range hosts {
		stops[h]()
	}
}

func TestWorldSurvivesNATSRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("whole-world test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	w := startWorld(t, ctx)
	b := w.openBrowser()
	stop := w.startDaemon("solo")
	defer stop()
	b.wait(t, "solo reachable", 15*time.Second, func(v view) bool { return v.server.Connected && reachable(v, "solo", true) })

	// NATS goes away. The citadel reports the loss and, unable to ping,
	// shows the island as unreachable.
	w.hub.Shutdown()
	b.wait(t, "NATS lost", 15*time.Second, func(v view) bool { return !v.server.Connected && reachable(v, "solo", false) })

	// NATS comes back on the same ports. The citadel reconnects, the
	// daemon's leaf node reconnects, and the island is reachable again.
	w.startHub()
	v := b.wait(t, "NATS back", 30*time.Second, func(v view) bool { return v.server.Connected && reachable(v, "solo", true) })
	if v.server.Name != "e2e" {
		t.Errorf("reconnected to %q, want e2e", v.server.Name)
	}
}

// gitRepo makes a repository with committed files and returns its path.
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

// shortSocket returns a socket path short enough for every platform's limit.
func shortSocket(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "sk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "d.sock")
}

func TestRepoRegisterMakesATerrace(t *testing.T) {
	if testing.Short() {
		t.Skip("whole-world test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	w := startWorld(t, ctx)
	b := w.openBrowser()
	sock := shortSocket(t)
	stop := w.startDaemonWithSocket("ws", sock)
	defer stop()
	b.wait(t, "ws reachable", 15*time.Second, func(v view) bool { return reachable(v, "ws", true) })

	// The local CLI registers a repo through the daemon's socket.
	repoDir := gitRepo(t, "main.go", "README.md", "go.mod")
	cli := socket.Dial(sock)
	var info socket.RepoInfo
	var err error
	for i := 0; i < 100; i++ {
		if info, err = cli.RegisterRepo(ctx, repoDir); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil || info.Files != 3 {
		t.Fatalf("RegisterRepo = %+v, %v", info, err)
	}

	// The event crosses the leaf link into the mirror, and the citadel
	// projects it into a terrace the browser sees, with every building.
	v := b.wait(t, "the terrace", 20*time.Second, func(v view) bool {
		r, ok := v.repos[info.ID]
		return ok && len(r.Files) == 3
	})
	r := v.repos[info.ID]
	if r.Host != "ws" || r.Name != info.Name || r.Path != info.Path || r.Commit == "" || r.RegisteredAt.IsZero() || r.SnapshottedAt.IsZero() {
		t.Errorf("repo = %+v", r)
	}

	// A later browser gets the terrace in its snapshot, and the daemon's
	// status lists it.
	b2 := w.openBrowser()
	b2.wait(t, "a snapshot with the terrace", 5*time.Second, func(v view) bool {
		r, ok := v.repos[info.ID]
		return ok && len(r.Files) == 3
	})
	st, err := cli.Status(ctx)
	if err != nil || len(st.Repos) != 1 || st.Repos[0].ID != info.ID {
		t.Errorf("Status = %+v, %v", st, err)
	}
}

func TestSessionStartedFromTheWorld(t *testing.T) {
	if testing.Short() {
		t.Skip("whole-world test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	w := startWorld(t, ctx)
	b := w.openBrowser()
	sock := shortSocket(t)
	stop := w.startDaemonWithSocket("ws", sock)
	defer stop()
	b.wait(t, "ws reachable", 15*time.Second, func(v view) bool { return reachable(v, "ws", true) })

	repoDir := gitRepo(t, "main.go", "util.go")
	cli := socket.Dial(sock)
	var info socket.RepoInfo
	var err error
	for i := 0; i < 100; i++ {
		if info, err = cli.RegisterRepo(ctx, repoDir); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("RegisterRepo: %v", err)
	}
	b.wait(t, "the terrace", 20*time.Second, func(v view) bool { _, ok := v.repos[info.ID]; return ok })

	post := func(body string) (int, map[string]any) {
		t.Helper()
		resp, err := http.Post(w.webURL+"/api/sessions", "application/json", bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		data, _ := io.ReadAll(resp.Body)
		var out map[string]any
		_ = json.Unmarshal(data, &out)
		return resp.StatusCode, out
	}

	// Bad orders are refused with a reason.
	if code, out := post(`{"host":"ws","repoId":"nope","agent":"fake","prompt":"x"}`); code != http.StatusBadRequest || out["error"] == nil {
		t.Errorf("unknown repo: %d %v", code, out)
	}
	if code, _ := post(`{"host":"nowhere","repoId":"x","agent":"fake","prompt":"x"}`); code != http.StatusNotFound {
		t.Errorf("unknown host: %d", code)
	}

	// A good one starts a session on the island, and its events stream back.
	code, out := post(`{"host":"ws","repoId":"` + info.ID + `","agent":"fake","prompt":"make it faster"}`)
	if code != http.StatusCreated || out["sessionId"] == nil {
		t.Fatalf("start session: %d %v", code, out)
	}
	id := out["sessionId"].(string)
	v := b.wait(t, "the session to end", 30*time.Second, func(v view) bool {
		s, ok := v.sessions[id]
		return ok && s.Status == "ended"
	})
	s := v.sessions[id]
	if s.Host != "ws" || s.RepoID != info.ID || s.Prompt != "make it faster" || s.AgentName != "skylands-fakeagent" || s.StopReason != "end_turn" {
		t.Errorf("session = %+v", s)
	}
	if len(s.Recent) != 3 || s.Files["main.go"] != "edit" || s.Files["util.go"] != "read" || s.CurrentFile != "main.go" || s.Used == 0 {
		t.Errorf("activity: recent=%+v files=%v current=%q used=%d", s.Recent, s.Files, s.CurrentFile, s.Used)
	}

	// A later browser gets the session in its snapshot.
	b2 := w.openBrowser()
	b2.wait(t, "a snapshot with the session", 5*time.Second, func(v view) bool { s, ok := v.sessions[id]; return ok && s.Status == "ended" })

	// The response and the diff are fetched from the host through the citadel.
	get := func(path string) (int, string) {
		t.Helper()
		resp, err := http.Get(w.webURL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		data, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(data)
	}
	if code, body := get("/api/sessions/ws/" + id + "/transcript"); code != http.StatusOK || !strings.Contains(body, "make it faster") || !strings.Contains(body, "Done.") {
		t.Errorf("transcript: %d %s", code, body)
	}
	if code, body := get("/api/sessions/ws/" + id + "/diff"); code != http.StatusOK || !strings.Contains(body, `"text"`) {
		t.Errorf("diff: %d %s", code, body)
	}
	if code, _ := get("/api/sessions/ws/nope/transcript"); code != http.StatusNotFound {
		t.Errorf("unknown session transcript: %d", code)
	}
	if code, _ := get("/api/sessions/nowhere/x/transcript"); code != http.StatusBadGateway {
		t.Errorf("unknown host transcript: %d", code)
	}
}
