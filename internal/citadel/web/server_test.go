package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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
