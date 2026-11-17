package cli

import (
	"bytes"
	"strings"
	"testing"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/daemon/socket"
	"github.com/skylands-ai/skylands/internal/dispatch"
)

func candidate(id string, conf float64, reasons ...string) v1.MatchCandidate {
	return v1.MatchCandidate{RepoID: id, Name: id, Confidence: conf, Reasons: reasons}
}

func verdict(cands ...v1.MatchCandidate) dispatch.Verdict {
	return dispatch.Decide([]dispatch.Answer{{Host: "ws", Candidates: cands}})
}

func TestRepoMatchListsWhatMatched(t *testing.T) {
	var out bytes.Buffer
	printMatch(&out, socket.MatchInfo{Repos: 2, Verdict: verdict(candidate("storefront", 0.7, "pricing in internal/pricing/", "currency in the README"), candidate("catalog", 0.2, "pricing in a commit"))})
	if want := "REPO        CONFIDENCE  MATCHED\nstorefront  0.70        pricing in internal/pricing/; currency in the README\ncatalog     0.20        pricing in a commit\n"; out.String() != want {
		t.Errorf("repo match printed %q, want %q", out.String(), want)
	}
	out.Reset()
	printMatch(&out, socket.MatchInfo{Repos: 2, Verdict: verdict()})
	if out.String() != "No repository on this island matches.\n" {
		t.Errorf("with no match, repo match printed %q", out.String())
	}
	out.Reset()
	printMatch(&out, socket.MatchInfo{})
	if out.String() != "No repository is registered on this island.\n" {
		t.Errorf("with no repositories, repo match printed %q", out.String())
	}
}

func TestRepoMatchReplayTallies(t *testing.T) {
	sessions := []socket.ReplayedSession{
		{Session: "s1", RepoID: "storefront", Repo: "storefront", Prompt: "round prices", Verdict: verdict(candidate("storefront", 0.7))},
		{Session: "s2", RepoID: "catalog", Repo: "catalog", Prompt: "fix the\nsearch box", Verdict: verdict(candidate("storefront", 0.7))},
		{Session: "s3", RepoID: "catalog", Repo: "catalog", Prompt: "speed up", Verdict: verdict(candidate("storefront", 0.5), candidate("catalog", 0.45))},
		{Session: "s4", RepoID: "docs", Repo: "docs", Prompt: "speed up", Verdict: verdict(candidate("storefront", 0.5), candidate("catalog", 0.45))},
		{Session: "s5", RepoID: "docs", Repo: "docs", Prompt: "tidy", Verdict: verdict()},
	}
	got := tallyReplay(sessions)
	if got.sessions != 5 || got.right != 1 || got.wrong != 1 || got.agent != 2 || got.agentHad != 1 || got.person != 1 || len(got.misses) != 1 || got.misses[0].Session != "s2" {
		t.Errorf("tally = %+v", got)
	}
	var out bytes.Buffer
	printReplay(&out, sessions)
	for _, want := range []string{
		"Placed the first prompts of 5 past sessions on this island:\n",
		"  1 would start in their own repository (20%)\n",
		"  2 would go to the island's agent, 1 of them with their own repository among its choices\n",
		"s2       catalog  storefront      fix the search box\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("replay printed %q, lacking %q", out.String(), want)
		}
	}
	out.Reset()
	printReplay(&out, nil)
	if !strings.Contains(out.String(), "No past session") {
		t.Errorf("an empty replay printed %q", out.String())
	}
}
