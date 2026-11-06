package session

// A session's changes sit in its worktree until a person does something
// with them: commits them, merges the branch into its base in the
// registered checkout, or opens a pull request from it. The worktree and
// the branch stay until removed, one at a time or by pruning what ended
// sessions left behind. A session in the checkout has no branch of its
// own, so only a commit applies to it.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/approval"
)

// Changes reports where a session's changes stand on disk.
func (m *Manager) Changes(ctx context.Context, id string) (v1.ChangesResult, error) {
	s, ok := m.Get(id)
	if !ok {
		return v1.ChangesResult{}, fmt.Errorf("%w: %s", ErrUnknownSession, id)
	}
	out := v1.ChangesResult{Branch: s.Branch, Base: s.Base, InCheckout: s.InCheckout, Changed: []string{}, WorktreeRemoved: s.WorktreeRemoved}
	if s.WorktreeRemoved {
		return out, nil
	}
	if _, err := os.Stat(s.Worktree); err != nil {
		return out, fmt.Errorf("the worktree is gone: %s", s.Worktree)
	}
	changed, err := changedFiles(ctx, s.Worktree)
	if err != nil {
		return out, err
	}
	out.Changed = changed
	if !s.InCheckout {
		if out.Ahead, err = ahead(ctx, s.Worktree, s.Base); err != nil {
			return out, err
		}
	}
	return out, nil
}

// Commit handles CommitChanges: everything in the worktree becomes one
// commit on the session's branch. The agent must not be mid-turn.
func (m *Manager) Commit(ctx context.Context, cmd v1.CommitChanges) (Session, error) {
	m.cmdMu.Lock()
	defer m.cmdMu.Unlock()
	s, err := m.forChanges(cmd.ID, cmd.SessionID)
	if err != nil || s.Commit != "" && m.wasHandled(cmd.ID) {
		return s, err
	}
	approver, signature, err := m.signedChange(ctx, s, cmd.Revision, cmd.Signature, approval.ChangeCommit, approval.TextHash(cmd.Message))
	if err != nil {
		return s, err
	}
	message := strings.TrimSpace(cmd.Message)
	if message == "" {
		message = s.Title
	}
	if message == "" {
		message = firstLine(s.Prompt)
	}
	if message == "" {
		message = "Changes from a Skylands session"
	}
	if _, err := git(ctx, s.Worktree, "add", "-A"); err != nil {
		return s, err
	}
	changed, err := changedFiles(ctx, s.Worktree)
	if err != nil {
		return s, err
	}
	if len(changed) == 0 {
		return s, ErrNothingToCommit
	}
	if _, err := git(ctx, s.Worktree, "commit", "-q", "-m", message); err != nil {
		return s, err
	}
	commit, err := git(ctx, s.Worktree, "rev-parse", "HEAD")
	if err != nil {
		return s, err
	}
	files, _ := git(ctx, s.Worktree, "show", "--name-only", "--format=", "HEAD")
	if err := m.record(ctx, s.ID, cmd.ID, v1.EventChangesCommitted, v1.ChangesCommitted{SessionID: s.ID, Commit: commit, Message: message, Files: lines(files), Approver: approver, Signature: signature}); err != nil {
		return Session{}, err
	}
	m.note(s.ID, "Committed "+short(commit)+": "+firstLine(message)+".")
	slog.Info("session: committed", "session", s.ID, "commit", short(commit), "files", len(lines(files)))
	out, _ := m.Get(s.ID)
	return out, nil
}

// Merge handles MergeChanges: the session's branch is merged into its base
// in the registered checkout, which must be on that base and must not
// conflict. A merge that does conflict is aborted and reported.
func (m *Manager) Merge(ctx context.Context, cmd v1.MergeChanges) (Session, error) {
	m.cmdMu.Lock()
	defer m.cmdMu.Unlock()
	s, err := m.forChanges(cmd.ID, cmd.SessionID)
	if err != nil || s.MergedInto != "" && m.wasHandled(cmd.ID) {
		return s, err
	}
	approver, signature, err := m.signedChange(ctx, s, cmd.Revision, cmd.Signature, approval.ChangeMerge, "")
	if err != nil {
		return s, err
	}
	if s.InCheckout {
		return s, ErrInCheckout
	}
	if err := m.committedAndAhead(ctx, s); err != nil {
		return s, err
	}
	repo, ok := m.cfg.Repos.Repo(s.RepoID)
	if !ok {
		return s, fmt.Errorf("%w: %s", ErrUnknownRepo, s.RepoID)
	}
	if s.Base == "HEAD" {
		return s, errors.New("the checkout was on no branch when the session started, so there is nothing to merge into")
	}
	on, err := currentBranch(ctx, repo.Path)
	if err != nil {
		return s, err
	}
	if on != s.Base {
		return s, fmt.Errorf("the checkout is on %s, not %s; switch to %s first", on, s.Base, s.Base)
	}
	if _, err := git(ctx, repo.Path, "merge", "--no-edit", s.Branch); err != nil {
		_, _ = git(ctx, repo.Path, "merge", "--abort")
		return s, err
	}
	commit, err := git(ctx, repo.Path, "rev-parse", "HEAD")
	if err != nil {
		return s, err
	}
	if err := m.record(ctx, s.ID, cmd.ID, v1.EventChangesMerged, v1.ChangesMerged{SessionID: s.ID, Into: s.Base, Commit: commit, Approver: approver, Signature: signature}); err != nil {
		return Session{}, err
	}
	m.note(s.ID, "Merged into "+s.Base+" as "+short(commit)+".")
	slog.Info("session: merged", "session", s.ID, "into", s.Base, "commit", short(commit))
	out, _ := m.Get(s.ID)
	return out, nil
}

// OpenPullRequest handles OpenPullRequest: the session's branch is pushed
// to the repository's origin and a pull request is opened from it with gh,
// which must be installed and signed in on the host.
func (m *Manager) OpenPullRequest(ctx context.Context, cmd v1.OpenPullRequest) (Session, error) {
	m.cmdMu.Lock()
	defer m.cmdMu.Unlock()
	s, err := m.forChanges(cmd.ID, cmd.SessionID)
	if err != nil || s.PullRequest != "" && m.wasHandled(cmd.ID) {
		return s, err
	}
	text, err := approval.PullRequestHash(cmd.Title, cmd.Body)
	if err != nil {
		return s, err
	}
	approver, signature, err := m.signedChange(ctx, s, cmd.Revision, cmd.Signature, approval.ChangePullRequest, text)
	if err != nil {
		return s, err
	}
	if s.InCheckout {
		return s, ErrInCheckout
	}
	if err := m.committedAndAhead(ctx, s); err != nil {
		return s, err
	}
	title := strings.TrimSpace(cmd.Title)
	if title == "" {
		title = s.Title
	}
	if title == "" {
		title = firstLine(s.Prompt)
	}
	body := strings.TrimSpace(cmd.Body)
	if body == "" {
		body = strings.TrimSpace(s.Prompt)
	}
	if _, err := git(ctx, s.Worktree, "push", "-u", "origin", s.Branch); err != nil {
		return s, err
	}
	gh, err := exec.LookPath("gh")
	if err != nil {
		return s, errors.New("the branch " + s.Branch + " was pushed, but gh is not installed on this host, so open the pull request by hand")
	}
	run := exec.CommandContext(ctx, gh, "pr", "create", "--head", s.Branch, "--base", s.Base, "--title", title, "--body", body)
	run.Dir = s.Worktree
	run.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "GIT_TERMINAL_PROMPT=0")
	var stdout, stderr bytes.Buffer
	run.Stdout, run.Stderr = &stdout, &stderr
	if err := run.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return s, errors.New("the branch " + s.Branch + " was pushed, but gh could not open the pull request: " + msg)
	}
	url := ""
	for _, l := range lines(stdout.String()) {
		if strings.HasPrefix(l, "http") {
			url = l
		}
	}
	if url == "" {
		url = strings.TrimSpace(stdout.String())
	}
	if err := m.record(ctx, s.ID, cmd.ID, v1.EventPullRequestOpened, v1.PullRequestOpened{SessionID: s.ID, URL: url, Branch: s.Branch, Approver: approver, Signature: signature}); err != nil {
		return Session{}, err
	}
	m.note(s.ID, "Opened pull request "+url+".")
	slog.Info("session: pull request opened", "session", s.ID, "url", url)
	out, _ := m.Get(s.ID)
	return out, nil
}

// Remove deletes a finished session's worktree and branch. Without force
// a worktree with uncommitted changes is left alone, and so is a branch
// with commits its base lacks, which is then reported as kept.
func (m *Manager) Remove(ctx context.Context, commandID, id string, force bool) (s Session, branchDeleted bool, err error) {
	m.cmdMu.Lock()
	defer m.cmdMu.Unlock()
	m.mu.Lock()
	if handled, ok := m.handled[commandID]; ok && commandID != "" {
		s := *m.sessions[handled]
		m.mu.Unlock()
		return s, false, nil
	}
	cur, ok := m.sessions[id]
	if !ok {
		m.mu.Unlock()
		return Session{}, false, fmt.Errorf("%w: %s", ErrUnknownSession, id)
	}
	s = *cur
	m.mu.Unlock()
	if s.Status == StatusRunning || s.Status == StatusIdle {
		return s, false, ErrOpen
	}
	if s.InCheckout {
		return s, false, ErrInCheckout
	}
	if s.WorktreeRemoved {
		return s, false, nil
	}
	repo, ok := m.cfg.Repos.Repo(s.RepoID)
	if !ok {
		return s, false, fmt.Errorf("%w: %s", ErrUnknownRepo, s.RepoID)
	}
	if _, err := os.Stat(s.Worktree); err == nil {
		args := []string{"worktree", "remove"}
		if force {
			args = append(args, "--force")
		}
		if _, err := git(ctx, repo.Path, append(args, s.Worktree)...); err != nil {
			return s, false, err
		}
	} else {
		// Deleted by hand: git keeps its record until told.
		_, _ = git(ctx, repo.Path, "worktree", "prune")
	}
	flag := "-d"
	if force {
		flag = "-D"
	}
	_, branchErr := git(ctx, repo.Path, "branch", flag, s.Branch)
	branchDeleted = branchErr == nil
	if err := m.record(ctx, s.ID, commandID, v1.EventWorktreeRemoved, v1.WorktreeRemoved{SessionID: s.ID, BranchDeleted: branchDeleted}); err != nil {
		return Session{}, false, err
	}
	if branchDeleted {
		m.note(s.ID, "Worktree and branch removed.")
	} else {
		m.note(s.ID, "Worktree removed; the branch "+s.Branch+" is kept because "+s.Base+" lacks its commits.")
	}
	slog.Info("session: worktree removed", "session", s.ID, "branch", s.Branch, "branchDeleted", branchDeleted)
	out, _ := m.Get(s.ID)
	return out, branchDeleted, nil
}

// Pruned is what Prune did with one session.
type Pruned struct {
	Session       Session
	Removed       bool
	BranchDeleted bool
	// Kept says why the worktree stayed, when it did.
	Kept string
}

// Prune removes the worktrees and branches that finished sessions left
// behind, keeping any with uncommitted changes or unmerged commits, and
// reports each one.
func (m *Manager) Prune(ctx context.Context) []Pruned {
	var out []Pruned
	for _, s := range m.List() {
		if s.InCheckout || s.WorktreeRemoved || s.Status == StatusRunning || s.Status == StatusIdle {
			continue
		}
		after, branchDeleted, err := m.Remove(ctx, "", s.ID, false)
		if err != nil {
			out = append(out, Pruned{Session: s, Kept: err.Error()})
			continue
		}
		out = append(out, Pruned{Session: after, Removed: true, BranchDeleted: branchDeleted})
	}
	return out
}

// signedChange checks a signed commit, merge, or pull request: its
// signature over the action, the session's revision, and text, which is
// as approval.Change describes.
func (m *Manager) signedChange(ctx context.Context, s Session, revision int, sig *v1.DecisionSignature, action, text string) (string, string, error) {
	return m.signedAt(ctx, s, revision, sig, action+" in "+s.ID, func(world string) approval.Decision {
		return approval.Change{V: approval.DecisionVersion, Kind: approval.KindChange, World: world, Host: m.cfg.Host, Session: s.ID, Revision: s.Revision, Action: action, Text: text}
	})
}

// forChanges finds the session a changes command is for and checks that
// its worktree can be worked on: the session is known, its agent is not
// mid-turn, and the worktree is still there.
func (m *Manager) forChanges(commandID, id string) (Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if handled, ok := m.handled[commandID]; ok && commandID != "" {
		return *m.sessions[handled], nil
	}
	s, ok := m.sessions[id]
	if !ok {
		return Session{}, fmt.Errorf("%w: %s", ErrUnknownSession, id)
	}
	switch {
	case s.Status == StatusRunning:
		return *s, ErrBusy
	case s.WorktreeRemoved:
		return *s, ErrNoWorktree
	}
	if _, err := os.Stat(s.Worktree); err != nil {
		return *s, fmt.Errorf("the worktree is gone: %s", s.Worktree)
	}
	return *s, nil
}

// wasHandled says whether a command id is already in the history, in
// which case forChanges returned the session as it stands and nothing
// more should happen.
func (m *Manager) wasHandled(commandID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.handled[commandID]
	return ok && commandID != ""
}

// committedAndAhead checks that a worktree session has nothing
// uncommitted and that its branch has commits to merge.
func (m *Manager) committedAndAhead(ctx context.Context, s Session) error {
	changed, err := changedFiles(ctx, s.Worktree)
	if err != nil {
		return err
	}
	if len(changed) > 0 {
		return ErrUncommitted
	}
	n, err := ahead(ctx, s.Worktree, s.Base)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNothingToMerge
	}
	return nil
}

// note appends a line to the transcript, set off by a rule, where the
// conversation shows it between the messages.
func (m *Manager) note(id, text string) {
	m.transcript(id).Append("\n\n---\n\n*" + text + "*\n\n")
}

// git runs one git command in dir and returns its output without the
// trailing newline, or an error carrying what git said. Leading spaces
// stay: git status --porcelain starts a line with one. It never prompts.
func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_EDITOR=true")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", args[0], msg)
	}
	return strings.TrimRight(stdout.String(), "\r\n\t "), nil
}

// currentBranch is the branch dir is on, or "HEAD" when it is on none.
func currentBranch(ctx context.Context, dir string) (string, error) {
	return git(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
}

// changedFiles lists the files in dir with uncommitted changes, staged or
// not, including untracked ones.
func changedFiles(ctx context.Context, dir string) ([]string, error) {
	out, err := git(ctx, dir, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	files := []string{}
	for _, l := range strings.Split(out, "\n") {
		if len(l) < 4 {
			continue
		}
		path := l[3:]
		if _, to, ok := strings.Cut(path, " -> "); ok {
			path = to
		}
		files = append(files, strings.Trim(path, "\""))
	}
	return files, nil
}

// ahead counts the commits in dir's HEAD that base lacks.
func ahead(ctx context.Context, dir, base string) (int, error) {
	if base == "" || base == "HEAD" {
		return 0, nil
	}
	out, err := git(ctx, dir, "rev-list", "--count", base+"..HEAD")
	if err != nil {
		return 0, err
	}
	var n int
	_, err = fmt.Sscan(out, &n)
	return n, err
}

func lines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}

// short is the usual seven characters of a commit hash.
func short(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}
