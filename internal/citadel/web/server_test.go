package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/coder/websocket"
)

func newTestServer(t *testing.T, dist *fstest.MapFS) *httptest.Server {
	t.Helper()
	cfg := Config{
		Snapshot: func() Message {
			return Message{Type: "snapshot", Server: &ServerInfo{Connected: true, Name: "stub", Version: "0.0.0"}}
		},
	}
	if dist != nil {
		cfg.Dist = dist
	}
	ts := httptest.NewServer(New(cfg).Handler())
	t.Cleanup(ts.Close)
	return ts
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func TestServesBuiltClient(t *testing.T) {
	ts := newTestServer(t, &fstest.MapFS{"index.html": {Data: []byte("<title>built client</title>")}})
	code, body := get(t, ts.URL+"/")
	if code != http.StatusOK || !strings.Contains(body, "built client") {
		t.Errorf("GET / = %d %q, want 200 with the built page", code, body)
	}
	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("page Cache-Control = %q, want no-cache", cc)
	}
	code, body = get(t, ts.URL+"/healthz")
	if code != http.StatusOK || body != "ok\n" {
		t.Errorf("GET /healthz = %d %q, want 200 %q", code, body, "ok\n")
	}
}

// TestServesWebAppFiles serves the manifest as one, and the service worker
// fresh, so an upgraded citadel's worker replaces the old one.
func TestServesWebAppFiles(t *testing.T) {
	ts := newTestServer(t, &fstest.MapFS{
		"index.html":           {Data: []byte("<title>built client</title>")},
		"manifest.webmanifest": {Data: []byte(`{"name":"Skylands"}`)},
		"sw.js":                {Data: []byte("self.addEventListener('fetch', () => {})")},
	})
	for path, want := range map[string]string{"/manifest.webmanifest": "application/manifest+json", "/sw.js": "text/javascript"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if ct := resp.Header.Get("Content-Type"); resp.StatusCode != http.StatusOK || !strings.HasPrefix(ct, want) {
			t.Errorf("GET %s = %d %q, want 200 %s", path, resp.StatusCode, ct, want)
		}
		if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
			t.Errorf("GET %s Cache-Control = %q, want no-cache", path, cc)
		}
	}
}

// TestServesInboxLinks serves the world's page at the inbox's links, which
// a notification opens, and sends them to setup while there is no world.
func TestServesInboxLinks(t *testing.T) {
	dist := &fstest.MapFS{"index.html": {Data: []byte("<title>built client</title>")}, "setup.html": {Data: []byte("<title>setup</title>")}}
	ts := newTestServer(t, dist)
	for _, path := range []string{"/inbox", "/inbox/", "/inbox/workstation/S1", "/inbox/workstation/S1/requests/R1", "/inbox/workstation/S1/questions/Q1"} {
		if code, body := get(t, ts.URL+path); code != http.StatusOK || !strings.Contains(body, "built client") {
			t.Errorf("GET %s = %d %q, want 200 with the world's page", path, code, body)
		}
	}
	if code, _ := get(t, ts.URL+"/inboxes"); code != http.StatusNotFound {
		t.Errorf("GET /inboxes = %d, want 404", code)
	}

	unset := httptest.NewServer(New(Config{Dist: dist, NeedsSetup: func() bool { return true }}).Handler())
	defer unset.Close()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(unset.URL + "/inbox/workstation/S1/requests/R1")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/setup" {
		t.Errorf("an inbox link before setup = %d to %q, want 302 to /setup", resp.StatusCode, resp.Header.Get("Location"))
	}
}

func TestServesPlaceholderWithoutBuild(t *testing.T) {
	ts := newTestServer(t, nil)
	code, body := get(t, ts.URL+"/")
	if code != http.StatusOK || !strings.Contains(body, "make web") || !strings.Contains(body, ReleasesURL) {
		t.Errorf("GET / = %d, want 200 with build instructions and a link to releases; body:\n%s", code, body)
	}
	if code, _ := get(t, ts.URL+"/missing"); code != http.StatusNotFound {
		t.Errorf("GET /missing = %d, want 404", code)
	}
}

func TestWebSocketSendsSnapshotThenBroadcasts(t *testing.T) {
	cfg := Config{Snapshot: func() Message { return Message{Type: "snapshot", Server: &ServerInfo{Connected: true, Name: "stub"}} }}
	s := New(cfg)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/ws", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = c.CloseNow() }()

	read := func() Message {
		t.Helper()
		_, data, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		var m Message
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("decode %q: %v", data, err)
		}
		return m
	}

	first := read()
	if first.Type != "snapshot" || first.Server == nil || first.Server.Name != "stub" || !first.Server.Connected {
		t.Fatalf("first message = %+v, want a snapshot with connected server %q", first, "stub")
	}

	// The handler registers the connection before its first write, so by now
	// a broadcast reaches it.
	s.Broadcast(Message{Type: "server", Server: &ServerInfo{Connected: false, URL: "nats://gone"}})
	second := read()
	if second.Server == nil || second.Server.Connected || second.Server.URL != "nats://gone" {
		t.Fatalf("broadcast message = %+v, want disconnected server", second)
	}
}

// TestWebSocketTicket: a socket opened with a ticket from GET /ws/ticket
// connects whatever Origin and Host it arrives with, as through a port
// forwarder that sends the citadel's own address. Without a ticket, or
// with one for someone else, expired, or altered, its Origin must match.
func TestWebSocketTicket(t *testing.T) {
	cfg := Config{
		Snapshot: func() Message { return Message{Type: "snapshot"} },
		SignedIn: func(r *http.Request) (string, int, string) {
			if r.Header.Get("X-Who") == "" {
				return "", http.StatusUnauthorized, "sign in"
			}
			return r.Header.Get("X-Who"), 0, ""
		},
	}
	s := New(cfg)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ticketFor := func(who string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/ws/ticket", nil)
		if who != "" {
			req.Header.Set("X-Who", who)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		var body struct{ Ticket string }
		_ = json.NewDecoder(resp.Body).Decode(&body)
		return resp.StatusCode, body.Ticket
	}
	dial := func(who, ticket string) error {
		t.Helper()
		u := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
		if ticket != "" {
			u += "?ticket=" + url.QueryEscape(ticket)
		}
		c, _, err := websocket.Dial(ctx, u, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {"http://localhost:8081"}, "X-Who": {who}}})
		if err == nil {
			_, _, err = c.Read(ctx)
			_ = c.CloseNow()
		}
		return err
	}

	if code, _ := ticketFor(""); code != http.StatusUnauthorized {
		t.Errorf("a ticket without signing in = %d, want 401", code)
	}
	code, alice := ticketFor("alice")
	if code != http.StatusOK || alice == "" {
		t.Fatalf("a ticket for alice = %d %q", code, alice)
	}
	if err := dial("alice", alice); err != nil {
		t.Errorf("alice's socket with her ticket: %v", err)
	}
	if err := dial("alice", alice); err != nil {
		t.Errorf("alice's socket with her ticket again: %v", err)
	}
	for what, ticket := range map[string]string{
		"no ticket":                  "",
		"bob's ticket":               s.ticket("bob", time.Now()),
		"an expired ticket":          s.ticket("alice", time.Now().Add(-ticketLife-time.Second)),
		"an altered ticket":          altered(alice),
		"its last character changed": alice[:len(alice)-1] + map[bool]string{true: "B", false: "A"}[strings.HasSuffix(alice, "A")],
		"another citadel's":          New(cfg).ticket("alice", time.Now()),
	} {
		if err := dial("alice", ticket); err == nil {
			t.Errorf("alice's socket from another origin with %s connected", what)
		}
	}
}

// altered is t with a character of its signature changed.
func altered(t string) string {
	i := len(t) - 10
	if t[i] == 'A' {
		return t[:i] + "B" + t[i+1:]
	}
	return t[:i] + "A" + t[i+1:]
}
