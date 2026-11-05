package host

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/es"
	"github.com/skylands-ai/skylands/internal/natsembed"
)

func gitRepo(t *testing.T, files ...string) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f), []byte(f), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("add", ".")
	run("commit", "-q", "-m", "init")
	return dir
}

func TestRegisterRepoRecordsAndReloads(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	srv, err := natsembed.Start(ctx, natsembed.Config{Name: "host", ClientAddr: "127.0.0.1:0", StoreDir: t.TempDir(), Domain: "ws"})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Shutdown()
	nc, err := nats.Connect(srv.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	store, err := es.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureStream(ctx, v1.IslandStreamName("ws"), v1.IslandSubjects("ws")); err != nil {
		t.Fatal(err)
	}

	h, err := Load(ctx, store, "ws")
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Repos()) != 0 {
		t.Fatalf("fresh host has repos: %+v", h.Repos())
	}

	dir := gitRepo(t, "a.txt", "bb.txt")
	repo, err := h.RegisterRepo(ctx, v1.RegisterRepo{ID: "cmd-1", Path: dir})
	if err != nil {
		t.Fatalf("RegisterRepo: %v", err)
	}
	if repo.Name != filepath.Base(dir) || len(repo.Files) != 2 || repo.TotalFiles != 2 || repo.ID == "" || repo.RegisteredAt.IsZero() || repo.SnapshottedAt.IsZero() {
		t.Errorf("repo = %+v", repo)
	}

	// The same command id gets the same repo and adds nothing to the stream.
	again, err := h.RegisterRepo(ctx, v1.RegisterRepo{ID: "cmd-1", Path: dir})
	if err != nil || again.ID != repo.ID {
		t.Errorf("repeat = %+v, %v", again, err)
	}
	// A new command for the same path is refused, and a non-repo too.
	if _, err := h.RegisterRepo(ctx, v1.RegisterRepo{ID: "cmd-2", Path: dir}); !errors.Is(err, ErrAlreadyRegistered) {
		t.Errorf("duplicate path err = %v", err)
	}
	if _, err := h.RegisterRepo(ctx, v1.RegisterRepo{ID: "cmd-3", Path: t.TempDir()}); err == nil {
		t.Error("a non-repository was registered")
	}

	count := 0
	if err := store.Replay(ctx, v1.IslandStreamName("ws"), []string{v1.IslandEventFilter("ws")}, func(uint64, v1.Event) error { count++; return nil }); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("stream holds %d host events, want 2", count)
	}

	// A fresh aggregate rebuilt from the stream knows the repo and the command.
	h2, err := Load(ctx, store, "ws")
	if err != nil {
		t.Fatal(err)
	}
	repos := h2.Repos()
	if len(repos) != 1 || repos[0].ID != repo.ID || len(repos[0].Files) != 2 || repos[0].Commit != repo.Commit {
		t.Fatalf("reloaded repos = %+v", repos)
	}
	if _, err := h2.RegisterRepo(ctx, v1.RegisterRepo{ID: "cmd-1", Path: dir}); err != nil {
		t.Errorf("reloaded host forgot cmd-1: %v", err)
	}
}

func TestSetRulesRecordsAndReloads(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	srv, err := natsembed.Start(ctx, natsembed.Config{Name: "host", ClientAddr: "127.0.0.1:0", StoreDir: t.TempDir(), Domain: "ws"})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Shutdown()
	nc, err := nats.Connect(srv.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	store, err := es.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureStream(ctx, v1.IslandStreamName("ws"), v1.IslandSubjects("ws")); err != nil {
		t.Fatal(err)
	}
	h, err := Load(ctx, store, "ws")
	if err != nil {
		t.Fatal(err)
	}
	repo, err := h.RegisterRepo(ctx, v1.RegisterRepo{ID: "c1", Path: gitRepo(t, "a.go")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.SetRules(ctx, v1.SetRepoRules{ID: "c2", RepoID: "nope", Rules: nil}, "local"); !errors.Is(err, ErrUnknownRepo) {
		t.Errorf("rules for an unknown repo: err = %v", err)
	}
	if _, err := h.SetRules(ctx, v1.SetRepoRules{ID: "c3", RepoID: repo.ID, Rules: []v1.Rule{{Decision: "allow", Kind: "nope"}}}, "local"); err == nil {
		t.Error("a bad kind was accepted")
	}
	// Rules are normalized and lose any source: the repository is the source.
	set, err := h.SetRules(ctx, v1.SetRepoRules{ID: "c4", RepoID: repo.ID, Rules: []v1.Rule{{Decision: "Allow", Kind: "", Pattern: " go test * ", Source: "repo"}}}, "local")
	if err != nil || len(set.Rules) != 1 || set.Rules[0].Decision != "allow" || set.Rules[0].Kind != "*" || set.Rules[0].Pattern != "go test *" || set.Rules[0].Source != "" {
		t.Fatalf("SetRules = %+v, %v", set.Rules, err)
	}
	// The same command id changes nothing more.
	if again, err := h.SetRules(ctx, v1.SetRepoRules{ID: "c4", RepoID: repo.ID, Rules: nil}, "local"); err != nil || len(again.Rules) != 1 {
		t.Errorf("repeated SetRules = %+v, %v", again.Rules, err)
	}
	h2, err := Load(ctx, store, "ws")
	if err != nil {
		t.Fatal(err)
	}
	if r, ok := h2.Repo(repo.ID); !ok || len(r.Rules) != 1 || r.Rules[0].Pattern != "go test *" {
		t.Errorf("reloaded rules = %+v", r.Rules)
	}
}

func TestRecordAgentsRecordsOnlyChanges(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	srv, err := natsembed.Start(ctx, natsembed.Config{Name: "host", ClientAddr: "127.0.0.1:0", StoreDir: t.TempDir(), Domain: "ws"})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Shutdown()
	nc, err := nats.Connect(srv.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	store, err := es.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureStream(ctx, v1.IslandStreamName("ws"), v1.IslandSubjects("ws")); err != nil {
		t.Fatal(err)
	}
	h, err := Load(ctx, store, "ws")
	if err != nil {
		t.Fatal(err)
	}
	if h.Agents() != nil {
		t.Errorf("Agents before any check = %+v, want nil", h.Agents())
	}
	ready := []v1.AgentAvailability{{Name: "claude", Available: true, Agent: "adapter 1.0.0", Login: "Claude Max"}, {Name: "fake", Available: true}}
	loggedOut := []v1.AgentAvailability{{Name: "claude", Notes: []v1.AvailabilityNote{{Check: "login claude", Failed: true, Found: "not logged in"}}}, {Name: "fake", Available: true}}
	for i, step := range []struct {
		agents []v1.AgentAvailability
		want   bool
	}{{ready, true}, {ready, false}, {loggedOut, true}, {loggedOut, false}, {ready, true}} {
		recorded, err := h.RecordAgents(ctx, step.agents)
		if err != nil || recorded != step.want {
			t.Errorf("step %d: recorded = %v, %v; want %v", i, recorded, err, step.want)
		}
	}
	count := 0
	if err := store.Replay(ctx, v1.IslandStreamName("ws"), []string{v1.IslandEventSubject("ws", v1.EventAdapterAvailabilityChanged)}, func(uint64, v1.Event) error { count++; return nil }); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Errorf("%d events recorded, want 3, one per change", count)
	}

	// A daemon that restarts knows what it last recorded, so a check that
	// finds the same records nothing.
	again, err := Load(ctx, store, "ws")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(again.Agents(), ready) {
		t.Errorf("reloaded Agents = %+v, want %+v", again.Agents(), ready)
	}
	if recorded, err := again.RecordAgents(ctx, ready); err != nil || recorded {
		t.Errorf("same agents after a reload: recorded = %v, %v; want nothing", recorded, err)
	}
}
