package webpush_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/skylands-ai/skylands/internal/webpush"
	"github.com/skylands-ai/skylands/internal/webpush/webpushtest"
)

func TestSendReachesThePushServiceEncrypted(t *testing.T) {
	svc := webpushtest.New(t)
	keys, err := webpush.NewKeys()
	if err != nil {
		t.Fatal(err)
	}
	// The key survives being written down and read back.
	again, err := webpush.ParseKeys(keys.Private())
	if err != nil || again.Public() != keys.Public() {
		t.Fatalf("ParseKeys(Private()) = %v, %v; want the same key", again, err)
	}
	sender := &webpush.Sender{Keys: again, Subject: "https://citadel.example", Client: svc.Client()}
	sub := svc.Subscribe(keys.Public())
	ctx := context.Background()

	if err := sender.Send(ctx, sub, webpush.Message{Payload: []byte(`{"title":"hello"}`), Topic: "abc", TTL: 90 * time.Second, Urgency: "high"}); err != nil {
		t.Fatalf("Send = %v; refused: %v", err, svc.Refused())
	}
	got := svc.Received()
	if len(got) != 1 {
		t.Fatalf("received %d pushes, want 1", len(got))
	}
	if p := got[0]; string(p.Payload) != `{"title":"hello"}` || p.Topic != "abc" || p.TTL != 90*time.Second || p.Urgency != "high" || p.Subject != "https://citadel.example" {
		t.Errorf("received %+v", p)
	}

	// A push signed with another key is refused, as a real push service
	// refuses it.
	other, _ := webpush.NewKeys()
	wrong := &webpush.Sender{Keys: other, Subject: "https://citadel.example", Client: svc.Client()}
	if err := wrong.Send(ctx, sub, webpush.Message{Payload: []byte("x")}); err == nil || errors.Is(err, webpush.ErrGone) {
		t.Errorf("Send with another VAPID key = %v, want a refusal", err)
	}

	// Once the browser unsubscribes, the push service says it's gone.
	svc.Gone(sub)
	if err := sender.Send(ctx, sub, webpush.Message{Payload: []byte("x")}); !errors.Is(err, webpush.ErrGone) {
		t.Errorf("Send to a gone subscription = %v, want ErrGone", err)
	}
}

func TestSendRefusesWhatItCantSend(t *testing.T) {
	svc := webpushtest.New(t)
	keys, _ := webpush.NewKeys()
	sender := &webpush.Sender{Keys: keys, Subject: "https://citadel.example", Client: svc.Client()}
	sub := svc.Subscribe(keys.Public())
	ctx := context.Background()

	if err := sender.Send(ctx, sub, webpush.Message{Payload: []byte(strings.Repeat("x", webpush.MaxPayload))}); err != nil {
		t.Errorf("Send of MaxPayload bytes = %v; refused: %v", err, svc.Refused())
	}
	if err := sender.Send(ctx, sub, webpush.Message{Payload: []byte(strings.Repeat("x", webpush.MaxPayload+1))}); err == nil {
		t.Error("Send of more than MaxPayload bytes succeeded")
	}
	plain := sub
	plain.Endpoint = strings.Replace(sub.Endpoint, "https://", "http://", 1)
	if err := plain.Check(); err == nil {
		t.Error("Check took an http endpoint")
	}
	short := sub
	short.Keys.Auth = "AAAA"
	if err := short.Check(); err == nil {
		t.Error("Check took a 3-byte auth secret")
	}
	bad := sub
	bad.Keys.P256dh = "BAAA"
	if err := bad.Check(); err == nil {
		t.Error("Check took a p256dh key that isn't a point")
	}
}
