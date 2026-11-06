package agents

// CodexAdapterPackage is the npm package, at an exact version, that runs
// Codex over ACP, pinned for the same reason as ClaudeAdapterPackage. The
// adapter brings its own Codex, which npm resolves within the range the
// adapter declares, so the Codex underneath can be newer than the one the
// adapter was released with.
const CodexAdapterPackage = "@agentclientprotocol/codex-acp@2.5.0"

// codexModes are the modes a Codex session may be in, set through its
// mode setting: read-only alone. Codex has a sandbox and approval policy
// of its own, and the adapter's default mode lets Codex edit the worktree
// and run commands inside it without asking, so those calls would never
// reach the daemon's rules or a person. Read-only is the one built-in
// mode that changes nothing unasked; its workspace-write, agent, and
// agent-full-access modes each act without asking. Codex asks before
// every edit. A command runs without asking in Codex's read-only sandbox,
// where it can read but not write or reach the network; one the sandbox
// stops fails there, unless Codex asks to run it outside the sandbox,
// which it does only when its model chooses to. The adapter offers no
// other approval policy for its built-in modes, so codexInstructions
// tells the model when to ask. Codex's Plan is not one of its modes but a
// separate collaboration_mode setting, which does not change what it
// asks about.
var codexModes = []string{"read-only"}

// codexInstructions go after Codex's own developer instructions, including
// any in the person's Codex config. Without them Codex's model tends to
// report a command its sandbox stopped as blocked, rather than ask to run
// it outside the sandbox, which is the request a person or the host's
// rules can allow.
const codexInstructions = "Skylands runs this session and shows a person every action that needs approval. " +
	"Your shell sandbox is read-only and has no network access, and that is deliberate: it is how actions reach the person. " +
	"When a command you need fails because of the sandbox, such as a read-only file system, a denied write, or no network, " +
	"run it again with escalated permissions and a short justification instead of giving up or asking in your reply. " +
	"Ask for escalated permissions from the start for a command you know must write or reach the network, " +
	"such as a build, a test run, a package install, or a git commit. " +
	"The request goes to the person or to the host's rules, which allow or deny it."
