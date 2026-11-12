package session

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nats-io/nkeys"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/approval"
	"github.com/skylands-ai/skylands/internal/daemon/blobs"
	"github.com/skylands-ai/skylands/internal/daemon/host"
	"github.com/skylands-ai/skylands/internal/fakeagent"
)

// attachFixture is a fixture whose fake agent takes what the options say,
// whose host found it takes the same, and which keeps blobs and uploads.
// Its repository has a short text file, a long one, a binary one, a file
// in secrets/, and a link to a file outside it, and its rules allow
// edits, so a turn runs without anyone asked.
func attachFixture(t *testing.T, images, embedded bool) (*fixture, host.Repo) {
	t.Helper()
	f := setup(t)
	gitIdentity(t)
	ad := f.cfg.Registry["fake"]
	if images {
		ad.Env = append(ad.Env, fakeagent.EnvImages+"=1")
	}
	if embedded {
		ad.Env = append(ad.Env, fakeagent.EnvEmbeddedContext+"=1")
	}
	f.cfg.Registry["fake"] = ad
	f.cfg.Agents = func(name string) (v1.AgentAvailability, bool) {
		return v1.AgentAvailability{Name: name, Available: true, Images: images, EmbeddedContext: embedded}, name == "fake"
	}
	dir := filepath.Dir(f.cfg.WorktreesDir)
	f.cfg.Blobs = blobs.New(filepath.Join(dir, "blobs"))
	f.cfg.Uploads = blobs.NewUploads(filepath.Join(dir, "uploads"), f.cfg.Blobs)

	repo := gitRepo(t, "a.go")
	outside := filepath.Join(t.TempDir(), "elsewhere.txt")
	write := func(name string, data []byte) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(repo, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("docs/long.txt", bytes.Repeat([]byte("a line of a long file\n"), 6000))
	write("tool.bin", []byte{0x7f, 'E', 'L', 'F', 0, 0, 1, 2})
	write("secrets/key.txt", []byte("hunter2"))
	if err := os.WriteFile(outside, []byte("not the repository's"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(repo, "out.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("secrets", "key.txt"), filepath.Join(repo, "alias.txt")); err != nil {
		t.Fatal(err)
	}
	gitOut(t, repo, "add", ".")
	gitOut(t, repo, "commit", "-q", "-m", "more")
	r, err := f.agg.RegisterRepo(f.ctx, v1.RegisterRepo{ID: "reg-attach", Path: repo})
	if err != nil {
		t.Fatal(err)
	}
	if r, err = f.agg.SetRules(f.ctx, v1.SetRepoRules{ID: "rules-attach", RepoID: r.ID, Rules: []v1.Rule{{Decision: v1.RuleAllow, Kind: "edit"}}}, "local"); err != nil {
		t.Fatal(err)
	}
	return f, r
}

// png is a PNG's header followed by n bytes, which is all a type check
// looks at.
func png(n int) []byte {
	return append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, bytes.Repeat([]byte{7}, n)...)
}

// upload puts an image in the host's uploads, as the citadel does, and
// names it as a prompt does.
func upload(t *testing.T, f *fixture, name string, data []byte) v1.PromptImage {
	t.Helper()
	res, err := f.cfg.Uploads.Receive(v1.UploadPiece{Upload: "Up" + strings.Repeat("0", 8) + blobs.Hash(data)[:8], Data: data, Size: int64(len(data)), MimeType: "image/png"})
	if err != nil || res.Hash == "" {
		t.Fatalf("upload %s = %+v, %v", name, res, err)
	}
	return v1.PromptImage{Name: name, MimeType: "image/png", Hash: res.Hash, Size: int64(len(data))}
}

// promptsSent are the session's PromptSent events, oldest first.
func promptsSent(t *testing.T, f *fixture, id string) []v1.PromptSent {
	t.Helper()
	var out []v1.PromptSent
	_ = f.store.Replay(f.ctx, v1.IslandStreamName("ws"), []string{v1.SessionEventFilter("ws")}, func(_ uint64, ev v1.Event) error {
		var d v1.PromptSent
		if ev.Type == v1.EventPromptSent && ev.Decode(&d) == nil && d.SessionID == id {
			out = append(out, d)
		}
		return nil
	})
	return out
}

func transcriptOf(t *testing.T, f *fixture, id string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.cfg.TranscriptsDir, id+".md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestMentionsGoWholeOrAsLinks(t *testing.T) {
	f, repo := attachFixture(t, true, true)
	m, err := Load(f.ctx, f.store, f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Wait()

	// The mentions go in the order the text has them, whatever order the
	// command lists them in.
	text := "Compare @docs/long.txt with @a.go and look at @tool.bin"
	if _, err := m.Start(f.ctx, v1.StartSession{ID: "c1", SessionID: "s1", RepoID: repo.ID, Agent: "fake", Prompt: text, Mentions: []string{"a.go", "tool.bin", "docs/long.txt", "a.go"}}); err != nil {
		t.Fatal(err)
	}
	s := waitStatus(t, m, "s1", StatusIdle)
	sent := promptsSent(t, f, "s1")
	if len(sent) != 1 || len(sent[0].Mentions) != 3 {
		t.Fatalf("PromptSent = %+v", sent)
	}
	want := []v1.PromptMention{
		{Path: "docs/long.txt"},
		{Path: "a.go", Embedded: true, Hash: blobs.Hash([]byte("a.go")), Size: 4},
		{Path: "tool.bin"},
	}
	for i, mt := range sent[0].Mentions {
		if mt != want[i] {
			t.Errorf("mention %d = %+v, want %+v", i, mt, want[i])
		}
	}
	if kept, err := f.cfg.Blobs.Get(want[1].Hash); err != nil || string(kept) != "a.go" {
		t.Errorf("the embedded file kept = %q, %v", kept, err)
	}
	// The agent got a short text file whole, and a long file and a binary
	// one as links, which it reads itself.
	tr := transcriptOf(t, f, "s1")
	wt := "file://" + filepath.ToSlash(s.Worktree)
	for _, w := range []string{
		"It was given resource_link docs/long.txt " + wt + "/docs/long.txt; resource " + wt + "/a.go (4 bytes); resource_link tool.bin " + wt + "/tool.bin.",
		"## You\n\n" + text + "\n\n\U0001F4C4 docs/long.txt (link)\n\U0001F4C4 a.go (contents)\n\U0001F4C4 tool.bin (link)\n\n## Agent\n\n",
	} {
		if !strings.Contains(tr, w) {
			t.Errorf("the transcript lacks %q:\n%s", w, tr)
		}
	}

	// A read the rules would ask about goes as a link, and a rule that
	// denies reading a file refuses the prompt, naming the rule.
	if _, err := m.SetRules(f.ctx, v1.SetSessionRules{ID: "r1", SessionID: "s1", Rules: []v1.Rule{{Decision: v1.RuleAsk, Kind: "read", Pattern: "a.go"}, {Decision: v1.RuleDeny, Kind: "read", Pattern: "secrets/*"}, {Decision: v1.RuleAllow, Kind: "edit"}}}, "local"); err != nil {
		t.Fatal(err)
	}
	_, err = m.Prompt(f.ctx, v1.SendPrompt{ID: "p1", SessionID: "s1", Text: "and @secrets/key.txt", Mentions: []string{"secrets/key.txt"}})
	if !errors.Is(err, ErrMentionDenied) || !strings.Contains(err.Error(), "deny read secrets/*") {
		t.Errorf("a mention a rule denies: err = %v", err)
	}
	// A link inside the worktree doesn't lead around the deny.
	if _, err := m.Prompt(f.ctx, v1.SendPrompt{ID: "p-alias", SessionID: "s1", Text: "and @alias.txt", Mentions: []string{"alias.txt"}}); !errors.Is(err, ErrMentionDenied) {
		t.Errorf("a link to a denied file: err = %v", err)
	}
	if _, err := m.Prompt(f.ctx, v1.SendPrompt{ID: "p2", SessionID: "s1", Text: "again @a.go", Mentions: []string{"a.go"}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, m, "s1", func(s Session) bool { return s.Status == StatusIdle && s.Turns == 2 })
	if sent := promptsSent(t, f, "s1"); len(sent) != 2 || len(sent[1].Mentions) != 1 || sent[1].Mentions[0] != (v1.PromptMention{Path: "a.go"}) {
		t.Errorf("a mention the rules ask about = %+v, want a link", sent)
	}
	if tr := transcriptOf(t, f, "s1"); !strings.Contains(tr, "It was given resource_link a.go "+wt+"/a.go.") {
		t.Errorf("the second turn's answer lacks the link:\n%s", tr)
	}

	// A mention outside the worktree, one of nothing, and one the text
	// doesn't have are refused, as is a path that isn't relative.
	for name, tc := range map[string]struct {
		text    string
		mention string
	}{
		"a link outside":  {"see @out.txt", "out.txt"},
		"a path up":       {"see @../../x", "../../x"},
		"no such file":    {"see @nope.go", "nope.go"},
		"a directory":     {"see @docs", "docs"},
		"not in the text": {"see the file", "a.go"},
		"an absolute one": {"see @/etc/hostname", "/etc/hostname"},
	} {
		if _, err := m.Prompt(f.ctx, v1.SendPrompt{ID: "bad-" + name, SessionID: "s1", Text: tc.text, Mentions: []string{tc.mention}}); !errors.Is(err, ErrMention) {
			t.Errorf("%s: err = %v, want ErrMention", name, err)
		}
	}
	if _, err := m.Prompt(f.ctx, v1.SendPrompt{ID: "bad-out", SessionID: "s1", Text: "see @out.txt", Mentions: []string{"out.txt"}}); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Errorf("a link outside the worktree: err = %v, want it to say so", err)
	}
}

func TestStartRefusedForAMentionLeavesNoWorktree(t *testing.T) {
	f, repo := attachFixture(t, false, true)
	if _, err := f.agg.SetRules(f.ctx, v1.SetRepoRules{ID: "deny-secrets", RepoID: repo.ID, Revision: repo.RulesRevision, Rules: []v1.Rule{{Decision: v1.RuleDeny, Kind: "read", Pattern: "secrets/*"}}}, "local"); err != nil {
		t.Fatal(err)
	}
	m, err := Load(f.ctx, f.store, f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Wait()
	_, err = m.Start(f.ctx, v1.StartSession{ID: "c1", SessionID: "s1", RepoID: repo.ID, Agent: "fake", Prompt: "read @secrets/key.txt", Mentions: []string{"secrets/key.txt"}})
	if !errors.Is(err, ErrMentionDenied) {
		t.Fatalf("a start mentioning a denied file: err = %v", err)
	}
	if _, ok := m.Get("s1"); ok {
		t.Error("the refused start made a session")
	}
	if _, err := os.Stat(filepath.Join(f.cfg.WorktreesDir, "s1")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the refused start left its worktree: %v", err)
	}
	if out := gitOut(t, repo.Path, "branch", "--list", "skylands/s1"); out != "" {
		t.Errorf("the refused start left its branch: %q", out)
	}
}

func TestImagesGoToAnAgentThatTakesThem(t *testing.T) {
	f, repo := attachFixture(t, true, false)
	m, err := Load(f.ctx, f.store, f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Wait()
	shot := png(5000)
	img := upload(t, f, "screen shot.png", shot)
	// Without embedded context, a mention goes as a link, however short.
	if _, err := m.Start(f.ctx, v1.StartSession{ID: "c1", SessionID: "s1", RepoID: repo.ID, Agent: "fake", Prompt: "what is wrong in @a.go here", Mentions: []string{"a.go"}, Images: []v1.PromptImage{img}}); err != nil {
		t.Fatal(err)
	}
	s := waitStatus(t, m, "s1", StatusIdle)
	if len(s.Images) != 1 || s.Images[0] != img {
		t.Errorf("the session's images = %+v", s.Images)
	}
	tr := transcriptOf(t, f, "s1")
	for _, w := range []string{
		"It was given resource_link a.go file://" + filepath.ToSlash(s.Worktree) + "/a.go; image image/png (5008 bytes).",
		"\U0001F4C4 a.go (link)\n\U0001F5BC screen shot.png <!-- image:" + img.Hash + " -->\n\n## Agent",
	} {
		if !strings.Contains(tr, w) {
			t.Errorf("the transcript lacks %q:\n%s", w, tr)
		}
	}
	// The image moved from the uploads into the store, and a reader of the
	// session can fetch it there, but not anything else the host keeps.
	if _, err := os.Stat(filepath.Join(filepath.Dir(f.cfg.WorktreesDir), "uploads", img.Hash)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the image still waits in the uploads: %v", err)
	}
	got, data, err := m.Image("s1", img.Hash)
	if err != nil || got != img || !bytes.Equal(data, shot) {
		t.Errorf("Image = %+v, %d bytes, %v", got, len(data), err)
	}
	if _, _, err := m.Image("s1", blobs.Hash([]byte("a.go"))); err == nil {
		t.Error("a blob no prompt carried was served as an image")
	}

	// The same image can go again in a later prompt, from the store; one
	// never uploaded can't, and nor can one whose size is misstated.
	if _, err := m.Prompt(f.ctx, v1.SendPrompt{ID: "p1", SessionID: "s1", Text: "and again", Images: []v1.PromptImage{img}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, m, "s1", func(s Session) bool { return s.Status == StatusIdle && s.Turns == 2 })
	missing := v1.PromptImage{Name: "x.png", MimeType: "image/png", Hash: blobs.Hash(png(3)), Size: 11}
	if _, err := m.Prompt(f.ctx, v1.SendPrompt{ID: "p2", SessionID: "s1", Text: "this", Images: []v1.PromptImage{missing}}); !errors.Is(err, blobs.ErrNotUploaded) {
		t.Errorf("an image never uploaded: err = %v", err)
	}
	wrong := img
	wrong.Size++
	if _, err := m.Prompt(f.ctx, v1.SendPrompt{ID: "p3", SessionID: "s1", Text: "this", Images: []v1.PromptImage{wrong}}); !errors.Is(err, ErrImage) {
		t.Errorf("an image of another size: err = %v", err)
	}
	many := []v1.PromptImage{img, img, img, img, img, img}
	if _, err := m.Prompt(f.ctx, v1.SendPrompt{ID: "p4", SessionID: "s1", Text: "these", Images: many}); !errors.Is(err, ErrImage) {
		t.Errorf("six images: err = %v", err)
	}
}

func TestImagesAreRefusedForAnAgentThatTakesNone(t *testing.T) {
	f, repo := attachFixture(t, false, false)
	m, err := Load(f.ctx, f.store, f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Wait()
	img := upload(t, f, "a.png", png(100))

	// The host found the agent takes none, so a start with one is refused
	// before anything is made.
	if _, err := m.Start(f.ctx, v1.StartSession{ID: "c1", SessionID: "s1", RepoID: repo.ID, Agent: "fake", Prompt: "look", Images: []v1.PromptImage{img}}); !errors.Is(err, ErrNoImages) {
		t.Errorf("a start with an image: err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.cfg.WorktreesDir, "s1")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the refused start left a worktree: %v", err)
	}
	// A later prompt is checked against the agent that is running.
	if _, err := m.Start(f.ctx, v1.StartSession{ID: "c2", SessionID: "s2", RepoID: repo.ID, Agent: "fake", Prompt: fakeagent.SettingsQuestion}); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, m, "s2", StatusIdle)
	if _, err := m.Prompt(f.ctx, v1.SendPrompt{ID: "p1", SessionID: "s2", Text: "look", Images: []v1.PromptImage{img}}); !errors.Is(err, ErrNoImages) {
		t.Errorf("a prompt with an image: err = %v", err)
	}
	// The image waits in the uploads, where the sweep finds it in an hour.
	if _, err := f.cfg.Uploads.Take(img); err != nil {
		t.Errorf("the refused image is gone from the uploads: %v", err)
	}
}

func TestTheRunningAgentIsAskedAgainAtTheStart(t *testing.T) {
	f, repo := attachFixture(t, false, false)
	// The host's last check says the agent takes images, but the agent
	// that starts doesn't: the first prompt is refused, not sent without
	// its image.
	f.cfg.Agents = func(name string) (v1.AgentAvailability, bool) {
		return v1.AgentAvailability{Name: name, Available: true, Images: true}, true
	}
	m, err := Load(f.ctx, f.store, f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Wait()
	img := upload(t, f, "a.png", png(100))
	if _, err := m.Start(f.ctx, v1.StartSession{ID: "c1", SessionID: "s1", RepoID: repo.ID, Agent: "fake", Prompt: "look", Images: []v1.PromptImage{img}}); err != nil {
		t.Fatal(err)
	}
	s := waitStatus(t, m, "s1", StatusFailed)
	if !strings.Contains(s.Error, "doesn't accept images") {
		t.Errorf("the session failed with %q", s.Error)
	}
}

func TestSignedPromptsCoverWhatTheyCarry(t *testing.T) {
	f, repo := attachFixture(t, true, true)
	owner, _ := nkeys.CreateOperator()
	world, _ := owner.PublicKey()
	kp, _ := nkeys.CreateUser()
	f.cfg.World = func() string { return world }
	f.cfg.VerifyDecision = func(_ context.Context, d approval.Decision, s v1.DecisionSignature) error {
		return approval.VerifyDecision(d, s)
	}
	m, err := Load(f.ctx, f.store, f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Wait()
	sign := func(d approval.Decision) *v1.DecisionSignature {
		t.Helper()
		s, err := approval.SignDecision(kp, d)
		if err != nil {
			t.Fatal(err)
		}
		return &s
	}
	img := upload(t, f, "a.png", png(100))
	text := "fix @a.go"
	startAs := func(mentions []string, images []v1.PromptImage) approval.Decision {
		h, _ := approval.AttachmentsHash(mentions, images)
		return approval.Start{V: approval.DecisionVersion, Kind: approval.KindStart, World: world, Host: "ws", Session: "s1", Repo: repo.ID, Agent: "fake", Prompt: approval.TextHash(text), Attachments: h}
	}
	start := v1.StartSession{ID: "c1", SessionID: "s1", RepoID: repo.ID, Agent: "fake", Prompt: text, Mentions: []string{"a.go"}, Images: []v1.PromptImage{img}}
	// A signature over the text alone doesn't cover a mention or an image
	// added on the way.
	for name, sig := range map[string]*v1.DecisionSignature{
		"over the text alone":       sign(startAs(nil, nil)),
		"without the image":         sign(startAs([]string{"a.go"}, nil)),
		"over another mention":      sign(startAs([]string{"b.go"}, []v1.PromptImage{img})),
		"over another image's size": sign(startAs([]string{"a.go"}, []v1.PromptImage{{Name: img.Name, MimeType: img.MimeType, Hash: img.Hash, Size: 1}})),
	} {
		start.Signature = sig
		if _, err := m.Start(f.ctx, start); !errors.Is(err, approval.ErrBadSignature) {
			t.Errorf("a start signed %s: err = %v", name, err)
		}
	}
	start.Signature = sign(startAs([]string{"a.go"}, []v1.PromptImage{img}))
	if _, err := m.Start(f.ctx, start); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, m, "s1", StatusIdle)
	promptAs := func(images []v1.PromptImage) approval.Decision {
		h, _ := approval.AttachmentsHash(nil, images)
		return approval.Prompt{V: approval.DecisionVersion, Kind: approval.KindPrompt, World: world, Host: "ws", Session: "s1", Revision: 1, Prompt: approval.TextHash("more"), Attachments: h}
	}
	if _, err := m.Prompt(f.ctx, v1.SendPrompt{ID: "p1", SessionID: "s1", Text: "more", Images: []v1.PromptImage{img}, Revision: 1, Signature: sign(promptAs(nil))}); !errors.Is(err, approval.ErrBadSignature) {
		t.Errorf("a prompt signed without its image: err = %v", err)
	}
	if _, err := m.Prompt(f.ctx, v1.SendPrompt{ID: "p2", SessionID: "s1", Text: "more", Images: []v1.PromptImage{img}, Revision: 1, Signature: sign(promptAs([]v1.PromptImage{img}))}); err != nil {
		t.Fatal(err)
	}
}
