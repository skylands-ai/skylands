package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"

	"github.com/nats-io/nkeys"
	"github.com/nats-io/nuid"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/approval"
	"github.com/skylands-ai/skylands/internal/citadel/projections"
	"github.com/skylands-ai/skylands/internal/daemon/session"
	"github.com/skylands-ai/skylands/internal/daemon/socket"
	"github.com/skylands-ai/skylands/internal/rules"
)

// setUp sets the world up as its setup page does, with the token from the
// link the citadel offered: an owner key, and one approver, which then
// signs in. Every call the tests make to the citadel goes through w.http,
// which sends the sign-in and signs what decides something, as a page
// does.
func (w *world) setUp(token string) {
	w.t.Helper()
	w.owner, _ = nkeys.CreateOperator()
	w.ownerPub, _ = w.owner.PublicKey()
	w.approver, _ = nkeys.CreateUser()
	approverPub, _ := w.approver.PublicKey()
	record, _ := approval.Sign(w.owner, v1.WorldRecord{Type: "world", Name: "e2e", Owner: w.ownerPub, Created: approval.Now()})
	entry, _ := approval.Sign(w.owner, v1.ApproverEntry{Type: "approver", World: w.ownerPub, Key: approverPub, Name: "e2e browser", Created: approval.Now()})
	body, _ := json.Marshal(map[string]any{"token": token, "world": record, "approver": entry})
	resp, err := http.Post(w.webURL+"/api/world/setup", "application/json", bytes.NewReader(body))
	if err != nil {
		w.t.Fatal(err)
	}
	out, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		w.t.Fatalf("set the world up: %d %s", resp.StatusCode, out)
	}

	jar, _ := cookiejar.New(nil)
	w.http = &http.Client{Jar: jar, Transport: &signing{w: w, base: http.DefaultTransport}}
	resp, err = w.http.Get(w.webURL + "/api/signin")
	if err != nil {
		w.t.Fatal(err)
	}
	var in v1.SignIn
	err = json.NewDecoder(resp.Body).Decode(&in)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || err != nil {
		w.t.Fatalf("GET /api/signin = %d, %v", resp.StatusCode, err)
	}
	in.Type = "signin"
	signed, _ := approval.Sign(w.approver, in)
	body, _ = json.Marshal(map[string]any{"signin": signed})
	resp, err = w.http.Post(w.webURL+"/api/signin", "application/json", bytes.NewReader(body))
	if err != nil {
		w.t.Fatal(err)
	}
	out, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		w.t.Fatalf("sign in: %d %s", resp.StatusCode, out)
	}
}

// signDecision signs d with the world's approver key.
func (w *world) signDecision(d approval.Decision) *v1.DecisionSignature {
	w.t.Helper()
	s, err := approval.SignDecision(w.approver, d)
	if err != nil {
		w.t.Fatal(err)
	}
	return &s
}

// declineOverSocket declines q from the host's CLI path, signed by the
// world's approver, as the CLI signs it.
func (w *world) declineOverSocket(ctx context.Context, cli *socket.Client, host, sessionID string, q projections.Question) error {
	form, _ := approval.FormHash(q.Message, q.Fields)
	sig := w.signDecision(approval.Answer{V: approval.DecisionVersion, Kind: approval.KindAnswer, World: w.ownerPub, Host: host, Session: sessionID, Question: q.QuestionID, Form: form, Declined: true})
	_, err := cli.AnswerQuestion(ctx, q.QuestionID, socket.AnswerRequest{Decline: true, Signature: sig})
	return err
}

// session and repo are a session and a repository as the first browser a
// test opened sees them: the browser the test waits on before it acts, so
// a decision names the revision the test saw.
func (w *world) session(id string) (projections.Session, bool) {
	w.mu.Lock()
	b := w.first
	w.mu.Unlock()
	if b == nil {
		return projections.Session{}, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.sessions[id]
	return s, ok
}

func (w *world) repo(id string) (projections.Repo, bool) {
	w.mu.Lock()
	b := w.first
	w.mu.Unlock()
	if b == nil {
		return projections.Repo{}, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	r, ok := b.repos[id]
	return r, ok
}

// signing sends a request to the citadel as a page does: a request that
// decides something carries the approver's signature over the decision
// the daemon will rebuild from its own record, at the revision the page
// shows. Everything else passes through as it is.
type signing struct {
	w    *world
	base http.RoundTripper
}

func (s *signing) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method != http.MethodPost || r.Body == nil || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		return s.base.RoundTrip(r)
	}
	data, err := io.ReadAll(r.Body)
	_ = r.Body.Close()
	if err != nil {
		return nil, err
	}
	data = s.w.sign(r.URL.Path, data)
	out := r.Clone(r.Context())
	out.Body = io.NopCloser(bytes.NewReader(data))
	out.ContentLength = int64(len(data))
	out.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(data)), nil }
	return s.base.RoundTrip(out)
}

// sign adds what the route's decision needs to body: the signature, and
// the revision, option, or session id a page would send with it. A body
// it can't build a decision for, such as one naming a request nobody
// waits on, goes as it is, for the daemon to refuse.
func (w *world) sign(path string, body []byte) []byte {
	var m map[string]any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if dec.Decode(&m) != nil || m == nil {
		return body
	}
	_, hasRevision := m["revision"]
	revision := func(n int) int {
		if hasRevision {
			var r struct {
				Revision int `json:"revision"`
			}
			_ = json.Unmarshal(body, &r)
			return r.Revision
		}
		m["revision"] = n
		return n
	}
	base := approval.Change{V: approval.DecisionVersion, Kind: approval.KindChange, World: w.ownerPub}
	var d approval.Decision
	p := strings.Split(strings.TrimPrefix(path, "/api/"), "/")
	switch {
	case len(p) == 1 && p[0] == "sessions":
		var b struct {
			Host       string           `json:"host"`
			RepoID     string           `json:"repoId"`
			Agent      string           `json:"agent"`
			Mode       string           `json:"mode"`
			Model      string           `json:"model"`
			Prompt     string           `json:"prompt"`
			SessionID  string           `json:"sessionId"`
			InCheckout bool             `json:"inCheckout"`
			Mentions   []string         `json:"mentions"`
			Images     []v1.PromptImage `json:"images"`
		}
		_ = json.Unmarshal(body, &b)
		id := strings.TrimSpace(b.SessionID)
		if id == "" {
			id = nuid.Next()
			m["sessionId"] = id
		}
		agent := strings.TrimSpace(b.Agent)
		if agent == "" {
			agent = "claude"
		}
		attachments, _ := approval.AttachmentsHash(b.Mentions, b.Images)
		d = approval.Start{V: approval.DecisionVersion, Kind: approval.KindStart, World: w.ownerPub, Host: strings.TrimSpace(b.Host), Session: id, Repo: strings.TrimSpace(b.RepoID), Agent: agent,
			Mode: strings.TrimSpace(b.Mode), Model: strings.TrimSpace(b.Model), InCheckout: b.InCheckout, Prompt: approval.TextHash(strings.TrimSpace(b.Prompt)), Attachments: attachments}

	case len(p) == 4 && p[0] == "repos" && p[3] == "rules":
		var b struct {
			Rules []v1.Rule `json:"rules"`
		}
		_ = json.Unmarshal(body, &b)
		list, err := rules.ForRepository(b.Rules)
		if err != nil {
			return body
		}
		r, _ := w.repo(p[2])
		hash, _ := approval.RulesHash(list)
		d = approval.RepoRules{V: approval.DecisionVersion, Kind: approval.KindRepoRules, World: w.ownerPub, Host: p[1], Repo: p[2], Rules: hash, Revision: revision(r.RulesRevision)}

	case len(p) == 4 && p[0] == "sessions":
		host, id := p[1], p[2]
		s, _ := w.session(id)
		base.Host, base.Session = host, id
		switch p[3] {
		case "prompt":
			var b struct {
				Text     string           `json:"text"`
				Mentions []string         `json:"mentions"`
				Images   []v1.PromptImage `json:"images"`
			}
			_ = json.Unmarshal(body, &b)
			attachments, _ := approval.AttachmentsHash(b.Mentions, b.Images)
			d = approval.Prompt{V: approval.DecisionVersion, Kind: approval.KindPrompt, World: w.ownerPub, Host: host, Session: id, Revision: revision(s.Revision), Prompt: approval.TextHash(strings.TrimSpace(b.Text)), Attachments: attachments}
		case "trust":
			var b struct {
				Trusted bool `json:"trusted"`
			}
			_ = json.Unmarshal(body, &b)
			d = approval.Trust{V: approval.DecisionVersion, Kind: approval.KindTrust, World: w.ownerPub, Host: host, Session: id, Trusted: b.Trusted, Revision: revision(s.TrustRevision)}
		case "rules":
			var b struct {
				Rules []v1.Rule `json:"rules"`
			}
			_ = json.Unmarshal(body, &b)
			list, err := rules.Validate(b.Rules)
			if err != nil {
				return body
			}
			hash, _ := approval.RulesHash(list)
			d = approval.SessionRules{V: approval.DecisionVersion, Kind: approval.KindSessionRules, World: w.ownerPub, Host: host, Session: id, Rules: hash, Revision: revision(s.RulesRevision)}
		case "commit":
			var b struct {
				Message string `json:"message"`
			}
			_ = json.Unmarshal(body, &b)
			base.Action, base.Text, base.Revision = approval.ChangeCommit, approval.TextHash(strings.TrimSpace(b.Message)), revision(s.Revision)
			d = base
		case "merge":
			base.Action, base.Revision = approval.ChangeMerge, revision(s.Revision)
			d = base
		case "pull-request":
			var b struct {
				Title string `json:"title"`
				Body  string `json:"body"`
			}
			_ = json.Unmarshal(body, &b)
			base.Action, base.Revision = approval.ChangePullRequest, revision(s.Revision)
			base.Text, _ = approval.PullRequestHash(strings.TrimSpace(b.Title), strings.TrimSpace(b.Body))
			d = base
		case "remove-worktree":
			base.Action, base.Revision = approval.ChangeRemove, revision(s.Revision)
			d = base
		case "terminals":
			var b struct {
				TerminalID string `json:"terminalId"`
			}
			_ = json.Unmarshal(body, &b)
			d = approval.Terminal{V: approval.DecisionVersion, Kind: approval.KindTerminal, World: w.ownerPub, Host: host, Session: id, Terminal: b.TerminalID}
		default:
			return body
		}

	case len(p) == 6 && p[0] == "sessions" && p[3] == "requests":
		host, id, rid := p[1], p[2], p[4]
		s, _ := w.session(id)
		var pending *projections.Permission
		for i := range s.Pending {
			if s.Pending[i].RequestID == rid {
				pending = &s.Pending[i]
			}
		}
		if pending == nil {
			return body
		}
		t := approval.ToolCall{V: approval.DecisionVersion, Kind: approval.KindToolCall, World: w.ownerPub, Host: host, Session: id, Request: rid, Decision: "deny", Input: pending.InputHash}
		switch p[5] {
		case "approve":
			var b struct {
				OptionID  string `json:"optionId"`
				InputHash string `json:"inputHash"`
			}
			_ = json.Unmarshal(body, &b)
			if b.OptionID == "" {
				for _, o := range pending.Options {
					if o.Kind == "allow_once" && b.OptionID == "" {
						b.OptionID = o.ID
					}
				}
				m["optionId"] = b.OptionID
			}
			t.Decision, t.Option, t.Input = "allow", b.OptionID, b.InputHash
		case "deny":
		default:
			return body
		}
		d = t

	case len(p) == 6 && p[0] == "sessions" && p[3] == "questions" && p[5] == "answer":
		host, id, qid := p[1], p[2], p[4]
		s, _ := w.session(id)
		var q *projections.Question
		for i := range s.Questions {
			if s.Questions[i].QuestionID == qid {
				q = &s.Questions[i]
			}
		}
		if q == nil {
			return body
		}
		var b struct {
			Answers json.RawMessage `json:"answers"`
			Decline bool            `json:"decline"`
		}
		_ = json.Unmarshal(body, &b)
		form, _ := approval.FormHash(q.Message, q.Fields)
		a := approval.Answer{V: approval.DecisionVersion, Kind: approval.KindAnswer, World: w.ownerPub, Host: host, Session: id, Question: qid, Form: form, Declined: b.Decline}
		if !b.Decline {
			checked, err := session.CheckAnswers(q.Fields, b.Answers)
			if err != nil {
				return body
			}
			a.Answers, _ = approval.AnswersHash(checked)
		}
		d = a

	default:
		return body
	}
	sig, err := approval.SignDecision(w.approver, d)
	if err != nil {
		return body
	}
	m["signature"] = sig
	out, err := json.Marshal(m)
	if err != nil {
		return body
	}
	return out
}

// signedInput is a keystroke into a terminal, signed as a page signs each
// one.
func (w *world) signedInput(host, sessionID, terminalID string, in v1.TerminalInput) *v1.TerminalInput {
	in.Signature = w.signDecision(approval.TerminalInput{V: approval.DecisionVersion, Kind: approval.KindTerminalIn, World: w.ownerPub, Host: host, Session: sessionID, Terminal: terminalID, Attach: in.Attach, Seq: in.Seq, Data: approval.BytesHash(in.Data)})
	return &in
}
