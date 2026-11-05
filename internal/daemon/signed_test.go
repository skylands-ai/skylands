package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
	w := &signedWorld{t: t, ctx: ctx, nc: nc, js: js, cli: socket.Dial(filepath.Join(sockDir, "d.sock")), owner: owner, ownerPub: ownerPub}

	fakeCmd, fakeEnv := fakeagent.TestCommand()
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- Run(runCtx, Config{
			Host: "ws", NATSURL: hub.LeafURL(), DataDir: t.TempDir(), SocketPath: filepath.Join(sockDir, "d.sock"), World: ownerPub, WorldDomain: "world",
			Adapters: agents.Registry{"fake": {Name: "fake", Provider: "local", Command: fakeCmd, Env: fakeEnv}},
		})
	}()
	t.Cleanup(func() {
		stop()
		if err := <-done; err != nil {
			t.Errorf("daemon.Run returned %v", err)
		}
	})
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
	if r := w.command(id, v1.CommandStartSession, v1.StartSession{ID: "start-" + id, SessionID: id, RepoID: repoID, Agent: "fake", Prompt: prompt}); r.Error != "" {
		w.t.Fatalf("StartSession: %s", r.Error)
	}
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
		if r := w.command(id, v1.CommandSendPrompt, v1.SendPrompt{ID: "again-" + id, Text: "fix it again"}); r.Error != "" {
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
