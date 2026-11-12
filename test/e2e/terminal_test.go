package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/citadel/web"
	"github.com/skylands-ai/skylands/internal/daemon/socket"
	"github.com/skylands-ai/skylands/internal/fakeagent"
)

// send writes one message to the citadel, as a page does.
func (b *browser) send(t *testing.T, in web.Inbound) {
	t.Helper()
	data, _ := json.Marshal(in)
	if err := b.c.Write(b.ctx, websocket.MessageText, data); err != nil {
		t.Fatal(err)
	}
}

// waitTerm blocks until the terminal the browser watches has shown text.
func (b *browser) waitTerm(t *testing.T, text string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		b.mu.Lock()
		got := b.term.String()
		b.mu.Unlock()
		if strings.Contains(got, text) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the terminal never showed %q; it showed %q", text, got)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// TestTerminalThroughTheCitadel opens a terminal in a session's worktree
// from the citadel's API, types into it and reads it over the WebSocket,
// and ends it with the session.
func TestTerminalThroughTheCitadel(t *testing.T) {
	if testing.Short() {
		t.Skip("whole-world test")
	}
	// A plain shell, whatever the person running the tests uses.
	t.Setenv("SHELL", "/bin/sh")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	w := startWorld(t, ctx)
	b := w.openBrowser()
	sock := shortSocket(t)
	stop := w.startDaemonWithSocket("ws", sock)
	defer stop()
	b.wait(t, "ws reachable", 15*time.Second, func(v view) bool { return reachable(v, "ws", true) })

	cli := socket.Dial(sock)
	var info socket.RepoInfo
	var err error
	for i := 0; i < 100; i++ {
		if info, err = cli.RegisterRepo(ctx, gitRepo(t, "main.go")); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("RegisterRepo: %v", err)
	}
	b.wait(t, "the terrace", 20*time.Second, func(v view) bool { _, ok := v.repos[info.ID]; return ok })

	call := func(method, path, body string) (int, string) {
		t.Helper()
		req, _ := http.NewRequestWithContext(ctx, method, w.webURL+path, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		data, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(data)
	}
	code, out := call("POST", "/api/sessions", `{"host":"ws","repoId":"`+info.ID+`","agent":"fake","sessionId":"Sess0001","prompt":"`+fakeagent.SettingsQuestion+`"}`)
	if code != http.StatusCreated {
		t.Fatalf("start session: %d %s", code, out)
	}
	b.wait(t, "the session to go idle", 30*time.Second, func(v view) bool { return v.sessions["Sess0001"].Status == "idle" })

	if code, out := call("POST", "/api/sessions/ws/Sess0001/terminals", `{"terminalId":"bad id"}`); code != http.StatusBadRequest {
		t.Errorf("a bad terminal id: %d %s", code, out)
	}
	code, out = call("POST", "/api/sessions/ws/Sess0001/terminals", `{"terminalId":"Term0001","cols":100,"rows":30}`)
	if code != http.StatusCreated || !strings.Contains(out, `"shell":"/bin/sh"`) {
		t.Fatalf("open a terminal: %d %s", code, out)
	}
	b.wait(t, "the terminal in the session", 10*time.Second, func(v view) bool {
		ts := v.sessions["Sess0001"].Terminals
		return len(ts) == 1 && ts[0].ID == "Term0001" && ts[0].Shell == "/bin/sh"
	})

	// What is typed reaches the shell in the worktree, and what it writes
	// comes back over the socket and from the host on request.
	at := web.Inbound{Host: "ws", ID: "Sess0001", Terminal: "Term0001"}
	watch := at
	watch.Type = "term-watch"
	b.send(t, watch)
	time.Sleep(200 * time.Millisecond) // the citadel subscribes before the shell answers
	input := at
	input.Type, input.Input = "term-input", &v1.TerminalInput{Attach: "Attach01", Seq: 1, Data: []byte("ls; echo through-\"\"the-citadel; stty size\n")}
	b.send(t, input)
	b.waitTerm(t, "through-the-citadel")
	b.waitTerm(t, "main.go")
	b.waitTerm(t, "30 100")
	size := at
	size.Type, size.Size = "term-size", &v1.TerminalSize{Cols: 120, Rows: 33}
	b.send(t, size)
	time.Sleep(200 * time.Millisecond)
	input.Input = &v1.TerminalInput{Attach: "Attach01", Seq: 2, Data: []byte("stty size\n")}
	b.send(t, input)
	b.waitTerm(t, "33 120")
	if code, out := call("GET", "/api/sessions/ws/Sess0001/terminals/Term0001", ""); code != http.StatusOK {
		t.Errorf("fetch the terminal: %d %s", code, out)
	} else {
		var screen v1.TerminalOutput
		if err := json.Unmarshal([]byte(out), &screen); err != nil || !bytes.Contains(screen.Data, []byte("through-the-citadel")) {
			t.Errorf("the terminal's screen = %q, %v", screen.Data, err)
		}
	}

	// Ending the session closes it.
	if code, out := call("POST", "/api/sessions/ws/Sess0001/stop", `{}`); code != http.StatusOK {
		t.Fatalf("stop: %d %s", code, out)
	}
	b.wait(t, "the terminal to close with the session", 15*time.Second, func(v view) bool {
		s := v.sessions["Sess0001"]
		return s.Status == "ended" && len(s.Terminals) == 0
	})
	if code, _ := call("GET", "/api/sessions/ws/Sess0001/terminals/Term0001", ""); code != http.StatusNotFound {
		t.Errorf("fetch a closed terminal: %d", code)
	}
	if code, _ := call("POST", "/api/sessions/ws/Sess0001/terminals", `{"terminalId":"Term0002"}`); code != http.StatusBadRequest {
		t.Errorf("open a terminal in an ended session: %d", code)
	}
}
