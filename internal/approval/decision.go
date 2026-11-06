package approval

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/nats-io/nkeys"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/canonical"
)

// DecisionVersion is the version of the decision formats below. A daemon
// refuses a signature over any other.
const DecisionVersion = 1

// The kinds of decision an approver signs. Each decision names its kind,
// so a signature over one kind never verifies as another.
const (
	KindToolCall     = "toolcall"
	KindAnswer       = "answer"
	KindTrust        = "trust"
	KindSessionRules = "session-rules"
	KindRepoRules    = "repo-rules"
	KindStart        = "start"
	KindPrompt       = "prompt"
	KindChange       = "change"
)

// The actions a Change decision names.
const (
	ChangeCommit      = "commit"
	ChangeMerge       = "merge"
	ChangePullRequest = "pull-request"
)

// Decision is what an approver signs: one of the decisions below. Its
// canonical JSON (RFC 8785), hashed with SHA-256, is what the signature
// covers. The browser builds it from what it shows, and the daemon from
// its own record, so a signature verifies only if the two agree on every
// field.
type Decision interface {
	// header is the decision's version, the kind it names, and the kind
	// its type is.
	header() (v int, kind, want string)
}

// ToolCall allows or denies one tool call.
type ToolCall struct {
	V    int    `json:"v"`
	Kind string `json:"kind"`
	// World is the world's owner key.
	World   string `json:"world"`
	Host    string `json:"host"`
	Session string `json:"session"`
	Request string `json:"request"`
	// Decision is "allow" or "deny".
	Decision string `json:"decision"`
	// Option is the option id an approval chose, and "" for a denial.
	Option string `json:"option"`
	// Input is the SHA-256 of the tool call's exact input, in lowercase
	// hex, as PermissionRequested names it.
	Input string `json:"input"`
}

func (d ToolCall) header() (int, string, string) { return d.V, d.Kind, KindToolCall }

// Answer answers a question, or declines it.
type Answer struct {
	V        int    `json:"v"`
	Kind     string `json:"kind"`
	World    string `json:"world"`
	Host     string `json:"host"`
	Session  string `json:"session"`
	Question string `json:"question"`
	// Form is FormHash of the question as the daemon recorded it, so a
	// question reworded on the way doesn't verify.
	Form     string `json:"form"`
	Declined bool   `json:"declined"`
	// Answers is the SHA-256 of the answers' canonical JSON, and "" for a
	// decline.
	Answers string `json:"answers"`
}

func (d Answer) header() (int, string, string) { return d.V, d.Kind, KindAnswer }

// Trust turns a session's trust on or off.
type Trust struct {
	V       int    `json:"v"`
	Kind    string `json:"kind"`
	World   string `json:"world"`
	Host    string `json:"host"`
	Session string `json:"session"`
	Trusted bool   `json:"trusted"`
	// Revision is the revision of the session's trust this replaces.
	Revision int `json:"revision"`
}

func (d Trust) header() (int, string, string) { return d.V, d.Kind, KindTrust }

// SessionRules sets a session's rules.
type SessionRules struct {
	V       int    `json:"v"`
	Kind    string `json:"kind"`
	World   string `json:"world"`
	Host    string `json:"host"`
	Session string `json:"session"`
	// Rules is RulesHash of the whole list.
	Rules string `json:"rules"`
	// Revision is the revision of the session's rules this replaces.
	Revision int `json:"revision"`
}

func (d SessionRules) header() (int, string, string) { return d.V, d.Kind, KindSessionRules }

// RepoRules sets a repository's rules.
type RepoRules struct {
	V     int    `json:"v"`
	Kind  string `json:"kind"`
	World string `json:"world"`
	Host  string `json:"host"`
	Repo  string `json:"repo"`
	// Rules is RulesHash of the whole list.
	Rules string `json:"rules"`
	// Revision is the revision of the repository's rules this replaces.
	Revision int `json:"revision"`
}

func (d RepoRules) header() (int, string, string) { return d.V, d.Kind, KindRepoRules }

// Start starts a session with its first prompt. The browser picks the
// session's id, so a signed start starts one session only.
type Start struct {
	V       int    `json:"v"`
	Kind    string `json:"kind"`
	World   string `json:"world"`
	Host    string `json:"host"`
	Session string `json:"session"`
	Repo    string `json:"repo"`
	Agent   string `json:"agent"`
	// Mode and Model are as the command names them: empty for the
	// adapter's first mode and the agent's own model.
	Mode       string `json:"mode"`
	Model      string `json:"model"`
	InCheckout bool   `json:"inCheckout"`
	// Prompt is TextHash of the prompt.
	Prompt string `json:"prompt"`
}

func (d Start) header() (int, string, string) { return d.V, d.Kind, KindStart }

// Prompt sends a session another prompt.
type Prompt struct {
	V       int    `json:"v"`
	Kind    string `json:"kind"`
	World   string `json:"world"`
	Host    string `json:"host"`
	Session string `json:"session"`
	// Revision is the session's revision the prompt was sent at, which
	// the prompt moves on, so the signature can't be used again.
	Revision int `json:"revision"`
	// Prompt is TextHash of the prompt.
	Prompt string `json:"prompt"`
}

func (d Prompt) header() (int, string, string) { return d.V, d.Kind, KindPrompt }

// Change commits a session's worktree, merges its branch, or opens a pull
// request from it.
type Change struct {
	V       int    `json:"v"`
	Kind    string `json:"kind"`
	World   string `json:"world"`
	Host    string `json:"host"`
	Session string `json:"session"`
	// Revision is as in Prompt.
	Revision int `json:"revision"`
	// Action is ChangeCommit, ChangeMerge, or ChangePullRequest.
	Action string `json:"action"`
	// Text is TextHash of a commit's message, PullRequestHash of a pull
	// request's title and body, and "" for a merge.
	Text string `json:"text"`
}

func (d Change) header() (int, string, string) { return d.V, d.Kind, KindChange }

// TextHash is the SHA-256 of a text's UTF-8 bytes, in lowercase hex.
func TextHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// PullRequestHash is the SHA-256 of a pull request's title and body as
// canonical JSON, in lowercase hex.
func PullRequestHash(title, body string) (string, error) {
	return HashJSON(map[string]string{"title": title, "body": body})
}

// Digest is the SHA-256 of the decision's canonical JSON, which is what is
// signed.
func Digest(d Decision) ([]byte, error) {
	data, err := canonical.Marshal(d)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	return sum[:], nil
}

// SignDecision signs d with an approver key. The browser signs the same
// way; this is for the Go side and for tests.
func SignDecision(kp nkeys.KeyPair, d Decision) (v1.DecisionSignature, error) {
	digest, err := Digest(d)
	if err != nil {
		return v1.DecisionSignature{}, err
	}
	pub, err := kp.PublicKey()
	if err != nil {
		return v1.DecisionSignature{}, err
	}
	sig, err := kp.Sign(digest)
	if err != nil {
		return v1.DecisionSignature{}, err
	}
	return v1.DecisionSignature{Approver: pub, Sig: base64.RawURLEncoding.EncodeToString(sig)}, nil
}

// ErrStale is a change to trust or rules made against an older revision
// than the daemon's: something else changed them since.
var ErrStale = errors.New("this changed since it was shown; look again")

// ErrUnsigned is a decision, or a command that sets an agent working or
// changes the repository, through the citadel that no approver signed.
var ErrUnsigned = errors.New("this must be signed by one of the world's approvers when it comes through the citadel")

// ErrBadSignature is a decision signature that doesn't verify.
var ErrBadSignature = errors.New("the decision's signature doesn't verify")

// VerifyDecision checks that s is its approver's signature over d. Whether
// the approver approves for the world is the registry's to say.
func VerifyDecision(d Decision, s v1.DecisionSignature) error {
	v, kind, want := d.header()
	if v != DecisionVersion {
		return fmt.Errorf("decision version %d is not %d", v, DecisionVersion)
	}
	if kind != want {
		return fmt.Errorf("a %s decision names the kind %q", want, kind)
	}
	if !nkeys.IsValidPublicUserKey(s.Approver) {
		return errors.New("the decision isn't signed by an approver key")
	}
	kp, err := nkeys.FromPublicKey(s.Approver)
	if err != nil {
		return err
	}
	sig, err := base64.RawURLEncoding.DecodeString(s.Sig)
	if err != nil {
		return ErrBadSignature
	}
	digest, err := Digest(d)
	if err != nil {
		return err
	}
	if kp.Verify(digest, sig) != nil {
		return ErrBadSignature
	}
	return nil
}

// HashJSON is the SHA-256 of v's canonical JSON, in lowercase hex.
func HashJSON(v any) (string, error) {
	data, err := canonical.Marshal(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// formOption and formField are a question's form with every property
// present, so the browser and the daemon hash the same JSON whatever
// either leaves out when it is empty.
type formOption struct {
	Value       string `json:"value"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

type formField struct {
	Key         string       `json:"key"`
	Title       string       `json:"title"`
	Description string       `json:"description"`
	Type        string       `json:"type"`
	Options     []formOption `json:"options"`
	Required    bool         `json:"required"`
}

// FormHash is the SHA-256 of a question's canonical JSON: its message and
// its fields, each with every property, an absent one empty.
func FormHash(message string, fields []v1.QuestionField) (string, error) {
	form := struct {
		Message string      `json:"message"`
		Fields  []formField `json:"fields"`
	}{Message: message, Fields: []formField{}}
	for _, f := range fields {
		ff := formField{Key: f.Key, Title: f.Title, Description: f.Description, Type: f.Type, Options: []formOption{}, Required: f.Required}
		for _, o := range f.Options {
			ff.Options = append(ff.Options, formOption(o))
		}
		form.Fields = append(form.Fields, ff)
	}
	return HashJSON(form)
}

// AnswersHash is the SHA-256 of answers' canonical JSON.
func AnswersHash(answers json.RawMessage) (string, error) {
	return HashJSON(answers)
}

// hashedRule is a rule with every property, as RulesHash hashes it.
type hashedRule struct {
	Decision string `json:"decision"`
	Kind     string `json:"kind"`
	Pattern  string `json:"pattern"`
	Source   string `json:"source"`
}

// RulesHash is the SHA-256 of a rule list's canonical JSON, each rule with
// every property, an absent one empty. The list is hashed as the daemon
// keeps it, normalized as rules.Validate leaves it.
func RulesHash(list []v1.Rule) (string, error) {
	out := make([]hashedRule, 0, len(list))
	for _, r := range list {
		out = append(out, hashedRule(r))
	}
	return HashJSON(out)
}
