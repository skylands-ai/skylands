// Package e2e runs a whole world in one test process: embedded NATS, a
// citadel, and real daemons, observed the way a browser observes it, over
// the citadel's WebSocket.
package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	v1 "github.com/skylands-ai/skylands/api/v1"
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
	// Until the citadel knows whether the world is set up, it lets no
	// browser in, since it can't tell whether one has to sign in. These
	// worlds are never set up, so nobody needs to.
	deadline := time.Now().Add(10 * time.Second)
	for {
		var info web.WorldInfo
		resp, err := http.Get(w.webURL + "/api/world")
		if err == nil {
			_ = json.NewDecoder(resp.Body).Decode(&info)
			_ = resp.Body.Close()
			if info.Checked {
				return w
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("citadel never looked for the world: %v", err)
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
	return w.startDaemonIn(host, socketPath, w.t.TempDir())
}

// startDaemonIn is startDaemonWithSocket with the daemon's data in dataDir,
// so a restart on the same directory finds its stream, worktrees, and
// transcripts again.
func (w *world) startDaemonIn(host, socketPath, dataDir string) (stop func()) {
	w.t.Helper()
	ctx, cancel := context.WithCancel(w.ctx)
	done := make(chan error, 1)
	fakeCmd, fakeEnv := fakeagent.TestCommand()
	go func() {
		done <- daemon.Run(ctx, daemon.Config{
			TrustFirstWorld: true,
			Host:            host, NATSURL: w.hub.LeafURL(), DataDir: dataDir, SocketPath: socketPath, Version: "v0.1.0",
			Adapters: agents.Registry{
				"fake": {Name: "fake", Provider: "local", Command: fakeCmd, Env: fakeEnv},
				// moded offers a mode and a model setting, lists two of its
				// modes, and withholds one of its commands.
				"moded": {Name: "moded", Provider: "local", Command: fakeCmd, Env: append(append([]string(nil), fakeEnv...), fakeagent.EnvConfigOptions+"=1"), Modes: []string{"ask", "plan"}, Withheld: []string{"config"}},
				// seeing takes images and files' contents in a prompt.
				"seeing": {Name: "seeing", Provider: "local", Command: fakeCmd, Env: append(append([]string(nil), fakeEnv...), fakeagent.EnvImages+"=1", fakeagent.EnvEmbeddedContext+"=1")},
			},
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
	c         *websocket.Conn
	ctx       context.Context
	mu        sync.Mutex
	live      []v1.Live
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
	b := &browser{c: c, ctx: w.ctx, islands: map[string]projections.Island{}, repos: map[string]projections.Repo{}, sessions: map[string]projections.Session{}}
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
			case "live":
				if m.Live != nil {
					b.live = append(b.live, *m.Live)
				}
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

// watch asks the citadel for one session's transcript as it is written.
func (b *browser) watch(host, id string) error {
	data, _ := json.Marshal(web.Inbound{Type: "watch", Host: host, ID: id})
	return b.c.Write(b.ctx, websocket.MessageText, data)
}

// liveText is everything heard live so far, in order of offset.
func (b *browser) liveText() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	lives := append([]v1.Live(nil), b.live...)
	sort.Slice(lives, func(i, j int) bool { return lives[i].Off < lives[j].Off })
	var sb strings.Builder
	for _, l := range lives {
		sb.WriteString(l.Text)
	}
	return sb.String()
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
	for _, kv := range [][2]string{{"GIT_AUTHOR_NAME", "t"}, {"GIT_AUTHOR_EMAIL", "t@t"}, {"GIT_COMMITTER_NAME", "t"}, {"GIT_COMMITTER_EMAIL", "t@t"}} {
		t.Setenv(kv[0], kv[1])
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
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

	post2 := func(path, body string) (int, map[string]any) {
		t.Helper()
		resp, err := http.Post(w.webURL+path, "application/json", bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		data, _ := io.ReadAll(resp.Body)
		var out map[string]any
		_ = json.Unmarshal(data, &out)
		return resp.StatusCode, out
	}
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

	// The edit waits on a person: the browser sees the request and answers it.
	v := b.wait(t, "a request to wait", 30*time.Second, func(v view) bool { s, ok := v.sessions[id]; return ok && len(s.Pending) == 1 })
	req := v.sessions[id].Pending[0]
	if req.Title != "Edit main.go" || req.Kind != "edit" || len(req.Options) != 3 {
		t.Errorf("pending request = %+v", req)
	}
	if code, out := post2("/api/sessions/ws/"+id+"/requests/nope/approve", `{}`); code != http.StatusBadRequest {
		t.Errorf("approve an unknown request: %d %v", code, out)
	}
	// An approval must name the input it approves, by hash. The browser
	// fetches the whole input from the host through the citadel, checks it
	// against the event's hash, and approves that hash.
	if code, out := post2("/api/sessions/ws/"+id+"/requests/"+req.RequestID+"/approve", `{}`); code == http.StatusOK || !strings.Contains(fmt.Sprint(out), "input") {
		t.Errorf("an approval naming no input: %d %v, want a refusal", code, out)
	}
	inResp, err := http.Get(w.webURL + "/api/sessions/ws/" + id + "/requests/" + req.RequestID + "/input")
	if err != nil {
		t.Fatal(err)
	}
	var in v1.RequestInput
	err = json.NewDecoder(inResp.Body).Decode(&in)
	_ = inResp.Body.Close()
	sum := sha256.Sum256([]byte(in.Input))
	if err != nil || hex.EncodeToString(sum[:]) != req.InputHash || in.Size != req.InputSize || !strings.Contains(in.Input, "main.go") {
		t.Fatalf("the request's input = %+v, %v; the event names hash %s", in, err, req.InputHash)
	}
	if code, out := post2("/api/sessions/ws/"+id+"/requests/"+req.RequestID+"/approve", fmt.Sprintf(`{"inputHash":%q}`, hex.EncodeToString(sum[:]))); code != http.StatusOK {
		t.Fatalf("approve: %d %v", code, out)
	}
	v = b.wait(t, "the first turn to end", 30*time.Second, func(v view) bool {
		s, ok := v.sessions[id]
		return ok && s.Status == "idle" && len(s.Pending) == 0
	})
	s := v.sessions[id]
	if s.Host != "ws" || s.RepoID != info.ID || s.Prompt != "make it faster" || s.AgentName != "skylands-fakeagent" || s.StopReason != "end_turn" || s.Turns != 1 {
		t.Errorf("session = %+v", s)
	}
	if len(s.Recent) != 3 || s.Files["main.go"] != "edit" || s.Files["util.go"] != "read" || s.CurrentFile != "main.go" || s.Used == 0 || s.Tokens.Total == 0 || s.Tokens.Total != s.Tokens.Input+s.Tokens.Output {
		t.Errorf("activity: recent=%+v files=%v current=%q used=%d tokens=%+v", s.Recent, s.Files, s.CurrentFile, s.Used, s.Tokens)
	}

	// Interrupting a turn keeps the session: the pending edit is withdrawn,
	// the session goes idle with the cancelled stop reason, and the next
	// prompt steers the same agent.
	if code, out := post2("/api/sessions/ws/"+id+"/prompt", `{"text":"take a detour"}`); code != http.StatusOK {
		t.Fatalf("prompt: %d %v", code, out)
	}
	b.wait(t, "the detour to wait on an edit", 30*time.Second, func(v view) bool { s, ok := v.sessions[id]; return ok && len(s.Pending) == 1 })
	if code, out := post2("/api/sessions/ws/"+id+"/cancel", `{}`); code != http.StatusOK {
		t.Fatalf("cancel: %d %v", code, out)
	}
	v = b.wait(t, "the interrupted turn to end", 30*time.Second, func(v view) bool {
		s, ok := v.sessions[id]
		return ok && s.Status == "idle" && s.StopReason == "cancelled" && len(s.Pending) == 0 && !s.Cancelling
	})
	if s := v.sessions[id]; s.Turns != 2 {
		t.Errorf("interrupted session = %+v", s)
	}
	if code, _ := post2("/api/sessions/ws/"+id+"/cancel", `{}`); code != http.StatusBadRequest {
		t.Errorf("cancel an idle session: %d", code)
	}

	// A trusted session asks no one, so the next turn runs through.
	if code, out := post2("/api/sessions/ws/"+id+"/trust", `{"trusted":true}`); code != http.StatusOK {
		t.Fatalf("trust: %d %v", code, out)
	}
	b.wait(t, "the session to be trusted", 10*time.Second, func(v view) bool { s, ok := v.sessions[id]; return ok && s.Trusted })

	// A browser reading the conversation hears the next turn as it is written.
	if err := b.watch("ws", id); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if code, out := post2("/api/sessions/ws/"+id+"/prompt", `{"text":"now make it smaller"}`); code != http.StatusOK {
		t.Fatalf("prompt: %d %v", code, out)
	}
	b.wait(t, "the second turn to end", 30*time.Second, func(v view) bool { s, ok := v.sessions[id]; return ok && s.Status == "idle" && s.Turns == 3 })
	for deadline := time.Now().Add(5 * time.Second); !strings.Contains(b.liveText(), "Done with \"now make it smaller\""); {
		if time.Now().After(deadline) {
			t.Fatalf("live text never carried the answer: %q", b.liveText())
		}
		time.Sleep(50 * time.Millisecond)
	}
	if live := b.liveText(); !strings.HasPrefix(live, "## You\n\nnow make it smaller") || !strings.Contains(live, "\u2699 Read") {
		t.Errorf("live text = %q", live)
	}
	v = b.wait(t, "the agent to name the conversation", 15*time.Second, func(v view) bool { s, ok := v.sessions[id]; return ok && s.Title != "" })
	if v.sessions[id].Title != "Make it faster" {
		t.Errorf("title = %q", v.sessions[id].Title)
	}
	if code, out := post2("/api/sessions/ws/"+id+"/stop", `{}`); code != http.StatusOK {
		t.Fatalf("stop: %d %v", code, out)
	}
	b.wait(t, "the session to end", 15*time.Second, func(v view) bool { s, ok := v.sessions[id]; return ok && s.Status == "ended" })
	if code, _ := post2("/api/sessions/ws/"+id+"/prompt", `{"text":"too late"}`); code != http.StatusBadRequest {
		t.Errorf("prompt after stop: %d", code)
	}
	if code, _ := post2("/api/sessions/ws/"+id+"/mode", `{"mode":"plan"}`); code != http.StatusBadRequest {
		t.Errorf("set the mode after stop: %d", code)
	}

	// A session starts in a mode its adapter lists, and its mode and model
	// change while it is idle.
	if code, out := post(`{"host":"ws","repoId":"` + info.ID + `","agent":"moded","mode":"auto","prompt":"x"}`); code != http.StatusBadRequest || !strings.Contains(fmt.Sprint(out["error"]), "not one the adapter lists") {
		t.Errorf("start in an unlisted mode: %d %v", code, out)
	}
	planCode, planOut := post(`{"host":"ws","repoId":"` + info.ID + `","agent":"moded","mode":"plan","prompt":"` + fakeagent.SettingsQuestion + `"}`)
	if planCode != http.StatusCreated || planOut["sessionId"] == nil {
		t.Fatalf("start a session in plan: %d %v", planCode, planOut)
	}
	moded := planOut["sessionId"].(string)
	b.wait(t, "the session in plan to wait", 30*time.Second, func(v view) bool {
		s, ok := v.sessions[moded]
		return ok && s.Status == "idle" && s.Mode == "plan"
	})
	if code, out := post2("/api/sessions/ws/"+moded+"/mode", `{"mode":"ask"}`); code != http.StatusOK {
		t.Errorf("set the mode: %d %v", code, out)
	}
	if code, out := post2("/api/sessions/ws/"+moded+"/model", `{"model":"fake-large"}`); code != http.StatusOK {
		t.Errorf("set the model: %d %v", code, out)
	}
	if code, out := post2("/api/sessions/ws/"+moded+"/mode", `{"mode":"auto"}`); code != http.StatusBadRequest || !strings.Contains(fmt.Sprint(out["error"]), "not one the adapter lists") {
		t.Errorf("set an unlisted mode: %d %v", code, out)
	}
	if code, _ := post2("/api/sessions/ws/"+moded+"/model", `{}`); code != http.StatusBadRequest {
		t.Errorf("set no model: %d", code)
	}
	b.wait(t, "the new mode and model to show", 10*time.Second, func(v view) bool {
		s, ok := v.sessions[moded]
		return ok && s.Mode == "ask" && s.Model == "fake-large"
	})

	// The agent's commands are listed less the withheld one, which is
	// refused as a prompt, as is one the agent didn't announce.
	cresp, err := http.Get(w.webURL + "/api/sessions/ws/" + moded + "/commands")
	if err != nil {
		t.Fatal(err)
	}
	cbody, _ := io.ReadAll(cresp.Body)
	_ = cresp.Body.Close()
	if body := string(cbody); cresp.StatusCode != http.StatusOK || !strings.Contains(body, `"announced":true`) || !strings.Contains(body, `"name":"review"`) || strings.Contains(body, `"config"`) {
		t.Errorf("commands: %d %s", cresp.StatusCode, body)
	}
	if code, out := post2("/api/sessions/ws/"+moded+"/prompt", `{"text":"/config x=1"}`); code != http.StatusBadRequest || !strings.Contains(fmt.Sprint(out["error"]), "doesn't run this command") {
		t.Errorf("a withheld command: %d %v", code, out)
	}
	if code, out := post2("/api/sessions/ws/"+moded+"/prompt", `{"text":"/clear"}`); code != http.StatusBadRequest || !strings.Contains(fmt.Sprint(out["error"]), "isn't one of the commands") {
		t.Errorf("an unannounced command: %d %v", code, out)
	}
	if code, out := post2("/api/sessions/ws/"+moded+"/prompt", `{"text":"/review the diff"}`); code != http.StatusOK {
		t.Errorf("an announced command: %d %v", code, out)
	}
	b.wait(t, "the command's turn to end", 30*time.Second, func(v view) bool {
		s, ok := v.sessions[moded]
		return ok && s.Status == "idle" && s.Turns == 2
	})
	if code, out := post2("/api/sessions/ws/"+moded+"/stop", `{}`); code != http.StatusOK {
		t.Fatalf("stop the session in plan: %d %v", code, out)
	}

	// A later browser gets the session in its snapshot.
	b2 := w.openBrowser()
	b2.wait(t, "a snapshot with the session", 5*time.Second, func(v view) bool { s, ok := v.sessions[id]; return ok && s.Status == "ended" && s.Turns == 3 })

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
	if code, body := get("/api/sessions/ws/" + id + "/transcript"); code != http.StatusOK || !strings.Contains(body, "make it faster") || !strings.Contains(body, "now make it smaller") || !strings.Contains(body, "Done with") {
		t.Errorf("transcript: %d %s", code, body)
	}
	if code, body := get("/api/sessions/ws/" + id + "/diff"); code != http.StatusOK || !strings.Contains(body, `"text"`) {
		t.Errorf("diff: %d %s", code, body)
	}
	// A tool call's record comes the same way: the transcript's line names
	// the call, and the record holds its input and what it produced.
	if code, body := get("/api/sessions/ws/" + id + "/tools/t1-read-0"); code != http.StatusOK || !strings.Contains(body, `"title":"Read `) || !strings.Contains(body, `"status":"completed"`) || !strings.Contains(body, `"file_path"`) || !strings.Contains(body, `"type":"text"`) {
		t.Errorf("tool call: %d %s", code, body)
	}
	if code, _ := get("/api/sessions/ws/" + id + "/tools/nope"); code != http.StatusNotFound {
		t.Errorf("unknown tool call: %d", code)
	}
	if live := b.liveText(); !strings.Contains(live, " <!-- tool:t3-read-0 -->") {
		t.Errorf("live text lacks the tool marker: %q", live)
	}
	if code, _ := get("/api/sessions/ws/nope/transcript"); code != http.StatusNotFound {
		t.Errorf("unknown session transcript: %d", code)
	}
	if code, _ := get("/api/sessions/nowhere/x/transcript"); code != http.StatusBadGateway {
		t.Errorf("unknown host transcript: %d", code)
	}

	// The session's changes are committed and merged into the checkout from
	// the browser. The fake agent changes nothing, so the test does.
	worktree := out["worktree"].(string)
	if err := os.WriteFile(filepath.Join(worktree, "faster.go"), []byte("package faster\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, body := get("/api/sessions/ws/" + id + "/changes"); code != http.StatusOK || !strings.Contains(body, `"changed":["faster.go"]`) || !strings.Contains(body, `"ahead":0`) {
		t.Errorf("changes: %d %s", code, body)
	}
	if code, out := post2("/api/sessions/ws/"+id+"/merge", `{}`); code != http.StatusBadRequest || !strings.Contains(out["error"].(string), "uncommitted") {
		t.Errorf("merge before a commit: %d %v", code, out)
	}
	if code, out := post2("/api/sessions/ws/"+id+"/commit", `{"message":"Make it faster"}`); code != http.StatusOK {
		t.Fatalf("commit: %d %v", code, out)
	}
	v = b.wait(t, "the commit", 10*time.Second, func(v view) bool { s, ok := v.sessions[id]; return ok && s.Commits == 1 })
	if code, out := post2("/api/sessions/ws/"+id+"/merge", `{}`); code != http.StatusOK {
		t.Fatalf("merge: %d %v", code, out)
	}
	v = b.wait(t, "the merge", 10*time.Second, func(v view) bool { s, ok := v.sessions[id]; return ok && s.MergedInto != "" })
	if s := v.sessions[id]; s.MergedInto != s.Base || s.MergedCommit != s.Commit || s.InCheckout {
		t.Errorf("merged session = base %q, mergedInto %q, commits %q/%q", s.Base, s.MergedInto, s.MergedCommit, s.Commit)
	}
	if _, err := os.Stat(filepath.Join(repoDir, "faster.go")); err != nil {
		t.Errorf("the checkout lacks the merged file: %v", err)
	}
	if code, body := get("/api/sessions/ws/" + id + "/transcript"); code != http.StatusOK || !strings.Contains(body, "Committed ") || !strings.Contains(body, "Merged into ") {
		t.Errorf("transcript after the merge: %d %s", code, body)
	}

	// The session is over, so its worktree and merged branch are removed
	// from the browser. Asked again, the host says the same.
	for range 2 {
		if code, out := post2("/api/sessions/ws/"+id+"/remove-worktree", `{}`); code != http.StatusOK || out["branchDeleted"] != true {
			t.Fatalf("remove the worktree: %d %v", code, out)
		}
	}
	b.wait(t, "the removal", 10*time.Second, func(v view) bool { s := v.sessions[id]; return s.WorktreeRemoved && !s.BranchKept })
	if _, err := os.Stat(worktree); err == nil {
		t.Error("the worktree is still there")
	}

	// A session can work in the checkout itself, one at a time.
	code, out = post(`{"host":"ws","repoId":"` + info.ID + `","agent":"fake","prompt":"work in place","inCheckout":true}`)
	if code != http.StatusCreated {
		t.Fatalf("start a checkout session: %d %v", code, out)
	}
	inPlace := out["sessionId"].(string)
	v = b.wait(t, "the checkout session", 15*time.Second, func(v view) bool { _, ok := v.sessions[inPlace]; return ok })
	if s := v.sessions[inPlace]; !s.InCheckout || s.Branch != v.sessions[id].Base {
		t.Errorf("checkout session = %+v", s)
	}
	if code, out := post(`{"host":"ws","repoId":"` + info.ID + `","agent":"fake","prompt":"me too","inCheckout":true}`); code != http.StatusBadRequest || !strings.Contains(out["error"].(string), "another session") {
		t.Errorf("second checkout session: %d %v", code, out)
	}
	if code, out := post2("/api/sessions/ws/"+inPlace+"/remove-worktree", `{}`); code != http.StatusBadRequest || !strings.Contains(fmt.Sprint(out["error"]), "still open") {
		t.Errorf("remove an open session's worktree: %d %v", code, out)
	}
	if code, out := post2("/api/sessions/ws/"+inPlace+"/stop", `{}`); code != http.StatusOK {
		t.Fatalf("stop the checkout session: %d %v", code, out)
	}
}

func TestSessionSurvivesDaemonRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("whole-world test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	w := startWorld(t, ctx)
	b := w.openBrowser()
	sock := shortSocket(t)
	dataDir := t.TempDir()
	stop := w.startDaemonIn("ws", sock, dataDir)
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

	post := func(path, body string) (int, map[string]any) {
		t.Helper()
		resp, err := http.Post(w.webURL+path, "application/json", bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		data, _ := io.ReadAll(resp.Body)
		var out map[string]any
		_ = json.Unmarshal(data, &out)
		return resp.StatusCode, out
	}
	code, out := post("/api/sessions", `{"host":"ws","repoId":"`+info.ID+`","agent":"fake","prompt":"keep going"}`)
	if code != http.StatusCreated || out["sessionId"] == nil {
		t.Fatalf("start session: %d %v", code, out)
	}
	id := out["sessionId"].(string)
	v := b.wait(t, "a request to wait", 30*time.Second, func(v view) bool { s, ok := v.sessions[id]; return ok && len(s.Pending) == 1 })
	first := v.sessions[id].Pending[0].RequestID

	// The daemon goes down mid-turn with the request unanswered. The island
	// goes grey, and the session keeps the last state seen.
	stop()
	v = b.wait(t, "ws unreachable", 15*time.Second, func(v view) bool { return reachable(v, "ws", false) })
	if s := v.sessions[id]; s.Status != "running" || len(s.Pending) != 1 {
		t.Errorf("session while the daemon is down = %+v", s)
	}

	// It comes back on the same data. The history is intact, the request
	// has expired, the agent is loaded again and continues the turn, and
	// it asks about the edit again.
	stop = w.startDaemonIn("ws", sock, dataDir)
	defer stop()
	v = b.wait(t, "the session to resume", 30*time.Second, func(v view) bool { s, ok := v.sessions[id]; return ok && s.Resumes == 1 })
	s := v.sessions[id]
	if s.Status != "running" || s.Resumed.How != "load" || !s.Resumed.TurnResumed || s.Resumed.RestartedAt.IsZero() || s.Error != "" || s.Turns != 1 || s.Prompt != "keep going" || s.AgentName != "skylands-fakeagent" {
		t.Errorf("resumed session = %+v", s)
	}
	v = b.wait(t, "the edit to be asked again", 30*time.Second, func(v view) bool {
		s, ok := v.sessions[id]
		return ok && len(s.Pending) == 1 && s.Pending[0].RequestID != first
	})
	if code, out := post("/api/sessions/ws/"+id+"/requests/"+v.sessions[id].Pending[0].RequestID+"/approve", approval(v.sessions[id].Pending[0])); code != http.StatusOK {
		t.Fatalf("approve: %d %v", code, out)
	}
	v = b.wait(t, "the resumed turn to end", 30*time.Second, func(v view) bool {
		s, ok := v.sessions[id]
		return ok && s.Status == "idle" && len(s.Pending) == 0
	})
	if s := v.sessions[id]; s.Turns != 1 || s.Files["main.go"] != "edit" {
		t.Errorf("after the resumed turn = %+v", s)
	}

	// The conversation goes on where it was.
	if code, out := post("/api/sessions/ws/"+id+"/prompt", `{"text":"and again"}`); code != http.StatusOK {
		t.Fatalf("prompt: %d %v", code, out)
	}
	v = b.wait(t, "the next edit to wait", 30*time.Second, func(v view) bool { s, ok := v.sessions[id]; return ok && len(s.Pending) == 1 })
	if code, out := post("/api/sessions/ws/"+id+"/requests/"+v.sessions[id].Pending[0].RequestID+"/approve", approval(v.sessions[id].Pending[0])); code != http.StatusOK {
		t.Fatalf("approve: %d %v", code, out)
	}
	b.wait(t, "the second turn to end", 30*time.Second, func(v view) bool { s, ok := v.sessions[id]; return ok && s.Status == "idle" && s.Turns == 2 })
	resp, err := http.Get(w.webURL + "/api/sessions/ws/" + id + "/transcript")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if text := string(body); resp.StatusCode != http.StatusOK || !strings.Contains(text, "keep going") || !strings.Contains(text, "The daemon restarted at") || !strings.Contains(text, `Done with \"and again\".`) || strings.Contains(text, "Failed") {
		t.Errorf("transcript: %d %s", resp.StatusCode, text)
	}

	// A later browser sees the same in its snapshot.
	b2 := w.openBrowser()
	b2.wait(t, "a snapshot with the resumed session", 5*time.Second, func(v view) bool {
		s, ok := v.sessions[id]
		return ok && s.Resumes == 1 && s.Status == "idle" && s.Turns == 2
	})
}

func TestRulesSettleToolCallsAndSessionsInheritThem(t *testing.T) {
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
	repoDir := gitRepo(t, "main.go")
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

	post := func(path, body string) (int, map[string]any) {
		t.Helper()
		resp, err := http.Post(w.webURL+path, "application/json", bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		data, _ := io.ReadAll(resp.Body)
		var out map[string]any
		_ = json.Unmarshal(data, &out)
		return resp.StatusCode, out
	}

	// Rules set on the repository from the browser reach the terrace, and
	// a session started on it copies them: its edit never waits.
	if code, out := post("/api/repos/ws/"+info.ID+"/rules", `{"rules":[{"decision":"allow","kind":"edit","pattern":"worktree"}]}`); code != http.StatusOK {
		t.Fatalf("set repo rules: %d %v", code, out)
	}
	if code, out := post("/api/repos/ws/"+info.ID+"/rules", `{"rules":[{"decision":"sometimes","kind":"edit"}]}`); code != http.StatusBadRequest || out["error"] == nil {
		t.Errorf("bad rule: %d %v", code, out)
	}
	b.wait(t, "the repo's rules", 10*time.Second, func(v view) bool {
		r, ok := v.repos[info.ID]
		return ok && len(r.Rules) == 1 && r.Rules[0].Pattern == "worktree"
	})
	code, out := post("/api/sessions", `{"host":"ws","repoId":"`+info.ID+`","agent":"fake","prompt":"edit freely"}`)
	if code != http.StatusCreated {
		t.Fatalf("start session: %d %v", code, out)
	}
	id := out["sessionId"].(string)
	v := b.wait(t, "the first turn to end", 30*time.Second, func(v view) bool { s, ok := v.sessions[id]; return ok && s.Status == "idle" })
	s := v.sessions[id]
	if len(s.Rules) != 1 || s.Rules[0].Source != "repo" || len(s.Pending) != 0 || s.Files["main.go"] != "edit" || s.Recent[len(s.Recent)-1].Status != "completed" {
		t.Errorf("session with an inherited rule = rules %+v, pending %d, recent %+v", s.Rules, len(s.Pending), s.Recent)
	}

	// A rule set on the session beats the copied one, and the host's CLI
	// sees the repository's rules too.
	if code, out := post("/api/sessions/ws/"+id+"/rules", `{"rules":[{"decision":"allow","kind":"edit","pattern":"worktree","source":"repo"},{"decision":"deny","kind":"edit","pattern":"worktree"}]}`); code != http.StatusOK {
		t.Fatalf("set session rules: %d %v", code, out)
	}
	b.wait(t, "the session's rules", 10*time.Second, func(v view) bool { s, ok := v.sessions[id]; return ok && len(s.Rules) == 2 })
	if code, out := post("/api/sessions/ws/"+id+"/prompt", `{"text":"try again"}`); code != http.StatusOK {
		t.Fatalf("prompt: %d %v", code, out)
	}
	v = b.wait(t, "the denied turn to end", 30*time.Second, func(v view) bool { s, ok := v.sessions[id]; return ok && s.Status == "idle" && s.Turns == 2 })
	if s := v.sessions[id]; len(s.Pending) != 0 || s.Recent[len(s.Recent)-1].Status != "failed" {
		t.Errorf("session after a denying rule = pending %d, recent %+v", len(s.Pending), s.Recent)
	}
	if list, err := cli.Rules(ctx); err != nil || len(list) != 1 || len(list[0].Rules) != 1 || list[0].Repo != info.Name {
		t.Errorf("Rules over the socket = %+v, %v", list, err)
	}
}

func TestQuestionsReachTheBrowser(t *testing.T) {
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
	post := func(path, body string) (int, map[string]any) {
		t.Helper()
		resp, err := http.Post(w.webURL+path, "application/json", bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		data, _ := io.ReadAll(resp.Body)
		var out map[string]any
		_ = json.Unmarshal(data, &out)
		return resp.StatusCode, out
	}
	code, out := post("/api/sessions", `{"host":"ws","repoId":"`+info.ID+`","agent":"fake","prompt":"ask me which file"}`)
	if code != http.StatusCreated {
		t.Fatalf("start session: %d %v", code, out)
	}
	id := out["sessionId"].(string)

	// The agent's question reaches the browser with its form, the host's
	// CLI lists it, and the browser's answer steers the agent.
	v := b.wait(t, "the question", 30*time.Second, func(v view) bool { s, ok := v.sessions[id]; return ok && len(s.Questions) == 1 })
	q := v.sessions[id].Questions[0]
	if q.Message != "Which file should I edit?" || len(q.Fields) != 2 || len(q.Fields[0].Options) != 2 || q.Fields[1].Title != "Other" {
		t.Errorf("question = %+v", q)
	}
	if list, err := cli.Questions(ctx); err != nil || len(list) != 1 || list[0].ID != q.QuestionID || list[0].Repo != info.Name {
		t.Errorf("Questions over the socket = %+v, %v", list, err)
	}
	if code, out := post("/api/sessions/ws/"+id+"/questions/"+q.QuestionID+"/answer", `{}`); code != http.StatusBadRequest {
		t.Errorf("an empty answer: %d %v", code, out)
	}
	if code, out := post("/api/sessions/ws/"+id+"/questions/"+q.QuestionID+"/answer", `{"answers":{"question_0":"util.go"}}`); code != http.StatusOK {
		t.Fatalf("answer: %d %v", code, out)
	}
	v = b.wait(t, "the edit to wait", 30*time.Second, func(v view) bool { s, ok := v.sessions[id]; return ok && len(s.Questions) == 0 && len(s.Pending) == 1 })
	if p := v.sessions[id].Pending[0]; p.Title != "Edit util.go" {
		t.Errorf("the agent edits %q, want util.go", p.Title)
	}
	if code, out := post("/api/sessions/ws/"+id+"/requests/"+v.sessions[id].Pending[0].RequestID+"/approve", approval(v.sessions[id].Pending[0])); code != http.StatusOK {
		t.Fatalf("approve: %d %v", code, out)
	}
	b.wait(t, "the turn to end", 30*time.Second, func(v view) bool { s, ok := v.sessions[id]; return ok && s.Status == "idle" })

	// The next question is declined from the host.
	if code, out := post("/api/sessions/ws/"+id+"/prompt", `{"text":"ask me again"}`); code != http.StatusOK {
		t.Fatalf("prompt: %d %v", code, out)
	}
	v = b.wait(t, "the second question", 30*time.Second, func(v view) bool { s, ok := v.sessions[id]; return ok && len(s.Questions) == 1 })
	if _, err := cli.AnswerQuestion(ctx, v.sessions[id].Questions[0].QuestionID, nil); err != nil {
		t.Fatalf("decline over the socket: %v", err)
	}
	v = b.wait(t, "the edit to wait again", 30*time.Second, func(v view) bool { s, ok := v.sessions[id]; return ok && len(s.Questions) == 0 && len(s.Pending) == 1 })
	if p := v.sessions[id].Pending[0]; p.Title != "Edit main.go" {
		t.Errorf("after a refusal the agent edits %q, want main.go", p.Title)
	}
}

// approval is the body of an approval of p, naming the hash of its input,
// which for the fake agent's small edits is all in the preview.
func approval(p projections.Permission) string {
	sum := sha256.Sum256([]byte(p.InputPreview))
	return fmt.Sprintf(`{"inputHash":%q}`, hex.EncodeToString(sum[:]))
}

// TestPromptsCarryMentionsAndImages starts a session from the world with a
// file mentioned and an image attached, as a browser does: it asks the
// host for the files to mention, uploads the image through the citadel,
// which relays it in pieces, and fetches it back for the conversation.
func TestPromptsCarryMentionsAndImages(t *testing.T) {
	if testing.Short() {
		t.Skip("whole-world test")
	}
	for _, kv := range [][2]string{{"GIT_AUTHOR_NAME", "t"}, {"GIT_AUTHOR_EMAIL", "t@t"}, {"GIT_COMMITTER_NAME", "t"}, {"GIT_COMMITTER_EMAIL", "t@t"}} {
		t.Setenv(kv[0], kv[1])
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	w := startWorld(t, ctx)
	b := w.openBrowser()
	sock := shortSocket(t)
	stop := w.startDaemonWithSocket("ws", sock)
	defer stop()
	b.wait(t, "ws reachable", 15*time.Second, func(v view) bool { return reachable(v, "ws", true) })
	repoDir := gitRepo(t, "main.go", "util.go", "notes.md")
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
	// The browser knows which agents take images from the island.
	b.wait(t, "the agents checked", 60*time.Second, func(v view) bool {
		for _, a := range v.islands["ws"].Agents {
			if a.Name == "seeing" && a.Available && a.Images && a.EmbeddedContext {
				return true
			}
		}
		return false
	})
	get := func(path string) (int, string, []byte) {
		t.Helper()
		resp, err := http.Get(w.webURL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		data, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.Header.Get("Content-Type"), data
	}
	send := func(path, contentType string, body []byte) (int, map[string]any) {
		t.Helper()
		resp, err := http.Post(w.webURL+path, contentType, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		data, _ := io.ReadAll(resp.Body)
		var out map[string]any
		_ = json.Unmarshal(data, &out)
		return resp.StatusCode, out
	}

	// What follows an @ is matched against the checkout's files.
	code, _, data := get("/api/repos/ws/" + info.ID + "/files?q=util")
	var files v1.FilesResult
	if err := json.Unmarshal(data, &files); code != http.StatusOK || err != nil || len(files.Files) == 0 || files.Files[0] != "util.go" {
		t.Fatalf("files matching util = %d %s", code, data)
	}

	// An image bigger than a NATS message goes up in pieces.
	image := append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, bytes.Repeat([]byte("pixels"), 400_000)...)
	sum := sha256.Sum256(image)
	hash := hex.EncodeToString(sum[:])
	if code, out := send("/api/islands/ws/images", "text/plain", []byte("hello")); code != http.StatusUnsupportedMediaType {
		t.Errorf("a text upload = %d %v", code, out)
	}
	if code, out := send("/api/islands/ws/images", "image/gif", image); code != http.StatusBadRequest {
		t.Errorf("a PNG called a GIF = %d %v", code, out)
	}
	code, out := send("/api/islands/ws/images", "image/png", image)
	if code != http.StatusCreated || out["hash"] != hash || out["size"] != float64(len(image)) {
		t.Fatalf("upload = %d %v, want hash %s", code, out, hash)
	}

	// The edit is allowed, so the turn runs to its end unasked.
	if code, out := send("/api/repos/ws/"+info.ID+"/rules", "application/json", []byte(`{"rules":[{"decision":"allow","kind":"edit"}],"revision":0}`)); code != http.StatusOK {
		t.Fatalf("set the repository's rules: %d %v", code, out)
	}
	start, _ := json.Marshal(map[string]any{
		"host": "ws", "repoId": info.ID, "agent": "seeing", "prompt": "what does @util.go do to this?",
		"mentions": []string{"util.go"}, "images": []v1.PromptImage{{Name: "screen.png", MimeType: "image/png", Hash: hash, Size: int64(len(image))}},
	})
	code, out = send("/api/sessions", "application/json", start)
	if code != http.StatusCreated || out["sessionId"] == nil {
		t.Fatalf("start session: %d %v", code, out)
	}
	id := out["sessionId"].(string)
	b.wait(t, "the turn to end", 60*time.Second, func(v view) bool { s, ok := v.sessions[id]; return ok && s.Status == "idle" })

	code, _, data = get("/api/sessions/ws/" + id + "/transcript")
	var tr v1.TextResult
	if err := json.Unmarshal(data, &tr); code != http.StatusOK || err != nil {
		t.Fatalf("transcript = %d %s", code, data)
	}
	for _, want := range []string{
		"\U0001F4C4 util.go (contents)\n\U0001F5BC screen.png <!-- image:" + hash + " -->",
		"/util.go (7 bytes); image image/png (" + fmt.Sprint(len(image)) + " bytes).",
	} {
		if !strings.Contains(tr.Text, want) {
			t.Errorf("the transcript lacks %q:\n%s", want, tr.Text)
		}
	}

	// The conversation fetches the image back, in pieces, whole.
	code, contentType, data := get("/api/sessions/ws/" + id + "/attachments/" + hash)
	if code != http.StatusOK || contentType != "image/png" || !bytes.Equal(data, image) {
		t.Errorf("the image fetched back = %d %s, %d bytes", code, contentType, len(data))
	}
	if code, _, _ := get("/api/sessions/ws/" + id + "/attachments/" + strings.Repeat("0", 64)); code != http.StatusNotFound {
		t.Errorf("an image the session never had = %d", code)
	}
	// An open session's files are its worktree's.
	code, _, data = get("/api/sessions/ws/" + id + "/files?q=notes")
	if err := json.Unmarshal(data, &files); code != http.StatusOK || err != nil || len(files.Files) != 1 || files.Files[0] != "notes.md" {
		t.Errorf("the session's files matching notes = %d %s", code, data)
	}
}
