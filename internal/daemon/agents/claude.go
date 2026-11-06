package agents

import "github.com/spachava753/acp-sdk/acp"

// claudeModes are the modes a Claude Code session may be in, set through
// its mode setting: Manual, which the adapter describes as "Always ask
// before making changes", where every session starts, and Plan, which
// makes no changes until a person approves its plan. Left alone, the
// adapter starts a new session in the permissions.defaultMode of Claude
// Code's settings, and a loaded one in the mode its transcript was last
// in. A repository's committed settings can't name a mode that asks
// less, but the user's settings and the repository's uncommitted local
// settings can. acceptEdits, auto, and bypassPermissions each let Claude
// Code act without asking, so those calls would never reach the daemon's
// rules or a person. Leaving Plan goes through Claude Code's plan
// approval, whose choices that switch to one of those are "always"
// options, which Skylands never gives. Claude Code still runs, without
// asking, a call that an allow rule in its own settings matches, which
// preflight warns about.
var claudeModes = []string{"default", "plan"}

// claudeWithheld are Claude Code's commands that act outside the
// session's tool calls: remote-control opens the session to claude.ai and
// the Claude app; config sets Claude Code's settings directly; schedule,
// ultrareview, and loop start cloud agents or recurring runs, some of
// them paid; and the rest change the account or Claude Code's
// configuration. Checked against the commands Claude Code announced in a
// live session under the pinned adapter; check them again when the pin
// moves, since Claude Code's commands come with it.
var claudeWithheld = []string{
	"remote-control", "config", "schedule", "ultrareview", "loop",
	"design", "design-consent", "design-revoke", "usage-credits", "import", "auto-mode-setup",
}

// claudeMeta goes with every session Claude Code opens or loads. It takes
// bypassPermissions out of the session's modes, so nothing can switch the
// session to it, Claude Code included, whatever its settings allow.
func claudeMeta() acp.Meta {
	return acp.Meta{"claudeCode": map[string]any{"options": map[string]any{"allowDangerouslySkipPermissions": false}}}
}
