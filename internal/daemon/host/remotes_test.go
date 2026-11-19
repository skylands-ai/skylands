package host

import (
	"context"
	"os/exec"
	"reflect"
	"testing"
	"time"

	v1 "github.com/skylands-ai/skylands/api/v1"
)

func TestRemotesAreRecordedWhenRegisteredAndWhenTheyChange(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store := hostStore(t, ctx)
	h, err := Load(ctx, store, "ws")
	if err != nil {
		t.Fatal(err)
	}
	git := func(dir string, args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	// One with no remotes records none.
	if _, err := h.RegisterRepo(ctx, v1.RegisterRepo{ID: "c1", Path: gitRepo(t, "a")}); err != nil {
		t.Fatal(err)
	}
	if n := countEvents(t, ctx, store); n != 2 {
		t.Errorf("a repository with no remotes recorded %d events, want 2", n)
	}

	dir := gitRepo(t, "main.go")
	git(dir, "remote", "add", "origin", "https://ghp_secret@github.com/Org/storefront.git")
	repo, err := h.RegisterRepo(ctx, v1.RegisterRepo{ID: "c2", Path: dir})
	if err != nil {
		t.Fatal(err)
	}
	origin := v1.RepoRemote{Name: "origin", URL: "https://github.com/Org/storefront.git", Host: "github.com", Path: "org/storefront"}
	if !reflect.DeepEqual(repo.Remotes, []v1.RepoRemote{origin}) {
		t.Fatalf("remotes = %+v", repo.Remotes)
	}

	// Nothing changed, nothing recorded; a remote added is.
	before := countEvents(t, ctx, store)
	h.CheckRemotes(ctx)
	if n := countEvents(t, ctx, store); n != before {
		t.Errorf("an unchanged check recorded %d events", n-before)
	}
	git(dir, "remote", "add", "upstream", "git@github.com:org/storefront-upstream.git")
	h.CheckRemotes(ctx)
	if n := countEvents(t, ctx, store); n != before+1 {
		t.Errorf("a changed check recorded %d events, want 1", n-before)
	}
	upstream := v1.RepoRemote{Name: "upstream", URL: "git@github.com:org/storefront-upstream.git", Host: "github.com", Path: "org/storefront-upstream"}
	want := []v1.RepoRemote{origin, upstream}
	if r, _ := h.Repo(repo.ID); !reflect.DeepEqual(r.Remotes, want) {
		t.Errorf("remotes after a check = %+v", r.Remotes)
	}
	h2, err := Load(ctx, store, "ws")
	if err != nil {
		t.Fatal(err)
	}
	if r, _ := h2.Repo(repo.ID); !reflect.DeepEqual(r.Remotes, want) {
		t.Errorf("reloaded remotes = %+v", r.Remotes)
	}
}
