package fetch

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/daemon/host"
	"github.com/skylands-ai/skylands/internal/natsembed"
)

type oneRepo host.Repo

func (r oneRepo) Repo(id string) (host.Repo, bool) { return host.Repo(r), id == r.ID }

func TestIssueReadsOnlyAnIssueInTheRepositorysRemotes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	srv, err := natsembed.Start(ctx, natsembed.Config{Name: "fetch", ClientAddr: "127.0.0.1:0", StoreDir: t.TempDir(), Domain: "ws"})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Shutdown()
	nc, err := nats.Connect(srv.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()

	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"remote", "add", "origin", "git@github.com:me/storefront.git"}, {"remote", "add", "upstream", "https://github.com/Org/storefront.git"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	var asked []string
	old := readIssue
	readIssue = func(_ context.Context, in, url string) ([]byte, error) {
		if in != dir {
			t.Errorf("gh ran in %s, not the checkout", in)
		}
		asked = append(asked, url)
		if strings.HasSuffix(url, "/404") {
			return nil, errors.New("gh couldn't read the issue: Could not resolve to an issue")
		}
		return []byte(`{"number":12,"title":"Prices round wrong","body":"Totals are off by a cent.","url":"https://github.com/org/storefront/issues/12","state":"OPEN"}`), nil
	}
	t.Cleanup(func() { readIssue = old })

	r := New("ws", nil, oneRepo{ID: "r1", Name: "storefront", Path: dir}, t.TempDir())
	sub, err := nc.Subscribe(v1.IslandFetchFilter("ws"), func(msg *nats.Msg) { r.Handle(ctx, msg) })
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sub.Unsubscribe() }()
	fetch := func(repoID, url string) (v1.Issue, string) {
		t.Helper()
		data, _ := json.Marshal(v1.IssueRequest{RepoID: repoID, URL: url})
		msg, err := nc.Request(v1.IslandFetchSubject("ws", v1.FetchIssue), data, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		var reply v1.Reply
		_ = json.Unmarshal(msg.Data, &reply)
		var issue v1.Issue
		_ = json.Unmarshal(reply.Result, &issue)
		return issue, reply.Error
	}

	issue, errText := fetch("r1", "https://github.com/org/StoreFront/issues/12")
	if errText != "" || issue.Title != "Prices round wrong" || issue.Body != "Totals are off by a cent." || issue.Number != 12 || issue.URL != "https://github.com/org/StoreFront/issues/12" {
		t.Errorf("issue = %+v, %q", issue, errText)
	}
	for _, c := range []struct{ repo, url, says string }{
		{"r1", "https://github.com/other/storefront/issues/12", "isn't in a repository storefront's remotes point to"},
		{"r1", "https://gitlab.com/org/storefront/issues/12", "isn't in a repository"},
		{"r1", "https://github.com/org/storefront/issues/12 and more", "isn't an issue's link"},
		{"r1", "https://github.com/org/storefront/pull/12", "isn't an issue's link"},
		{"r2", "https://github.com/org/storefront/issues/12", "no repository r2"},
		{"r1", "https://github.com/org/storefront/issues/404", "Could not resolve"},
	} {
		if _, errText := fetch(c.repo, c.url); !strings.Contains(errText, c.says) {
			t.Errorf("%s in %s: %q, want %q", c.url, c.repo, errText, c.says)
		}
	}
	if len(asked) != 2 {
		t.Errorf("gh was asked for %q, want only the issues in the remotes", asked)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal(err)
	}
}
