package citadel

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/citadel/projections"
	"github.com/skylands-ai/skylands/internal/dispatch"
	"github.com/skylands-ai/skylands/internal/issuelink"
	"github.com/skylands-ai/skylands/internal/natsembed"
)

// dispatchWorld is a citadel that knows three islands: ws and laptop,
// which it can reach, and attic, which it can't. Each answers a match
// with what answers holds for it, and laptop doesn't answer at all when
// silent is set.
type dispatchWorld struct {
	c       *citadel
	mu      sync.Mutex
	asked   map[string]string
	answers map[string][]v1.MatchCandidate
	silent  bool
}

func newDispatchWorld(t *testing.T) *dispatchWorld {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	srv, err := natsembed.Start(ctx, natsembed.Config{Name: "hub", ClientAddr: "127.0.0.1:0", StoreDir: t.TempDir(), Domain: "world"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Shutdown)
	nc, err := nats.Connect(srv.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	w := &dispatchWorld{asked: map[string]string{}, answers: map[string][]v1.MatchCandidate{}}
	islands := projections.NewIslands(nil)
	reply := func(host string) projections.Reply {
		return projections.Reply{ID: host, Metadata: map[string]string{"host": host}}
	}
	islands.Observe([]projections.Reply{reply("ws"), reply("laptop"), reply("attic")}, time.Now())
	for range projections.MissesBeforeUnreachable {
		islands.Observe([]projections.Reply{reply("ws"), reply("laptop")}, time.Now())
	}
	terrain := projections.NewTerrain(nil)
	for _, r := range []struct{ host, id, name string }{{"ws", "sf", "storefront"}, {"ws", "cat", "catalog"}, {"laptop", "docs", "docs-site"}, {"laptop", "sf2", "storefront"}, {"attic", "old", "archive"}} {
		ev, _ := v1.NewEvent(v1.EventRepoRegistered, "", v1.RepoRegistered{RepoID: r.id, Name: r.name, Path: "/src/" + r.name})
		terrain.Apply(r.host, ev)
	}
	for _, host := range []string{"ws", "laptop", "attic"} {
		_, err := nc.Subscribe(v1.IslandFetchSubject(host, v1.FetchMatch), func(m *nats.Msg) {
			var req v1.MatchRequest
			_ = json.Unmarshal(m.Data, &req)
			w.mu.Lock()
			w.asked[host] = req.Prompt
			silent := w.silent && host == "laptop"
			cands := w.answers[host]
			w.mu.Unlock()
			if silent {
				return
			}
			data, _ := json.Marshal(v1.MatchResult{Candidates: cands, Repos: 2})
			out, _ := json.Marshal(v1.Reply{Result: data})
			_ = m.Respond(out)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}
	w.c = &citadel{islands: islands, terrain: terrain, nc: nc}
	return w
}

func (w *dispatchWorld) place(t *testing.T, prompt, host string) dispatchResult {
	t.Helper()
	w.mu.Lock()
	w.asked = map[string]string{}
	w.mu.Unlock()
	return w.c.place(context.Background(), dispatchRequest{Prompt: prompt, Host: host})
}

func (w *dispatchWorld) wasAsked() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []string
	for h := range w.asked {
		out = append(out, h)
	}
	slices.Sort(out)
	return out
}

func TestDispatchAsksEveryReachableIslandAndCompares(t *testing.T) {
	w := newDispatchWorld(t)
	w.answers["ws"] = []v1.MatchCandidate{{RepoID: "sf", Name: "storefront", Confidence: 0.7, Reasons: []string{"pricing in internal/pricing/"}}, {RepoID: "cat", Name: "catalog", Confidence: 0.2}}
	w.answers["laptop"] = []v1.MatchCandidate{{RepoID: "docs", Name: "docs-site", Confidence: 0.3}}
	res := w.place(t, "add multi-currency support to pricing", "")
	if res.Do != dispatch.Start || res.Target.Host != "ws" || res.Target.RepoID != "sf" {
		t.Errorf("verdict = %+v", res.Verdict)
	}
	// The unreachable island isn't asked, and says so.
	if got := w.wasAsked(); !slices.Equal(got, []string{"laptop", "ws"}) || !slices.Equal(res.Asked, []string{"laptop", "ws"}) {
		t.Errorf("asked %q, result says %q", got, res.Asked)
	}
	if !slices.Equal(res.LeftOut, []leftOut{{Host: "attic", Why: leftUnreachable}}) || res.Repos != 5 {
		t.Errorf("left out %+v of %d repos", res.LeftOut, res.Repos)
	}
	if len(res.Ranked) != 3 {
		t.Errorf("ranked = %+v", res.Ranked)
	}
}

func TestDispatchLeavesOutAnIslandThatDoesntAnswerInTime(t *testing.T) {
	w := newDispatchWorld(t)
	w.silent = true
	w.answers["ws"] = []v1.MatchCandidate{{RepoID: "sf", Name: "storefront", Confidence: 0.5}, {RepoID: "cat", Name: "catalog", Confidence: 0.4}}
	start := time.Now()
	res := w.place(t, "speed up the cart", "")
	if took := time.Since(start); took > v1.MatchTimeout+2*time.Second {
		t.Errorf("placing took %s", took)
	}
	// What ws answered is a close call on ws, which goes to ws's agent.
	if res.Do != dispatch.AskAgent || res.Host != "ws" || len(res.Among) != 2 {
		t.Errorf("verdict = %+v", res.Verdict)
	}
	if !slices.Contains(res.LeftOut, leftOut{Host: "laptop", Why: leftNoAnswer}) || !slices.Equal(res.Asked, []string{"ws"}) {
		t.Errorf("left out %+v, asked %q", res.LeftOut, res.Asked)
	}
}

func TestDispatchAtAnIslandAsksOnlyIt(t *testing.T) {
	w := newDispatchWorld(t)
	w.answers["ws"] = []v1.MatchCandidate{{RepoID: "cat", Name: "catalog", Confidence: 0.6}}
	// The prompt names a repository on laptop, but at ws only ws counts.
	res := w.place(t, "move docs-site search to catalog", "ws")
	if got := w.wasAsked(); len(got) != 0 {
		t.Errorf("asked %q, though the prompt names catalog on ws", got)
	}
	if res.Do != dispatch.Start || res.Why != dispatch.WhyNamed || res.Target.RepoID != "cat" {
		t.Errorf("verdict = %+v", res.Verdict)
	}
	res = w.place(t, "speed up search", "ws")
	if got := w.wasAsked(); !slices.Equal(got, []string{"ws"}) || res.Do != dispatch.Start || res.Target.RepoID != "cat" || res.Repos != 2 {
		t.Errorf("asked %q, verdict %+v", got, res.Verdict)
	}
}

func TestDispatchSettlesANamedRepositoryWithoutScoring(t *testing.T) {
	w := newDispatchWorld(t)
	res := w.place(t, "fix the docs-site sidebar", "")
	if got := w.wasAsked(); len(got) != 0 {
		t.Errorf("asked %q for a named repository", got)
	}
	if res.Do != dispatch.Start || res.Target.Host != "laptop" || res.Target.RepoID != "docs" {
		t.Errorf("verdict = %+v", res.Verdict)
	}
	// storefront is on two islands, so a person chooses between them.
	res = w.place(t, "fix the storefront header", "")
	if res.Do != dispatch.AskPerson || res.Why != dispatch.WhyNamed || len(res.Among) != 2 {
		t.Errorf("verdict for a name on two islands = %+v", res.Verdict)
	}
	// Naming the island as well settles it.
	res = w.place(t, "fix the storefront header on laptop", "")
	if res.Do != dispatch.Start || res.Target.Host != "laptop" || res.NamedIsland != "laptop" {
		t.Errorf("verdict with the island named = %+v", res.Verdict)
	}
	// A repository named only on an unreachable island goes to a person.
	res = w.place(t, "restore the archive", "")
	if res.Do != dispatch.AskPerson || res.Why != dispatch.WhyNamed || len(res.Among) != 1 || res.Among[0].Host != "attic" || res.Target != nil {
		t.Errorf("verdict for an unreachable name = %+v", res.Verdict)
	}
}

func TestDispatchNamingAnIslandAsksOnlyIt(t *testing.T) {
	w := newDispatchWorld(t)
	w.answers["laptop"] = []v1.MatchCandidate{{RepoID: "docs", Name: "docs-site", Confidence: 0.2}}
	res := w.place(t, "on laptop, tidy the sidebar", "")
	if got := w.wasAsked(); !slices.Equal(got, []string{"laptop"}) || res.NamedIsland != "laptop" || res.Repos != 2 {
		t.Errorf("asked %q, named %q, repos %d", got, res.NamedIsland, res.Repos)
	}
	if res.Do != dispatch.AskPerson || res.Why != dispatch.WhyWeak {
		t.Errorf("verdict = %+v", res.Verdict)
	}
}

func TestDispatchToAnIslandWithOneRepositoryGoesThere(t *testing.T) {
	w := newDispatchWorld(t)
	w.c.islands.Observe([]projections.Reply{
		{ID: "ws", Metadata: map[string]string{"host": "ws"}},
		{ID: "laptop", Metadata: map[string]string{"host": "laptop"}},
		{ID: "cellar", Metadata: map[string]string{"host": "cellar"}},
	}, time.Now())
	ev, _ := v1.NewEvent(v1.EventRepoRegistered, "", v1.RepoRegistered{RepoID: "notes", Name: "notes", Path: "/src/notes"})
	w.c.terrain.Apply("cellar", ev)
	// At the island, and from the world naming it, nothing is asked: the
	// one repository is all there is.
	for _, host := range []string{"cellar", ""} {
		prompt := "tidy this up"
		if host == "" {
			prompt = "on cellar, tidy this up"
		}
		res := w.place(t, prompt, host)
		if res.Do != dispatch.Start || res.Why != dispatch.WhyOnly || res.Target == nil || res.Target.Host != "cellar" || res.Target.RepoID != "notes" || len(res.Asked) != 0 {
			t.Errorf("at %q: verdict = %+v, asked %q", host, res.Verdict, res.Asked)
		}
		if got := w.wasAsked(); len(got) != 0 {
			t.Errorf("at %q: asked %q", host, got)
		}
	}
	// An island with one repository it can't reach is left out as before.
	res := w.place(t, "restore something", "attic")
	if res.Do != dispatch.AskPerson || !slices.Equal(res.LeftOut, []leftOut{{Host: "attic", Why: leftUnreachable}}) {
		t.Errorf("at the unreachable attic: verdict = %+v, left out %+v", res.Verdict, res.LeftOut)
	}
}

func TestDispatchPlacesAnIssueLinkByRemoteWithoutAskingAnyIsland(t *testing.T) {
	w := newDispatchWorld(t)
	remotes := func(host, repo string, urls ...string) {
		var list []v1.RepoRemote
		for i, u := range urls {
			list = append(list, issuelink.Remote([]string{"origin", "upstream"}[i], u))
		}
		ev, _ := v1.NewEvent(v1.EventRepoRemotesChanged, "", v1.RepoRemotesChanged{RepoID: repo, Remotes: list})
		w.c.terrain.Apply(host, ev)
	}
	remotes("ws", "sf", "https://github.com/org/storefront.git")
	remotes("laptop", "sf2", "git@github.com:me/storefront.git", "https://github.com/Org/StoreFront")
	remotes("attic", "old", "git@github.com:org/archive.git")
	// Words that would match elsewhere don't count once a link places it.
	w.answers["laptop"] = []v1.MatchCandidate{{RepoID: "docs", Name: "docs-site", Confidence: 0.9}}
	ids := func(cs []dispatch.Candidate) []string {
		out := []string{}
		for _, c := range cs {
			out = append(out, c.Host+"/"+c.RepoID)
		}
		return out
	}

	res := w.place(t, "fix the docs https://github.com/org/storefront/issues/12#issuecomment-1", "")
	if res.Do != dispatch.AskPerson || res.Why != dispatch.WhyIssue || !slices.Equal(ids(res.Among), []string{"laptop/sf2", "ws/sf"}) && !slices.Equal(ids(res.Among), []string{"ws/sf", "laptop/sf2"}) {
		t.Errorf("at the world, the issue in two repositories = %+v", res.Verdict)
	}
	if res.Issue == nil || res.Issue.URL != "https://github.com/org/storefront/issues/12" || res.Issue.Ref != "org/storefront#12" || res.Issue.CloneURL != "https://github.com/org/storefront.git" || res.Issue.Number != 12 {
		t.Errorf("issue = %+v", res.Issue)
	}
	if asked := w.wasAsked(); len(asked) != 0 {
		t.Errorf("asked %v", asked)
	}

	res = w.place(t, "https://github.com/org/storefront/issues/12", "ws")
	if res.Do != dispatch.Start || res.Target == nil || res.Target.RepoID != "sf" || res.Target.Reasons[0] != "its remote origin is github.com/org/storefront, where the issue is" {
		t.Errorf("at ws = %+v", res.Verdict)
	}
	if len(res.IssueRepos) != 2 {
		t.Errorf("issue repos = %v, want both, in scope or not", ids(res.IssueRepos))
	}

	// Only on an island that can't be reached: a person chooses, knowing.
	res = w.place(t, "https://github.com/org/archive/issues/3", "")
	if res.Do != dispatch.AskPerson || !slices.Equal(ids(res.Among), []string{"attic/old"}) || len(res.LeftOut) != 1 || res.LeftOut[0] != (leftOut{Host: "attic", Why: leftUnreachable}) {
		t.Errorf("only on attic = %+v, left out %+v", res.Verdict, res.LeftOut)
	}
	// Elsewhere than the island it was asked at: none in scope, and where it is.
	res = w.place(t, "https://github.com/org/archive/issues/3", "ws")
	if res.Do != dispatch.AskPerson || len(res.Among) != 0 || !slices.Equal(ids(res.IssueRepos), []string{"attic/old"}) {
		t.Errorf("at ws, an issue on attic = %+v, issue repos %v", res.Verdict, ids(res.IssueRepos))
	}
	// On no island at all.
	res = w.place(t, "https://github.com/org/brand-new/issues/1", "")
	if res.Do != dispatch.AskPerson || len(res.Among) != 0 || res.IssueRepos == nil || len(res.IssueRepos) != 0 || res.Ranked == nil {
		t.Errorf("on no island = %+v, issue repos %v", res.Verdict, res.IssueRepos)
	}
	if asked := w.wasAsked(); len(asked) != 0 {
		t.Errorf("asked %v", asked)
	}
}
