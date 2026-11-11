package citadel

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nats.go/micro"
	"github.com/nats-io/nkeys"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/approval"
	"github.com/skylands-ai/skylands/internal/citadel/projections"
	"github.com/skylands-ai/skylands/internal/daemon"
	"github.com/skylands-ai/skylands/internal/natsembed"
	"github.com/skylands-ai/skylands/internal/webpush"
	"github.com/skylands-ai/skylands/internal/webpush/webpushtest"
)

// fakeIsland is a host as the citadel sees one: a leaf node with the
// island's stream, and the daemon's micro service, so the citadel finds
// and follows it. The test writes the island's events itself, in place of
// a daemon.
type fakeIsland struct {
	t    *testing.T
	host string
	js   jetstream.JetStream
}

func startFakeIsland(t *testing.T, ctx context.Context, hub *natsembed.Server, host string) *fakeIsland {
	t.Helper()
	leaf, err := natsembed.StartLeaf(ctx, natsembed.LeafConfig{Name: host, RemoteURL: hub.LeafURL(), StoreDir: t.TempDir(), Domain: host})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(leaf.Shutdown)
	nc, err := nats.Connect("", leaf.InProcess())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	js, _ := jetstream.New(nc)
	if _, err := js.CreateStream(ctx, jetstream.StreamConfig{Name: v1.IslandStreamName(host), Subjects: v1.IslandSubjects(host), Storage: jetstream.FileStorage}); err != nil {
		t.Fatal(err)
	}
	svc, err := micro.AddService(nc, micro.Config{Name: daemon.ServiceName, Version: "0.1.0", Metadata: map[string]string{"host": host, "os": runtime.GOOS, "arch": runtime.GOARCH}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Stop() })
	return &fakeIsland{t: t, host: host, js: js}
}

// record appends one event to the island's stream, at a time of its own
// when at isn't zero. An empty session makes it a host event.
func (f *fakeIsland) record(session, eventType string, data any, at time.Time) {
	f.t.Helper()
	ev, err := v1.NewEvent(eventType, "", data)
	if err != nil {
		f.t.Fatal(err)
	}
	if !at.IsZero() {
		ev.Time = at
	}
	subject := v1.IslandEventSubject(f.host, eventType)
	if session != "" {
		subject = v1.SessionEventSubject(f.host, session, eventType)
	}
	payload, _ := json.Marshal(ev)
	if _, err := f.js.Publish(context.Background(), subject, payload); err != nil {
		f.t.Fatal(err)
	}
}

// pushWorld is a set-up world on a citadel that sends pushes to a fake
// push service, with two approvers signed in: a laptop and a phone.
type pushWorld struct {
	t                   *testing.T
	ctx                 context.Context
	hub                 *natsembed.Server
	base                string
	svc                 *webpushtest.Service
	owner               nkeys.KeyPair
	ownerPub            string
	laptop, phone       nkeys.KeyPair
	laptopPub, phonePub string
	asLaptop, asPhone   *http.Client
	kv                  jetstream.KeyValue
	presence            jetstream.KeyValue
	pushKey             string
}

const pushOrigin = "https://citadel.test"

func startPushWorld(t *testing.T) *pushWorld {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	hub, err := natsembed.Start(ctx, natsembed.Config{Name: "hub", ClientAddr: "127.0.0.1:0", LeafAddr: freeAddr(t), StoreDir: t.TempDir(), Domain: "world"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(hub.Shutdown)
	w := &pushWorld{t: t, ctx: ctx, hub: hub, svc: webpushtest.New(t)}

	addr := freeAddr(t)
	links := make(chan string, 4)
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- Run(runCtx, Config{NATSURL: hub.ClientURL(), HTTPAddr: addr, PublicURL: pushOrigin, OnSetupLink: func(l string) { links <- l },
			PollInterval: 200 * time.Millisecond, PollWindow: 100 * time.Millisecond, PushClient: w.svc.Client(), PushHold: time.Second})
	}()
	t.Cleanup(func() {
		stop()
		if err := <-done; err != nil {
			t.Errorf("citadel.Run returned %v", err)
		}
	})
	w.base = "http://" + addr
	var token string
	select {
	case l := <-links:
		token = strings.TrimPrefix(l, pushOrigin+"/setup#")
	case <-time.After(15 * time.Second):
		t.Fatal("the citadel never offered a setup link")
	}

	w.owner, _ = nkeys.CreateOperator()
	w.ownerPub, _ = w.owner.PublicKey()
	w.laptop, w.laptopPub = userKey(t)
	w.phone, w.phonePub = userKey(t)
	world, _ := approval.Sign(w.owner, v1.WorldRecord{Type: "world", Name: "w", Owner: w.ownerPub, Created: approval.Now()})
	first, _ := approval.Sign(w.owner, v1.ApproverEntry{Type: "approver", World: w.ownerPub, Key: w.laptopPub, Name: "laptop", Created: approval.Now()})
	if code, out := postJSON(t, w.base+"/api/world/setup", setupRequest{Token: token, World: world, Approver: first}); code != http.StatusOK {
		t.Fatalf("setup = %d %s", code, out)
	}
	second, _ := approval.Sign(w.owner, v1.ApproverEntry{Type: "approver", World: w.ownerPub, Key: w.phonePub, Name: "phone", Created: approval.Now()})
	if code, out := postJSON(t, w.base+"/api/world/approvers", map[string]any{"approver": second}); code != http.StatusOK {
		t.Fatalf("enroll the phone = %d %s", code, out)
	}
	w.asLaptop, w.asPhone = signIn(t, w.base, w.laptop), signIn(t, w.base, w.phone)

	for deadline := time.Now().Add(15 * time.Second); w.pushKey == ""; time.Sleep(50 * time.Millisecond) {
		w.pushKey = getWorld(t, w.base).PushKey
		if time.Now().After(deadline) {
			t.Fatal("the world never named a push key")
		}
	}
	nc, err := nats.Connect(hub.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	js, _ := jetstream.New(nc)
	if w.kv, err = js.KeyValue(ctx, pushBucket); err != nil {
		t.Fatal(err)
	}
	if w.presence, err = js.KeyValue(ctx, presenceBucket); err != nil {
		t.Fatal(err)
	}
	return w
}

// subscribe makes a subscription at the fake push service and signs it
// with kp, at origin.
func (w *pushWorld) subscribe(kp nkeys.KeyPair, origin string) (webpush.Subscription, v1.Signed) {
	w.t.Helper()
	sub := w.svc.Subscribe(w.pushKey)
	signed, err := approval.Sign(kp, v1.PushSubscription{Type: "push", World: w.ownerPub, Origin: origin, Endpoint: sub.Endpoint, P256dh: sub.Keys.P256dh, Auth: sub.Keys.Auth, Created: approval.Now()})
	if err != nil {
		w.t.Fatal(err)
	}
	return sub, signed
}

// stored says whether the push bucket holds approver's subscription to sub.
func (w *pushWorld) stored(approver string, sub webpush.Subscription) bool {
	w.t.Helper()
	_, err := w.kv.Get(w.ctx, subKey(approver, sub.Endpoint))
	if err != nil && !errors.Is(err, jetstream.ErrKeyNotFound) {
		w.t.Fatal(err)
	}
	return err == nil
}

// waitFor waits for cond, or fails with what.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(15 * time.Second); !cond(); time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal(what)
		}
	}
}

// pushesTo is what the push service accepted for sub.
func pushesTo(svc *webpushtest.Service, sub webpush.Subscription) []notice {
	var out []notice
	for _, p := range svc.Received() {
		if p.Endpoint == sub.Endpoint {
			var n notice
			_ = json.Unmarshal(p.Payload, &n)
			out = append(out, n)
		}
	}
	return out
}

func TestPushNotifiesApprovers(t *testing.T) {
	w := startPushWorld(t)
	t.Logf("push key %s", w.pushKey)

	// Each browser registers its own subscription, signed with its own key,
	// made at the citadel's --url.
	phoneSub, phoneSigned := w.subscribe(w.phone, pushOrigin)
	laptopSub, laptopSigned := w.subscribe(w.laptop, pushOrigin+"/")
	_, elsewhere := w.subscribe(w.laptop, "http://127.0.0.1:8080")
	for name, tc := range map[string]struct {
		client *http.Client
		sub    v1.Signed
		want   int
	}{
		"another approver's":      {w.asLaptop, phoneSigned, http.StatusForbidden},
		"made at another address": {w.asLaptop, elsewhere, http.StatusBadRequest},
		"the phone's":             {w.asPhone, phoneSigned, http.StatusOK},
		"the laptop's":            {w.asLaptop, laptopSigned, http.StatusOK},
	} {
		if code, out := postWith(t, tc.client, w.base+"/api/push/subscriptions", map[string]any{"subscription": tc.sub}, nil); code != tc.want {
			t.Errorf("subscribing %s = %d %s, want %d", name, code, out, tc.want)
		}
	}
	if code, _ := postJSON(t, w.base+"/api/push/subscriptions", map[string]any{"subscription": phoneSigned}); code != http.StatusUnauthorized {
		t.Errorf("subscribing without signing in = %d, want 401", code)
	}
	if !w.stored(w.phonePub, phoneSub) || !w.stored(w.laptopPub, laptopSub) {
		t.Fatal("the subscriptions weren't stored by approver")
	}

	// The laptop has the world's page in sight.
	ws, _, err := dialWith(w.ctx, w.base, w.asLaptop)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ws.CloseNow() }()
	go func() {
		for {
			if _, _, err := ws.Read(w.ctx); err != nil {
				return
			}
		}
	}()
	if err := ws.Write(w.ctx, websocket.MessageText, []byte(`{"type":"visible"}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the laptop's page never counted as in sight", func() bool { return w.presence != nil && listsAny(w.ctx, w.presence, w.laptopPub+".*") })

	isl := startFakeIsland(t, w.ctx, w.hub, "ws")
	isl.record("", v1.EventRepoRegistered, v1.RepoRegistered{RepoID: "r1", Name: "storefront", Path: "/src/storefront"}, time.Time{})
	isl.record("s1", v1.EventSessionStarted, v1.SessionStarted{SessionID: "s1", RepoID: "r1", Agent: "claude"}, time.Time{})
	waitFor(t, "the citadel never made the island's push consumer", func() bool {
		_, err := w.kvJS().Consumer(w.ctx, v1.IslandStreamName("ws"), pushConsumer)
		return err == nil
	})

	// A request goes to the phone at once, and to the laptop, whose page
	// is in sight, only after the hold.
	isl.record("s1", v1.EventPermissionRequested, v1.PermissionRequested{SessionID: "s1", RequestID: "r1", ToolCallID: "t1", Title: "cargo test", Kind: "execute", InputHash: "x", InputPreview: "cargo test -p storefront"}, time.Time{})
	waitFor(t, "the phone never got the request", func() bool { return len(pushesTo(w.svc, phoneSub)) == 1 })
	if got := pushesTo(w.svc, laptopSub); len(got) != 0 {
		t.Errorf("the laptop, in sight, got %+v before the hold ended", got)
	}
	want := notice{Title: "Claude Code in storefront on ws wants to run a command", Body: "Approve or deny it in Skylands.", Tag: "request:r1", URL: pushOrigin + "/inbox/ws/s1/requests/r1"}
	if got := pushesTo(w.svc, phoneSub)[0]; got != want {
		t.Errorf("the phone's push = %+v, want %+v", got, want)
	}
	for _, p := range w.svc.Received() {
		if strings.Contains(string(p.Payload), "cargo test") {
			t.Errorf("a push carries the tool's input: %s", p.Payload)
		}
		if len(p.Topic) != 32 || p.Urgency != "high" || p.TTL <= 29*time.Minute || p.TTL > 30*time.Minute || p.Subject != pushOrigin {
			t.Errorf("push %+v, want a 32-character topic, high urgency, a TTL just under 30 minutes, and the citadel's --url as subject", p)
		}
	}
	waitFor(t, "the laptop never got the request after the hold", func() bool { return len(pushesTo(w.svc, laptopSub)) == 1 })
	if got := pushesTo(w.svc, laptopSub)[0]; got != want {
		t.Errorf("the laptop's push = %+v, want %+v", got, want)
	}

	// A question answered while the laptop's push is held isn't pushed to
	// the laptop once the hold ends.
	isl.record("s1", v1.EventQuestionAsked, v1.QuestionAsked{SessionID: "s1", QuestionID: "q1", Message: "which file?"}, time.Time{})
	waitFor(t, "the phone never got the question", func() bool { return len(pushesTo(w.svc, phoneSub)) == 2 })
	if got := pushesTo(w.svc, phoneSub)[1]; got.Title != "Claude Code in storefront on ws has a question" || got.Tag != "question:q1" || got.URL != pushOrigin+"/inbox/ws/s1/questions/q1" || strings.Contains(got.Title+got.Body, "which file") {
		t.Errorf("the question's push = %+v", got)
	}
	isl.record("s1", v1.EventQuestionAnswered, v1.QuestionAnswered{SessionID: "s1", QuestionID: "q1", Declined: true, By: "local"}, time.Time{})

	// A request older than a request lives isn't pushed.
	isl.record("s1", v1.EventPermissionRequested, v1.PermissionRequested{SessionID: "s1", RequestID: "old", Kind: "edit"}, time.Now().Add(-31*time.Minute))
	time.Sleep(2 * time.Second)
	for _, n := range append(pushesTo(w.svc, phoneSub), pushesTo(w.svc, laptopSub)...) {
		if n.Tag == "request:old" {
			t.Error("a request older than 30 minutes was pushed")
		}
	}
	if got := pushesTo(w.svc, laptopSub); len(got) != 1 {
		t.Errorf("the laptop got %+v, want only the first request: the question was answered during its hold", got)
	}

	// Once the laptop's page is out of sight, its pushes go at once.
	if err := ws.Write(w.ctx, websocket.MessageText, []byte(`{"type":"hidden"}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the laptop's page still counts as in sight", func() bool { return !listsAny(w.ctx, w.presence, w.laptopPub+".*") })
	isl.record("s1", v1.EventPermissionRequested, v1.PermissionRequested{SessionID: "s1", RequestID: "r2", Kind: "edit"}, time.Time{})
	waitFor(t, "the laptop, out of sight, never got the second request", func() bool { return len(pushesTo(w.svc, laptopSub)) == 2 })

	// A subscription the push service says is gone is deleted.
	w.svc.Gone(phoneSub)
	isl.record("s1", v1.EventPermissionRequested, v1.PermissionRequested{SessionID: "s1", RequestID: "r3", Kind: "edit"}, time.Time{})
	waitFor(t, "the phone's gone subscription was never deleted", func() bool { return !w.stored(w.phonePub, phoneSub) })

	// Unsubscribing deletes this approver's subscription, and revoking an
	// approver deletes all of its own.
	other, otherSigned := w.subscribe(w.phone, pushOrigin)
	if code, out := postWith(t, w.asPhone, w.base+"/api/push/subscriptions", map[string]any{"subscription": otherSigned}, nil); code != http.StatusOK {
		t.Fatalf("subscribing again = %d %s", code, out)
	}
	if code, out := postWith(t, w.asLaptop, w.base+"/api/push/unsubscribe", map[string]any{"endpoint": other.Endpoint}, nil); code != http.StatusOK || !w.stored(w.phonePub, other) {
		t.Errorf("the laptop unsubscribing the phone's endpoint = %d %s; it should leave the phone's subscription", code, out)
	}
	if code, out := postWith(t, w.asPhone, w.base+"/api/push/unsubscribe", map[string]any{"endpoint": other.Endpoint}, nil); code != http.StatusOK || w.stored(w.phonePub, other) {
		t.Errorf("unsubscribing = %d %s, and the subscription is still stored: %v", code, out, w.stored(w.phonePub, other))
	}
	revocation, _ := approval.Sign(w.owner, v1.Revocation{Type: "revocation", World: w.ownerPub, Key: w.laptopPub, Created: approval.Now()})
	if code, out := postWith(t, w.asPhone, w.base+"/api/world/revocations", map[string]any{"revocation": revocation}, nil); code != http.StatusOK {
		t.Fatalf("revoke the laptop = %d %s", code, out)
	}
	waitFor(t, "the revoked laptop's subscription was never deleted", func() bool { return !w.stored(w.laptopPub, laptopSub) })

	if refused := w.svc.Refused(); len(refused) != 1 {
		t.Errorf("the push service refused %v, want only the push to the gone subscription", refused)
	}
}

// kvJS reaches the world's JetStream from the test.
func (w *pushWorld) kvJS() jetstream.JetStream {
	w.t.Helper()
	nc, err := nats.Connect(w.hub.ClientURL())
	if err != nil {
		w.t.Fatal(err)
	}
	w.t.Cleanup(nc.Close)
	js, _ := jetstream.New(nc)
	return js
}

// listsAny says whether kv has a key matching filter.
func listsAny(ctx context.Context, kv jetstream.KeyValue, filter string) bool {
	keys, err := kv.ListKeysFiltered(ctx, filter)
	if err != nil {
		return false
	}
	defer func() { _ = keys.Stop() }()
	for range keys.Keys() {
		return true
	}
	return false
}

func TestPushIsOffWithoutHTTPS(t *testing.T) {
	base, _ := startCitadel(t)
	if w := getWorld(t, base); w.PushKey != "" {
		t.Errorf("a citadel at %s names a push key", base)
	}
	if on, why := pushOn(Config{}, "http://127.0.0.1:8080"); on || why == "" {
		t.Errorf("pushOn for http = %v, %q", on, why)
	}
	if on, why := pushOn(Config{NoPush: true}, pushOrigin); on || why != "--no-push" {
		t.Errorf("pushOn with --no-push = %v, %q", on, why)
	}
	if on, _ := pushOn(Config{}, pushOrigin); !on {
		t.Error("pushOn for https is off")
	}
}

func TestNoticeNamesWhoAndWhereOnly(t *testing.T) {
	s := projections.Session{Host: "work station", ID: "s1", RepoID: "r1", Agent: "codex",
		Pending:   []projections.Permission{{RequestID: "r/1", Kind: "fetch", Title: "curl https://secret.example"}},
		Questions: []projections.Question{{QuestionID: "q1", Message: "the password?"}}}
	n, ok := noticeFor(s, "", v1.EventPermissionRequested, "r/1", "", pushOrigin)
	if !ok || n.Title != "Codex in r1 on work station wants to fetch from the web" || n.URL != pushOrigin+"/inbox/work%20station/s1/requests/r%2F1" || strings.Contains(n.Title+n.Body, "secret") {
		t.Errorf("request notice = %+v, %v", n, ok)
	}
	if _, ok := noticeFor(s, "storefront", v1.EventPermissionRequested, "gone", "", pushOrigin); ok {
		t.Error("a request no longer waiting has a notice")
	}
	if n, ok := noticeFor(s, "storefront", v1.EventQuestionAsked, "", "q1", pushOrigin); !ok || n.Title != "Codex in storefront on work station has a question" || strings.Contains(n.Title+n.Body, "password") {
		t.Errorf("question notice = %+v, %v", n, ok)
	}
}

func TestSameOriginURL(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"https://skylands.example", "https://skylands.example", true},
		{"https://Skylands.example/", "https://skylands.example", true},
		{"https://skylands.example:443", "https://skylands.example", true},
		{"https://skylands.example:8443", "https://skylands.example", false},
		{"http://skylands.example", "https://skylands.example", false},
		{"https://[::1]:443", "https://[::1]", true},
		{"", "", false},
	} {
		if got := sameOriginURL(tc.a, tc.b); got != tc.want {
			t.Errorf("sameOriginURL(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
