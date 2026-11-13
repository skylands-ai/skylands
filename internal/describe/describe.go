// Package describe writes the text that goes with a session's changes when
// a person leaves it to Skylands: a commit's message, and a pull request's
// title and body. The text comes from the changes alone, never from the
// conversation that made them, since a prompt or a title taken from it can
// carry what was never meant for a repository or a public pull request.
//
// The agent writes it in a side session of its own that is given only the
// diff, and the branch's commit messages for a pull request. Without an
// agent, or when its reply is unusable, git's own account of the changes
// stands in: the files a commit changes, or the branch's commit messages.
package describe

import (
	"fmt"
	"strings"
)

// Markers open each request, so a test agent can tell one apart.
const (
	commitMarker = "Write a commit message for the changes below."
	pullMarker   = "Write a pull request title and description for the commits below."
)

// MaxDiff is the most of a diff a request carries, in bytes. A longer diff
// is cut at a line, after the summary of changed files, which goes whole.
const MaxDiff = 40_000

// MaxSubject is the longest subject line, in characters, that is kept from
// an agent's reply. A longer first line is more likely a paragraph than a
// subject, so the reply is unusable.
const MaxSubject = 120

// maxReply is the longest reply kept, in bytes.
const maxReply = 20_000

// maxListed is how many files or commits a fallback lists.
const maxListed = 50

// CommitRequest builds the prompt asking for a commit message for staged
// changes: stat is git's summary of the changed files, diff the patch.
func CommitRequest(stat, diff string) string {
	return commitMarker + " Reply with only the message: a subject line of at most 72 characters in the imperative mood, such as \"Add retries to the upload client\", then, if the changes need explaining, a blank line and a short plain-text body wrapped at 72 characters. Describe only what the changes do. Do not run any tools or read any files.\n\n" + changes(stat, diff)
}

// PullRequestRequest builds the prompt asking for a pull request's title
// and body: log is the branch's commit messages, oldest first, and stat and
// diff are as in CommitRequest, for the whole branch.
func PullRequestRequest(log, stat, diff string) string {
	return pullMarker + " Reply with only the title on the first line, at most 72 characters of plain text, then a blank line and the description in Markdown: what the changes do, and why where the commits say. Describe only what the commits and the diff hold. Do not run any tools or read any files.\n\nThe commits, oldest first:\n\n" + strings.TrimSpace(log) + "\n\n" + changes(stat, diff)
}

// changes is the part of a request that holds the diff, cut to MaxDiff.
func changes(stat, diff string) string {
	diff = strings.TrimSpace(diff)
	if len(diff) > MaxDiff {
		cut := strings.LastIndexByte(diff[:MaxDiff], '\n')
		if cut < 0 {
			cut = MaxDiff
		}
		diff = diff[:cut] + fmt.Sprintf("\n\n[The diff is cut here. It runs to %d bytes.]", len(diff))
	}
	return "The files changed:\n\n" + strings.Trim(stat, "\n") + "\n\nThe diff:\n\n" + diff
}

// IsCommitRequest reports whether a prompt asks for a commit message.
func IsCommitRequest(prompt string) bool {
	return strings.HasPrefix(strings.TrimSpace(prompt), commitMarker)
}

// IsPullRequestRequest reports whether a prompt asks for a pull request's
// title and body.
func IsPullRequestRequest(prompt string) bool {
	return strings.HasPrefix(strings.TrimSpace(prompt), pullMarker)
}

// Clean turns an agent's reply into a subject line and a body, which may
// be empty. ok is false when the reply is unusable: empty, too long, or
// starting with a line too long for a subject.
func Clean(reply string) (subject, body string, ok bool) {
	text := strings.TrimSpace(strings.ReplaceAll(reply, "\r\n", "\n"))
	if len(text) > maxReply {
		return "", "", false
	}
	// A reply wrapped in a code fence is taken from inside it.
	if strings.HasPrefix(text, "```") {
		_, inside, _ := strings.Cut(text, "\n")
		text = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(inside), "```"))
	}
	first, rest, _ := strings.Cut(text, "\n")
	subject = strings.TrimSpace(first)
	for _, label := range []string{"Subject:", "Title:"} {
		subject = strings.TrimSpace(strings.TrimPrefix(subject, label))
	}
	subject = strings.TrimLeft(subject, "# ")
	subject = strings.Trim(subject, " \t\"'`*_")
	subject = strings.TrimSpace(strings.TrimRight(subject, "."))
	if subject == "" || len([]rune(subject)) > MaxSubject {
		return "", "", false
	}
	body = strings.TrimSpace(rest)
	for _, label := range []string{"Body:", "Description:"} {
		body = strings.TrimSpace(strings.TrimPrefix(body, label))
	}
	return subject, body, true
}

// Message joins a subject and a body into a commit message.
func Message(subject, body string) string {
	if body == "" {
		return subject
	}
	return subject + "\n\n" + body
}

// CommitMessage writes a commit message from the staged changes alone,
// given as git diff --cached --name-status prints them: what happened to
// one file, or how many changed over what happened to each.
func CommitMessage(nameStatus string) (subject, body string) {
	var changes []string
	verbs := map[string]bool{}
	for l := range strings.SplitSeq(nameStatus, "\n") {
		f := strings.Split(strings.TrimSpace(l), "\t")
		if len(f) < 2 || f[0] == "" {
			continue
		}
		status, path := f[0][0], f[len(f)-1]
		verb := "Update"
		switch {
		case status == 'A':
			verb = "Add"
		case status == 'D':
			verb = "Delete"
		case status == 'R' && len(f) >= 3:
			verb, path = "Rename", f[1]+" to "+f[2]
		case status == 'C' && len(f) >= 3:
			verb, path = "Copy", f[1]+" to "+f[2]
		}
		verbs[verb] = true
		changes = append(changes, verb+" "+path)
	}
	switch len(changes) {
	case 0:
		return "Update files", ""
	case 1:
		return changes[0], ""
	}
	verb := "Change"
	if len(verbs) == 1 {
		for v := range verbs {
			verb = v
		}
	}
	return fmt.Sprintf("%s %d files", verb, len(changes)), list(changes, "", "files")
}

// Commit is one commit on a branch.
type Commit struct {
	Subject string
	Body    string
}

// ParseLog reads git log -z --format=%B: each commit's message, NUL
// after each.
func ParseLog(out string) []Commit {
	var commits []Commit
	for msg := range strings.SplitSeq(out, "\x00") {
		msg = strings.TrimSpace(msg)
		if msg == "" {
			continue
		}
		subject, body, _ := strings.Cut(msg, "\n")
		commits = append(commits, Commit{Subject: strings.TrimSpace(subject), Body: strings.TrimSpace(body)})
	}
	return commits
}

// Log writes the branch's commits, oldest first, as a request carries
// them.
func Log(commits []Commit) string {
	parts := make([]string, 0, len(commits))
	for _, c := range commits {
		parts = append(parts, "- "+indent(Message(c.Subject, c.Body)))
	}
	return strings.Join(parts, "\n\n")
}

// PullRequest writes a pull request's title and body from the branch's
// commits alone, oldest first: one commit's message, or the first commit's
// subject over a list of every commit's.
func PullRequest(commits []Commit) (title, body string) {
	switch len(commits) {
	case 0:
		return "Changes", ""
	case 1:
		return commits[0].Subject, commits[0].Body
	}
	subjects := make([]string, 0, len(commits))
	for _, c := range commits {
		subjects = append(subjects, c.Subject)
	}
	return commits[0].Subject, list(subjects, "- ", "commits")
}

// list writes items one to a line, each after prefix, the first maxListed
// of them and then how many more there are.
func list(items []string, prefix, what string) string {
	var b strings.Builder
	for i, it := range items {
		if i == maxListed {
			fmt.Fprintf(&b, "%sand %d more %s\n", prefix, len(items)-maxListed, what)
			break
		}
		b.WriteString(prefix)
		b.WriteString(it)
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

// indent puts every line after the first under a list item's text,
// leaving blank lines blank.
func indent(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if i > 0 && l != "" {
			lines[i] = "  " + l
		}
	}
	return strings.Join(lines, "\n")
}

// Fake is what a test agent answers a request with: a subject naming the
// files in the diff, which tells a written description apart from the
// fallback, and a body saying where it came from.
func Fake(request string) string {
	var files []string
	for l := range strings.SplitSeq(request, "\n") {
		if rest, ok := strings.CutPrefix(l, "diff --git a/"); ok {
			if a, _, ok := strings.Cut(rest, " b/"); ok {
				files = append(files, a)
			}
		}
	}
	subject := "Change " + strings.Join(files, ", ")
	if len(files) == 0 {
		subject = "Change nothing"
	}
	if IsPullRequestRequest(request) {
		return subject + "\n\nA pull request written from its commits and diff."
	}
	return subject + "\n\nWritten from the diff alone."
}
