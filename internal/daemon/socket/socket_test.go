package socket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/daemon/host"
	"github.com/skylands-ai/skylands/internal/daemon/session"
)

// shortSocket returns a socket path short enough for every platform's limit.
func shortSocket(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "sk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "d.sock")
}

type fakeAPI struct {
	repos    []RepoInfo
	sessions []SessionInfo
	rules    []v1.Rule
	// decided, setRules, and answered are the last decision, rule
	// change, and answer, as the API got them, which the server's
	// goroutines write under mu.
	mu       sync.Mutex
	decided  DecideRequest
	setRules SetRulesRequest
	answered AnswerRequest
}

// last returns what the API last got, for the test to check.
func (f *fakeAPI) last() (DecideRequest, SetRulesRequest, AnswerRequest) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.decided, f.setRules, f.answered
}

func (f *fakeAPI) Sessions(context.Context) []SessionInfo { return f.sessions }
func (f *fakeAPI) StopSession(_ context.Context, id, _ string) (SessionInfo, error) {
	for i := range f.sessions {
		if f.sessions[i].ID == id {
			f.sessions[i].Status = "ended"
			return f.sessions[i], nil
		}
	}
	return SessionInfo{}, session.ErrUnknownSession
}
func (f *fakeAPI) CancelTurn(_ context.Context, id, _ string) (SessionInfo, error) {
	for i := range f.sessions {
		if f.sessions[i].ID == id {
			if f.sessions[i].Status != "running" {
				return SessionInfo{}, session.ErrNotRunning
			}
			f.sessions[i].Cancelling = true
			return f.sessions[i], nil
		}
	}
	return SessionInfo{}, session.ErrUnknownSession
}
func (f *fakeAPI) SetMode(_ context.Context, id, _, mode string) (SessionInfo, error) {
	for i := range f.sessions {
		if f.sessions[i].ID == id {
			if mode != "plan" && mode != "default" {
				return SessionInfo{}, session.ErrModeNotListed
			}
			f.sessions[i].Mode = mode
			return f.sessions[i], nil
		}
	}
	return SessionInfo{}, session.ErrUnknownSession
}
func (f *fakeAPI) SetModel(_ context.Context, id, _, model string) (SessionInfo, error) {
	for i := range f.sessions {
		if f.sessions[i].ID == id {
			f.sessions[i].Model = model
			return f.sessions[i], nil
		}
	}
	return SessionInfo{}, session.ErrUnknownSession
}
func (f *fakeAPI) RemoveSession(_ context.Context, id, _ string, force bool) (RemoveResult, error) {
	for i := range f.sessions {
		if f.sessions[i].ID == id {
			if f.sessions[i].Status != "ended" {
				return RemoveResult{}, session.ErrOpen
			}
			f.sessions[i].WorktreeRemoved = true
			return RemoveResult{Session: f.sessions[i], BranchDeleted: force}, nil
		}
	}
	return RemoveResult{}, session.ErrUnknownSession
}
func (f *fakeAPI) PruneSessions(context.Context) []PruneResult { return []PruneResult{} }
func (f *fakeAPI) Questions(context.Context) []QuestionInfo    { return []QuestionInfo{} }
func (f *fakeAPI) AnswerQuestion(_ context.Context, id string, a AnswerRequest) (QuestionInfo, error) {
	if id != "q1" {
		return QuestionInfo{}, session.ErrUnknownQuestion
	}
	f.mu.Lock()
	f.answered = a
	f.mu.Unlock()
	return QuestionInfo{ID: id, Message: string(a.Answers), Agent: fmt.Sprint(a.Decline)}, nil
}
func (f *fakeAPI) Match(context.Context, string) MatchInfo  { return MatchInfo{} }
func (f *fakeAPI) Replay(context.Context) []ReplayedSession { return nil }

func (f *fakeAPI) CheckAgents(context.Context) ([]v1.AgentAvailability, error) {
	return []v1.AgentAvailability{{Name: "fake", Available: true}}, nil
}

func (f *fakeAPI) Rules(context.Context) []RepoRulesInfo {
	out := []RepoRulesInfo{}
	for _, r := range f.repos {
		out = append(out, RepoRulesInfo{RepoID: r.ID, Repo: r.Name, Rules: f.rules})
	}
	return out
}
func (f *fakeAPI) SetRepoRules(_ context.Context, repoID string, req SetRulesRequest) (RepoRulesInfo, error) {
	for _, r := range f.repos {
		if r.ID == repoID {
			f.mu.Lock()
			f.rules, f.setRules = req.Rules, req
			f.mu.Unlock()
			return RepoRulesInfo{RepoID: r.ID, Repo: r.Name, Rules: req.Rules, Revision: req.Revision + 1}, nil
		}
	}
	return RepoRulesInfo{}, host.ErrUnknownRepo
}

func (f *fakeAPI) Requests(context.Context) []RequestInfo { return []RequestInfo{} }
func (f *fakeAPI) History(context.Context, string) []DecisionInfo {
	return []DecisionInfo{}
}
func (f *fakeAPI) Decision(_ context.Context, requestID string) (DecisionDetail, error) {
	if requestID != "r1" {
		return DecisionDetail{}, session.ErrUnknownRequest
	}
	return DecisionDetail{DecisionInfo: DecisionInfo{Request: "r1", Answer: "allowed"}, InputHash: "h", InputSize: 2, Input: []byte("{}"), InputKept: true}, nil
}
func (f *fakeAPI) RequestInput(_ context.Context, requestID string) (v1.RequestInput, error) {
	if requestID != "r1" {
		return v1.RequestInput{}, session.ErrUnknownRequest
	}
	return v1.RequestInput{RequestID: requestID, Input: `{"command":"go test ./..."}`, Hash: "h", Size: 27}, nil
}
func (f *fakeAPI) Approve(_ context.Context, requestID string, d DecideRequest) (RequestInfo, error) {
	f.mu.Lock()
	f.decided = d
	f.mu.Unlock()
	return RequestInfo{ID: requestID}, nil
}
func (f *fakeAPI) Deny(_ context.Context, requestID string, d DecideRequest) (RequestInfo, error) {
	f.mu.Lock()
	f.decided = d
	f.mu.Unlock()
	return RequestInfo{ID: requestID}, nil
}

func (f *fakeAPI) Status(context.Context) StatusInfo {
	return StatusInfo{Host: "ws", World: "nats-leaf://x:7422", Version: "v1", Started: time.Now(), Repos: f.repos}
}

func (f *fakeAPI) RegisterRepo(_ context.Context, path, commandID string) (RepoInfo, error) {
	if commandID == "" {
		return RepoInfo{}, errors.New("missing command id")
	}
	for _, r := range f.repos {
		if r.Path == path {
			return r, host.ErrAlreadyRegistered
		}
	}
	r := RepoInfo{ID: "r1", Name: filepath.Base(path), Path: path, Files: 3}
	f.repos = append(f.repos, r)
	return r, nil
}

func TestServerAndClient(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	path := shortSocket(t)
	api := &fakeAPI{}
	ln, err := Listen(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	sctx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- Serve(sctx, ln, api) }()

	c := Dial(path)
	var st StatusInfo
	for i := 0; i < 50; i++ {
		if st, err = c.Status(ctx); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil || st.Host != "ws" {
		t.Fatalf("Status = %+v, %v", st, err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("socket mode = %v, %v; want 0600", info.Mode(), err)
	}
	if agents, err := c.CheckAgents(ctx); err != nil || len(agents) != 1 || agents[0].Name != "fake" || !agents[0].Available {
		t.Errorf("CheckAgents = %+v, %v", agents, err)
	}

	repo, err := c.RegisterRepo(ctx, "/code/storefront")
	if err != nil || repo.Name != "storefront" || repo.Files != 3 {
		t.Fatalf("RegisterRepo = %+v, %v", repo, err)
	}
	if _, err := c.RegisterRepo(ctx, "/code/storefront"); err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Errorf("duplicate err = %v", err)
	}
	if st, _ := c.Status(ctx); len(st.Repos) != 1 {
		t.Errorf("status repos = %+v", st.Repos)
	}

	// Sessions are listed, stopped, and removed, with the daemon's refusals
	// coming back as errors.
	api.sessions = []SessionInfo{{ID: "s1", Status: "idle", Branch: "skylands/s1"}}
	if list, err := c.Sessions(ctx); err != nil || len(list) != 1 || list[0].Branch != "skylands/s1" {
		t.Errorf("Sessions = %+v, %v", list, err)
	}
	if _, err := c.RemoveSession(ctx, "s1", false); err == nil || !strings.Contains(err.Error(), "still open") {
		t.Errorf("remove an open session: err = %v", err)
	}
	if _, err := c.CancelTurn(ctx, "s1"); err == nil || !strings.Contains(err.Error(), "no turn") {
		t.Errorf("cancel an idle session: err = %v", err)
	}
	api.sessions[0].Status = "running"
	if s, err := c.CancelTurn(ctx, "s1"); err != nil || !s.Cancelling {
		t.Errorf("CancelTurn = %+v, %v", s, err)
	}
	api.sessions[0].Status = "idle"
	if s, err := c.SetMode(ctx, "s1", "plan"); err != nil || s.Mode != "plan" {
		t.Errorf("SetMode = %+v, %v", s, err)
	}
	if _, err := c.SetMode(ctx, "s1", "bypassPermissions"); err == nil || !strings.Contains(err.Error(), "not one the adapter lists") {
		t.Errorf("set an unlisted mode: err = %v", err)
	}
	if s, err := c.SetModel(ctx, "s1", "opus"); err != nil || s.Model != "opus" {
		t.Errorf("SetModel = %+v, %v", s, err)
	}
	if _, err := c.SetModel(ctx, "nope", "opus"); err == nil || !strings.Contains(err.Error(), "no such session") {
		t.Errorf("set the model of an unknown session: err = %v", err)
	}
	if s, err := c.StopSession(ctx, "s1"); err != nil || s.Status != "ended" {
		t.Errorf("StopSession = %+v, %v", s, err)
	}
	if _, err := c.StopSession(ctx, "nope"); err == nil || !strings.Contains(err.Error(), "no such session") {
		t.Errorf("stop an unknown session: err = %v", err)
	}
	if res, err := c.RemoveSession(ctx, "s1", true); err != nil || !res.Session.WorktreeRemoved || !res.BranchDeleted {
		t.Errorf("RemoveSession = %+v, %v", res, err)
	}
	if pruned, err := c.PruneSessions(ctx); err != nil || len(pruned) != 0 {
		t.Errorf("PruneSessions = %+v, %v", pruned, err)
	}

	// A question is answered, or declined, by its id.
	sig := &v1.DecisionSignature{Approver: "UAPPROVER", Sig: "c2ln"}
	if q, err := c.AnswerQuestion(ctx, "q1", AnswerRequest{Answers: json.RawMessage(`{"question_0":"a.go"}`), Signature: sig}); err != nil || q.Message != `{"question_0":"a.go"}` || q.Agent != "false" {
		t.Errorf("AnswerQuestion = %+v, %v", q, err)
	}
	if _, _, a := api.last(); a.Signature == nil || *a.Signature != *sig || a.CommandID == "" {
		t.Errorf("the answer reached the API as %+v, without its signature or command id", a)
	}
	if q, err := c.AnswerQuestion(ctx, "q1", AnswerRequest{Decline: true}); err != nil || q.Agent != "true" {
		t.Errorf("decline = %+v, %v", q, err)
	}
	if _, err := c.AnswerQuestion(ctx, "nope", AnswerRequest{Decline: true}); err == nil || !strings.Contains(err.Error(), "no such question") {
		t.Errorf("answer an unknown question: err = %v", err)
	}

	// A decision carries the option it chooses, the hash of the input it
	// approves, and a signature, and the input comes whole to hash.
	if in, err := c.RequestInput(ctx, "r1"); err != nil || in.Input != `{"command":"go test ./..."}` {
		t.Errorf("RequestInput = %+v, %v", in, err)
	}
	if _, err := c.RequestInput(ctx, "nope"); err == nil || !strings.Contains(err.Error(), "no such request") {
		t.Errorf("the input of an unknown request: err = %v", err)
	}
	if r, err := c.Approve(ctx, "r1", DecideRequest{OptionID: "allow", InputHash: "h", Signature: sig}); err != nil || r.ID != "r1" {
		t.Errorf("Approve = %+v, %v", r, err)
	}
	if d, _, _ := api.last(); d.OptionID != "allow" || d.InputHash != "h" || d.Signature == nil || *d.Signature != *sig || d.CommandID == "" {
		t.Errorf("the approval reached the API as %+v", d)
	}
	if _, err := c.Deny(ctx, "r1", DecideRequest{Signature: sig}); err != nil {
		t.Errorf("Deny: %v", err)
	}
	if d, _, _ := api.last(); d.OptionID != "" || d.Signature == nil || *d.Signature != *sig {
		t.Errorf("the denial reached the API as %+v", d)
	}

	// Rules are listed per repository and replaced as a whole.
	if list, err := c.Rules(ctx); err != nil || len(list) != 1 || len(list[0].Rules) != 0 {
		t.Errorf("Rules = %+v, %v", list, err)
	}
	if info, err := c.SetRepoRules(ctx, "r1", SetRulesRequest{Rules: []v1.Rule{{Decision: "allow", Kind: "edit", Pattern: "worktree"}}, Revision: 3, Signature: sig}); err != nil || len(info.Rules) != 1 || info.Repo != "storefront" || info.Revision != 4 {
		t.Errorf("SetRepoRules = %+v, %v", info, err)
	}
	if _, r, _ := api.last(); r.Revision != 3 || r.Signature == nil || *r.Signature != *sig {
		t.Errorf("the rule change reached the API as %+v, without its revision or signature", r)
	}
	if _, err := c.SetRepoRules(ctx, "nope", SetRulesRequest{}); err == nil || !strings.Contains(err.Error(), "no such repository") {
		t.Errorf("rules for an unknown repo: err = %v", err)
	}

	stop()
	if err := <-done; err != nil {
		t.Errorf("Serve returned %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("socket file left behind: %v", err)
	}
	if _, err := c.Status(ctx); !errors.Is(err, ErrNoDaemon) {
		t.Errorf("after stop, err = %v, want ErrNoDaemon", err)
	}
}
