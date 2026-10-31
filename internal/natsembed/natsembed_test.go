package natsembed

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
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

func TestStartServesClients(t *testing.T) {
	s, err := Start(context.Background(), Config{
		Name:       "test",
		ClientAddr: "127.0.0.1:0",
		StoreDir:   t.TempDir(),
		Domain:     "testworld",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Shutdown()

	if s.LeafURL() != "" {
		t.Errorf("LeafURL = %q, want empty with no leaf listener", s.LeafURL())
	}
	nc, err := nats.Connect(s.ClientURL())
	if err != nil {
		t.Fatalf("connect to %s: %v", s.ClientURL(), err)
	}
	defer nc.Close()
	if got := nc.ConnectedServerName(); got != "test" {
		t.Errorf("server name = %q, want %q", got, "test")
	}
	if got := nc.ConnectedDomain(); got != "testworld" {
		t.Errorf("JetStream domain = %q, want %q", got, "testworld")
	}
}

func TestStartRejectsBadAddress(t *testing.T) {
	if _, err := Start(context.Background(), Config{ClientAddr: "nope", StoreDir: t.TempDir()}); err == nil {
		t.Fatal("Start accepted an address without a port")
	}
}

func TestLeafExtendsTheWorld(t *testing.T) {
	ctx := context.Background()
	hub, err := Start(ctx, Config{Name: "hub", ClientAddr: "127.0.0.1:0", LeafAddr: freePort(t), StoreDir: t.TempDir(), Domain: "world"})
	if err != nil {
		t.Fatalf("Start hub: %v", err)
	}
	defer hub.Shutdown()

	leaf, err := StartLeaf(ctx, LeafConfig{Name: "leaf", RemoteURL: hub.LeafURL(), StoreDir: t.TempDir(), Domain: "island"})
	if err != nil {
		t.Fatalf("StartLeaf: %v", err)
	}
	defer leaf.Shutdown()
	if leaf.ClientURL() != "" {
		t.Errorf("leaf ClientURL = %q, want empty since it does not listen", leaf.ClientURL())
	}

	hubClient, err := nats.Connect(hub.ClientURL())
	if err != nil {
		t.Fatalf("connect to hub: %v", err)
	}
	defer hubClient.Close()
	leafClient, err := nats.Connect("", leaf.InProcess())
	if err != nil {
		t.Fatalf("connect in-process to leaf: %v", err)
	}
	defer leafClient.Close()
	if got := leafClient.ConnectedServerName(); got != "leaf" {
		t.Errorf("in-process connection reached %q, want leaf", got)
	}

	// Interest propagates once the leaf link is up, so publish until the
	// hub-side subscriber hears it.
	sub, err := hubClient.SubscribeSync("hello")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if err := leafClient.Publish("hello", []byte("from the island")); err != nil {
			t.Fatal(err)
		}
		if msg, err := sub.NextMsg(200 * time.Millisecond); err == nil {
			if string(msg.Data) != "from the island" {
				t.Fatalf("got %q", msg.Data)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("a message published on the leaf never reached the hub")
		}
	}
}
