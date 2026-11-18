package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
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
	"github.com/skylands-ai/skylands/internal/rules"
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
	// starter is an approver that signs the sessions' starts, and is never
	// revoked.
	starter nkeys.KeyPair
	// root is the daemon's clone root.
	root string
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

// islandCommand sends a command for the host itself as the citadel does,
// and returns the daemon's reply.
func (w *signedWorld) islandCommand(typ string, body any) v1.Reply {
	w.t.Helper()
	data, _ := json.Marshal(body)
	msg, err := w.nc.Request(v1.IslandCommandSubject("ws", typ), data, 10*time.Second)
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
	if r := w.command(session, v1.CommandStartSession, w.startCommand(session, repoID, "fix it")); r.Error != "" {
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

// startSignedWorld starts a hub holding the world's approver stream, an
// owner key, and a daemon in the world running the fake agent, with one
// repository registered. Everything stops when the test ends.
func startSignedWorld(t *testing.T, ctx context.Context) (*signedWorld, socket.RepoInfo) {
	t.Helper()
	hub, err := natsembed.Start(ctx, natsembed.Config{Name: "hub", ClientAddr: "127.0.0.1:0", LeafAddr: freePort(t), StoreDir: t.TempDir(), Domain: "world"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(hub.Shutdown)
	nc, err := nats.Connect(hub.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
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
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	w := &signedWorld{t: t, ctx: ctx, nc: nc, js: js, cli: socket.Dial(filepath.Join(sockDir, "d.sock")), owner: owner, ownerPub: ownerPub, root: t.TempDir()}

	fakeCmd, fakeEnv := fakeagent.TestCommand()
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- Run(runCtx, Config{
			Host: "ws", NATSURL: hub.LeafURL(), DataDir: t.TempDir(), SocketPath: filepath.Join(sockDir, "d.sock"), CloneRoot: w.root, World: ownerPub, WorldDomain: "world",
			Adapters: agents.Registry{"fake": {Name: "fake", Provider: "local", Command: fakeCmd, Env: fakeEnv}},
		})
	}()
	t.Cleanup(func() {
		stop()
		if err := <-done; err != nil {
			t.Errorf("daemon.Run returned %v", err)
		}
	})
	w.starter, _ = w.approver("starter")
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
	return w, repo
}

// sign signs a decision with an approver key.
func (w *signedWorld) sign(kp nkeys.KeyPair, d approval.Decision) *v1.DecisionSignature {
	w.t.Helper()
	s, err := approval.SignDecision(kp, d)
	if err != nil {
		w.t.Fatal(err)
	}
	return &s
}

// revoke revokes an approver with the owner key.
func (w *signedWorld) revoke(pub string) {
	w.t.Helper()
	revocation, _ := approval.Sign(w.owner, v1.Revocation{Type: "revocation", World: w.ownerPub, Key: pub, Created: approval.Now()})
	if _, err := approval.Append(w.ctx, w.js, v1.ApproverSubject("revoked", pub), revocation); err != nil {
		w.t.Fatal(err)
	}
}

// TestDecisionsMustBeSigned refuses decisions through the citadel that
// aren't signed by one of the world's approvers over the decision as the
// daemon records it, and keeps the approver and signature of the one it
// accepts.
func TestDecisionsMustBeSigned(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	w, repo := startSignedWorld(t, ctx)
	owner, ownerPub, js := w.owner, w.ownerPub, w.js
	laptop, laptopPub := w.approver("laptop")
	phone, phonePub := w.approver("phone")

	req := w.waiting("sess-1", repo.ID)
	sum := sha256.Sum256(req.Input)
	input := hex.EncodeToString(sum[:])
	allow := approval.ToolCall{V: approval.DecisionVersion, Kind: approval.KindToolCall, World: ownerPub, Host: "ws", Session: "sess-1", Request: req.ID, Decision: "allow", Option: "allow", Input: input}
	approve := func(id string, sig *v1.DecisionSignature, option string) string {
		t.Helper()
		return w.command("sess-1", v1.CommandApproveToolCall, v1.ApproveToolCall{ID: id, RequestID: req.ID, OptionID: option, InputHash: input, Signature: sig}).Error
	}
	sign := w.sign

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
	// An "always" option would settle later calls inside the agent, out of
	// reach of rules and revocation, so it is refused even when signed.
	if err := approve("a4b", sign(laptop, always), "always"); !strings.Contains(err, "allow once") {
		t.Errorf("a signed approval of the always option = %q, want a refusal", err)
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
	deny := approval.ToolCall{V: approval.DecisionVersion, Kind: approval.KindToolCall, World: ownerPub, Host: "ws", Session: "sess-2", Request: req2.ID, Decision: "deny", Input: hex.EncodeToString(sum2[:])}
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

	// Each decision shows whole: its signature, the exact input it covers,
	// and what the tool call produced, kept on the host under their hashes.
	var shown socket.DecisionDetail
	var err error
	w.waitFor("the approved edit's output", func() bool {
		shown, err = w.cli.Decision(ctx, req.ID)
		return err == nil && shown.CallStatus != ""
	})
	if !shown.InputKept || hex.EncodeToString(func() []byte { s := sha256.Sum256(shown.Input); return s[:] }()) != shown.InputHash || shown.InputHash != input {
		t.Errorf("the approved input = kept %v, %d bytes, hash %s", shown.InputKept, len(shown.Input), shown.InputHash)
	}
	outSum := sha256.Sum256(shown.Output)
	if shown.CallStatus != "completed" || !shown.OutputKept || hex.EncodeToString(outSum[:]) != shown.OutputHash || len(shown.Output) != shown.OutputSize {
		t.Errorf("the approved edit's output = %s, kept %v, %d of %d bytes", shown.CallStatus, shown.OutputKept, len(shown.Output), shown.OutputSize)
	}
	if shown.Sig == "" || shown.World != ownerPub || shown.Approver != laptopPub || shown.Signature != "verified" || shown.ToolCall == "" {
		t.Errorf("the approval shown = %+v", shown.DecisionInfo)
	}
	verdict := approval.ToolCall{V: approval.DecisionVersion, Kind: approval.KindToolCall, World: shown.World, Host: "ws", Session: shown.Session, Request: shown.Request, Decision: "allow", Option: shown.Option, Input: shown.InputHash}
	if err := approval.VerifyDecision(verdict, v1.DecisionSignature{Approver: shown.Approver, Sig: shown.Sig}); err != nil {
		t.Errorf("the signature shown doesn't verify over the decision shown: %v", err)
	}
	w.waitFor("the denied edit to fail", func() bool {
		shown, err = w.cli.Decision(ctx, req2.ID)
		return err == nil && shown.CallStatus != ""
	})
	if shown.Answer != "denied" || !shown.InputKept || shown.CallStatus != "failed" || !shown.OutputKept {
		t.Errorf("the denial shown = %s, input kept %v, call %s, output kept %v", shown.Answer, shown.InputKept, shown.CallStatus, shown.OutputKept)
	}
	if _, err := w.cli.Decision(ctx, "no-such-request"); err == nil {
		t.Error("a decision on no request was shown")
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

// session is one session as the host's CLI sees it.
func (w *signedWorld) session(id string) socket.SessionInfo {
	w.t.Helper()
	list, err := w.cli.Sessions(w.ctx)
	if err != nil {
		w.t.Fatal(err)
	}
	for _, s := range list {
		if s.ID == id {
			return s
		}
	}
	return socket.SessionInfo{}
}

// waitFor polls until ok holds, and fails the test after a while.
func (w *signedWorld) waitFor(what string, ok func() bool) {
	w.t.Helper()
	for deadline := time.Now().Add(20 * time.Second); !ok(); {
		if time.Now().After(deadline) {
			w.t.Fatalf("waited too long for %s", what)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// start starts a session on the fake agent.
func (w *signedWorld) start(id, repoID, prompt string) {
	w.t.Helper()
	if r := w.command(id, v1.CommandStartSession, w.startCommand(id, repoID, prompt)); r.Error != "" {
		w.t.Fatalf("StartSession: %s", r.Error)
	}
}

// startCommand starts a session on the fake agent, signed by the starter.
func (w *signedWorld) startCommand(id, repoID, prompt string) v1.StartSession {
	w.t.Helper()
	cmd := v1.StartSession{ID: "start-" + id, SessionID: id, RepoID: repoID, Agent: "fake", Prompt: prompt}
	cmd.Signature = w.sign(w.starter, approval.Start{V: approval.DecisionVersion, Kind: approval.KindStart, World: w.ownerPub, Host: "ws", Session: id, Repo: repoID, Agent: "fake", Prompt: approval.TextHash(prompt)})
	return cmd
}

// repoRules is a repository's rules as the host's CLI sees them.
func (w *signedWorld) repoRules(repoID string) socket.RepoRulesInfo {
	w.t.Helper()
	list, err := w.cli.Rules(w.ctx)
	if err != nil {
		w.t.Fatal(err)
	}
	for _, r := range list {
		if r.RepoID == repoID {
			return r
		}
	}
	return socket.RepoRulesInfo{}
}

// TestAnswersTrustAndRulesMustBeSigned refuses answers, trust, and rule
// changes through the citadel that no approver signed over what the daemon
// records, refuses a change made against an older revision, and ends what
// an approver approved in advance once it is revoked.
func TestAnswersTrustAndRulesMustBeSigned(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	w, repo := startSignedWorld(t, ctx)
	laptop, laptopPub := w.approver("laptop")
	phone, phonePub := w.approver("phone")
	world := w.ownerPub

	// An answer is signed over the question as the daemon recorded it and
	// over the answers' canonical JSON, which must fit the form.
	w.start("ask", repo.ID, "ask me which file")
	var q socket.QuestionInfo
	w.waitFor("the question", func() bool {
		list, _ := w.cli.Questions(ctx)
		for _, x := range list {
			if x.Session == "ask" {
				q = x
				return true
			}
		}
		return false
	})
	form, _ := approval.FormHash(q.Message, q.Fields)
	reworded, _ := approval.FormHash(q.Message+"?", q.Fields)
	answerOver := func(form, answers string, declined bool) approval.Answer {
		d := approval.Answer{V: approval.DecisionVersion, Kind: approval.KindAnswer, World: world, Host: "ws", Session: "ask", Question: q.ID, Form: form, Declined: declined}
		if !declined {
			d.Answers, _ = approval.AnswersHash(json.RawMessage(answers))
		}
		return d
	}
	answer := func(id, answers string, decline bool, sig *v1.DecisionSignature) string {
		t.Helper()
		return w.command("ask", v1.CommandAnswerQuestion, v1.AnswerQuestion{ID: id, QuestionID: q.ID, Answers: json.RawMessage(answers), Decline: decline, Signature: sig}).Error
	}
	good := `{"question_0":"main.go"}`
	refusals := []struct {
		name, answers string
		decline       bool
		sig           *v1.DecisionSignature
		want          string
	}{
		{"unsigned", good, false, nil, "signed"},
		{"not on the form", `{"question_0":"other.go"}`, false, w.sign(laptop, answerOver(form, `{"question_0":"other.go"}`, false)), "isn't one of the options"},
		{"over a reworded question", good, false, w.sign(laptop, answerOver(reworded, good, false)), "doesn't verify"},
		{"over other answers", good, false, w.sign(laptop, answerOver(form, `{"question_0_custom":"x"}`, false)), "doesn't verify"},
		{"a decline signed as an answer", "", true, w.sign(laptop, answerOver(form, good, false)), "doesn't verify"},
		{"an unsigned decline", "", true, nil, "signed"},
	}
	for i, r := range refusals {
		if err := answer(fmt.Sprintf("q%d", i), r.answers, r.decline, r.sig); !strings.Contains(err, r.want) {
			t.Errorf("an answer %s = %q, want %q", r.name, err, r.want)
		}
	}
	// The same answer written differently is the same canonical JSON.
	if err := answer("q-ok", `{ "question_0" : "main.go" }`, false, w.sign(laptop, answerOver(form, good, false))); err != "" {
		t.Fatalf("the laptop's signed answer = %q", err)
	}

	// Trust is signed over the revision it replaces, so an old trust
	// can't be sent again. The answer led to an edit, which waits.
	w.waitFor("the edit after the answer", func() bool { return w.session("ask").Pending == 1 })
	trustOver := func(trusted bool, revision int) approval.Trust {
		return approval.Trust{V: approval.DecisionVersion, Kind: approval.KindTrust, World: world, Host: "ws", Session: "ask", Trusted: trusted, Revision: revision}
	}
	trust := func(id string, revision int, sig *v1.DecisionSignature) string {
		t.Helper()
		return w.command("ask", v1.CommandTrustSession, v1.TrustSession{ID: id, Trusted: true, Revision: revision, Signature: sig}).Error
	}
	if err := trust("t1", 0, nil); !strings.Contains(err, "signed") {
		t.Errorf("unsigned trust = %q", err)
	}
	if err := trust("t2", 1, w.sign(phone, trustOver(true, 1))); !strings.Contains(err, "changed since") {
		t.Errorf("trust against a revision ahead = %q", err)
	}
	if err := trust("t3", 0, w.sign(phone, trustOver(true, 1))); !strings.Contains(err, "doesn't verify") {
		t.Errorf("trust signed over another revision = %q", err)
	}
	phoneTrust := w.sign(phone, trustOver(true, 0))
	if err := trust("t4", 0, phoneTrust); err != "" {
		t.Fatalf("the phone's signed trust = %q", err)
	}
	w.waitFor("trust to answer the edit", func() bool { s := w.session("ask"); return s.Trusted && s.Pending == 0 })
	if err := trust("t5", 0, phoneTrust); !strings.Contains(err, "changed since") {
		t.Errorf("the same trust sent again = %q", err)
	}

	// A repository's rules are signed over the whole list and the revision
	// they replace. Sessions copy them, standing on the same signature.
	allowEdits := []v1.Rule{{Decision: "allow", Kind: "edit", Pattern: "worktree"}}
	rulesOver := func(list []v1.Rule, revision int) approval.RepoRules {
		h, _ := approval.RulesHash(list)
		return approval.RepoRules{V: approval.DecisionVersion, Kind: approval.KindRepoRules, World: world, Host: "ws", Repo: repo.ID, Rules: h, Revision: revision}
	}
	setRepoRules := func(id string, list []v1.Rule, revision int, sig *v1.DecisionSignature) string {
		t.Helper()
		data, _ := json.Marshal(v1.SetRepoRules{ID: id, RepoID: repo.ID, Rules: list, Revision: revision, Signature: sig})
		msg, err := w.nc.Request(v1.RepoCommandSubject("ws", repo.ID, v1.CommandSetRepoRules), data, 10*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		var r v1.Reply
		_ = json.Unmarshal(msg.Data, &r)
		return r.Error
	}
	if err := setRepoRules("r1", allowEdits, 0, nil); !strings.Contains(err, "signed") {
		t.Errorf("unsigned repo rules = %q", err)
	}
	if err := setRepoRules("r2", allowEdits, 0, w.sign(laptop, rulesOver([]v1.Rule{{Decision: "allow", Kind: "*"}}, 0))); !strings.Contains(err, "doesn't verify") {
		t.Errorf("repo rules signed over another list = %q", err)
	}
	if err := setRepoRules("r3", allowEdits, 0, w.sign(phone, rulesOver(allowEdits, 0))); err != "" {
		t.Fatalf("the phone's signed repo rules = %q", err)
	}
	if r := w.repoRules(repo.ID); r.By != "citadel" || r.Approver != phonePub || r.Lapsed {
		t.Errorf("repo rules = %+v", r)
	}
	w.start("copy", repo.ID, "fix it")
	w.start("own", repo.ID, "fix it")
	for _, id := range []string{"copy", "own"} {
		w.waitFor(id+"'s edit to pass by the copied rule", func() bool { s := w.session(id); return s.Status == "idle" && s.Pending == 0 })
	}
	// A session's own rules are signed too; "own" now stands on the laptop.
	h, _ := approval.RulesHash(rules.Inherit(allowEdits))
	ownRules := approval.SessionRules{V: approval.DecisionVersion, Kind: approval.KindSessionRules, World: world, Host: "ws", Session: "own", Rules: h}
	if r := w.command("own", v1.CommandSetSessionRules, v1.SetSessionRules{ID: "s1", Rules: rules.Inherit(allowEdits)}); !strings.Contains(r.Error, "signed") {
		t.Errorf("unsigned session rules = %+v", r)
	}
	if r := w.command("own", v1.CommandSetSessionRules, v1.SetSessionRules{ID: "s2", Rules: rules.Inherit(allowEdits), Signature: w.sign(laptop, ownRules)}); r.Error != "" {
		t.Fatalf("the laptop's signed session rules = %q", r.Error)
	}

	// Revoking the phone ends what it approved in advance: the trust it
	// turned on, its repository rules, and the copy a session stands on.
	// The laptop's list stands.
	w.revoke(phonePub)
	w.waitFor("the phone's trust and rules to lapse", func() bool {
		return !w.session("ask").Trusted && w.session("copy").RulesLapsed && w.repoRules(repo.ID).Lapsed
	})
	if w.session("own").RulesLapsed {
		t.Error("the laptop's session rules lapsed with the phone")
	}
	prompt := func(id string) {
		t.Helper()
		// Each session has had its first prompt, which is its revision.
		sig := w.sign(w.starter, approval.Prompt{V: approval.DecisionVersion, Kind: approval.KindPrompt, World: world, Host: "ws", Session: id, Revision: 1, Prompt: approval.TextHash("fix it again")})
		if r := w.command(id, v1.CommandSendPrompt, v1.SendPrompt{ID: "again-" + id, Text: "fix it again", Revision: 1, Signature: sig}); r.Error != "" {
			t.Fatalf("prompt %s: %s", id, r.Error)
		}
	}
	prompt("copy")
	prompt("own")
	prompt("ask")
	w.waitFor("the lapsed copy's edit to wait", func() bool { return w.session("copy").Pending == 1 })
	w.waitFor("the untrusted session's edit to wait", func() bool { return w.session("ask").Pending == 1 })
	w.waitFor("the laptop's rules to pass the edit", func() bool { s := w.session("own"); return s.Turns == 2 && s.Status == "idle" && s.Pending == 0 })
	w.start("after", repo.ID, "fix it")
	w.waitFor("a new session's edit to wait on the lapsed list", func() bool { return w.session("after").Pending == 1 })

	// The laptop approves the list again as it stands.
	if err := setRepoRules("r4", allowEdits, 1, w.sign(laptop, rulesOver(allowEdits, 1))); err != "" {
		t.Fatalf("approving the rules again = %q", err)
	}
	if r := w.repoRules(repo.ID); r.Lapsed || r.Approver != laptopPub {
		t.Errorf("repo rules approved again = %+v", r)
	}
}

// TestHostDecisionsMustBeSigned refuses decisions and rule changes from
// the host's CLI that no approver signed, as it refuses those through the
// citadel: the socket is open to every process the daemon's user runs,
// agents included. It records the CLI's key on what it takes, and lapses
// rules the CLI set once the CLI is revoked.
func TestHostDecisionsMustBeSigned(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	w, repo := startSignedWorld(t, ctx)
	cliKey, cliPub := w.approver("CLI on ws")
	stranger, _ := nkeys.CreateUser()

	req := w.waiting("sess-h", repo.ID)
	in, err := w.cli.RequestInput(ctx, req.ID)
	if err != nil {
		t.Fatal(err)
	}
	input := approval.BytesHash([]byte(in.Input))
	allowAs := func(input string) approval.Decision {
		return approval.ToolCall{V: approval.DecisionVersion, Kind: approval.KindToolCall, World: w.ownerPub, Host: "ws", Session: "sess-h", Request: req.ID, Decision: "allow", Option: "allow", Input: input}
	}
	for name, d := range map[string]socket.DecideRequest{
		"unsigned":                  {OptionID: "allow", InputHash: input},
		"signed by a stranger":      {OptionID: "allow", InputHash: input, Signature: w.sign(stranger, allowAs(input))},
		"signed over another input": {OptionID: "allow", InputHash: input, Signature: w.sign(cliKey, allowAs(strings.Repeat("0", 64)))},
	} {
		if _, err := w.cli.Approve(ctx, req.ID, d); err == nil {
			t.Errorf("%s: the host's approval was taken", name)
		}
	}
	if _, err := w.cli.Deny(ctx, req.ID, socket.DecideRequest{}); err == nil || !strings.Contains(err.Error(), "must be signed") {
		t.Errorf("an unsigned denial from the host = %v, want a refusal", err)
	}
	if _, err := w.cli.Approve(ctx, req.ID, socket.DecideRequest{OptionID: "allow", InputHash: input, Signature: w.sign(cliKey, allowAs(input))}); err != nil {
		t.Fatalf("a signed approval from the host: %v", err)
	}
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		h, _ := w.cli.History(ctx, "sess-h")
		if len(h) == 1 {
			if h[0].By != "local" || h[0].Approver != cliPub || h[0].ApproverName != "CLI on ws" || h[0].Signature != "verified" {
				t.Errorf("the host's approval in the history = %+v", h[0])
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("history = %+v", h)
		}
	}

	// A rule change from the host names the revision it replaces and is
	// signed over the list as the host keeps it.
	r := w.repoRules(repo.ID)
	list := []v1.Rule{{Decision: "Allow", Kind: "execute", Pattern: " go test * "}}
	kept, err := rules.ForRepository(list)
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := approval.RulesHash(kept)
	rulesAs := func(revision int) approval.Decision {
		return approval.RepoRules{V: approval.DecisionVersion, Kind: approval.KindRepoRules, World: w.ownerPub, Host: "ws", Repo: repo.ID, Rules: hash, Revision: revision}
	}
	if _, err := w.cli.SetRepoRules(ctx, repo.ID, socket.SetRulesRequest{Rules: list, Revision: r.Revision}); err == nil || !strings.Contains(err.Error(), "must be signed") {
		t.Errorf("unsigned rules from the host = %v, want a refusal", err)
	}
	if _, err := w.cli.SetRepoRules(ctx, repo.ID, socket.SetRulesRequest{Rules: list, Revision: r.Revision + 1, Signature: w.sign(cliKey, rulesAs(r.Revision+1))}); err == nil || !strings.Contains(err.Error(), "changed since") {
		t.Errorf("rules from the host at another revision = %v, want a refusal", err)
	}
	set, err := w.cli.SetRepoRules(ctx, repo.ID, socket.SetRulesRequest{Rules: list, Revision: r.Revision, Signature: w.sign(cliKey, rulesAs(r.Revision))})
	if err != nil || set.By != "local" || set.Approver != cliPub || set.Revision != r.Revision+1 || len(set.Rules) != 1 || set.Rules[0].Pattern != "go test *" {
		t.Fatalf("signed rules from the host = %+v, %v", set, err)
	}

	// Revoking the CLI lapses the rules it set, as for any approver.
	w.revoke(cliPub)
	for deadline := time.Now().Add(10 * time.Second); !w.repoRules(repo.ID).Lapsed; time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the rules the revoked CLI set never lapsed: %+v", w.repoRules(repo.ID))
		}
	}
}

// TestAddRepoThroughTheCitadelMustBeSigned adds a repository inside the
// clone root only for an approver's signature over the addition, and
// keeps who signed it.
func TestAddRepoThroughTheCitadelMustBeSigned(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	w, _ := startSignedWorld(t, ctx)
	if err := os.Rename(gitRepo(t, "main.go"), filepath.Join(w.root, "shop")); err != nil {
		t.Fatal(err)
	}
	d := approval.RepoAdd{V: approval.DecisionVersion, Kind: approval.KindRepoAdd, World: w.ownerPub, Host: "ws", ID: "Add1d3ntity", Path: "shop"}
	if r := w.islandCommand(v1.CommandAddRepo, v1.AddRepo{ID: d.ID, Path: d.Path}); r.Error != approval.ErrUnsigned.Error() {
		t.Errorf("unsigned AddRepo = %+v", r)
	}
	revoked, revokedPub := w.approver("gone")
	w.revoke(revokedPub)
	if r := w.islandCommand(v1.CommandAddRepo, v1.AddRepo{ID: d.ID, Path: d.Path, Signature: w.sign(revoked, d)}); !strings.Contains(r.Error, "no longer approves") {
		t.Errorf("AddRepo by a revoked approver = %+v", r)
	}

	r := w.islandCommand(v1.CommandAddRepo, v1.AddRepo{ID: d.ID, Path: d.Path, Signature: w.sign(w.starter, d)})
	if r.Error != "" {
		t.Fatalf("AddRepo: %s", r.Error)
	}
	var out v1.AddRepoResult
	if err := json.Unmarshal(r.Result, &out); err != nil || out.RepoID == "" || out.Name != "shop" {
		t.Fatalf("result = %s, %v", r.Result, err)
	}
	st, err := w.cli.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.CloneRoot != w.root {
		t.Errorf("status names the clone root %q, want %q", st.CloneRoot, w.root)
	}
	found := false
	for _, repo := range st.Repos {
		found = found || repo.ID == out.RepoID
	}
	if !found {
		t.Errorf("status lists %+v, not %s", st.Repos, out.RepoID)
	}

	starterPub, _ := w.starter.PublicKey()
	var registered v1.RepoRegistered
	ev := w.lastHostEvent(v1.EventRepoRegistered)
	if err := ev.Decode(&registered); err != nil || registered.RepoID != out.RepoID || registered.By != "citadel" || registered.Approver != starterPub || registered.Signature == "" || ev.CommandID != d.ID {
		t.Errorf("RepoRegistered = %+v, command %s, %v", registered, ev.CommandID, err)
	}
}

// lastHostEvent is the last host event of the given type in the island's
// stream, in the leaf's own domain.
func (w *signedWorld) lastHostEvent(eventType string) v1.Event {
	w.t.Helper()
	js, err := jetstream.NewWithDomain(w.nc, "ws")
	if err != nil {
		w.t.Fatal(err)
	}
	var last v1.Event
	for deadline := time.Now().Add(10 * time.Second); ; {
		msg, err := js.Stream(w.ctx, v1.IslandStreamName("ws"))
		if err == nil {
			if m, err := msg.GetLastMsgForSubject(w.ctx, v1.IslandEventSubject("ws", eventType)); err == nil {
				if json.Unmarshal(m.Data, &last) == nil {
					return last
				}
			}
		}
		if time.Now().After(deadline) {
			w.t.Fatalf("no %s in the island's stream: %v", eventType, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestStartFromAnIssueIsSigned starts a session from an issue only when
// the start's signature names the issue and the issue is in a repository
// the session's repository's remotes point to, and records it.
func TestStartFromAnIssueIsSigned(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	w, _ := startSignedWorld(t, ctx)
	dir := gitRepo(t, "main.go")
	if out, err := exec.Command("git", "-C", dir, "remote", "add", "origin", "https://github.com/org/storefront.git").CombinedOutput(); err != nil {
		t.Fatalf("git remote add: %v\n%s", err, out)
	}
	repo, err := w.cli.RegisterRepo(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	const issue = "https://github.com/org/storefront/issues/12"
	start := func(id, issue, signed string) v1.Reply {
		cmd := v1.StartSession{ID: "start-" + id, SessionID: id, RepoID: repo.ID, Agent: "fake", Prompt: "Resolve this issue.", Issue: issue}
		cmd.Signature = w.sign(w.starter, approval.Start{V: approval.DecisionVersion, Kind: approval.KindStart, World: w.ownerPub, Host: "ws", Session: id, Repo: repo.ID, Agent: "fake", Prompt: approval.TextHash(cmd.Prompt), Issue: signed})
		return w.command(id, v1.CommandStartSession, cmd)
	}
	if r := start("Unsigned1ssue", issue, ""); !strings.Contains(r.Error, approval.ErrBadSignature.Error()) {
		t.Errorf("a start whose signature doesn't name its issue = %+v", r)
	}
	if r := start("Elsewh3re", "https://github.com/org/other/issues/12", "https://github.com/org/other/issues/12"); !strings.Contains(r.Error, "isn't in a repository") {
		t.Errorf("a start from an issue in another repository = %+v", r)
	}
	if r := start("Fr0missue", issue, issue); r.Error != "" {
		t.Fatalf("StartSession: %s", r.Error)
	}
	ev := w.lastSessionEvent("Fr0missue", v1.EventSessionStarted)
	var started v1.SessionStarted
	if err := ev.Decode(&started); err != nil || started.Issue != issue {
		t.Errorf("SessionStarted = %+v, %v", started, err)
	}
}

// lastSessionEvent is the last event of the given type a session recorded
// in the island's stream, in the leaf's own domain.
func (w *signedWorld) lastSessionEvent(session, eventType string) v1.Event {
	w.t.Helper()
	js, err := jetstream.NewWithDomain(w.nc, "ws")
	if err != nil {
		w.t.Fatal(err)
	}
	var last v1.Event
	for deadline := time.Now().Add(10 * time.Second); ; {
		s, err := js.Stream(w.ctx, v1.IslandStreamName("ws"))
		if err == nil {
			if m, err := s.GetLastMsgForSubject(w.ctx, v1.SessionEventSubject("ws", session, eventType)); err == nil && json.Unmarshal(m.Data, &last) == nil {
				return last
			}
		}
		if time.Now().After(deadline) {
			w.t.Fatalf("no %s for %s in the island's stream: %v", eventType, session, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
