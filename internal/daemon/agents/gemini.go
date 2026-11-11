package agents

// GeminiPackage is the npm package, at an exact version, that runs Gemini
// CLI, pinned for the same reason as ClaudeAdapterPackage. Gemini CLI
// speaks ACP itself with --acp, so no adapter stands between them.
const GeminiPackage = "@google/gemini-cli@0.68.0"

// geminiModes are the modes a Gemini CLI session may be in, set through
// ACP's session modes, which Gemini CLI offers in place of a config
// option: default, which asks before every tool call its policies don't
// allow, and where every session starts, and plan, which is read-only.
// autoEdit approves edits itself and yolo every tool call, so those calls
// would never reach the daemon's rules or a person. Gemini CLI starts a
// session in its settings' general.defaultApprovalMode, which can be
// autoEdit, so the mode is always set.
//
// Gemini CLI also moves between them itself: its enter_plan_mode tool
// asks first and then goes to plan, and exit_plan_mode, once its plan is
// allowed, goes back to default when the client, like the daemon, is
// interactive. Only an "always" answer to an edit moves it to autoEdit,
// and the daemon never gives one.
var geminiModes = []string{"default", "plan"}

// geminiModeMarker begins the message chunk Gemini CLI sends when its
// mode changes, followed by the mode, in place of ACP's
// current_mode_update. It comes for a change the daemon asked for too.
const geminiModeMarker = "[MODE_UPDATE] "

// geminiWithheld are Gemini CLI's commands that act outside the session's
// tool calls: extensions installs, links, updates, enables, and removes
// extensions, which run code and change Gemini CLI's configuration for
// every session on the host; restore puts files and the conversation back
// to a checkpoint; and init writes GEMINI.md itself before asking the
// model to fill it in. Each is announced by its words, such as
// "extensions install", and withheld by its first. Checked against the
// commands Gemini CLI announced in a session at the pinned version.
var geminiWithheld = []string{"extensions", "restore", "init"}

// geminiLogin is how to log Gemini CLI in. It doesn't report its login
// over ACP, and fails to open a session without one. A login with Google
// is kept in ~/.gemini, so it reaches a service, which gets only PATH from
// the shell. ~/.gemini/.env is read only in a folder Gemini CLI trusts,
// which a session's worktree is not until someone trusts it.
const geminiLogin = "set GEMINI_API_KEY, or run npx -y " + GeminiPackage + " and log in with Google"
