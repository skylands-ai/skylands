package placement

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/daemon/agents"
	"github.com/skylands-ai/skylands/internal/daemon/host"
	"github.com/skylands-ai/skylands/internal/daemon/session"
	"github.com/skylands-ai/skylands/internal/dispatch"
	"github.com/skylands-ai/skylands/internal/fakeagent"
)

func TestMain(m *testing.M) {
	fakeagent.TestMainHook()
	os.Exit(m.Run())
}

// repo makes a git repository holding files, by path and contents, in one
// commit with the given subject.
func repo(t *testing.T, files map[string]string, subject string) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	commit(t, dir, files, subject)
	return dir
}

func commit(t *testing.T, dir string, files map[string]string, subject string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-q", "-m", subject)
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

type fakeRepos struct {
	repos  []host.Repo
	agents []v1.AgentAvailability
}

func (f *fakeRepos) Repos() []host.Repo             { return f.repos }
func (f *fakeRepos) Agents() []v1.AgentAvailability { return f.agents }

type fakeSessions struct {
	mu       sync.Mutex
	sessions []session.Session
	recorded map[string][]v1.TokensUsed
}

func (f *fakeSessions) List() []session.Session { return f.sessions }

func (f *fakeSessions) RecordTokens(_ context.Context, id, purpose string, t v1.TokenUsage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.recorded == nil {
		f.recorded = map[string][]v1.TokensUsed{}
	}
	f.recorded[id] = append(f.recorded[id], v1.TokensUsed{SessionID: id, For: purpose, Tokens: t})
	return nil
}

// world is an island with two repositories, storefront and catalog, that
// can run the fake agent.
func world(t *testing.T) (*Placer, *fakeRepos, *fakeSessions) {
	t.Helper()
	sf := repo(t, map[string]string{
		"go.mod":                       "module github.com/acme/storefront/v2\n\ngo 1.26\n",
		"crates/pricing/Cargo.toml":    "[workspace]\nmembers = []\n\n[package]\nname = \"storefront-pricing\"\nversion = \"0.1.0\"\n",
		"crates/pricing/src/engine.rs": "fn main() {}",
		"internal/pricing/rounding.go": "package pricing",
		"vendor/acme/search/go.mod":    "module acme/search",
		"README.md":                    "Storefront\n==========\n\nCarts and prices in any [currency](https://example.com/c).\n\n## Setup\n\nRun make to install the toolchain.\n",
	}, "Round prices to the cent")
	commit(t, sf, map[string]string{"internal/cart/cart.go": "package cart"}, "Add the cart")
	cat := repo(t, map[string]string{
		"package.json":        `{"name": "@acme/catalog-web", "private": true}`,
		"src/search/index.ts": "export {}",
		"README.md":           "# Catalog\n\nProducts and search.\n",
	}, "Show the pricing tier on products")
	repos := &fakeRepos{
		repos:  []host.Repo{{ID: "sf", Name: "storefront", Path: sf}, {ID: "cat", Name: "catalog", Path: cat}},
		agents: []v1.AgentAvailability{{Name: "fake", Available: true}, {Name: "claude", Available: false}},
	}
	sessions := &fakeSessions{}
	cmd, env := fakeagent.TestCommand()
	reg := agents.Registry{"fake": {Name: "fake", Provider: "local", Command: cmd, Env: env}, "claude": {Name: "claude"}}
	return New("ws", repos, sessions, reg, filepath.Join(t.TempDir(), "placement")), repos, sessions
}

func TestGatherReadsWhatNamesARepository(t *testing.T) {
	p, repos, _ := world(t)
	src, commitID, err := gather(context.Background(), repos.repos[0])
	if err != nil || commitID == "" {
		t.Fatalf("gather: %v, commit %q", err, commitID)
	}
	var mods []string
	for _, m := range src.Modules {
		mods = append(mods, m.Name+" "+m.Manifest)
	}
	// The Go module is named by its last element, less its major version;
	// a vendored module isn't the repository's own.
	if !slices.Equal(mods, []string{"storefront-pricing crates/pricing/Cargo.toml"}) {
		t.Errorf("modules = %q", mods)
	}
	if src.Readme != "Storefront\n\nCarts and prices in any [currency](https://example.com/c)." {
		t.Errorf("readme = %q", src.Readme)
	}
	if !slices.Equal(src.Commits, []string{"Add the cart", "Round prices to the cent"}) {
		t.Errorf("commits = %q", src.Commits)
	}
	cat, _, _ := gather(context.Background(), repos.repos[1])
	if len(cat.Modules) != 1 || cat.Modules[0].Name != "catalog-web" {
		t.Errorf("an npm package's name, without its scope = %+v", cat.Modules)
	}
	_ = p
}

func TestMatchRanksTheIslandsRepositories(t *testing.T) {
	p, repos, _ := world(t)
	res := p.Match(context.Background(), v1.MatchRequest{Prompt: "add multi-currency support to pricing"})
	if res.Repos != 2 || len(res.Candidates) != 2 || res.Candidates[0].RepoID != "sf" {
		t.Fatalf("match = %+v", res)
	}
	if v := dispatch.Decide([]dispatch.Answer{{Host: "ws", Candidates: res.Candidates}}); v.Do != dispatch.Start || v.Target.RepoID != "sf" {
		t.Errorf("verdict = %+v", v)
	}
	if !slices.Contains(res.Candidates[0].Reasons, "currency in the README") {
		t.Errorf("reasons = %q", res.Candidates[0].Reasons)
	}

	// A commit moves HEAD, so the repository is indexed again.
	if got := p.Match(context.Background(), v1.MatchRequest{Prompt: "coupons"}); len(got.Candidates) != 0 {
		t.Fatalf("coupons before = %+v", got)
	}
	commit(t, repos.repos[1].Path, map[string]string{"src/coupons/apply.ts": "export {}"}, "Start on coupons")
	if got := p.Match(context.Background(), v1.MatchRequest{Prompt: "coupons"}); len(got.Candidates) != 1 || got.Candidates[0].RepoID != "cat" {
		t.Errorf("coupons after = %+v", got)
	}

	// A repository no longer registered is no longer offered.
	repos.repos = repos.repos[:1]
	if got := p.Match(context.Background(), v1.MatchRequest{Prompt: "coupons"}); got.Repos != 1 || len(got.Candidates) != 0 {
		t.Errorf("after unregistering = %+v", got)
	}
}

func TestPickAsksAnAgentAndRecordsWhatItCost(t *testing.T) {
	p, _, sessions := world(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	got, err := p.Pick(ctx, v1.PickRequest{Prompt: "list products in the catalog", Among: []string{"sf", "cat"}, Agent: "claude", SessionID: "s1"})
	// claude can't run here, so the fake agent is asked; it picks the
	// repository the work names.
	if err != nil || got.RepoID != "cat" || got.Agent != "fake" {
		t.Fatalf("pick = %+v, %v", got, err)
	}
	p.Started(ctx, "s1")
	p.Started(ctx, "s1")
	rec := sessions.recorded["s1"]
	if len(rec) != 1 || rec[0].For != v1.TokensForDispatch || rec[0].Tokens.Input != fakeagent.PickInputTokens {
		t.Errorf("recorded = %+v", rec)
	}

	got, err = p.Pick(ctx, v1.PickRequest{Prompt: "speed it up", Among: []string{"sf", "cat"}})
	if err != nil || got.RepoID != "" {
		t.Errorf("an unsure pick = %+v, %v", got, err)
	}
	for _, bad := range [][]string{{"sf"}, {"sf", "nope"}, {"sf", "sf"}} {
		if _, err := p.Pick(ctx, v1.PickRequest{Prompt: "x", Among: bad}); err == nil {
			t.Errorf("a pick among %q went ahead", bad)
		}
	}
}

func TestPickNeedsAnAgent(t *testing.T) {
	p, repos, _ := world(t)
	repos.agents = []v1.AgentAvailability{{Name: "fake", Available: false}}
	if _, err := p.Pick(context.Background(), v1.PickRequest{Prompt: "x", Among: []string{"sf", "cat"}, Agent: "fake"}); err == nil || !strings.Contains(err.Error(), "can't run any agent") {
		t.Errorf("pick with no agent = %v", err)
	}
	// Before the island has checked, the agent asked for is tried, as a
	// session would be.
	repos.agents = nil
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if got, err := p.Pick(ctx, v1.PickRequest{Prompt: "list products in the catalog", Among: []string{"sf", "cat"}, Agent: "fake"}); err != nil || got.Agent != "fake" || got.RepoID != "cat" {
		t.Errorf("pick before the check = %+v, %v", got, err)
	}
}

func TestReplayLeavesOutTheSessionsOwnTitle(t *testing.T) {
	p, _, sessions := world(t)
	sessions.sessions = []session.Session{
		{ID: "s1", RepoID: "cat", Prompt: "the checkout button is slow", Title: "Checkout button is slow"},
		{ID: "s2", RepoID: "sf", Prompt: "round prices half up in pricing"},
		{ID: "s3", RepoID: "gone", Prompt: "pricing"},
		{ID: "s4", RepoID: "sf"},
	}
	got := p.Replay(context.Background())
	if len(got) != 2 {
		t.Fatalf("replayed %d sessions, want the two with prompts on registered repositories", len(got))
	}
	byID := map[string]dispatch.Verdict{}
	for _, r := range got {
		byID[r.Session.ID] = r.Verdict
	}
	// Only s1's own title mentions checkout, so without it nothing fits.
	if v := byID["s1"]; v.Do != dispatch.AskPerson || v.Why != dispatch.WhyNothing {
		t.Errorf("s1 = %+v", v)
	}
	if v := byID["s2"]; v.Do != dispatch.Start || v.Target.RepoID != "sf" {
		t.Errorf("s2 = %+v", v)
	}
	// Asked now, s1's title counts.
	if res := p.Match(context.Background(), v1.MatchRequest{Prompt: "the checkout button is slow"}); len(res.Candidates) == 0 || res.Candidates[0].RepoID != "cat" {
		t.Errorf("match with the title = %+v", res)
	}
}
