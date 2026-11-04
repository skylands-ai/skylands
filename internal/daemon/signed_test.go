package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	"github.com/skylands-ai/skylands/internal/daemon/agents"
	"github.com/skylands-ai/skylands/internal/daemon/socket"
	"github.com/skylands-ai/skylands/internal/fakeagent"
	"github.com/skylands-ai/skylands/internal/natsembed"
)

// signedWorld is a world with a registry of approvers, a daemon in it
// running the fake agent, and one session whose edit waits on a person.
type signedWorld struct {
	t        *testing.T
	ctx      context.Context
	nc       *nats.Conn
	js       jetstream.JetStream
	cli      *socket.Client
	owner    nkeys.KeyPair
	ownerPub string
}

func (w *signedWorld) approver(name string) (nkeys.KeyPair, string) {
	w.t.Helper()
	kp, _ := nkeys.CreateUser()
	pub, _ := kp.PublicKey()
	entry, _ := approval.Sign(w.owner, v1.ApproverEntry{Type: "approver", World: w.ownerPub, Key: pub, Name: name, Created: approval.Now()})
	if _, err := approval.Append(w.ctx, w.js, v1.ApproverSubject("approver", pub), entry); err != nil {
		w.t.Fatal(err)
	}
	return kp, pub
}

// command sends a session command as the citadel does, and returns the
// daemon's reply.
func (w *signedWorld) command(session, typ string, body any) v1.Reply {
	w.t.Helper()
	data, _ := json.Marshal(body)
	msg, err := w.nc.Request(v1.SessionCommandSubject("ws", session, typ), data, 10*time.Second)
	if err != nil {
		w.t.Fatalf("%s: %v", typ, err)
	}
	var r v1.Reply
	_ = json.Unmarshal(msg.Data, &r)
	return r
}

// waiting starts a session and returns its request once it waits.
func (w *signedWorld) waiting(session, repoID string) socket.RequestInfo {
	w.t.Helper()
	if r := w.command(session, v1.CommandStartSession, v1.StartSession{ID: "start-" + session, SessionID: session, RepoID: repoID, Agent: "fake", Prompt: "fix it"}); r.Error != "" {
		w.t.Fatalf("StartSession: %s", r.Error)
	}
	for deadline := time.Now().Add(20 * time.Second); ; {
		reqs, err := w.cli.Requests(w.ctx)
		for _, r := range reqs {
			if r.Session == session {
				return r
			}
		}
		if time.Now().After(deadline) {
			w.t.Fatalf("no request waited: %+v, %v", reqs, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestDecisionsMustBeSigned refuses decisions through the citadel that
// aren't signed by one of the world's approvers over the decision as the
// daemon records it, and keeps the approver and signature of the one it
// accepts.
func TestDecisionsMustBeSigned(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
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
	owner, ownerPub := ownerKey(t)
	sockDir, err := os.MkdirTemp("", "sk")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(sockDir) }()
	w := &signedWorld{t: t, ctx: ctx, nc: nc, js: js, cli: socket.Dial(filepath.Join(sockDir, "d.sock")), owner: owner, ownerPub: ownerPub}
	laptop, laptopPub := w.approver("laptop")
	phone, phonePub := w.approver("phone")

	fakeCmd, fakeEnv := fakeagent.TestCommand()
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- Run(runCtx, Config{
			Host: "ws", NATSURL: hub.LeafURL(), DataDir: t.TempDir(), SocketPath: filepath.Join(sockDir, "d.sock"), World: ownerPub, WorldDomain: "world",
			Adapters: agents.Registry{"fake": {Name: "fake", Provider: "local", Command: fakeCmd, Env: fakeEnv}},
		})
	}()
	defer func() {
		stop()
		if err := <-done; err != nil {
			t.Errorf("daemon.Run returned %v", err)
		}
	}()
	var repo socket.RepoInfo
	for deadline := time.Now().Add(10 * time.Second); ; {
		if repo, err = w.cli.RegisterRepo(ctx, gitRepo(t, "main.go")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("register repo: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}

	req := w.waiting("sess-1", repo.ID)
	sum := sha256.Sum256(req.Input)
	input := hex.EncodeToString(sum[:])
	allow := approval.Decision{V: approval.DecisionVersion, World: ownerPub, Host: "ws", Session: "sess-1", Request: req.ID, Decision: "allow", Option: "allow", Input: input}
	approve := func(id string, sig *v1.DecisionSignature, option string) string {
		t.Helper()
		return w.command("sess-1", v1.CommandApproveToolCall, v1.ApproveToolCall{ID: id, RequestID: req.ID, OptionID: option, InputHash: input, Signature: sig}).Error
	}
	sign := func(kp nkeys.KeyPair, d approval.Decision) *v1.DecisionSignature {
		s, err := approval.SignDecision(kp, d)
		if err != nil {
			t.Fatal(err)
		}
		return &s
	}

	if err := approve("a1", nil, "allow"); !strings.Contains(err, "signed") {
		t.Errorf("an unsigned approval = %q, want a refusal", err)
	}
	stranger, _ := nkeys.CreateUser()
	if err := approve("a2", sign(stranger, allow), "allow"); !strings.Contains(err, "isn't an approver") {
		t.Errorf("an approval signed by a stranger = %q, want a refusal", err)
	}
	other := allow
	other.Request = "another request"
	if err := approve("a3", sign(laptop, other), "allow"); !strings.Contains(err, "doesn't verify") {
		t.Errorf("a signature over another request = %q, want a refusal", err)
	}
	always := allow
	always.Option = "always"
	if err := approve("a4", sign(laptop, always), "allow"); !strings.Contains(err, "doesn't verify") {
		t.Errorf("a signature over another option = %q, want a refusal", err)
	}
	// The phone is revoked just before it approves: the daemon reads the
	// registry up to the revocation before it checks the signature.
	revocation, _ := approval.Sign(owner, v1.Revocation{Type: "revocation", World: ownerPub, Key: phonePub, Created: approval.Now()})
	if _, err := approval.Append(ctx, js, v1.ApproverSubject("revoked", phonePub), revocation); err != nil {
		t.Fatal(err)
	}
	if err := approve("a5", sign(phone, allow), "allow"); !strings.Contains(err, "no longer approves") {
		t.Errorf("an approval by a revoked approver = %q, want a refusal", err)
	}
	if reqs, _ := w.cli.Requests(ctx); len(reqs) != 1 {
		t.Fatalf("a refused approval settled the request: %+v", reqs)
	}
	if err := approve("a6", sign(laptop, allow), "allow"); err != "" {
		t.Fatalf("the laptop's signed approval = %q", err)
	}

	// A denial is signed too, with no option.
	req2 := w.waiting("sess-2", repo.ID)
	sum2 := sha256.Sum256(req2.Input)
	deny := approval.Decision{V: approval.DecisionVersion, World: ownerPub, Host: "ws", Session: "sess-2", Request: req2.ID, Decision: "deny", Input: hex.EncodeToString(sum2[:])}
	if r := w.command("sess-2", v1.CommandDenyToolCall, v1.DenyToolCall{ID: "d1", RequestID: req2.ID}); !strings.Contains(r.Error, "signed") {
		t.Errorf("an unsigned denial = %+v, want a refusal", r)
	}
	if r := w.command("sess-2", v1.CommandDenyToolCall, v1.DenyToolCall{ID: "d2", RequestID: req2.ID, Signature: sign(laptop, deny)}); r.Error != "" {
		t.Fatalf("the laptop's signed denial = %q", r.Error)
	}

	// The history keeps who decided, with signatures that verify against
	// the decisions as the daemon recorded them.
	history := func() map[string]socket.DecisionInfo {
		t.Helper()
		h, err := w.cli.History(ctx, "")
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]socket.DecisionInfo{}
		for _, d := range h {
			out[d.Session] = d
		}
		return out
	}
	h := history()
	if d := h["sess-1"]; d.Answer != "allowed" || d.By != "citadel" || d.Approver != laptopPub || d.ApproverName != "laptop" || d.Signature != "verified" || d.Option != "allow" {
		t.Errorf("the approval in the history = %+v", d)
	}
	if d := h["sess-2"]; d.Answer != "denied" || d.Approver != laptopPub || d.Signature != "verified" {
		t.Errorf("the denial in the history = %+v", d)
	}
	if only, err := w.cli.History(ctx, "sess-2"); err != nil || len(only) != 1 || only[0].Session != "sess-2" {
		t.Errorf("the history of one session = %+v, %v", only, err)
	}
	// Revoking the laptop leaves what it decided standing, and the history
	// says it no longer approves.
	revokeLaptop, _ := approval.Sign(owner, v1.Revocation{Type: "revocation", World: ownerPub, Key: laptopPub, Created: approval.Now()})
	if _, err := approval.Append(ctx, js, v1.ApproverSubject("revoked", laptopPub), revokeLaptop); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); ; {
		if d := history()["sess-1"]; d.Signature == "verified, but the approver no longer approves" && d.Answer == "allowed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after revoking the laptop, the history says %+v", history()["sess-1"])
		}
		time.Sleep(100 * time.Millisecond)
	}
}
