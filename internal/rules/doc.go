// Package rules defines the rule language that settles tool calls on a host
// before they reach a person, and evaluates a rule set against a tool call to
// allow it, deny it, or leave it for a person to decide.
//
// A rule is a decision, a tool call kind, and an optional pattern. Patterns
// are globs where * stands for anything: a command pattern for execute, a
// URL pattern for fetch, and a path pattern for the kinds that name files,
// where the word "worktree" means every location inside the session's
// worktree. A deny is final: any matching deny rule denies the call,
// whatever else matches. Otherwise the most specific matching rule wins;
// among equals an ask beats an allow, and a rule set on the session beats
// one copied from its repository, which beats a host default. No match
// means ask.
package rules
