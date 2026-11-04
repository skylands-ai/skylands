package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/approval"
	"github.com/skylands-ai/skylands/internal/natsembed"
)

func ownerKey(t *testing.T) (nkeys.KeyPair, string) {
	t.Helper()
	kp, err := nkeys.CreateOperator()
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := kp.PublicKey()
	return kp, pub
}

// TestPinWorld keeps the owner key an island was started with, refuses
// another later, and refuses to start a daemon outside devworld without
// one.
func TestPinWorld(t *testing.T) {
	dir := t.TempDir()
	_, owner := ownerKey(t)
	_, other := ownerKey(t)

	if _, err := pinWorld(dir, "wopr", "", false); !errors.Is(err, ErrNoWorld) {
		t.Errorf("no --world = %v, want ErrNoWorld", err)
	}
	if _, err := pinWorld(dir, "wopr", "not-a-key", false); err == nil {
		t.Error("--world that isn't an owner key was accepted")
	}
	p, err := pinWorld(dir, "wopr", owner, false)
	if err != nil || p.get() != owner {
		t.Fatalf("pin = %v, %v", p, err)
	}
	// The same island, started again: with the same key, with none, and
	// with another.
	if p, err := pinWorld(dir, "wopr", owner, false); err != nil || p.get() != owner {
		t.Errorf("same --world = %v, %v", p, err)
	}
	if p, err := pinWorld(dir, "wopr", "", false); err != nil || p.get() != owner {
		t.Errorf("no --world once pinned = %v, %v", p, err)
	}
	if _, err := pinWorld(dir, "wopr", other, false); err == nil || !strings.Contains(err.Error(), "already belongs") {
		t.Errorf("another --world = %v, want a refusal", err)
	}
	// devworld's daemon may start with nothing pinned.
	if p, err := pinWorld(dir, "other-island", "", true); err != nil || p.get() != "" {
		t.Errorf("devworld = %v, %v", p, err)
	}
}

// TestCheckWorld pins the owner key from the first world record when
// allowed to, accepts the record of the world the island belongs to, and
// refuses another world.
func TestCheckWorld(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	srv, err := natsembed.Start(ctx, natsembed.Config{Name: "hub", ClientAddr: "127.0.0.1:0", StoreDir: t.TempDir(), Domain: "world"})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Shutdown()
	nc, err := nats.Connect(srv.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	old := worldCheckEvery
	worldCheckEvery = 50 * time.Millisecond
	defer func() { worldCheckEvery = old }()

	// A stand-in citadel: no world at first, then this one.
	kp, owner := ownerKey(t)
	record, _ := approval.Sign(kp, v1.WorldRecord{Type: "world", Name: "w", Owner: owner, Created: approval.Now()})
	data, _ := json.Marshal(record)
	ready := make(chan struct{})
	sub, err := nc.Subscribe(v1.WorldRecordSubject, func(m *nats.Msg) {
		select {
		case <-ready:
			_ = m.Respond(data)
		default:
			_ = m.Respond(nil)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sub.Unsubscribe() }()

	dir := t.TempDir()
	first, _ := pinWorld(dir, "a", "", true)
	errc := make(chan error, 1)
	go func() { errc <- checkWorld(ctx, nc, first) }()
	time.Sleep(200 * time.Millisecond)
	close(ready)
	if err := <-errc; err != nil || first.get() != owner {
		t.Fatalf("first world: %v, pinned %q", err, first.get())
	}
	if again, _ := pinWorld(dir, "a", "", false); again.get() != owner {
		t.Errorf("the pinned key wasn't kept: %q", again.get())
	}

	same, _ := pinWorld(t.TempDir(), "b", owner, false)
	if err := checkWorld(ctx, nc, same); err != nil {
		t.Errorf("the island's own world: %v", err)
	}
	_, other := ownerKey(t)
	elsewhere, _ := pinWorld(t.TempDir(), "c", other, false)
	if err := checkWorld(ctx, nc, elsewhere); err == nil || !strings.Contains(err.Error(), "won't run in another world") {
		t.Errorf("another world: %v, want a refusal", err)
	}
}
