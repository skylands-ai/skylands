package rules

import (
	"encoding/json"
	"testing"

	v1 "github.com/skylands-ai/skylands/api/v1"
)

func TestGlob(t *testing.T) {
	cases := []struct {
		pattern, s string
		want       bool
	}{
		{"go test *", "go test ./...", true},
		{"go test *", "go test", false},
		{"*", "", true},
		{"*", "anything at all", true},
		{"web/*", "web/src/a.ts", true},
		{"web/*", "src/web/a.ts", false},
		{"*.go", "a/b/c.go", true},
		{"*.go", "c.gox", false},
		{"https://github.com/*", "https://github.com/x/y", true},
		{"rm -rf *", "rm -rf /", true},
		{"rm -rf *", "rm -r /", false},
		{"a*b*c", "axxbyyc", true},
		{"a*b*c", "axxbyy", false},
	}
	for _, c := range cases {
		if got := Glob(c.pattern, c.s); got != c.want {
			t.Errorf("Glob(%q, %q) = %v, want %v", c.pattern, c.s, got, c.want)
		}
	}
}

func TestDecide(t *testing.T) {
	input := func(kv ...string) json.RawMessage {
		m := map[string]string{}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		b, _ := json.Marshal(m)
		return b
	}
	defaults := Defaults([]string{"read", "search", "think"})
	repo := Inherit([]v1.Rule{
		{Decision: "allow", Kind: "edit", Pattern: "worktree"},
		{Decision: "allow", Kind: "execute", Pattern: "go test *"},
		{Decision: "deny", Kind: "execute", Pattern: "rm -rf *"},
		{Decision: "ask", Kind: "fetch"},
	})
	session := []v1.Rule{{Decision: "allow", Kind: "execute"}}
	all := append(append(append([]v1.Rule{}, session...), repo...), defaults...)

	cases := []struct {
		name  string
		rules []v1.Rule
		call  Call
		want  string
	}{
		{"nothing matches", nil, Call{Kind: "edit"}, "ask"},
		{"host default allows reads", defaults, Call{Kind: "read", Locations: []string{"a.go"}}, "allow"},
		{"edit inside the worktree", repo, Call{Kind: "edit", Locations: []string{"a.go", "b/c.go"}}, "allow"},
		{"edit outside the worktree", repo, Call{Kind: "edit", Locations: []string{"/etc/hosts"}}, "ask"},
		{"edit partly outside", repo, Call{Kind: "edit", Locations: []string{"a.go", "/etc/hosts"}}, "ask"},
		{"edit with no location", repo, Call{Kind: "edit"}, "ask"},
		{"go test", repo, Call{Kind: "execute", Input: input("command", "go test ./...")}, "allow"},
		{"bare go test", repo, Call{Kind: "execute", Input: input("command", "go test")}, "allow"},
		{"go build", repo, Call{Kind: "execute", Input: input("command", "go build ./...")}, "ask"},
		{"command from the title", repo, Call{Kind: "execute", Title: "go test ./internal/..."}, "allow"},
		{"rm -rf", repo, Call{Kind: "execute", Input: input("command", "rm -rf /tmp/x")}, "deny"},
		{"fetch asks", append(repo, v1.Rule{Decision: "allow", Kind: "*"}), Call{Kind: "fetch", Input: input("url", "https://x")}, "ask"},
		{"session allow beats nothing", all, Call{Kind: "execute", Input: input("command", "go build")}, "allow"},
		{"repo deny with a pattern beats a session allow without", all, Call{Kind: "execute", Input: input("command", "rm -rf /")}, "deny"},
		{"session rule beats a repo rule among equals", append([]v1.Rule{{Decision: "ask", Kind: "edit", Pattern: "worktree"}}, repo...), Call{Kind: "edit", Locations: []string{"a.go"}}, "ask"},
		{"a session deny beats a copied allow", append([]v1.Rule{{Decision: "deny", Kind: "edit", Pattern: "worktree"}}, repo...), Call{Kind: "edit", Locations: []string{"a.go"}}, "deny"},
		{"deny beats allow among equals", []v1.Rule{{Decision: "allow", Kind: "read"}, {Decision: "deny", Kind: "read"}}, Call{Kind: "read", Locations: []string{"a"}}, "deny"},
		{"ask beats allow among equals", []v1.Rule{{Decision: "allow", Kind: "read"}, {Decision: "ask", Kind: "read"}}, Call{Kind: "read", Locations: []string{"a"}}, "ask"},
		{"a deny is final over a more specific allow", []v1.Rule{{Decision: "deny", Kind: "execute"}, {Decision: "allow", Kind: "execute", Pattern: "go test *"}}, Call{Kind: "execute", Input: input("command", "go test ./...")}, "deny"},
		{"a copied deny is final over the session's own allow", append([]v1.Rule{{Decision: "allow", Kind: "execute", Pattern: "rm -rf /tmp/*"}}, repo...), Call{Kind: "execute", Input: input("command", "rm -rf /tmp/x")}, "deny"},
		{"a deny of any kind is final", append([]v1.Rule{{Decision: "deny", Kind: "*"}}, defaults...), Call{Kind: "read", Locations: []string{"a"}}, "deny"},
		{"a more specific ask beats an allow", []v1.Rule{{Decision: "allow", Kind: "execute"}, {Decision: "ask", Kind: "execute", Pattern: "git push *"}}, Call{Kind: "execute", Input: input("command", "git push origin")}, "ask"},
		{"a deny on any location", []v1.Rule{{Decision: "deny", Kind: "edit", Pattern: "web/*"}}, Call{Kind: "edit", Locations: []string{"a.go", "web/x.ts"}}, "deny"},
		{"any kind with a pattern reads the call's kind", []v1.Rule{{Decision: "deny", Kind: "*", Pattern: "worktree"}}, Call{Kind: "delete", Locations: []string{"a.go"}}, "deny"},
		{"unknown kind only matches any", defaults, Call{Kind: ""}, "ask"},
	}
	for _, c := range cases {
		if got := Decide(c.rules, c.call); got != c.want {
			t.Errorf("%s: Decide = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestWithoutAllows(t *testing.T) {
	list := []v1.Rule{{Decision: "allow", Kind: "edit", Pattern: "worktree"}, {Decision: "deny", Kind: "execute", Pattern: "rm *"}, {Decision: "ask", Kind: "fetch"}}
	if !HasAllow(list) || HasAllow(list[1:]) {
		t.Errorf("HasAllow is wrong about %+v", list)
	}
	left := WithoutAllows(list)
	if len(left) != 2 || left[0].Decision != "deny" || left[1].Decision != "ask" {
		t.Errorf("WithoutAllows = %+v", left)
	}
	if got := Decide(append(left, Defaults([]string{"read"})...), Call{Kind: "edit", Locations: []string{"a.go"}}); got != "ask" {
		t.Errorf("an edit the lapsed list allowed = %q, want ask", got)
	}
	if got := Decide(append(left, Defaults([]string{"read"})...), Call{Kind: "read", Locations: []string{"a.go"}}); got != "allow" {
		t.Errorf("a read the host allows = %q, want allow", got)
	}
}

func TestValidate(t *testing.T) {
	got, err := Validate([]v1.Rule{{Decision: " Allow ", Kind: "", Pattern: " go test * "}, {Decision: "deny", Kind: "any"}, {Decision: "ask", Kind: "fetch", Source: "repo"}})
	if err != nil || len(got) != 3 || got[0].Decision != "allow" || got[0].Kind != "*" || got[0].Pattern != "go test *" || got[1].Kind != "*" || got[2].Source != "repo" {
		t.Errorf("Validate = %+v, %v", got, err)
	}
	for _, bad := range [][]v1.Rule{
		{{Decision: "maybe", Kind: "read"}},
		{{Decision: "allow", Kind: "shout"}},
		{{Decision: "allow", Kind: "read", Source: "host"}},
		make([]v1.Rule, MaxRules+1),
	} {
		if _, err := Validate(bad); err == nil {
			t.Errorf("Validate(%+v) accepted", bad[:1])
		}
	}
	if Text(v1.Rule{Decision: "allow", Kind: "execute", Pattern: "go test *"}) != "allow execute go test *" || Text(v1.Rule{Decision: "ask", Kind: "fetch"}) != "ask fetch" {
		t.Error("Text")
	}
}
