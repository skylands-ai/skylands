package projections

import (
	"context"
	"sync"
	"testing"
	"time"
)

func reply(host, id string) Reply {
	return Reply{ID: id, Version: "0.0.0", Metadata: map[string]string{"host": host, "os": "linux", "arch": "amd64", "started": "2026-10-31T12:00:00Z", "skylands_version": "v0.1.0"}}
}

func TestObserveTracksReachability(t *testing.T) {
	var changes []Island
	p := NewIslands(func(i Island) { changes = append(changes, i) })
	t0 := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)

	p.Observe([]Reply{reply("ws", "d1")}, t0)
	if len(changes) != 1 || !changes[0].Reachable || changes[0].Host != "ws" || changes[0].Daemon.Version != "v0.1.0" {
		t.Fatalf("after first reply, changes = %+v", changes)
	}
	if changes[0].Daemon.Started.IsZero() || changes[0].Daemon.OS != "linux" {
		t.Errorf("daemon facts not parsed: %+v", changes[0].Daemon)
	}

	// A steady host changes nothing but its last-seen time.
	p.Observe([]Reply{reply("ws", "d1")}, t0.Add(2*time.Second))
	if len(changes) != 1 {
		t.Fatalf("a repeat reply produced a change: %+v", changes)
	}
	if isl, _ := p.Get("ws"); !isl.LastSeen.Equal(t0.Add(2 * time.Second)) {
		t.Errorf("LastSeen = %v, want %v", isl.LastSeen, t0.Add(2*time.Second))
	}

	// Misses accrue, and the island goes grey only at the threshold.
	for i := 1; i < MissesBeforeUnreachable; i++ {
		p.Observe(nil, t0.Add(time.Duration(2+2*i)*time.Second))
		if len(changes) != 1 {
			t.Fatalf("miss %d marked the host unreachable early", i)
		}
	}
	p.Observe(nil, t0.Add(20*time.Second))
	if len(changes) != 2 || changes[1].Reachable {
		t.Fatalf("after %d misses, changes = %+v", MissesBeforeUnreachable, changes)
	}
	if isl, _ := p.Get("ws"); !isl.LastSeen.Equal(t0.Add(2 * time.Second)) {
		t.Errorf("LastSeen moved while unreachable: %v", isl.LastSeen)
	}

	// Further misses are quiet, and one reply brings it back.
	p.Observe(nil, t0.Add(22*time.Second))
	p.Observe([]Reply{reply("ws", "d1")}, t0.Add(24*time.Second))
	if len(changes) != 3 || !changes[2].Reachable {
		t.Fatalf("after return, changes = %+v", changes)
	}

	// A restarted daemon has a new service id, which is a change.
	p.Observe([]Reply{reply("ws", "d2")}, t0.Add(26*time.Second))
	if len(changes) != 4 || changes[3].Daemon.ID != "d2" {
		t.Fatalf("restart not reported: %+v", changes)
	}

	// Replies without a host are ignored, and a second host is independent.
	p.Observe([]Reply{{ID: "x"}, reply("nuc", "d9"), reply("ws", "d2")}, t0.Add(28*time.Second))
	list := p.List()
	if len(list) != 2 || list[0].Host != "nuc" || list[1].Host != "ws" {
		t.Fatalf("List = %+v", list)
	}
}

type fakePinger struct {
	mu      sync.Mutex
	replies []Reply
}

func (f *fakePinger) Ping(context.Context) ([]Reply, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.replies, nil
}

func (f *fakePinger) set(r []Reply) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replies = r
}

func TestPollDrivesTheProjection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	events := make(chan Island, 16)
	p := NewIslands(func(i Island) { events <- i })
	pinger := &fakePinger{replies: []Reply{reply("ws", "d1")}}
	go Poll(ctx, pinger, p, 20*time.Millisecond)

	next := func(want bool) {
		t.Helper()
		select {
		case isl := <-events:
			if isl.Reachable != want {
				t.Fatalf("got reachable=%v, want %v", isl.Reachable, want)
			}
		case <-ctx.Done():
			t.Fatal("timed out waiting for a change")
		}
	}
	next(true)
	pinger.set(nil)
	next(false)
	pinger.set([]Reply{reply("ws", "d1")})
	next(true)
}
