package fetch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"

	"github.com/nats-io/nats.go"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/daemon/terrain"
	"github.com/skylands-ai/skylands/internal/issuelink"
)

// readIssue reads the issue at url with gh in dir, as JSON with its
// number, title, body, URL, and state. Tests replace it.
var readIssue = func(ctx context.Context, dir, url string) ([]byte, error) {
	gh, err := exec.LookPath("gh")
	if err != nil {
		return nil, errNoGH
	}
	cmd := exec.CommandContext(ctx, gh, "issue", "view", url, "--json", "number,title,body,url,state")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "GIT_TERMINAL_PROMPT=0", "NO_COLOR=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, errors.New("gh couldn't read the issue: " + msg)
	}
	return stdout.Bytes(), nil
}

var errNoGH = errors.New("gh isn't installed on this host, so it can't read the issue")

// issue answers an issue fetch: the issue at the request's URL, read with
// the host's gh, when it is in a repository one of the checkout's remotes
// points to. Nothing else is read, so the fetch can't be turned on
// another repository's issues, or on any other page.
func (r *Responder) issue(ctx context.Context, msg *nats.Msg) {
	var req v1.IssueRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil || req.RepoID == "" || req.URL == "" {
		r.reply(msg, v1.Reply{Error: "a repoId and an issue's URL are required"})
		return
	}
	repo, ok := r.repos.Repo(req.RepoID)
	if !ok {
		r.reply(msg, v1.Reply{Error: "no repository " + req.RepoID + " on " + r.host})
		return
	}
	l, ok := issuelink.Parse(req.URL)
	if !ok {
		r.reply(msg, v1.Reply{Error: req.URL + " isn't an issue's link"})
		return
	}
	ctx, cancel := context.WithTimeout(ctx, v1.IssueTimeout)
	defer cancel()
	// The remotes as the checkout has them now, not as last recorded.
	remotes, err := terrain.Remotes(ctx, repo.Path)
	if err != nil {
		r.reply(msg, v1.Reply{Error: err.Error()})
		return
	}
	if !l.InAny(remotes) {
		r.reply(msg, v1.Reply{Error: l.Ref() + " isn't in a repository " + repo.Name + "'s remotes point to"})
		return
	}
	data, err := readIssue(ctx, repo.Path, l.URL())
	var issue v1.Issue
	if err == nil {
		err = json.Unmarshal(data, &issue)
	}
	if err != nil {
		if errors.Is(err, errNoGH) {
			err = errors.New("gh isn't installed on " + r.host + ", so it can't read the issue")
		}
		r.reply(msg, v1.Reply{Error: err.Error()})
		return
	}
	// The link is the one asked for, whatever gh says it is now, such as
	// after the issue moved to another repository.
	issue.URL = l.URL()
	out, _ := json.Marshal(issue)
	r.reply(msg, v1.Reply{Result: out})
}
