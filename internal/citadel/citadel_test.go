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
		done <- Run(runCtx, Config{NATSURL: srv.ClientURL(), HTTPAddr: addr, PollInterval: 200 * time.Millisecond, PollWindow: 100 * time.Millisecond})
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
		daemonDone <- daemon.Run(daemonCtx, daemon.Config{Host: "workstation", NATSURL: srv.LeafURL(), DataDir: t.TempDir(), Version: "v0.1.0",
			Adapters: agents.Registry{"fake": {Name: "fake", Provider: "local", Command: fakeCmd, Env: fakeEnv}}})
	}()
	waitIsland := func(reachable bool) {
		t.Helper()
		for i := 0; i < 50; i++ {
			m := read()
			if m.Type == "island" && m.Island != nil && m.Island.Host == "workstation" && m.Island.Reachable == reachable {
				if reachable && (m.Island.Daemon.Version != "v0.1.0" || m.Island.Daemon.OS == "") {
					t.Errorf("island daemon facts = %+v", m.Island.Daemon)
				}
				return
			}
		}
		t.Fatalf("never saw workstation with reachable=%v", reachable)
	}
	waitIsland(true)

	// The daemon checks which agents it can launch, and the island carries
	// what it recorded.
	for i := 0; ; i++ {
		if i == 50 {
			t.Fatal("the island never listed its agents")
		}
		m := read()
		if m.Type == "island" && m.Island != nil && m.Island.Host == "workstation" && len(m.Island.Agents) > 0 {
			if a := m.Island.Agents[0]; len(m.Island.Agents) != 1 || a.Name != "fake" || !a.Available {
				t.Errorf("island agents = %+v, want the fake agent available", m.Island.Agents)
			}
			break
		}
	}

	// Stopping the daemon makes the island unreachable after a few misses.
	stopDaemon()
	if err := <-daemonDone; err != nil {
		t.Errorf("daemon.Run returned %v", err)
	}
	waitIsland(false)

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

func TestMain(m *testing.M) {
	fakeagent.TestMainHook()
	os.Exit(m.Run())
}
