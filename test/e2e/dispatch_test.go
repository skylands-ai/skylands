package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/nats-io/nuid"

	"github.com/skylands-ai/skylands/internal/daemon/socket"
	"github.com/skylands-ai/skylands/internal/dispatch"
	"github.com/skylands-ai/skylands/internal/fakeagent"
)

// TestWorkPlacedFromTheWorld asks for work at the world and at an island,
// and the daemon picks the repository by searching its own: a clear
// winner takes it, a close call goes to the island's agent, whose answer
// costs the session it starts, and nothing fitting goes to a person.
func TestWorkPlacedFromTheWorld(t *testing.T) {
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

	cli := socket.Dial(sock)
	register := func(files ...string) socket.RepoInfo {
		t.Helper()
		dir := gitRepo(t, files...)
		var info socket.RepoInfo
		var err error
		for range 100 {
			if info, err = cli.RegisterRepo(ctx, dir); err == nil {
				return info
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("RegisterRepo: %v", err)
		return info
	}
	shop := register("pricing.go", "cart.go")
	books := register("search.go", "shelves.go")
	b.wait(t, "the terraces", 20*time.Second, func(v view) bool {
		_, a := v.repos[shop.ID]
		_, c := v.repos[books.ID]
		return a && c
	})

	post := func(path string, body any, out any) int {
		t.Helper()
		data, _ := json.Marshal(body)
		resp, err := w.http.Post(w.webURL+path, "application/json", bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		got, _ := io.ReadAll(resp.Body)
		if out != nil {
			_ = json.Unmarshal(got, out)
		}
		return resp.StatusCode
	}
	type placed struct {
		dispatch.Verdict
		Asked []string `json:"asked"`
	}

	// At the world and at the island, a path that matches settles it.
	for _, host := range []string{"", "ws"} {
		var res placed
		if code := post("/api/dispatch", map[string]string{"prompt": "make pricing faster", "host": host}, &res); code != http.StatusOK || res.Do != dispatch.Start || res.Target == nil || res.Target.RepoID != shop.ID || res.Target.Host != "ws" || len(res.Asked) != 1 {
			t.Errorf("placing at %q = %d %+v", host, code, res)
		}
	}
	var res placed
	if post("/api/dispatch", map[string]string{"prompt": "make it faster"}, &res); res.Do != dispatch.AskPerson || res.Why != dispatch.WhyNothing {
		t.Errorf("placing what fits nowhere = %+v", res)
	}
	if code := post("/api/dispatch", map[string]string{"prompt": "x", "host": "nowhere"}, nil); code != http.StatusNotFound {
		t.Errorf("placing at an unknown island = %d", code)
	}

	// A close call goes to the island's agent, which picks a repository;
	// what it cost is recorded on the session started there.
	session := nuid.Next()
	var picked struct {
		RepoID string `json:"repoId"`
		Agent  string `json:"agent"`
	}
	work := "move the search into " + books.Name
	if code := post("/api/dispatch/pick", map[string]any{"prompt": work, "host": "ws", "among": []string{shop.ID, books.ID}, "agent": "fake", "sessionId": session}, &picked); code != http.StatusOK || picked.RepoID != books.ID || picked.Agent != "fake" {
		t.Fatalf("pick = %d %+v", code, picked)
	}
	if code := post("/api/sessions", map[string]any{"host": "ws", "repoId": picked.RepoID, "agent": "fake", "prompt": work, "sessionId": session}, nil); code != http.StatusCreated {
		t.Fatalf("start = %d", code)
	}
	v := b.wait(t, "the pick's cost on the session", 30*time.Second, func(v view) bool {
		s, ok := v.sessions[session]
		return ok && s.Tokens.Input >= fakeagent.PickInputTokens
	})
	if in := v.sessions[session].Tokens.Input; in != fakeagent.PickInputTokens && in != fakeagent.PickInputTokens+fakeagent.TitleInputTokens {
		t.Errorf("the session's input tokens = %d, want the pick's %d, and the title's at most", in, fakeagent.PickInputTokens)
	}

	// On the host, the CLI places a prompt the same way, and replays the
	// session just started.
	info, err := cli.Match(ctx, "make pricing faster")
	if err != nil || info.Repos != 2 || info.Verdict.Do != dispatch.Start || info.Verdict.Target.RepoID != shop.ID {
		t.Errorf("Match = %+v, %v", info, err)
	}
	replayed, err := cli.Replay(ctx)
	if err != nil || len(replayed) != 1 || replayed[0].Session != session || replayed[0].RepoID != books.ID || replayed[0].Verdict.Do != dispatch.Start || replayed[0].Verdict.Target.RepoID != books.ID {
		t.Errorf("Replay = %+v, %v", replayed, err)
	}
}
