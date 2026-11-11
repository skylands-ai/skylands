package daemon

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestWhenIdleWaitsForTheTurnsToEnd(t *testing.T) {
	stop := make(chan struct{})
	close(stop)
	var checks atomic.Int32
	// An agent works on a turn for the first three checks.
	busy := func() bool { return checks.Add(1) <= 3 }
	if !whenIdle(context.Background(), stop, busy, time.Millisecond) {
		t.Fatal("whenIdle = false, want true once no turn is being worked on")
	}
	if got := checks.Load(); got != 4 {
		t.Errorf("checked %d times, want 4: three busy and one idle", got)
	}
}

func TestWhenIdleWaitsForStop(t *testing.T) {
	stop := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	checked := false
	if whenIdle(ctx, stop, func() bool { checked = true; return false }, time.Millisecond) {
		t.Error("whenIdle = true before stop closed")
	}
	if checked {
		t.Error("checked for turns before stop closed")
	}
}

func TestWhenIdleGivesUpWhenCtxEnds(t *testing.T) {
	stop := make(chan struct{})
	close(stop)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if whenIdle(ctx, stop, func() bool { return true }, time.Millisecond) {
		t.Error("whenIdle = true while a turn never ended")
	}
}
