package citadel

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/micro"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/citadel/projections"
	"github.com/skylands-ai/skylands/internal/citadel/web"
	"github.com/skylands-ai/skylands/internal/daemon"
	"github.com/skylands-ai/skylands/internal/es"
	"github.com/skylands-ai/skylands/internal/natsembed"
)

func TestCatchUpHoldsHostsUntilRead(t *testing.T) {
	var reports []*web.Loading
	c := newCatchUp(func(l *web.Loading) { reports = append(reports, l) })
	want := func(step string, nats bool, hosts ...string) {
		t.Helper()
		var l *web.Loading
		if nats || len(hosts) > 0 {
			l = &web.Loading{NATS: nats, Hosts: hosts}
		}
		if got := c.state(); !sameLoading(got, l) {
			t.Fatalf("%s: loading = %+v, want %+v", step, got, l)
		}
		// What it starts with goes out in snapshots, and each change after.
		if len(reports) > 0 && !sameLoading(reports[len(reports)-1], l) {
			t.Fatalf("%s: last report = %+v, want %+v", step, reports[len(reports)-1], l)
		}
	}

	// Until the citadel has listed the hosts it followed before, it holds
	// back every host, since any of them may be one it waits for.
	want("before NATS", true)
	if !c.holds("anywhere") {
		t.Error("before the list, a host isn't held back")
	}
	c.start("a")
	want("reading a before the list", true, "a")
	c.listHosts([]string{"a", "b"})
	want("listed", false, "a", "b")
	c.listHosts([]string{"z"})
	want("listed again", false, "a", "b")

	c.done("a", true)
	want("a read", false, "b")
	if c.holds("a") || !c.holds("b") || c.holds("c") {
		t.Errorf("holds a, b, c = %v, %v, %v; want only b, which hasn't answered yet", c.holds("a"), c.holds("b"), c.holds("c"))
	}
	n := len(reports)
	c.stopWaiting()
	if c.state() != nil || len(reports) != n+1 || reports[n] != nil {
		t.Fatalf("after giving up on b: loading = %+v, reports %+v, want nothing being read, reported once", c.state(), reports[n:])
	}
	if c.holds("b") {
		t.Error("a host given up on is still held back")
	}

	// A host followed later is held back while it's read, and so is one
	// that stopped short, until it's followed again.
	c.start("c")
	want("reading c", false, "c")
	c.done("c", false)
	if c.state() != nil || reports[len(reports)-1] != nil {
		t.Fatalf("after reading c stopped: loading = %+v, want nil", c.state())
	}

	// A host already read by the time the list comes isn't waited for.
	d := newCatchUp(nil)
	d.start("a")
	d.done("a", true)
	d.listHosts([]string{"a"})
	if l := d.state(); l != nil {
		t.Errorf("loading = %+v, want nothing: a was read before the list", l)
	}
}

// A restarted citadel reads every event a host's stream holds again. A
// browser connected to it sees none of the states the projections pass
// through on the way: only the world once it has caught up, and what
// happens after.
func TestRestartedCitadelSendsTheWorldOnceCaughtUp(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	srv, err := natsembed.Start(ctx, natsembed.Config{Name: "catchup", ClientAddr: "127.0.0.1:0", LeafAddr: freeAddr(t), StoreDir: t.TempDir(), Domain: "e2eworld"})
	if err != nil {
		t.Fatalf("natsembed: %v", err)
	}
	defer srv.Shutdown()
	// A host, as a daemon makes one: a leaf with its own JetStream domain
	// and stream, and the service the citadel pings.
	leaf, err := natsembed.StartLeaf(ctx, natsembed.LeafConfig{Name: "leaf-ws", RemoteURL: srv.LeafURL(), StoreDir: t.TempDir(), Domain: "ws"})
	if err != nil {
		t.Fatalf("leaf: %v", err)
	}
	defer leaf.Shutdown()
	nc, err := nats.Connect("", leaf.InProcess())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	store, err := es.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureStream(ctx, v1.IslandStreamName("ws"), v1.IslandSubjects("ws")); err != nil {
		t.Fatal(err)
	}
	record := func(eventType string, data any) {
		t.Helper()
		ev, err := v1.NewEvent(eventType, "cmd", data)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.Append(ctx, v1.SessionEventSubject("ws", "s1", eventType), ev); err != nil {
			t.Fatal(err)
		}
	}
	answer := func() micro.Service {
		t.Helper()
		svc, err := micro.AddService(nc, micro.Config{Name: daemon.ServiceName, Version: "0.1.0", Metadata: map[string]string{"host": "ws"}})
		if err != nil {
			t.Fatal(err)
		}
		return svc
	}

	// A session that has taken turns, each one running, then idle.
	const turns = 5
	record(v1.EventSessionStarted, v1.SessionStarted{SessionID: "s1", RepoID: "r1", Agent: "fake"})
	for i := range turns {
		record(v1.EventPromptSent, v1.PromptSent{SessionID: "s1", Text: fmt.Sprintf("turn %d", i+1)})
		record(v1.EventTurnEnded, v1.TurnEnded{SessionID: "s1", StopReason: "end_turn"})
	}

	// The first citadel follows the host, which mirrors its stream into
	// the world's domain.
	svc := answer()
	addr, stop := runCitadel(ctx, t, srv, 200*time.Millisecond)
	read, hangUp := dialCitadel(ctx, t, addr)
	for s := (projections.Session{}); s.Turns != turns; {
		m := read()
		for _, x := range append(m.Sessions, ptrs(m.Session)...) {
			if x.ID == "s1" {
				s = x
			}
		}
	}
	hangUp()
	stop()
	_ = svc.Stop()

	// The second citadel starts with the host's mirror in place. The
	// browser connects before the host answers, so the citadel is still
	// waiting for it.
	addr, stop = runCitadel(ctx, t, srv, 500*time.Millisecond)
	defer stop()
	read, hangUp = dialCitadel(ctx, t, addr)
	defer hangUp()
	first := read()
	if first.Type != "snapshot" || first.Loading == nil || len(first.Sessions) != 0 || len(first.Islands) != 0 {
		t.Fatalf("first message = %+v, want a snapshot that says the citadel is loading, without the host", first)
	}
	if !first.Loading.NATS && !slices.Contains(first.Loading.Hosts, "ws") {
		t.Errorf("loading = %+v, want ws, which the citadel followed before, or NATS", first.Loading)
	}
	svc = answer()
	defer func() { _ = svc.Stop() }()
	var caught web.Message
	for caught.Type == "" {
		m := read()
		switch {
		case m.Type == "session" || m.Type == "island" || m.Type == "repo":
			t.Fatalf("got %s %+v while the citadel was reading the host's events", m.Type, m)
		case m.Type == "snapshot" && m.Loading != nil && len(m.Sessions)+len(m.Islands) > 0:
			t.Fatalf("a snapshot while loading has the host: %+v", m)
		case m.Type == "loading" && (m.Loading == nil || !slices.Contains(m.Loading.Hosts, "ws")):
			t.Fatalf("loading = %+v, want ws until it has been read", m.Loading)
		case m.Type == "snapshot" && m.Loading == nil:
			caught = m
		}
	}
	if len(caught.Sessions) != 1 || caught.Sessions[0].Turns != turns || caught.Sessions[0].Status != "idle" {
		t.Fatalf("caught-up sessions = %+v, want s1 idle after %d turns", caught.Sessions, turns)
	}
	if len(caught.Islands) != 1 || !caught.Islands[0].Reachable {
		t.Errorf("caught-up islands = %+v, want ws, reachable", caught.Islands)
	}

	// What happens after that comes as it happens.
	record(v1.EventPromptSent, v1.PromptSent{SessionID: "s1", Text: "one more"})
	for {
		m := read()
		if m.Type == "session" && m.Session != nil && m.Session.ID == "s1" {
			if m.Session.Turns != turns+1 || m.Session.Status != "running" {
				t.Fatalf("live session = turns %d, %s; want %d, running", m.Session.Turns, m.Session.Status, turns+1)
			}
			break
		}
	}
}

func ptrs[T any](p *T) []T {
	if p == nil {
		return nil
	}
	return []T{*p}
}

// runCitadel runs a citadel of the world srv serves until stop, once it is
// up and has looked for the world.
func runCitadel(ctx context.Context, t *testing.T, srv *natsembed.Server, poll time.Duration) (addr string, stop func()) {
	t.Helper()
	addr = freeAddr(t)
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- Run(runCtx, Config{NATSURL: srv.ClientURL(), HTTPAddr: addr, PollInterval: poll, PollWindow: 100 * time.Millisecond})
	}()
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
		time.Sleep(20 * time.Millisecond)
	}
	waitChecked(t, "http://"+addr)
	var once bool
	return addr, func() {
		if once {
			return
		}
		once = true
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Run returned %v after cancel, want nil", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("the citadel didn't stop")
		}
	}
}

// dialCitadel opens a browser's WebSocket to the citadel at addr.
func dialCitadel(ctx context.Context, t *testing.T, addr string) (read func() web.Message, hangUp func()) {
	t.Helper()
	c, _, err := websocket.Dial(ctx, "ws://"+addr+"/ws", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return func() web.Message {
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
	}, func() { _ = c.CloseNow() }
}
