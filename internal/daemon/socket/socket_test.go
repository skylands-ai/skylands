package socket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
func (f *fakeAPI) AnswerQuestion(_ context.Context, id, _ string, answers json.RawMessage, decline bool) (QuestionInfo, error) {
	if id != "q1" {
		return QuestionInfo{}, session.ErrUnknownQuestion
	}
	return QuestionInfo{ID: id, Message: string(answers), Agent: fmt.Sprint(decline)}, nil
}
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
func (f *fakeAPI) SetRepoRules(_ context.Context, repoID, _ string, rules []v1.Rule) (RepoRulesInfo, error) {
	for _, r := range f.repos {
		if r.ID == repoID {
			f.rules = rules
			return RepoRulesInfo{RepoID: r.ID, Repo: r.Name, Rules: rules}, nil
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
func (f *fakeAPI) Approve(_ context.Context, requestID, _ string) (RequestInfo, error) {
	return RequestInfo{ID: requestID}, nil
}
func (f *fakeAPI) Deny(_ context.Context, requestID, _ string) (RequestInfo, error) {
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
	if q, err := c.AnswerQuestion(ctx, "q1", json.RawMessage(`{"question_0":"a.go"}`)); err != nil || q.Message != `{"question_0":"a.go"}` || q.Agent != "false" {
		t.Errorf("AnswerQuestion = %+v, %v", q, err)
	}
	if q, err := c.AnswerQuestion(ctx, "q1", nil); err != nil || q.Agent != "true" {
		t.Errorf("decline = %+v, %v", q, err)
	}
	if _, err := c.AnswerQuestion(ctx, "nope", nil); err == nil || !strings.Contains(err.Error(), "no such question") {
		t.Errorf("answer an unknown question: err = %v", err)
	}

	// Rules are listed per repository and replaced as a whole.
	if list, err := c.Rules(ctx); err != nil || len(list) != 1 || len(list[0].Rules) != 0 {
		t.Errorf("Rules = %+v, %v", list, err)
	}
	if info, err := c.SetRepoRules(ctx, "r1", []v1.Rule{{Decision: "allow", Kind: "edit", Pattern: "worktree"}}); err != nil || len(info.Rules) != 1 || info.Repo != "storefront" {
		t.Errorf("SetRepoRules = %+v, %v", info, err)
	}
	if _, err := c.SetRepoRules(ctx, "nope", nil); err == nil || !strings.Contains(err.Error(), "no such repository") {
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
