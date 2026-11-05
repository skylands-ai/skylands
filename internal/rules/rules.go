package rules

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	v1 "github.com/skylands-ai/skylands/api/v1"
)

// Kinds are the tool call kinds ACP defines, which a rule can name.
var Kinds = []string{"read", "edit", "delete", "move", "search", "execute", "think", "fetch", "other"}

// MaxRules is the most rules one repository or session keeps.
const MaxRules = 100

// SourceHost marks a host default rule, which any other rule beats among
// equals.
const SourceHost = "host"

// Call is a tool call as the agent proposed it, which rules are matched
// against. Locations are repo-relative inside the worktree and absolute
// outside it.
type Call struct {
	Kind      string
	Title     string
	Locations []string
	Input     json.RawMessage
}

// Decide applies rules to a call and returns v1.RuleAllow, v1.RuleAsk, or
// v1.RuleDeny. A deny is final: any matching deny rule denies the call,
// however specific the allow rules beside it. Otherwise the most specific
// matching rule wins, among equals an ask beats an allow and a session's
// own rule beats a copied or default one, and no match means ask.
func Decide(rules []v1.Rule, c Call) string {
	best, bestScore := "", -1
	for _, r := range rules {
		if !Matches(r, c) {
			continue
		}
		if r.Decision == v1.RuleDeny {
			return v1.RuleDeny
		}
		score := 0
		if r.Kind != v1.RuleAnyKind {
			score += 8
		}
		if r.Pattern != "" {
			score += 4
		}
		switch r.Source {
		case "":
			score += 2
		case v1.RuleFromRepo:
			score++
		}
		score = score*4 + rank(r.Decision)
		if score > bestScore {
			best, bestScore = r.Decision, score
		}
	}
	if best == "" {
		return v1.RuleAsk
	}
	return best
}

func rank(decision string) int {
	switch decision {
	case v1.RuleDeny:
		return 2
	case v1.RuleAsk:
		return 1
	}
	return 0
}

// Matches reports whether one rule applies to a call.
func Matches(r v1.Rule, c Call) bool {
	if r.Kind != v1.RuleAnyKind && r.Kind != c.Kind {
		return false
	}
	if r.Pattern == "" {
		return true
	}
	switch c.Kind {
	case "execute":
		return matchCommand(r.Pattern, inputString(c.Input, "command", c.Title))
	case "fetch":
		return Glob(r.Pattern, inputString(c.Input, "url", c.Title))
	}
	if len(c.Locations) == 0 {
		return false
	}
	// A deny applies if any location matches; anything else needs all of
	// them to, so an allow never covers more than it names.
	any := r.Decision == v1.RuleDeny
	for _, l := range c.Locations {
		m := matchPath(r.Pattern, l)
		if any && m {
			return true
		}
		if !any && !m {
			return false
		}
	}
	return !any
}

// matchCommand matches a command against a pattern, where a pattern that
// ends in " *" also matches the command without the trailing arguments,
// so "go test *" covers a bare "go test".
func matchCommand(pattern, command string) bool {
	if Glob(pattern, command) {
		return true
	}
	return strings.HasSuffix(pattern, " *") && Glob(strings.TrimSuffix(pattern, " *"), command)
}

// matchPath matches one location against a path pattern. The word
// "worktree" matches every relative location, which is what the daemon
// makes of a path inside the worktree.
func matchPath(pattern, loc string) bool {
	if pattern == v1.RuleInWorktree {
		return !filepath.IsAbs(loc)
	}
	return Glob(pattern, loc)
}

// Glob matches s against pattern, where * stands for any run of
// characters including none, and everything else matches itself.
func Glob(pattern, s string) bool {
	px, sx := 0, 0
	star, mark := -1, 0
	for sx < len(s) {
		switch {
		case px < len(pattern) && pattern[px] == '*':
			star, mark = px, sx
			px++
		case px < len(pattern) && pattern[px] == s[sx]:
			px++
			sx++
		case star >= 0:
			px = star + 1
			mark++
			sx = mark
		default:
			return false
		}
	}
	for px < len(pattern) && pattern[px] == '*' {
		px++
	}
	return px == len(pattern)
}

// inputString reads one string field of a tool call's input, or falls
// back to the given text.
func inputString(input json.RawMessage, field, fallback string) string {
	var m map[string]any
	if len(input) > 0 && json.Unmarshal(input, &m) == nil {
		if v, ok := m[field].(string); ok && v != "" {
			return v
		}
	}
	return fallback
}

// Validate checks a rule set and returns it normalized: decisions and
// kinds known, patterns trimmed and short, and no more than MaxRules.
func Validate(rules []v1.Rule) ([]v1.Rule, error) {
	if len(rules) > MaxRules {
		return nil, fmt.Errorf("too many rules: %d, the most is %d", len(rules), MaxRules)
	}
	out := make([]v1.Rule, 0, len(rules))
	for i, r := range rules {
		r.Decision = strings.ToLower(strings.TrimSpace(r.Decision))
		switch r.Decision {
		case v1.RuleAllow, v1.RuleAsk, v1.RuleDeny:
		default:
			return nil, fmt.Errorf("rule %d: decision %q is not allow, ask, or deny", i+1, r.Decision)
		}
		r.Kind = strings.ToLower(strings.TrimSpace(r.Kind))
		if r.Kind == "" || r.Kind == "any" {
			r.Kind = v1.RuleAnyKind
		}
		if r.Kind != v1.RuleAnyKind && !knownKind(r.Kind) {
			return nil, fmt.Errorf("rule %d: kind %q is not one of %s, or *", i+1, r.Kind, strings.Join(Kinds, ", "))
		}
		r.Pattern = strings.TrimSpace(r.Pattern)
		if len(r.Pattern) > 256 {
			return nil, fmt.Errorf("rule %d: the pattern is too long", i+1)
		}
		switch r.Source {
		case "", v1.RuleFromRepo:
		default:
			return nil, errors.New("a rule's source can only be the repository")
		}
		out = append(out, r)
	}
	return out, nil
}

func knownKind(kind string) bool {
	for _, k := range Kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// Defaults are a host's rules when nothing more specific applies: the
// given kinds pass.
func Defaults(kinds []string) []v1.Rule {
	out := make([]v1.Rule, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, v1.Rule{Decision: v1.RuleAllow, Kind: k, Source: SourceHost})
	}
	return out
}

// Inherit copies a repository's rules for a new session, marking where
// they came from.
func Inherit(rules []v1.Rule) []v1.Rule {
	out := make([]v1.Rule, 0, len(rules))
	for _, r := range rules {
		r.Source = v1.RuleFromRepo
		out = append(out, r)
	}
	return out
}

// WithoutAllows is a rule list with its allow rules left out, which is
// how a list applies once the approver who signed it no longer approves:
// its deny and ask rules still apply, and what it allowed asks.
func WithoutAllows(list []v1.Rule) []v1.Rule {
	out := make([]v1.Rule, 0, len(list))
	for _, r := range list {
		if r.Decision != v1.RuleAllow {
			out = append(out, r)
		}
	}
	return out
}

// HasAllow reports whether a list allows anything, which is what a
// signature behind it vouches for.
func HasAllow(list []v1.Rule) bool {
	for _, r := range list {
		if r.Decision == v1.RuleAllow {
			return true
		}
	}
	return false
}

// Text is a rule on one line, the way the CLI and the browser show it.
func Text(r v1.Rule) string {
	s := r.Decision + " " + r.Kind
	if r.Pattern != "" {
		s += " " + r.Pattern
	}
	return s
}
