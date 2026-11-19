package issuelink

import (
	"testing"

	v1 "github.com/skylands-ai/skylands/api/v1"
)

func TestFindReadsTheFirstIssueLink(t *testing.T) {
	storefront := Link{Host: "github.com", Owner: "Org", Repo: "storefront", Number: 12}
	for text, want := range map[string]Link{
		"https://github.com/Org/storefront/issues/12":                                storefront,
		"fix https://github.com/Org/storefront/issues/12 today":                      storefront,
		"(https://github.com/Org/storefront/issues/12)":                              storefront,
		"https://github.com/Org/storefront/issues/12#issuecomment-99":                storefront,
		"https://github.com/Org/storefront/issues/12?q=1":                            storefront,
		"https://github.com/Org/storefront/issues/12/ and https://x.io/a/b/issues/3": storefront,
		"see:\nhttps://GitHub.com/Org/storefront.git/issues/12":                      storefront,
		"https://ghe.corp.example/team/api.v2/issues/7":                              {Host: "ghe.corp.example", Owner: "team", Repo: "api.v2", Number: 7},
	} {
		got, ok := Find(text)
		if !ok || got != want {
			t.Errorf("Find(%q) = %+v, %v; want %+v", text, got, ok, want)
		}
	}
	for _, text := range []string{
		"",
		"fix the pricing bug",
		"http://github.com/org/storefront/issues/12",
		"https://github.com/org/storefront/pull/12",
		"https://github.com/org/storefront/issues",
		"https://github.com/org/storefront/issues/0",
		"https://github.com/org/storefront/issues/x",
		"https://github.com/org/issues/12",
		"xhttps://github.com/org/storefront/issues/12",
		"https://github.com/org/storefront/issues/12345678901",
		"https://github.com/org/storefront/issues/12abc",
	} {
		if got, ok := Find(text); ok {
			t.Errorf("Find(%q) = %+v, want none", text, got)
		}
	}
}

func TestParseTakesALinkAndNothingElse(t *testing.T) {
	if l, ok := Parse("https://github.com/org/storefront/issues/12"); !ok || l.Ref() != "org/storefront#12" {
		t.Errorf("Parse = %+v, %v", l, ok)
	}
	for _, raw := range []string{"see https://github.com/org/storefront/issues/12", "https://github.com/org/storefront/issues/12 ", "https://github.com/org/storefront/issues/12\nmore"} {
		if l, ok := Parse(raw); ok {
			t.Errorf("Parse(%q) = %+v, want none", raw, l)
		}
	}
}

func TestLinkNamesItsIssueAndRepository(t *testing.T) {
	l := Link{Host: "github.com", Owner: "Org", Repo: "storefront", Number: 12}
	if l.URL() != "https://github.com/Org/storefront/issues/12" || l.Ref() != "Org/storefront#12" || l.CloneURL() != "https://github.com/Org/storefront.git" {
		t.Errorf("URL %s, Ref %s, CloneURL %s", l.URL(), l.Ref(), l.CloneURL())
	}
	if again, ok := Parse(l.URL()); !ok || again != l {
		t.Errorf("Parse(URL()) = %+v, %v", again, ok)
	}
}

func TestRemoteKeepsNoCredentials(t *testing.T) {
	for raw, want := range map[string]v1.RepoRemote{
		"https://github.com/Org/Storefront.git":            {URL: "https://github.com/Org/Storefront.git", Host: "github.com", Path: "org/storefront"},
		"https://ghp_secret@github.com/org/storefront.git": {URL: "https://github.com/org/storefront.git", Host: "github.com", Path: "org/storefront"},
		"https://me:hunter2@github.com/org/storefront/":    {URL: "https://github.com/org/storefront/", Host: "github.com", Path: "org/storefront"},
		"ssh://git@github.com/org/storefront.git":          {URL: "ssh://git@github.com/org/storefront.git", Host: "github.com", Path: "org/storefront"},
		"ssh://git:secret@ghe.example:2222/team/api.git":   {URL: "ssh://git@ghe.example:2222/team/api.git", Host: "ghe.example", Path: "team/api"},
		"git@github.com:Org/storefront.git":                {URL: "git@github.com:Org/storefront.git", Host: "github.com", Path: "org/storefront"},
		"GitHub.com:org/storefront":                        {URL: "GitHub.com:org/storefront", Host: "github.com", Path: "org/storefront"},
		"/home/me/code/storefront":                         {URL: "/home/me/code/storefront"},
		"../storefront":                                    {URL: "../storefront"},
		"file:///srv/git/storefront.git":                   {URL: "file:///srv/git/storefront.git"},
	} {
		want.Name = "origin"
		if got := Remote("origin", raw); got != want {
			t.Errorf("Remote(%q) = %+v, want %+v", raw, got, want)
		}
	}
}

func TestAnIssueIsInTheRepositoryARemotePointsTo(t *testing.T) {
	l, _ := Find("https://github.com/Org/StoreFront/issues/12")
	remotes := []v1.RepoRemote{Remote("fork", "git@github.com:me/storefront.git"), Remote("upstream", "https://github.com/org/storefront.git")}
	if !l.InAny(remotes) || l.In(remotes[0]) || !l.In(remotes[1]) {
		t.Errorf("In the fork %v, upstream %v", l.In(remotes[0]), l.In(remotes[1]))
	}
	for _, other := range []string{"https://gitlab.com/org/storefront.git", "https://github.com/org/storefront-web.git", "/srv/org/storefront"} {
		if l.In(Remote("origin", other)) {
			t.Errorf("the issue is in %s", other)
		}
	}
}
