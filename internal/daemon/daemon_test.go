package daemon

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/micro"

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
