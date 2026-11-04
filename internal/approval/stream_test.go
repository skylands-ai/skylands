package approval

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/natsembed"
)

// TestStreamKeepsEveryRecord stores registry records once each, and keeps
// them: a second record on a subject, a deleted message, and a purge are
// all refused, and Follow reads back what was stored with the server's
// sequence and time.
func TestStreamKeepsEveryRecord(t *testing.T) {
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
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := js.CreateOrUpdateStream(ctx, StreamConfig())
	if err != nil {
		t.Fatal(err)
	}

	w := newWorld(t)
	laptop, laptopPub := w.user()
	entry, _ := Sign(w.owner, v1.ApproverEntry{Type: "approver", World: w.ownerPub, Key: laptopPub, Name: "laptop", Created: Now()})
	revocation, _ := Sign(w.owner, v1.Revocation{Type: "revocation", World: w.ownerPub, Key: laptopPub, Created: Now()})
	before := time.Now().Add(-time.Second)
	first, err := Append(ctx, js, v1.ApproverSubject("approver", laptopPub), entry)
	if err != nil || first.Revision != 1 || first.Stored.Before(before) {
		t.Fatalf("Append = %+v, %v", first, err)
	}
	if _, err := Append(ctx, js, v1.ApproverSubject("revoked", laptopPub), revocation); err != nil {
		t.Fatal(err)
	}
	// Nothing written can be replaced or taken back.
	again, _ := Sign(laptop, v1.Revocation{Type: "revocation", World: w.ownerPub, Key: laptopPub, Created: Now()})
	if _, err := Append(ctx, js, v1.ApproverSubject("revoked", laptopPub), again); !errors.Is(err, ErrTaken) {
		t.Errorf("a second record on a subject = %v, want ErrTaken", err)
	}
	if _, err := js.Publish(ctx, v1.ApproverSubject("revoked", laptopPub), []byte("{}")); err == nil {
		t.Error("a plain publish replaced a stored revocation")
	}
	if err := stream.DeleteMsg(ctx, 2); err == nil {
		t.Error("the revocation was deleted")
	}
	if err := stream.Purge(ctx); err == nil {
		t.Error("the registry was purged")
	}

	got := make(chan []Record, 4)
	go Follow(ctx, js, func(r []Record) { got <- r })
	select {
	case records := <-got:
		if len(records) != 2 || records[0].Revision != 1 || records[1].Revision != 2 || !records[0].Stored.Equal(first.Stored) {
			t.Fatalf("Follow read %+v", records)
		}
		if r := NewRegistry(w.ownerPub, records); r.Valid(laptopPub) {
			t.Error("the stored revocation didn't count")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Follow never caught up")
	}
}
