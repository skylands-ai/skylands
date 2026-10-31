package socket

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/skylands-ai/skylands/internal/daemon/host"
)

// shortSocket returns a socket path short enough for every platform's limit.
func shortSocket(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "sk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "d.sock")
}

type fakeAPI struct{ repos []RepoInfo }

func (f *fakeAPI) Status(context.Context) StatusInfo {
	return StatusInfo{Host: "ws", World: "nats-leaf://x:7422", Version: "v1", Started: time.Now(), Repos: f.repos}
}

func (f *fakeAPI) RegisterRepo(_ context.Context, path, commandID string) (RepoInfo, error) {
	if commandID == "" {
		return RepoInfo{}, errors.New("missing command id")
	}
	for _, r := range f.repos {
		if r.Path == path {
			return r, host.ErrAlreadyRegistered
		}
	}
	r := RepoInfo{ID: "r1", Name: filepath.Base(path), Path: path, Files: 3}
	f.repos = append(f.repos, r)
	return r, nil
}

func TestServerAndClient(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	path := shortSocket(t)
	api := &fakeAPI{}
	sctx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- Serve(sctx, path, api) }()

	c := Dial(path)
	var st StatusInfo
	var err error
	for i := 0; i < 50; i++ {
		if st, err = c.Status(ctx); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil || st.Host != "ws" {
		t.Fatalf("Status = %+v, %v", st, err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("socket mode = %v, %v; want 0600", info.Mode(), err)
	}

	repo, err := c.RegisterRepo(ctx, "/code/storefront")
	if err != nil || repo.Name != "storefront" || repo.Files != 3 {
		t.Fatalf("RegisterRepo = %+v, %v", repo, err)
	}
	if _, err := c.RegisterRepo(ctx, "/code/storefront"); err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Errorf("duplicate err = %v", err)
	}
	if st, _ := c.Status(ctx); len(st.Repos) != 1 {
		t.Errorf("status repos = %+v", st.Repos)
	}

	stop()
	if err := <-done; err != nil {
		t.Errorf("Serve returned %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("socket file left behind: %v", err)
	}
	if _, err := c.Status(ctx); !errors.Is(err, ErrNoDaemon) {
		t.Errorf("after stop, err = %v, want ErrNoDaemon", err)
	}
}
