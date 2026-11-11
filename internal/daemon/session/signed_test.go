package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/nats-io/nkeys"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/approval"
	"github.com/skylands-ai/skylands/internal/fakeagent"
)

// TestPromptsAndChangesMustBeSigned starts a session, prompts it, and
// commits and merges its changes in a world, where each must be signed by
// an approver over what it does and the session's revision, so a signature
// can't be used for anything else or used again.
func TestPromptsAndChangesMustBeSigned(t *testing.T) {
	f := setup(t)
	gitIdentity(t)
	owner, _ := nkeys.CreateOperator()
	world, _ := owner.PublicKey()
	kp, _ := nkeys.CreateUser()
	pub, _ := kp.PublicKey()
	f.cfg.World = func() string { return world }
	f.cfg.VerifyDecision = func(_ context.Context, d approval.Decision, s v1.DecisionSignature) error {
		return approval.VerifyDecision(d, s)
	}
	m, err := Load(f.ctx, f.store, f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Wait()
	sign := func(d approval.Decision) *v1.DecisionSignature {
		t.Helper()
		s, err := approval.SignDecision(kp, d)
		if err != nil {
			t.Fatal(err)
		}
		return &s
	}
	const q = fakeagent.SettingsQuestion // answered without a tool call

	// A start is signed over its session, repository, agent, and prompt.
	start := v1.StartSession{ID: "c1", SessionID: "s1", RepoID: f.repo.ID, Agent: "fake", Prompt: q}
	startAs := func(prompt string) approval.Decision {
		return approval.Start{V: approval.DecisionVersion, Kind: approval.KindStart, World: world, Host: "ws", Session: "s1", Repo: f.repo.ID, Agent: "fake", Prompt: approval.TextHash(prompt)}
	}
	if _, err := m.Start(f.ctx, start); !errors.Is(err, approval.ErrUnsigned) {
		t.Errorf("an unsigned start: err = %v", err)
	}
	start.Signature = sign(startAs("something else"))
	if _, err := m.Start(f.ctx, start); !errors.Is(err, approval.ErrBadSignature) {
		t.Errorf("a start signed over another prompt: err = %v", err)
	}
	start.Signature = sign(startAs(q))
	if _, err := m.Start(f.ctx, start); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, m, "s1", StatusIdle)
	again := start
	again.ID = "c2"
	if _, err := m.Start(f.ctx, again); !errors.Is(err, ErrSessionExists) {
		t.Errorf("the signed start sent again: err = %v", err)
	}

	// A prompt is signed over its text and the session's revision.
	promptAs := func(revision int, text string) approval.Decision {
		return approval.Prompt{V: approval.DecisionVersion, Kind: approval.KindPrompt, World: world, Host: "ws", Session: "s1", Revision: revision, Prompt: approval.TextHash(text)}
	}
	for _, tc := range []struct {
		name     string
		revision int
		sig      *v1.DecisionSignature
		want     error
	}{
		{"unsigned", 1, nil, approval.ErrUnsigned},
		{"at an old revision", 0, sign(promptAs(0, q)), ErrStale},
		{"over another text", 1, sign(promptAs(1, "something else")), approval.ErrBadSignature},
	} {
		if _, err := m.Prompt(f.ctx, v1.SendPrompt{ID: "p-" + tc.name, SessionID: "s1", Text: q, Revision: tc.revision, Signature: tc.sig}); !errors.Is(err, tc.want) {
			t.Errorf("a prompt %s: err = %v, want %v", tc.name, err, tc.want)
		}
	}
	signed := sign(promptAs(1, q))
	if _, err := m.Prompt(f.ctx, v1.SendPrompt{ID: "p1", SessionID: "s1", Text: q, Revision: 1, Signature: signed}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, m, "s1", func(s Session) bool { return s.Status == StatusIdle && s.Turns == 2 && s.Revision == 2 })
	if _, err := m.Prompt(f.ctx, v1.SendPrompt{ID: "p2", SessionID: "s1", Text: q, Revision: 1, Signature: signed}); !errors.Is(err, ErrStale) {
		t.Errorf("the signed prompt sent again: err = %v", err)
	}

	// A commit, a merge, and a pull request are signed over what they do
	// and the session's revision, which each moves on.
	s, _ := m.Get("s1")
	if err := os.WriteFile(filepath.Join(s.Worktree, "new.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changeAs := func(revision int, action, text string) approval.Decision {
		return approval.Change{V: approval.DecisionVersion, Kind: approval.KindChange, World: world, Host: "ws", Session: "s1", Revision: revision, Action: action, Text: text}
	}
	if _, err := m.Commit(f.ctx, v1.CommitChanges{ID: "k0", SessionID: "s1", Message: "Add new", Revision: 2}); !errors.Is(err, approval.ErrUnsigned) {
		t.Errorf("an unsigned commit: err = %v", err)
	}
	if _, err := m.Commit(f.ctx, v1.CommitChanges{ID: "k1", SessionID: "s1", Message: "Add new", Revision: 2, Signature: sign(changeAs(2, approval.ChangeCommit, approval.TextHash("Add new")))}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Merge(f.ctx, v1.MergeChanges{ID: "k2", SessionID: "s1", Revision: 2, Signature: sign(changeAs(2, approval.ChangeMerge, ""))}); !errors.Is(err, ErrStale) {
		t.Errorf("a merge signed before the commit: err = %v", err)
	}
	if _, err := m.Merge(f.ctx, v1.MergeChanges{ID: "k3", SessionID: "s1", Revision: 3, Signature: sign(changeAs(3, approval.ChangeMerge, ""))}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.OpenPullRequest(f.ctx, v1.OpenPullRequest{ID: "k4", SessionID: "s1", Title: "Add new", Revision: 4}); !errors.Is(err, approval.ErrUnsigned) {
		t.Errorf("an unsigned pull request: err = %v", err)
	}
	other, _ := approval.PullRequestHash("Something else", "")
	if _, err := m.OpenPullRequest(f.ctx, v1.OpenPullRequest{ID: "k5", SessionID: "s1", Title: "Add new", Revision: 4, Signature: sign(changeAs(4, approval.ChangePullRequest, other))}); !errors.Is(err, approval.ErrBadSignature) {
		t.Errorf("a pull request signed over another title: err = %v", err)
	}

	// Once the session is over, removing its worktree is signed too, over
	// the session's revision, which ending it doesn't move.
	if _, err := m.Stop(f.ctx, v1.StopAgent{ID: "c9", SessionID: "s1"}); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, m, "s1", StatusEnded)
	ended, _ := m.Get("s1")
	for _, tc := range []struct {
		name     string
		revision int
		sig      *v1.DecisionSignature
		want     error
	}{
		{"unsigned", ended.Revision, nil, approval.ErrUnsigned},
		{"at an old revision", ended.Revision - 1, sign(changeAs(ended.Revision-1, approval.ChangeRemove, "")), ErrStale},
		{"signed as a merge", ended.Revision, sign(changeAs(ended.Revision, approval.ChangeMerge, "")), approval.ErrBadSignature},
	} {
		if _, _, err := m.RemoveWorktree(f.ctx, v1.RemoveWorktree{ID: "r-" + tc.name, SessionID: "s1", Revision: tc.revision, Signature: tc.sig}); !errors.Is(err, tc.want) {
			t.Errorf("a removal %s: err = %v, want %v", tc.name, err, tc.want)
		}
	}
	removed, branchDeleted, err := m.RemoveWorktree(f.ctx, v1.RemoveWorktree{ID: "r1", SessionID: "s1", Revision: ended.Revision, Signature: sign(changeAs(ended.Revision, approval.ChangeRemove, ""))})
	if err != nil || !removed.WorktreeRemoved || !branchDeleted {
		t.Fatalf("RemoveWorktree = %+v, %v, %v; want the merged branch gone with the worktree", removed, branchDeleted, err)
	}

	// Each records who signed it.
	signedBy := map[string]int{}
	if err := f.store.Replay(f.ctx, v1.IslandStreamName("ws"), []string{v1.SessionEventFilter("ws")}, func(_ uint64, ev v1.Event) error {
		var d struct {
			Approver  string `json:"approver"`
			Signature string `json:"signature"`
		}
		_ = ev.Decode(&d)
		if d.Approver == pub && d.Signature != "" {
			signedBy[ev.Type]++
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if signedBy[v1.EventPromptSent] != 2 || signedBy[v1.EventChangesCommitted] != 1 || signedBy[v1.EventChangesMerged] != 1 || signedBy[v1.EventWorktreeRemoved] != 1 {
		t.Errorf("events signed by the approver = %v", signedBy)
	}
}
