package terrain

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	v1 "github.com/skylands-ai/skylands/api/v1"
)

// gitRepo makes a repository with a few committed files and returns its path.
func gitRepo(t *testing.T) string {
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
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("no"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "src/main.go", "README.md")
	run("commit", "-q", "-m", "init")
	return dir
}

func TestTakeListsTrackedFiles(t *testing.T) {
	dir := gitRepo(t)
	// Any path inside the working tree resolves to the top level.
	snap, err := Take(context.Background(), filepath.Join(dir, "src"))
	if err != nil {
		t.Fatalf("Take: %v", err)
	}
	if snap.Name != filepath.Base(dir) || snap.Commit == "" {
		t.Errorf("snapshot = %+v", snap)
	}
	if real, _ := filepath.EvalSymlinks(dir); snap.Path != real && snap.Path != dir {
		t.Errorf("Path = %q, want the top level %q", snap.Path, dir)
	}
	sizes := map[string]int64{}
	for _, f := range snap.Files {
		sizes[f.Path] = f.Size
	}
	if len(sizes) != 2 || sizes["src/main.go"] != int64(len("package main\n")) || sizes["README.md"] != int64(len("# hi\n")) {
		t.Errorf("files = %v", sizes)
	}
	if snap.TotalFiles != 2 || snap.Files[0].Path != "src/main.go" {
		t.Errorf("TotalFiles = %d, first = %+v; want 2 and the larger file first", snap.TotalFiles, snap.Files[0])
	}
}

func TestTakeKeepsTheLargestFiles(t *testing.T) {
	dir := t.TempDir()
	n := v1.MaxBuildings + 15
	for i := 0; i < n; i++ {
		// File i has i+1 bytes, so the largest files are the highest numbers.
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%03d", i)), make([]byte, i+1), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "init"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	snap, err := Take(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if snap.TotalFiles != n || len(snap.Files) != v1.MaxBuildings {
		t.Fatalf("TotalFiles = %d, len(Files) = %d; want %d and %d", snap.TotalFiles, len(snap.Files), n, v1.MaxBuildings)
	}
	if snap.Files[0].Size != int64(n) || snap.Files[len(snap.Files)-1].Size != int64(n-v1.MaxBuildings+1) {
		t.Errorf("kept sizes %d..%d, want %d..%d", snap.Files[0].Size, snap.Files[len(snap.Files)-1].Size, n, n-v1.MaxBuildings+1)
	}
}

func TestTakeRejectsNonRepositories(t *testing.T) {
	if _, err := Take(context.Background(), t.TempDir()); !errors.Is(err, ErrNotRepository) {
		t.Errorf("err = %v, want ErrNotRepository", err)
	}
	if _, err := Take(context.Background(), filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("a missing path was accepted")
	}
}
