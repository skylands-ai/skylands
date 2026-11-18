package clone

import (
	"context"
	"encoding/pem"
	"errors"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCheckURL(t *testing.T) {
	for _, u := range []string{
		"https://github.com/example/storefront.git",
		"https://git.example.com:8443/a/b",
		"ssh://git@github.com/example/storefront.git",
		"ssh://github.com:2222/example/storefront",
		"git@github.com:example/storefront.git",
		"github.com:example/storefront",
		"me@host.example_1:repo",
	} {
		if err := CheckURL(u); err != nil {
			t.Errorf("CheckURL(%q) = %v, want nil", u, err)
		}
	}
	for _, u := range []string{
		"",
		" https://github.com/example/r.git",
		"https://github.com/example/r.git\n",
		"https://github.com/ex ample/r.git",
		"http://github.com/example/r.git",
		"HTTPS://github.com/example/r.git",
		"file:///etc",
		"git://github.com/example/r.git",
		"ext::sh -c touch% /tmp/pwned",
		"fd::17",
		"/home/me/code/r",
		"./r",
		"r",
		"../r",
		"-uhttps://x",
		"--upload-pack=touch /tmp/x:repo",
		"-oProxyCommand=x:repo",
		"git@-oProxyCommand=x:repo",
		"https://token@github.com/example/r.git",
		"https://user:pass@github.com/example/r.git",
		"ssh://git:pass@github.com/example/r.git",
		"https:///example/r.git",
		"ssh://-oProxyCommand=x/r",
		"git@:repo",
		"@host:repo",
		"host:",
		"a/b:c",
	} {
		if err := CheckURL(u); err == nil {
			t.Errorf("CheckURL(%q) = nil, want a refusal", u)
		}
	}
}

func TestWithinKeepsPathsInsideTheRoot(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "code")
	outside := filepath.Join(base, "elsewhere")
	for _, d := range []string{filepath.Join(root, "a"), outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(root, "out")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "a"), filepath.Join(base, "via")); err != nil {
		t.Fatal(err)
	}
	real := func(p string) string {
		r, err := filepath.EvalSymlinks(p)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	realRoot := real(root)
	for path, want := range map[string]string{
		"a":                              filepath.Join(realRoot, "a"),
		" a ":                            filepath.Join(realRoot, "a"),
		"a/new/deeper":                   filepath.Join(realRoot, "a", "new", "deeper"),
		"new":                            filepath.Join(realRoot, "new"),
		".":                              realRoot,
		root:                             realRoot,
		filepath.Join(root, "a"):         filepath.Join(realRoot, "a"),
		filepath.Join(root, "a/../new"):  filepath.Join(realRoot, "new"),
		filepath.Join(base, "via", "x"):  filepath.Join(realRoot, "a", "x"),
		filepath.Join(root, "missing/b"): filepath.Join(realRoot, "missing", "b"),
	} {
		got, err := Within(root, path)
		if err != nil || got != want {
			t.Errorf("Within(%q) = %q, %v; want %q", path, got, err, want)
		}
	}
	for _, path := range []string{"", "  ", "..", "../elsewhere", "a/../../elsewhere", outside, "out", "out/new", filepath.Join(root, "out"), "/", base} {
		if got, err := Within(root, path); err == nil {
			t.Errorf("Within(%q) = %q, want a refusal", path, got)
		} else if strings.TrimSpace(path) != "" && !errors.Is(err, ErrOutside) {
			t.Errorf("Within(%q) = %v, want ErrOutside", path, err)
		}
	}
	// A root named through a link is the directory it leads to.
	if got, err := Within(filepath.Join(base, "via"), "x"); err != nil || got != filepath.Join(realRoot, "a", "x") {
		t.Errorf("Within through a linked root = %q, %v", got, err)
	}
}

func TestWithinTakesTheHomeDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	realHome, _ := filepath.EvalSymlinks(home)
	if got, err := Within(home, "~/code/r"); err != nil || got != filepath.Join(realHome, "code", "r") {
		t.Errorf("Within(~/code/r) = %q, %v", got, err)
	}
	if _, err := Within(filepath.Join(home, "code"), "~"); !errors.Is(err, ErrOutside) {
		t.Errorf("Within(~) under a root inside it = %v, want ErrOutside", err)
	}
}

// gitServer serves the bare repositories under a directory over https, as
// git http-backend does, and has git trust its certificate.
func gitServer(t *testing.T) (dir string, url string) {
	t.Helper()
	out, err := exec.Command("git", "--exec-path").Output()
	if err != nil {
		t.Skipf("no git: %v", err)
	}
	backend := filepath.Join(strings.TrimSpace(string(out)), "git-http-backend")
	if _, err := os.Stat(backend); err != nil {
		t.Skipf("no git http-backend: %v", err)
	}
	dir = t.TempDir()
	srv := httptest.NewTLSServer(&cgi.Handler{Path: backend, Env: []string{"GIT_PROJECT_ROOT=" + dir, "GIT_HTTP_EXPORT_ALL=1"}})
	t.Cleanup(srv.Close)
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_SSL_CAINFO", ca)
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	return dir, srv.URL
}

// bareRepo makes a bare repository at dir/name with one commit of files.
func bareRepo(t *testing.T, dir, name string, files ...string) {
	t.Helper()
	work := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("-C", work, "init", "-q")
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(work, f), []byte(f), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("-C", work, "add", ".")
	run("-C", work, "commit", "-q", "-m", "init")
	run("clone", "-q", "--bare", work, filepath.Join(dir, name))
}

type watched struct{ started, ended []int }

func (w *watched) Started(pgid int) { w.started = append(w.started, pgid) }
func (w *watched) Ended(pgid int)   { w.ended = append(w.ended, pgid) }

func TestIntoClonesOverHTTPS(t *testing.T) {
	served, url := gitServer(t)
	bareRepo(t, served, "storefront.git", "main.go", "README.md")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	root := t.TempDir()
	dir := filepath.Join(root, "org", "storefront")
	partial := Partial(dir, "Add1d3ntity")
	w := &watched{}
	if err := Into(ctx, url+"/storefront.git", dir, partial, w); err != nil {
		t.Fatalf("Into: %v", err)
	}
	for _, f := range []string{"main.go", "README.md", ".git"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("the clone has no %s: %v", f, err)
		}
	}
	if _, err := os.Stat(partial); !os.IsNotExist(err) {
		t.Errorf("the partial clone is still there: %v", err)
	}
	if len(w.started) != 1 || len(w.ended) != 1 || w.started[0] != w.ended[0] {
		t.Errorf("the watch saw %+v", w)
	}

	// A clone into a directory that exists is refused, and changes nothing.
	if err := Into(ctx, url+"/storefront.git", dir, Partial(dir, "Add2d3ntity"), nil); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("a clone over a directory = %v", err)
	}

	// A clone that fails leaves nothing behind.
	missing := filepath.Join(root, "missing")
	err := Into(ctx, url+"/missing.git", missing, Partial(missing, "Add3d3ntity"), nil)
	if err == nil || !strings.HasPrefix(err.Error(), "git clone: ") {
		t.Errorf("a clone of nothing = %v", err)
	}
	for _, p := range []string{missing, Partial(missing, "Add3d3ntity")} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s is left: %v", p, err)
		}
	}
}

func TestIntoRefusesWhatCheckURLRefuses(t *testing.T) {
	src := t.TempDir()
	bareRepo(t, src, "r.git", "a")
	dir := filepath.Join(t.TempDir(), "r")
	for _, u := range []string{filepath.Join(src, "r.git"), "file://" + filepath.Join(src, "r.git")} {
		if err := Into(context.Background(), u, dir, Partial(dir, "Add1d3ntity"), nil); err == nil {
			t.Errorf("Into(%q) cloned", u)
		}
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("a refused clone left %s: %v", dir, err)
	}
}
