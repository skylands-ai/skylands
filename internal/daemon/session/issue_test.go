package session

import (
	"errors"
	"testing"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/daemon/host"
)

func TestCheckIssueTakesOnlyAnIssueInTheRepository(t *testing.T) {
	repo := host.Repo{Name: "storefront", Remotes: []v1.RepoRemote{{Name: "origin", Host: "github.com", Path: "org/storefront"}}}
	for _, ok := range []string{"", "https://github.com/org/storefront/issues/12", "https://github.com/Org/StoreFront/issues/3"} {
		if err := checkIssue(ok, repo); err != nil {
			t.Errorf("checkIssue(%q) = %v", ok, err)
		}
	}
	if err := checkIssue("https://github.com/org/other/issues/12", repo); !errors.Is(err, ErrIssueElsewhere) {
		t.Errorf("an issue elsewhere = %v, want ErrIssueElsewhere", err)
	}
	for _, bad := range []string{"https://github.com/org/storefront/issues/12#top", "see https://github.com/org/storefront/issues/12", "https://github.com/org/storefront/pull/12"} {
		if err := checkIssue(bad, repo); err == nil || errors.Is(err, ErrIssueElsewhere) {
			t.Errorf("checkIssue(%q) = %v, want a refusal of the link", bad, err)
		}
	}
	if err := checkIssue("https://github.com/org/storefront/issues/12", host.Repo{}); !errors.Is(err, ErrIssueElsewhere) {
		t.Errorf("a repository with no remotes = %v, want ErrIssueElsewhere", err)
	}
}

func TestClosesIssueEndsAWrittenBody(t *testing.T) {
	const issue = "https://github.com/org/storefront/issues/12"
	for _, c := range []struct{ body, issue, want string }{
		{"Rounds totals once.\n", issue, "Rounds totals once.\n\nCloses org/storefront#12"},
		{"", issue, "Closes org/storefront#12"},
		{"Rounds totals once.", "", "Rounds totals once."},
		{"Rounds totals once.", "not a link", "Rounds totals once."},
	} {
		if got := closesIssue(c.body, c.issue); got != c.want {
			t.Errorf("closesIssue(%q, %q) = %q, want %q", c.body, c.issue, got, c.want)
		}
	}
}
