package describe

import (
	"strings"
	"testing"
)

func TestRequestsCarryOnlyTheChanges(t *testing.T) {
	commit := CommitRequest(" a.go | 2 +-", "diff --git a/a.go b/a.go\n-old\n+new")
	if !IsCommitRequest(commit) || IsPullRequestRequest(commit) {
		t.Errorf("commit request not recognized: %q", commit)
	}
	if !strings.Contains(commit, " a.go | 2 +-") || !strings.HasSuffix(commit, "diff --git a/a.go b/a.go\n-old\n+new") {
		t.Errorf("commit request = %q", commit)
	}
	pr := PullRequestRequest(Log([]Commit{{Subject: "Add a", Body: "Because.\nReally."}, {Subject: "Fix a"}}), " a.go | 2 +-", "diff --git a/a.go b/a.go")
	if !IsPullRequestRequest(pr) || IsCommitRequest(pr) {
		t.Errorf("pull request request not recognized: %q", pr)
	}
	if !strings.Contains(pr, "- Add a\n\n  Because.\n  Really.\n\n- Fix a\n\n") {
		t.Errorf("pull request request = %q", pr)
	}
}

func TestALongDiffIsCutAtALine(t *testing.T) {
	line := strings.Repeat("x", 99) + "\n"
	diff := strings.Repeat(line, MaxDiff/len(line)+10)
	req := CommitRequest("stat", diff)
	if len(req) > MaxDiff+1000 || !strings.Contains(req, "\n\n[The diff is cut here. It runs to ") {
		t.Fatalf("request is %d bytes, ends %q", len(req), req[len(req)-80:])
	}
	kept := req[strings.Index(req, "The diff:\n\n")+len("The diff:\n\n") : strings.Index(req, "\n\n[The diff")]
	if strings.Trim(kept, "x\n") != "" || !strings.HasSuffix(kept, "x") || len(kept)%len(line) != len(line)-1 {
		t.Errorf("the cut fell inside a line: %d bytes kept", len(kept))
	}
}

func TestClean(t *testing.T) {
	for _, c := range []struct{ reply, subject, body string }{
		{"Add retries to the upload client", "Add retries to the upload client", ""},
		{"Add retries.\n\nThe client gave up\non the first error.\n", "Add retries", "The client gave up\non the first error."},
		{"```\nAdd retries\n\nBody here.\n```", "Add retries", "Body here."},
		{"```text\nSubject: Add retries\n\nBody: Body here.\n```", "Add retries", "Body here."},
		{"Title: \"Add retries\"\r\n\r\nDescription: Some *Markdown*.", "Add retries", "Some *Markdown*."},
		{"# Add retries\n\n## Summary\n\n- one", "Add retries", "## Summary\n\n- one"},
	} {
		subject, body, ok := Clean(c.reply)
		if !ok || subject != c.subject || body != c.body {
			t.Errorf("Clean(%q) = %q, %q, %v; want %q, %q", c.reply, subject, body, ok, c.subject, c.body)
		}
	}
	for _, bad := range []string{"", "   \n ", "```\n```", strings.Repeat("word ", 30), "Fine\n\n" + strings.Repeat("x", maxReply)} {
		if s, b, ok := Clean(bad); ok {
			t.Errorf("Clean(%.40q) = %q, %q, usable", bad, s, b)
		}
	}
}

func TestCommitMessageFromTheFilesAlone(t *testing.T) {
	for _, c := range []struct{ nameStatus, want string }{
		{"A\tnew.go", "Add new.go"},
		{"M\ta.go", "Update a.go"},
		{"D\told.go", "Delete old.go"},
		{"R100\tfrom.go\tto.go", "Rename from.go to to.go"},
		{"A\tx.go\nA\ty.go", "Add 2 files\n\nAdd x.go\nAdd y.go"},
		{"M\ta.go\nA\tnew.go\n", "Change 2 files\n\nUpdate a.go\nAdd new.go"},
		{"", "Update files"},
	} {
		if got := Message(CommitMessage(c.nameStatus)); got != c.want {
			t.Errorf("CommitMessage(%q) = %q, want %q", c.nameStatus, got, c.want)
		}
	}
	var many []string
	for i := range maxListed + 3 {
		many = append(many, "M\tf"+string(rune('a'+i%26))+".go")
	}
	got := Message(CommitMessage(strings.Join(many, "\n")))
	if !strings.HasPrefix(got, "Update 53 files\n\n") || !strings.HasSuffix(got, "\nand 3 more files") || strings.Count(got, "\n") != maxListed+2 {
		t.Errorf("CommitMessage of %d files = %q", len(many), got)
	}
}

func TestPullRequestFromTheCommitsAlone(t *testing.T) {
	commits := ParseLog("Add a\n\nBecause.\n\x00Fix a\n\x00")
	if len(commits) != 2 || commits[0] != (Commit{Subject: "Add a", Body: "Because."}) || commits[1] != (Commit{Subject: "Fix a"}) {
		t.Fatalf("ParseLog = %+v", commits)
	}
	if title, body := PullRequest(commits[:1]); title != "Add a" || body != "Because." {
		t.Errorf("one commit = %q, %q", title, body)
	}
	if title, body := PullRequest(commits); title != "Add a" || body != "- Add a\n- Fix a" {
		t.Errorf("two commits = %q, %q", title, body)
	}
}

func TestFakeNamesTheFilesInTheDiff(t *testing.T) {
	req := CommitRequest(" a.go | 1 +\n new.go | 1 +", "diff --git a/a.go b/a.go\n+x\ndiff --git a/new.go b/new.go\n+y")
	if got := Fake(req); got != "Change a.go, new.go\n\nWritten from the diff alone." {
		t.Errorf("Fake commit = %q", got)
	}
	if got := Fake(PullRequestRequest("- Add a", "", "diff --git a/a.go b/a.go")); !strings.HasPrefix(got, "Change a.go\n\nA pull request") {
		t.Errorf("Fake pull request = %q", got)
	}
}
