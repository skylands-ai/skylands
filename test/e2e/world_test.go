// Package e2e runs a whole world in one test process: embedded NATS, a
// citadel, and real daemons, observed the way a browser observes it, over
// the citadel's WebSocket.
package e2e

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/skylands-ai/skylands/internal/citadel"
	"github.com/skylands-ai/skylands/internal/citadel/projections"
	"github.com/skylands-ai/skylands/internal/citadel/web"
	"github.com/skylands-ai/skylands/internal/daemon"
	"github.com/skylands-ai/skylands/internal/natsembed"
)

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
	w.t.Helper()
	ctx, cancel := context.WithCancel(w.ctx)
	done := make(chan error, 1)
	go func() {
		done <- daemon.Run(ctx, daemon.Config{Host: host, NATSURL: w.hub.LeafURL(), DataDir: w.t.TempDir(), Version: "v0.1.0"})
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
	b := &browser{islands: map[string]projections.Island{}}
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
			}
			b.mu.Unlock()
		}
	}()
	return b
}

type view struct {
	server  web.ServerInfo
	islands map[string]projections.Island
}

// wait blocks until pred holds for the browser's state, or fails the test
// after timeout.
func (b *browser) wait(t *testing.T, what string, timeout time.Duration, pred func(v view) bool) view {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		b.mu.Lock()
		v := view{server: b.server, islands: make(map[string]projections.Island, len(b.islands))}
		for k, isl := range b.islands {
			v.islands[k] = isl
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
