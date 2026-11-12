package es

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/natsembed"
)

func TestAppendReplayFollow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	srv, err := natsembed.Start(ctx, natsembed.Config{Name: "es", ClientAddr: "127.0.0.1:0", StoreDir: t.TempDir(), Domain: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Shutdown()
	nc, err := nats.Connect(srv.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	s, err := New(nc)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.EnsureStream(ctx, v1.IslandStreamName("ws"), v1.IslandSubjects("ws")); err != nil {
		t.Fatal(err)
	}
	// Repeating it is harmless.
	if err := s.EnsureStream(ctx, v1.IslandStreamName("ws"), v1.IslandSubjects("ws")); err != nil {
		t.Fatal(err)
	}

	// Replaying an empty stream delivers nothing and returns.
	n := 0
	if err := s.Replay(ctx, "island-ws", []string{v1.IslandEventFilter("ws")}, func(uint64, v1.Event) error { n++; return nil }); err != nil || n != 0 {
		t.Fatalf("empty replay: n=%d err=%v", n, err)
	}

	ev1, _ := v1.NewEvent(v1.EventRepoRegistered, "cmd-1", v1.RepoRegistered{RepoID: "r1", Name: "a", Path: "/a"})
	seq1, err := s.Append(ctx, v1.IslandEventSubject("ws", v1.EventRepoRegistered), ev1)
	if err != nil || seq1 != 1 {
		t.Fatalf("append 1: seq=%d err=%v", seq1, err)
	}
	// A session event is in the stream but outside the host-event filter.
	ev2, _ := v1.NewEvent("SessionStarted", "cmd-2", map[string]string{"session": "s1"})
	if _, err := s.Append(ctx, v1.SessionEventSubject("ws", "s1", "SessionStarted"), ev2); err != nil {
		t.Fatal(err)
	}
	ev3, _ := v1.NewEvent(v1.EventTerrainSnapshotted, "cmd-1", v1.TerrainSnapshotted{RepoID: "r1"})
	seq3, err := s.Append(ctx, v1.IslandEventSubject("ws", v1.EventTerrainSnapshotted), ev3)
	if err != nil || seq3 != 3 {
		t.Fatalf("append 3: seq=%d err=%v", seq3, err)
	}

	var got []string
	err = s.Replay(ctx, "island-ws", []string{v1.IslandEventFilter("ws")}, func(seq uint64, ev v1.Event) error {
		got = append(got, ev.Type)
		return nil
	})
	if err != nil || len(got) != 2 || got[0] != v1.EventRepoRegistered || got[1] != v1.EventTerrainSnapshotted {
		t.Fatalf("replay = %v, err %v", got, err)
	}

	// Follow replays, says it has caught up, then delivers new events as
	// they arrive. The marker goes down the same channel, so its place
	// among the events is checked.
	const caughtUp = "caught up"
	events := make(chan string, 8)
	var last v1.Event
	fctx, stop := context.WithCancel(ctx)
	defer stop()
	go func() {
		_ = s.Follow(fctx, "island-ws", []string{v1.IslandEventFilter("ws")}, func(seq uint64, ev v1.Event) { last = ev; events <- ev.Type }, func() { events <- caughtUp })
	}()
	next := func(what string) string {
		t.Helper()
		select {
		case got := <-events:
			return got
		case <-time.After(5 * time.Second):
			t.Fatalf("%s stalled", what)
			return ""
		}
	}
	for _, want := range []string{v1.EventRepoRegistered, v1.EventTerrainSnapshotted, caughtUp} {
		if got := next("follow replay"); got != want {
			t.Fatalf("follow replay got %s, want %s", got, want)
		}
	}
	ev4, _ := v1.NewEvent(v1.EventRepoRegistered, "cmd-3", v1.RepoRegistered{RepoID: "r2"})
	if _, err := s.Append(ctx, v1.IslandEventSubject("ws", v1.EventRepoRegistered), ev4); err != nil {
		t.Fatal(err)
	}
	if got := next("live event"); got != v1.EventRepoRegistered {
		t.Fatalf("live event = %s, want %s", got, v1.EventRepoRegistered)
	}
	var rr v1.RepoRegistered
	if err := last.Decode(&rr); err != nil || rr.RepoID != "r2" {
		t.Fatalf("live event = %+v, decode err %v", last, err)
	}
	// It catches up once: a new event's turn to be the last one stored
	// doesn't say so again.
	select {
	case got := <-events:
		t.Fatalf("after the live event, got %s, want nothing", got)
	case <-time.After(200 * time.Millisecond):
	}

	// Following events of which none are stored catches up before any arrives.
	quiet := make(chan struct{})
	go func() {
		_ = s.Follow(fctx, "island-ws", []string{v1.SessionEventSubject("ws", "nobody", "Nothing")}, func(uint64, v1.Event) { t.Error("an event matched a filter nothing matches") }, func() { close(quiet) })
	}()
	select {
	case <-quiet:
	case <-time.After(5 * time.Second):
		t.Fatal("following nothing stored never caught up")
	}
}
