package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/daemon/agents"
	"github.com/skylands-ai/skylands/internal/daemon/blobs"
	"github.com/skylands-ai/skylands/internal/rules"
)

// Refusals of what a prompt carries besides its text.
var (
	ErrMention       = errors.New("the prompt can't mention that file")
	ErrImage         = errors.New("the prompt can't carry that image")
	ErrNoImages      = errors.New("the agent doesn't accept images")
	ErrMentionDenied = errors.New("a rule denies reading a file the prompt mentions")
)

// attached is what a prompt carries besides its text, as the agent gets it
// and the record names it.
type attached struct {
	mentions []v1.PromptMention
	images   []v1.PromptImage
	// send is what goes to the agent after the text, mentions first.
	send []agents.Attachment
	// files and pictures are the bytes the store keeps once the prompt is
	// taken: each embedded file's contents, and each image's.
	files, pictures [][]byte
}

// needs says what the agent must accept to be given a.
func (a attached) needs() agents.Accepts {
	var n agents.Accepts
	for _, m := range a.mentions {
		n.EmbeddedContext = n.EmbeddedContext || m.Embedded
	}
	n.Images = len(a.images) > 0
	return n
}

// checkAccepts refuses a prompt that carries what an agent doesn't accept.
func (a attached) checkAccepts(agent string, accepts agents.Accepts) error {
	n := a.needs()
	switch {
	case n.Images && !accepts.Images:
		return fmt.Errorf("%w: %s", ErrNoImages, agent)
	case n.EmbeddedContext && !accepts.EmbeddedContext:
		return fmt.Errorf("the agent doesn't accept a file's contents in a prompt: %s", agent)
	}
	return nil
}

// checkAttachments checks what a command says a prompt carries besides
// its text, before anything is read or made for it.
func checkAttachments(mentions []string, images []v1.PromptImage) error {
	if len(mentions) > v1.MaxMentions {
		return fmt.Errorf("%w: a prompt mentions at most %d files", ErrMention, v1.MaxMentions)
	}
	for _, p := range mentions {
		if p == "" || strings.ContainsFunc(p, unicode.IsControl) || filepath.IsAbs(p) || strings.HasPrefix(p, "/") {
			return fmt.Errorf("%w: %q isn't a path in the session's directory", ErrMention, p)
		}
	}
	if len(images) > v1.MaxImages {
		return fmt.Errorf("%w: a prompt carries at most %d images", ErrImage, v1.MaxImages)
	}
	for _, img := range images {
		switch {
		case img.Name == "" || len(img.Name) > 255 || strings.ContainsFunc(img.Name, unicode.IsControl):
			return fmt.Errorf("%w: an image needs a name of one line, at most 255 bytes", ErrImage)
		case !slices.Contains(v1.ImageTypes, img.MimeType):
			return fmt.Errorf("%w: %s is %q, not PNG, JPEG, GIF, or WebP", ErrImage, img.Name, img.MimeType)
		case img.Size <= 0 || img.Size > v1.MaxImage:
			return fmt.Errorf("%w: %s is %d bytes, and an image is at most %d MB", ErrImage, img.Name, img.Size, v1.MaxImage/1024/1024)
		}
	}
	return nil
}

// attach makes ready what a prompt carries besides its text: each file it
// mentions, resolved in dir, the session's directory, and each image it
// names, taken from the host's uploads. Rules decide each mention as they
// would decide the agent reading the file, with the session s's rules and
// trust: a file a rule denies refuses the prompt, one the rules allow goes
// whole when the agent accepts files' contents and it is text under
// v1.MaxEmbeddedFile, and anything else goes as a link, which the agent
// reads itself, through the rules again. The mentions go in the order
// they appear in text.
func (m *Manager) attach(s Session, dir string, accepts agents.Accepts, text string, mentions []string, images []v1.PromptImage) (attached, error) {
	var out attached
	if len(mentions) > 0 {
		root, err := filepath.EvalSymlinks(dir)
		if err != nil {
			return out, fmt.Errorf("the session's directory is gone: %s", dir)
		}
		for _, p := range ordered(text, mentions) {
			if !strings.Contains(text, "@"+p) {
				return out, fmt.Errorf("%w: the prompt's text has no @%s", ErrMention, p)
			}
			mention, send, data, err := m.mention(s, dir, root, accepts, p)
			if err != nil {
				return out, err
			}
			out.mentions = append(out.mentions, mention)
			out.send = append(out.send, send)
			if data != nil {
				out.files = append(out.files, data)
			}
		}
	}
	if len(images) > 0 {
		if !accepts.Images {
			return out, fmt.Errorf("%w: %s", ErrNoImages, s.Agent)
		}
		if m.cfg.Uploads == nil {
			return out, fmt.Errorf("%w: this host takes no uploads", ErrImage)
		}
		for _, img := range images {
			data, err := m.cfg.Uploads.Take(img)
			if err != nil {
				return out, fmt.Errorf("%w: %w", ErrImage, err)
			}
			out.images = append(out.images, img)
			out.pictures = append(out.pictures, data)
			out.send = append(out.send, agents.Attachment{Name: img.Name, Image: data, MimeType: img.MimeType})
		}
	}
	return out, nil
}

// keep puts what the prompt gave the agent in the host's store, so the
// record names only what the host has: embedded files' contents, and
// images, which leave the uploads for good.
func (m *Manager) keep(a attached) error {
	if m.cfg.Blobs != nil {
		for _, data := range a.files {
			if _, err := m.cfg.Blobs.Put(data); err != nil {
				return err
			}
		}
	}
	for _, data := range a.pictures {
		if err := m.cfg.Uploads.Keep(data); err != nil {
			return err
		}
	}
	return nil
}

// ordered is mentions without repeats, in the order each first follows an
// @ in text. One the text doesn't have goes last, for attach to refuse.
func ordered(text string, mentions []string) []string {
	var out []string
	for _, p := range mentions {
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	at := func(p string) int {
		if i := strings.Index(text, "@"+p); i >= 0 {
			return i
		}
		return len(text)
	}
	sort.SliceStable(out, func(i, j int) bool { return at(out[i]) < at(out[j]) })
	return out
}

// mention resolves one mentioned file in root, which is dir with its
// links resolved, decides it by the rules, and returns what the record
// says of it, what the agent gets, and the contents to keep when it goes
// whole.
func (m *Manager) mention(s Session, dir, root string, accepts agents.Accepts, p string) (v1.PromptMention, agents.Attachment, []byte, error) {
	rel := filepath.Clean(filepath.FromSlash(p))
	abs := filepath.Join(root, rel)
	real, err := filepath.EvalSymlinks(abs)
	if errors.Is(err, fs.ErrNotExist) {
		return v1.PromptMention{}, agents.Attachment{}, nil, fmt.Errorf("%w: %s isn't a file in the session's directory", ErrMention, p)
	}
	if err != nil {
		return v1.PromptMention{}, agents.Attachment{}, nil, fmt.Errorf("%w: %s: %w", ErrMention, p, err)
	}
	inside, err := filepath.Rel(root, real)
	if err != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) || filepath.IsAbs(inside) {
		return v1.PromptMention{}, agents.Attachment{}, nil, fmt.Errorf("%w: %s lies outside the session's directory", ErrMention, p)
	}
	info, err := os.Stat(real)
	if err != nil || !info.Mode().IsRegular() {
		return v1.PromptMention{}, agents.Attachment{}, nil, fmt.Errorf("%w: %s isn't a file", ErrMention, p)
	}
	// The rules see the path as mentioned and as it resolves, so a link
	// can't lead them around a deny.
	locations := []string{filepath.ToSlash(rel)}
	if l := filepath.ToSlash(inside); l != locations[0] {
		locations = append(locations, l)
	}
	input, _ := json.Marshal(map[string]string{"path": filepath.ToSlash(rel)})
	decision, rule := m.decideCall(s, rules.Call{Kind: "read", Title: "Read " + filepath.ToSlash(rel), Locations: locations, Input: input})
	if decision == v1.RuleDeny {
		return v1.PromptMention{}, agents.Attachment{}, nil, fmt.Errorf("%w: %s (%s)", ErrMentionDenied, p, rules.Text(rule))
	}
	mention := v1.PromptMention{Path: p}
	// The agent is pointed at the file the path leads to, in the
	// directory as it knows it, so its own read of it is decided on that.
	send := agents.Attachment{Path: filepath.Join(dir, inside), Name: p}
	if decision != v1.RuleAllow || !accepts.EmbeddedContext || info.Size() > v1.MaxEmbeddedFile {
		return mention, send, nil, nil
	}
	data, err := readAtMost(real, v1.MaxEmbeddedFile)
	if err != nil || !isText(data) {
		return mention, send, nil, nil
	}
	mention.Embedded, mention.Hash, mention.Size = true, blobs.Hash(data), int64(len(data))
	send.Embedded, send.Text = true, string(data)
	return mention, send, data, nil
}

// readAtMost reads a file that is at most limit bytes long, and fails for
// one that grew past it since it was looked at.
func readAtMost(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("the file is too long")
	}
	return data, nil
}

// isText reports whether data reads as text: UTF-8, without the NUL bytes
// a binary file has.
func isText(data []byte) bool {
	return utf8.Valid(data) && !bytes.ContainsRune(data, 0)
}

// decideCall applies the session's rules, then the host's, and the
// session's trust, to a call, as they decide one the agent asks about:
// allow, ask, or deny, with the rule that denies. A list stands on the
// approver who signed it: once that approver no longer approves, its
// allow rules ask. A deny is final, so trust only settles what the rules
// would ask about.
func (m *Manager) decideCall(s Session, c rules.Call) (string, v1.Rule) {
	list := s.Rules
	if rules.HasAllow(list) && !m.stands(m.ctx, s.RulesBy, s.RulesApprover, s.RulesLapsed) {
		list = rules.WithoutAllows(list)
	}
	list = append(append([]v1.Rule{}, list...), m.hostRules()...)
	decision := rules.Decide(list, c)
	if decision == v1.RuleDeny {
		rule, _ := rules.Denying(list, c)
		return decision, rule
	}
	if decision == v1.RuleAsk && s.Trusted && m.stands(m.ctx, s.TrustedBy, s.TrustApprover, false) {
		decision = v1.RuleAllow
	}
	return decision, v1.Rule{}
}

// Image returns an image one of the session's prompts carried, and its
// bytes as the host keeps them. Only those are served, so a session's
// reader can't read anything else the host keeps.
func (m *Manager) Image(sessionID, hash string) (v1.PromptImage, []byte, error) {
	s, ok := m.Get(sessionID)
	if !ok {
		return v1.PromptImage{}, nil, fmt.Errorf("%w: %s", ErrUnknownSession, sessionID)
	}
	for _, img := range s.Images {
		if img.Hash == hash {
			data, err := m.Blob(hash)
			if err != nil {
				return v1.PromptImage{}, nil, err
			}
			return img, data, nil
		}
	}
	return v1.PromptImage{}, nil, errors.New("no prompt of the session carried that image")
}

// Transcript lines that list, under a prompt, what it carried besides its
// text.
const (
	// MentionLinePrefix opens the line for a file the prompt mentioned.
	MentionLinePrefix = "\U0001F4C4 "
	// ImageLinePrefix opens the line for an image the prompt carried.
	ImageLinePrefix = "\U0001F5BC "
)

// MentionLine is the transcript line for a mentioned file: its path, and
// whether the agent got its contents or a link to it.
func MentionLine(mt v1.PromptMention) string {
	if mt.Embedded {
		return MentionLinePrefix + mt.Path + " (contents)"
	}
	return MentionLinePrefix + mt.Path + " (link)"
}

// ImageLine is the transcript line for an image: its name, and a marker
// naming it by its hash, which a conversation's reader follows to fetch
// it and which Markdown doesn't show.
func ImageLine(img v1.PromptImage) string {
	return ImageLinePrefix + img.Name + " <!-- image:" + img.Hash + " -->"
}

// youSection is the transcript's record of a prompt: its text, then a
// line for each file it mentioned and each image it carried.
func youSection(text string, mentions []v1.PromptMention, images []v1.PromptImage) string {
	var b strings.Builder
	b.WriteString("## You\n\n" + strings.TrimSpace(text) + "\n\n")
	for _, mt := range mentions {
		b.WriteString(MentionLine(mt) + "\n")
	}
	for _, img := range images {
		b.WriteString(ImageLine(img) + "\n")
	}
	if len(mentions)+len(images) > 0 {
		b.WriteString("\n")
	}
	b.WriteString("## Agent\n\n")
	return b.String()
}

// dropWorktree removes a worktree and its branch that a start made before
// it was refused.
func dropWorktree(repo, dir, branch string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, _ = git(ctx, repo, "worktree", "remove", "--force", dir)
	_, _ = git(ctx, repo, "branch", "-D", branch)
}
