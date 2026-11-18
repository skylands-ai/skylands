package host

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/approval"
	"github.com/skylands-ai/skylands/internal/daemon/clone"
	"github.com/skylands-ai/skylands/internal/es"
	"github.com/skylands-ai/skylands/internal/natsembed"
)

const testWorld = "ODI3SD2AXZKYITZCULXVZS423WD53OXVF3CK7FOT4GWOL4HD24CN3F4O"

// hostStore starts a NATS server holding host ws's stream.
func hostStore(t *testing.T, ctx context.Context) *es.Store {
	t.Helper()
	srv, err := natsembed.Start(ctx, natsembed.Config{Name: "host", ClientAddr: "127.0.0.1:0", StoreDir: t.TempDir(), Domain: "ws"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Shutdown)
	nc, err := nats.Connect(srv.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	store, err := es.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureStream(ctx, v1.IslandStreamName("ws"), v1.IslandSubjects("ws")); err != nil {
		t.Fatal(err)
	}
	return store
}

// adder is a host in a world with one approver, which signs additions.
type adder struct {
	t    *testing.T
	h    *Host
	kp   nkeys.KeyPair
	pub  string
	root string
}

func newAdder(t *testing.T, ctx context.Context, store *es.Store, root string, cloner Cloner) *adder {
	t.Helper()
	h, err := Load(ctx, store, "ws")
	if err != nil {
		t.Fatal(err)
	}
	kp, _ := nkeys.CreateUser()
	pub, _ := kp.PublicKey()
	h.RequireSignatures(func() string { return testWorld }, func(_ context.Context, d approval.Decision, s v1.DecisionSignature) error {
		if s.Approver != pub {
			return errors.New("the decision is signed by a key that isn't an approver for this world")
		}
		return approval.VerifyDecision(d, s)
	})
	h.AllowAdding(ctx, root, cloner)
	return &adder{t: t, h: h, kp: kp, pub: pub, root: root}
}

// signed is an AddRepo as a browser sends it, signed by the approver.
func (a *adder) signed(id, path, url string) v1.AddRepo {
	a.t.Helper()
	s, err := approval.SignDecision(a.kp, approval.RepoAdd{V: approval.DecisionVersion, Kind: approval.KindRepoAdd, World: testWorld, Host: "ws", ID: id, Path: path, URL: url})
	if err != nil {
		a.t.Fatal(err)
	}
	return v1.AddRepo{ID: id, Path: path, URL: url, Signature: &s}
}

func waitUntil(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(15 * time.Second); !ok(); {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func countEvents(t *testing.T, ctx context.Context, store *es.Store) int {
	t.Helper()
	n := 0
	if err := store.Replay(ctx, v1.IslandStreamName("ws"), []string{v1.IslandEventFilter("ws")}, func(uint64, v1.Event) error { n++; return nil }); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestAddRepoRegistersASignedPathInsideTheRoot(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store := hostStore(t, ctx)
	root := t.TempDir()
	realRoot, _ := filepath.EvalSymlinks(root)
	shop := gitRepoAt(t, filepath.Join(root, "shop"), "main.go")
	a := newAdder(t, ctx, store, root, func(context.Context, string, string, string) error { return errors.New("no clones here") })

	unsigned := a.signed("Add1d3ntity", "shop", "")
	unsigned.Signature = nil
	if _, err := a.h.AddRepo(ctx, unsigned); !errors.Is(err, approval.ErrUnsigned) {
		t.Errorf("unsigned = %v, want ErrUnsigned", err)
	}
	// A signature over another path, or by a key that doesn't approve,
	// adds nothing.
	other := a.signed("Add1d3ntity", "elsewhere", "")
	other.Path = "shop"
	if _, err := a.h.AddRepo(ctx, other); !errors.Is(err, approval.ErrBadSignature) {
		t.Errorf("signed for another path = %v, want ErrBadSignature", err)
	}
	stranger, _ := nkeys.CreateUser()
	s, _ := approval.SignDecision(stranger, approval.RepoAdd{V: approval.DecisionVersion, Kind: approval.KindRepoAdd, World: testWorld, Host: "ws", ID: "Add1d3ntity", Path: "shop"})
	if _, err := a.h.AddRepo(ctx, v1.AddRepo{ID: "Add1d3ntity", Path: "shop", Signature: &s}); err == nil {
		t.Error("a stranger's signature added a repository")
	}
	if _, err := a.h.AddRepo(ctx, a.signed("short", "shop", "")); err == nil {
		t.Error("an id of five letters was taken")
	}
	if n := countEvents(t, ctx, store); n != 0 {
		t.Fatalf("refusals recorded %d events", n)
	}

	out, err := a.h.AddRepo(ctx, a.signed("Add1d3ntity", "shop", ""))
	if err != nil {
		t.Fatalf("AddRepo: %v", err)
	}
	if out.Cloning || out.RepoID == "" || out.Name != "shop" || out.Path != filepath.Join(realRoot, "shop") {
		t.Errorf("result = %+v", out)
	}
	r, ok := a.h.Repo(out.RepoID)
	if !ok || r.RegisteredBy != "citadel" || r.RegisteredApprover != a.pub || r.URL != "" || len(r.Files) != 1 {
		t.Errorf("repo = %+v", r)
	}
	// The same id gets the same outcome and records nothing more.
	if again, err := a.h.AddRepo(ctx, a.signed("Add1d3ntity", "shop", "")); err != nil || again != out {
		t.Errorf("again = %+v, %v", again, err)
	}
	if n := countEvents(t, ctx, store); n != 2 {
		t.Errorf("stream holds %d events, want 2", n)
	}
	if _, err := a.h.AddRepo(ctx, a.signed("Add2d3ntity", shop, "")); !errors.Is(err, ErrAlreadyRegistered) {
		t.Errorf("the same repository again = %v, want ErrAlreadyRegistered", err)
	}

	// Nothing outside the root, by a relative path, an absolute one, or a
	// link, and nothing whose repository reaches outside it.
	outside := gitRepo(t, "a")
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"..", outside, "link", "~"} {
		if _, err := a.h.AddRepo(ctx, a.signed("Add3d3ntity", p, "")); !errors.Is(err, clone.ErrOutside) {
			t.Errorf("AddRepo(%q) = %v, want ErrOutside", p, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(shop, "inner"), 0o755); err != nil {
		t.Fatal(err)
	}
	a.h.AllowAdding(ctx, filepath.Join(shop, "inner"), a.h.cloner)
	if _, err := a.h.AddRepo(ctx, a.signed("Add4d3ntity", ".", "")); !errors.Is(err, clone.ErrOutside) {
		t.Errorf("a root inside a repository = %v, want ErrOutside", err)
	}

	// The stream keeps who added it.
	h2, err := Load(ctx, store, "ws")
	if err != nil {
		t.Fatal(err)
	}
	if r2, ok := h2.Repo(out.RepoID); !ok || r2.RegisteredBy != "citadel" || r2.RegisteredApprover != a.pub {
		t.Errorf("reloaded repo = %+v", r2)
	}
}

func TestAddRepoWaitsForTheWorld(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store := hostStore(t, ctx)
	root := t.TempDir()
	gitRepoAt(t, filepath.Join(root, "shop"), "main.go")
	a := newAdder(t, ctx, store, root, nil)
	if _, err := a.h.AddRepo(ctx, a.signed("Add1d3ntity", "shop", "")); err == nil {
		t.Error("a host that clones nothing added a repository")
	}
	a.h.AllowAdding(ctx, root, func(context.Context, string, string, string) error { return nil })
	a.h.RequireSignatures(func() string { return "" }, nil)
	if _, err := a.h.AddRepo(ctx, a.signed("Add1d3ntity", "shop", "")); err == nil {
		t.Error("a host with no world added a repository")
	}
}

// localCloner clones from local repositories named by URL, as clone.Into
// does over the network: into partial, then moved to dir.
func localCloner(sources map[string]string) Cloner {
	return func(ctx context.Context, url, dir, partial string) error {
		src, ok := sources[url]
		if !ok {
			return errors.New("git clone: remote: Repository not found.")
		}
		if out, err := exec.CommandContext(ctx, "git", "clone", "-q", src, partial).CombinedOutput(); err != nil {
			return errors.New("git clone: " + strings.TrimSpace(string(out)))
		}
		return os.Rename(partial, dir)
	}
}

func TestAddRepoClonesInTheBackground(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store := hostStore(t, ctx)
	root := t.TempDir()
	realRoot, _ := filepath.EvalSymlinks(root)
	const url = "https://example.com/org/shop.git"
	a := newAdder(t, ctx, store, root, localCloner(map[string]string{url: gitRepo(t, "main.go", "go.mod")}))

	if _, err := a.h.AddRepo(ctx, a.signed("Add1d3ntity", "x", "file:///etc")); !errors.Is(err, clone.ErrURL) {
		t.Errorf("a file URL = %v, want ErrURL", err)
	}
	if _, err := a.h.AddRepo(ctx, a.signed("Add1d3ntity", "../x", url)); !errors.Is(err, clone.ErrOutside) {
		t.Errorf("a clone outside the root = %v, want ErrOutside", err)
	}
	if err := os.Mkdir(filepath.Join(root, "taken"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := a.h.AddRepo(ctx, a.signed("Add1d3ntity", "taken", url)); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("a clone over a directory = %v", err)
	}

	dir := filepath.Join(realRoot, "org", "shop")
	out, err := a.h.AddRepo(ctx, a.signed("Add1d3ntity", "org/shop", url))
	if err != nil || !out.Cloning || out.Path != dir || out.RepoID != "" {
		t.Fatalf("AddRepo = %+v, %v", out, err)
	}
	waitUntil(t, "the clone to be registered", func() bool { return len(a.h.Repos()) == 1 })
	a.h.Wait()
	r := a.h.Repos()[0]
	if r.Path != dir || r.URL != url || r.RegisteredBy != "citadel" || r.RegisteredApprover != a.pub || r.TotalFiles != 2 {
		t.Errorf("repo = %+v", r)
	}
	if c := a.h.Clones(); len(c) != 1 || !c[0].Registered || c[0].Error != "" || c[0].ID != "Add1d3ntity" || c[0].Approver != a.pub {
		t.Errorf("clones = %+v", c)
	}
	if again, err := a.h.AddRepo(ctx, a.signed("Add1d3ntity", "org/shop", url)); err != nil || again.RepoID != r.ID || again.Cloning {
		t.Errorf("again = %+v, %v", again, err)
	}
	if a.h.Cloning() {
		t.Error("Cloning after the clone was registered")
	}

	// A clone that fails says why, as the same id does after.
	if out, err := a.h.AddRepo(ctx, a.signed("Add2d3ntity", "gone", "https://example.com/gone.git")); err != nil || !out.Cloning {
		t.Fatalf("AddRepo of a missing repository = %+v, %v", out, err)
	}
	waitUntil(t, "the clone to fail", func() bool {
		for _, c := range a.h.Clones() {
			if c.ID == "Add2d3ntity" && c.Error != "" {
				return true
			}
		}
		return false
	})
	a.h.Wait()
	if _, err := a.h.AddRepo(ctx, a.signed("Add2d3ntity", "gone", "https://example.com/gone.git")); err == nil || !strings.Contains(err.Error(), "Repository not found") {
		t.Errorf("the failed clone's id again = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "gone")); !os.IsNotExist(err) {
		t.Errorf("a failed clone left its directory: %v", err)
	}

	// The stream keeps the clones, and where the repository came from.
	h2, err := Load(ctx, store, "ws")
	if err != nil {
		t.Fatal(err)
	}
	if r2, ok := h2.Repo(r.ID); !ok || r2.URL != url || r2.RegisteredApprover != a.pub {
		t.Errorf("reloaded repo = %+v", r2)
	}
	if c := h2.Clones(); len(c) != 2 || !c[0].Registered || c[1].Error == "" {
		t.Errorf("reloaded clones = %+v", c)
	}
}

func TestFailInterruptedClones(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store := hostStore(t, ctx)
	root := t.TempDir()
	// The clone fetches into its partial directory until the daemon stops.
	cloneCtx, stop := context.WithCancel(ctx)
	a := newAdder(t, ctx, store, root, nil)
	a.h.AllowAdding(cloneCtx, root, func(ctx context.Context, url, dir, partial string) error {
		if err := os.MkdirAll(filepath.Join(partial, ".git"), 0o755); err != nil {
			return err
		}
		<-ctx.Done()
		return ctx.Err()
	})
	out, err := a.h.AddRepo(ctx, a.signed("Add1d3ntity", "shop", "https://example.com/shop.git"))
	if err != nil {
		t.Fatal(err)
	}
	partial := clone.Partial(out.Path, "Add1d3ntity")
	waitUntil(t, "the clone to start", func() bool { _, err := os.Stat(partial); return err == nil })
	if !a.h.Cloning() {
		t.Error("not Cloning while a clone runs")
	}
	if _, err := a.h.AddRepo(ctx, a.signed("Add2d3ntity", "shop", "https://example.com/shop.git")); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Errorf("a second clone into the same directory = %v", err)
	}
	stop()
	a.h.Wait()
	if c := a.h.Clones(); len(c) != 1 || c[0].Error != "" {
		t.Fatalf("a clone cut off by the daemon stopping recorded %+v", c)
	}

	// The daemon, started again, ends the clone and removes what it fetched.
	h2, err := Load(ctx, store, "ws")
	if err != nil {
		t.Fatal(err)
	}
	if !h2.Cloning() {
		t.Fatal("the reloaded host doesn't know the clone was running")
	}
	h2.FailInterruptedClones(ctx)
	if c := h2.Clones(); len(c) != 1 || !strings.Contains(c[0].Error, "stopped") || h2.Cloning() {
		t.Errorf("clones = %+v", c)
	}
	if _, err := os.Stat(partial); !os.IsNotExist(err) {
		t.Errorf("the partial clone is left: %v", err)
	}
	h3, err := Load(ctx, store, "ws")
	if err != nil {
		t.Fatal(err)
	}
	if c := h3.Clones(); len(c) != 1 || c[0].Error == "" {
		t.Errorf("the failure wasn't recorded: %+v", c)
	}
}
