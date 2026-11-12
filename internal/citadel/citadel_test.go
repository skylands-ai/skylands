package citadel

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/skylands-ai/skylands/internal/citadel/projections"
	"github.com/skylands-ai/skylands/internal/citadel/web"
	"github.com/skylands-ai/skylands/internal/daemon"
	"github.com/skylands-ai/skylands/internal/daemon/agents"
	"github.com/skylands-ai/skylands/internal/fakeagent"
	"github.com/skylands-ai/skylands/internal/natsembed"
)

// freeAddr picks a port that is free right now. The listener comes a moment
// later, which is a small race the test accepts.
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

func TestRunServesWebAndTracksIslands(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	srv, err := natsembed.Start(ctx, natsembed.Config{Name: "e2e", ClientAddr: "127.0.0.1:0", LeafAddr: freeAddr(t), StoreDir: t.TempDir(), Domain: "e2eworld"})
	if err != nil {
		t.Fatalf("natsembed: %v", err)
	}
	defer srv.Shutdown()

	addr := freeAddr(t)
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- Run(runCtx, Config{Version: "v9.8.7", NATSURL: srv.ClientURL(), HTTPAddr: addr, PollInterval: 200 * time.Millisecond, PollWindow: 100 * time.Millisecond})
	}()

	// Wait for the web server to come up.
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, err := http.Get("http://" + addr + "/healthz")
		if err == nil {
			_ = resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("web server never answered: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}

	waitChecked(t, "http://"+addr)
	if w := getWorld(t, "http://"+addr); w.Version != "v9.8.7" {
		t.Errorf("the world's description names version %q, want the citadel's, v9.8.7", w.Version)
	}
	c, _, err := websocket.Dial(ctx, "ws://"+addr+"/ws", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = c.CloseNow() }()

	read := func() web.Message {
		t.Helper()
		_, data, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		var m web.Message
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("decode %q: %v", data, err)
		}
		return m
	}

	first := read()
	if first.Type != "snapshot" || first.Server == nil || len(first.Islands) != 0 {
		t.Fatalf("first message = %+v, want an empty snapshot", first)
	}

	// The connect callback may land after the snapshot, so read until the
	// citadel reports a connected server.
	info := *first.Server
	for i := 0; !info.Connected && i < 5; i++ {
		m := read()
		if m.Server != nil {
			info = *m.Server
		}
	}
	if !info.Connected || info.Name != "e2e" || info.Domain != "e2eworld" || !strings.HasPrefix(info.URL, "nats://") || info.Version == "" {
		t.Errorf("server info = %+v, want connected, name e2e, domain e2eworld, nats URL, and a version", info)
	}

	// A daemon joins through the leaf listener, and its island appears.
	daemonCtx, stopDaemon := context.WithCancel(ctx)
	daemonDone := make(chan error, 1)
	go func() {
		fakeCmd, fakeEnv := fakeagent.TestCommand()
		daemonDone <- daemon.Run(daemonCtx, daemon.Config{Host: "workstation", NATSURL: srv.LeafURL(), DataDir: t.TempDir(), TrustFirstWorld: true, Version: "v0.1.0",
			Adapters: agents.Registry{"fake": {Name: "fake", Provider: "local", Command: fakeCmd, Env: fakeEnv}}})
	}()
	// The island arrives in a snapshot once the citadel has read the host's
	// events, or on its own after that, so it is tracked as a browser does.
	var island *projections.Island
	waitIsland := func(what string, ok func(*projections.Island) bool) {
		t.Helper()
		for range 50 {
			m := read()
			switch m.Type {
			case "snapshot":
				island = nil
				for _, isl := range m.Islands {
					if isl.Host == "workstation" {
						island = &isl
					}
				}
			case "island":
				if m.Island != nil && m.Island.Host == "workstation" {
					island = m.Island
				}
			}
			if island != nil && ok(island) {
				return
			}
		}
		t.Fatalf("never saw workstation %s", what)
	}
	waitIsland("reachable", func(isl *projections.Island) bool { return isl.Reachable })
	if island.Daemon.Version != "v0.1.0" || island.Daemon.OS == "" {
		t.Errorf("island daemon facts = %+v", island.Daemon)
	}

	// The daemon checks which agents it can launch, and the island carries
	// what it recorded.
	waitIsland("list its agents", func(isl *projections.Island) bool { return len(isl.Agents) > 0 })
	if a := island.Agents[0]; len(island.Agents) != 1 || a.Name != "fake" || !a.Available {
		t.Errorf("island agents = %+v, want the fake agent available", island.Agents)
	}

	// Stopping the daemon makes the island unreachable after a few misses.
	stopDaemon()
	if err := <-daemonDone; err != nil {
		t.Errorf("daemon.Run returned %v", err)
	}
	waitIsland("unreachable", func(isl *projections.Island) bool { return !isl.Reachable })

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

// getWorld reads the world's description from the citadel at base.
func getWorld(t *testing.T, base string) web.WorldInfo {
	t.Helper()
	var w web.WorldInfo
	resp, err := http.Get(base + "/api/world")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := json.NewDecoder(resp.Body).Decode(&w); err != nil {
		t.Fatal(err)
	}
	return w
}

// waitChecked waits until the citadel at base knows whether the world is
// set up. Until then it lets no browser in, since it can't tell whether one
// has to sign in.
func waitChecked(t *testing.T, base string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		var w web.WorldInfo
		if resp, err := http.Get(base + "/api/world"); err == nil {
			_ = json.NewDecoder(resp.Body).Decode(&w)
			_ = resp.Body.Close()
		}
		if w.Checked {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the citadel never looked for the world")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestMain(m *testing.M) {
	fakeagent.TestMainHook()
	os.Exit(m.Run())
}
