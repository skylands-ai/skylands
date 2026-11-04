package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/approval"
	"github.com/skylands-ai/skylands/internal/daemon/socket"
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

// TestDaemonReadsTheApprovers reads the world's approver registry from the
// world's JetStream domain, through the island's leaf node, and counts the
// approvers in its status: none it can't trace to the pinned owner key.
func TestDaemonReadsTheApprovers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	hub, err := natsembed.Start(ctx, natsembed.Config{Name: "hub", ClientAddr: "127.0.0.1:0", LeafAddr: freePort(t), StoreDir: t.TempDir(), Domain: "world"})
	if err != nil {
		t.Fatal(err)
	}
	defer hub.Shutdown()
	nc, err := nats.Connect(hub.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := js.CreateOrUpdateStream(ctx, approval.StreamConfig()); err != nil {
		t.Fatal(err)
	}
	kp, owner := ownerKey(t)
	approver, _ := nkeys.CreateUser()
	approverPub, _ := approver.PublicKey()
	entry, _ := approval.Sign(kp, v1.ApproverEntry{Type: "approver", World: owner, Key: approverPub, Name: "laptop", Created: approval.Now()})
	if _, err := approval.Append(ctx, js, v1.ApproverSubject("approver", approverPub), entry); err != nil {
		t.Fatal(err)
	}
	// An entry another owner key signed doesn't count here.
	stranger, _ := ownerKey(t)
	other, _ := nkeys.CreateUser()
	otherPub, _ := other.PublicKey()
	stray, _ := approval.Sign(stranger, v1.ApproverEntry{Type: "approver", World: owner, Key: otherPub, Name: "stray", Created: approval.Now()})
	if _, err := approval.Append(ctx, js, v1.ApproverSubject("approver", otherPub), stray); err != nil {
		t.Fatal(err)
	}

	if err := Run(ctx, Config{Host: "world", NATSURL: hub.LeafURL(), DataDir: t.TempDir(), World: owner}); err == nil || !strings.Contains(err.Error(), "world's JetStream domain") {
		t.Errorf("an island named after the world's domain = %v, want a refusal", err)
	}

	// A short directory: a socket's path has a length limit.
	sockDir, err := os.MkdirTemp("", "sk")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(sockDir) }()
	sock := filepath.Join(sockDir, "d.sock")
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- Run(runCtx, Config{Host: "laptop", NATSURL: hub.LeafURL(), DataDir: t.TempDir(), SocketPath: sock, World: owner, WorldDomain: "world"})
	}()
	defer func() {
		stop()
		if err := <-done; err != nil {
			t.Errorf("daemon.Run returned %v", err)
		}
	}()
	deadline := time.Now().Add(15 * time.Second)
	for {
		st, err := socket.Dial(sock).Status(ctx)
		if err == nil && st.Approvers != nil {
			if *st.Approvers != 1 {
				t.Fatalf("the daemon counts %d approvers, want 1", *st.Approvers)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the daemon never read the approvers: %+v, %v", st, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
