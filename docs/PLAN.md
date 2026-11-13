# Plan: first MVP

The first MVP is the citadel web application showing the citadel and the
islands around it, with islands appearing as daemons come up and going grey
as they go down. It runs on NATS micro ping for presence, as the design
specifies. No events, streams, or aggregates are needed for it.

## Status

- Milestone 1 is done: `devworld up` serves the citadel in the sky with the
  NATS facts in the panel.
- Milestones 2 and 3 are done: `daemon up` registers the `skylands-daemon`
  micro service through its leaf node, the citadel polls it, and the browser
  gets a snapshot plus island deltas.
- The island part of milestone 4 is done: islands rise with stable placement
  and go grey on a slack tether when unreachable. What remains of milestone 4
  is the terrace, building, airship, and work order code.
- Milestone 5 is done: `test/e2e` runs hosts coming, going, and restarting,
  and a NATS restart under a live citadel and daemon. The README has the
  quickstart, and the browser shows a lost citadel as a dimmed world.
- The terrain slice is done, the first real event path: `repo register`
  reaches the daemon over its Unix socket, the Host aggregate records
  `RepoRegistered` and `TerrainSnapshotted` in the host's JetStream stream,
  the citadel mirrors that stream into the world's domain and projects it,
  and the browser shows a terrace with a building per tracked file, with
  terrace and file levels. The event-sourcing kit (`internal/es`), the
  public event types (`api/v1`), and the socket API now exist for everything
  after it.
- Milestone 6a is done: a session runs. The command bar on a terrace sends
  StartSession straight to that island's daemon by request-reply, the
  Session aggregate checks out a git worktree and drives the agent over ACP,
  and SessionStarted, PromptSent, AgentSpawned, ToolCallStarted and
  Completed, PlanUpdated, UsageReported, and SessionEnded or AgentFailed
  flow back through the mirror into a sessions projection the terrace panel
  shows. Two adapters exist: the built-in fake agent and Claude Code through
  its npm ACP adapter. Permissions are auto-allowed until milestone 7.
  Dispatch rules, SendPrompt, and Claude Code hooks are deferred.
- Milestone 6b is done: airships. Each running session is an airship on its
  terrace, hull and color by provider. It takes off from the terrace, flies
  to the building of the file the agent is on, sits on its roof, and lights
  it blue for a read or amber for an edit, red with a "2 sessions" tag when
  two sessions share it. Files without a building are worked from a slow
  circle over the terrace. Altitude and sandbags follow context use. A
  finished session's airship lifts away and fades. The agent level follows
  the airship, and the agent panel shows the plan, activity, response, and
  worktree diff fetched from the host. Compaction puffs and subagent leashes
  wait for their events.
- Milestone 7 is done: a working session. Sessions are multi-turn: the
  agent process stays up between turns, TurnEnded leaves the session idle
  and waiting for a person, and SendPrompt and StopAgent continue or end it
  from the conversation. The agent titles its conversation in a side session
  as the first turn starts (SessionTitled). The Inbox view replaces the
  world with a table of every session on every island, newest activity
  first, with an Open or All filter, and an open conversation reads as a
  chat with Markdown rendered safely as DOM and the reply box at the bottom.
  The conversation streams: every transcript append is published on the
  session's live subject with its byte offset, plain NATS with no stream,
  and a browser watching the conversation grows the running bubble in
  place, with tool calls as chips where they happened, and reads the file
  once when the turn ends. "Waiting on you" means idle with a last message
  you have not read, or a tool call waiting for an answer; the tab title
  and the Inbox badge count those.
- Milestone 8 is done: approvals. The host's rules let reads, searches, and
  thinking pass; everything else blocks the agent until a person answers
  from the conversation, the inbox, or `request approve` on the host, or
  the session is trusted. Every request and answer is recorded.
- Milestone 9, first part, is done: a session survives a daemon restart.
  On load the daemon expires what was waiting on a person and brings each
  open session's agent back in the background, through ACP session load
  when the agent supports it (Claude Code does; the fake agent does unless
  told not to) and otherwise through a new session whose first prompt
  carries the transcript. A turn the restart cut short is run again, so
  the agent asks again for whatever it was waiting on. SessionResumed
  records when the daemon restarted and how the agent came back, the
  conversation shows a note at that point, and the agent panel counts
  restarts. AgentFailed now means the agent itself failed, such as its
  worktree being gone.
- Milestone 9, second part, is done: merging a session's changes. A session
  runs in a worktree of its own or, from the command bar's Where chip, in
  the registered checkout. The conversation's changes card commits the
  worktree, merges the branch into its base in the checkout, or pushes it
  and opens a pull request with gh, and the agent panel and the inbox
  show what became of the changes. `skylands session list|stop|remove|
  prune` manage sessions and their worktrees and branches on the host.
- Rules are done. A rule is a decision (allow, ask, deny), a tool call
  kind or any, and an optional pattern: a command pattern for execute, a
  URL pattern for fetch, a path pattern for the rest, where "worktree"
  means everything inside the session's worktree. A deny is final: any
  matching deny rule refuses the call, and neither an allow rule nor
  trust overrides it. Otherwise the most specific matching rule wins, an
  ask beats an allow among equals, a session's own rule beats one copied
  from its repository, and no match asks. A
  repository's rules (RepoRulesSet) are copied into each session it
  starts (SessionStarted carries them) and the session can change its
  copy (SessionRulesSet). The approval card proposes a rule from the
  request and keeps it for this session or for every session on the
  repository; the same rules sheet shows on the terrace and behind the
  conversation's Rules button. `skylands rule list|add|remove` manage a
  repository's rules on the host. A refusal by a rule is recorded as a
  request denied by "rule", so the conversation shows it.
- Interrupting a turn is done. CancelTurn withdraws whatever the turn was
  waiting on a person for, tells the agent to stop, and leaves the session
  open; TurnEnded follows with the stop reason "cancelled" and the next
  prompt steers the same agent. TurnCancelled records who interrupted.
  The conversation has an Interrupt button beside End session, and
  `skylands session cancel` does the same on the host.
- Questions are done. The daemon declares ACP's form elicitation, so an
  agent's question to a person, such as Claude Code's AskUserQuestion,
  arrives as a form: a message and fields, each a choice or free text.
  QuestionAsked waits like a permission request, in the conversation,
  the inbox, the beacon, and `skylands request list`; QuestionAnswered
  carries the values by field or a refusal, and QuestionExpired records
  a timeout, a stop, or a restart. The conversation shows a question card
  with the choices and a box for anything else, and `skylands request
  answer` takes field=value pairs or --decline. The fake agent asks which
  file to edit when its prompt says "ask me", so every path is tested.
- Tool call details are done. A chip in the conversation said only its
  title, and an agent that streams a call's input titles it first with a
  placeholder, such as "Terminal" for a command it has not finished
  writing, which is what the chip kept. Now the daemon keeps a record per
  tool call beside the transcript, under `<session>.tools/`: the exact
  input as last reported, what the call produced as ACP content (text
  such as a command's output, a diff, or a terminal reference), the raw
  output when nothing else carried it, and when it started and ended,
  with long texts cut at a size limit. A call starts, for ToolCallStarted
  and the transcript's line, once its input is known rather than at the
  first report, so the line carries the real title, flattened to one line
  and cut short; ToolCallCompleted carries a title the agent changed
  since. Each line ends with a marker naming the call, which Markdown
  hides and the seed of a replacement session drops. The conversation's
  chip is a button: opening it fetches the record through the citadel
  (`GET /api/sessions/{host}/{id}/tools/{toolCallId}`, the "toolcall"
  fetch kind on the island) and shows the kind, how it went, how long it
  took, the input by field, and the output, refreshed while the call
  still runs. The fake agent reports inputs and results, and announces
  its edit under a placeholder first, so every path is tested.
- Token usage is done. Each request's cost comes back with its answer over
  ACP, and the daemon records it as TokensUsed: a turn's tokens as the
  turn ends, and the title request's with the title. A session's total is
  the sum, shown in the conversation's header and the agent panel; a
  repository's is the sum over its sessions, on the terrace panel under
  the tracked files; and the top of the inbox shows the total across
  every conversation. UsageReported stays what it was, a reading of the
  context window. The fake agent returns fixed counts, so the sums are
  tested end to end.
- Codex is in progress, the second real agent. It runs through
  `@agentclientprotocol/codex-acp` from npm, pinned like Claude Code's
  adapter, and reports its login with the same `_auth/status_update`
  extension, so preflight and the island list it the same way. Codex
  has its own sandbox and approval policy, and the adapter's default
  mode lets Codex edit the worktree without asking, so the daemon sets
  every session it opens or loads to read-only mode: every edit asks, and
  a command runs in Codex's read-only sandbox, where one that writes
  fails unless the model asks to leave the sandbox. The adapter takes no other
  approval policy for its built-in modes, so the daemon tells the model
  when to ask: an adapter's Instructions go to every session it opens or
  loads as ACP's session system prompt in append mode, after the
  developer instructions in the person's Codex config, and only to an
  agent that advertises the system prompt. The fake agent can advertise
  it and say what it got, so that path is tested.

  Only ACP reaches an agent: no environment variable an adapter reads.
  The mode and the model are ACP session config options, found by
  category ("mode", "model") among the ones the agent gives for each
  session it opens or loads, the title's side session included, and set
  with session/set_config_option. An adapter names the mode value to set
  (Codex's is read-only); the mode must read back as set or the session
  does not start, since it is what keeps an agent asking. The model is
  the one chosen in the command bar; an agent may take a value it does
  not list, as Claude Code takes a full model id for one it lists by
  alias, and a refusal names the values it lists. Claude Code's model
  moved from ANTHROPIC_MODEL to its model option the same way. The fake
  agent can offer both options, or take changes and ignore them, so
  every path is tested. A permission request that
  leaves out what the agent already reported about the call, as Codex's
  do for a command it has started, takes the call's kind, title, and
  input from that report. The command bar lists the agents the island
  can run.

  Run live with a ChatGPT Free login: the edit waited for approval, the
  title came from a side session, tokens added up, a daemon restart
  brought the session back by load and back to read-only, a model the
  account lacks stopped the session with the account's models named, and
  Claude Code ran on the model chosen for it.
  Without the instructions, Codex reported `touch` blocked by its sandbox
  and asked only when told to; with them, it asked to escalate on its own,
  and the command waited for approval and then ran. Open: Codex does not
  report a command its sandbox stopped, so it is missing from the
  conversation.
- Each account's models are done. An agent lists the models it offers
  its login only in a session, as the choices of its ACP config option in
  the "model" category, so the agent check opens one without prompting
  it, reads the choices and the one the session started on, and closes
  it when the agent can. AgentAvailability carries them (ModelsRead,
  Models, DefaultModel) in AdapterAvailabilityChanged, preflight shows
  them as a "models" check, and an agent that would not open a session
  is a warning. Opening a session starts what a session needs, such as
  MCP servers, so the daemon reads models at startup and when preflight
  asks it to check, and in between only for an agent whose login changed
  or whose models it has not read. Agents are probed in one directory
  in the user's cache, since Claude Code keeps a project folder for
  each directory it opens a session in. The model chip lists the chosen
  agent's models for the island's login, with the default named and each
  model's description as its tooltip; it is disabled for an agent with
  no model setting, and offers the default and a typed id until the
  island has said. Run live: Claude Max offered 13 models and Codex on
  ChatGPT Free its 3, a session ran on one picked from each list, and the
  fake agent's chip was disabled.
- World themes are done. A theme (`web/src/world/theme.ts`) draws the
  citadel and an island, each with its selection ring, a terrace's plaza
  and boundary, a building, and an airship, each around its own origin
  from a few numbers, and gives the sky's look; everything else in the
  world is shared, so a theme changes only how the world is drawn. The
  world places what a theme draws, tags it with what a click on it
  means, and shows a ring only while what it rings is selected, so a
  theme is given nothing that names a host, a repo, or a file.
  Shared code lays out terraces and their grid of building lots and
  heights (`terraceLots`), and the world stands each theme's buildings on
  them, so an airship lands on the same roof in either theme, and dresses
  them in the theme's state materials, which it tells states apart by.
  The old look moved unchanged into `themes/default`, where the ring is a
  circle of short arcs turning through pastel hues. `themes/scifi` draws
  a space station with a spinning habitat ring and blinking navigation
  lights for the citadel; asteroids sliced flat on top for islands, with
  outposts and blinking masts but nothing painted on the surface, where a
  ring would read as a selection; a cyan targeting reticle for the ring,
  a thin circle with four turning brackets whose markers point in at
  what is selected; hexagonal towers with lit bands
  on a glowing deck for terraces, ringed by landing-strip lights on poles
  in the repo's color that blink slowly in a wave, faster while
  selected; a shuttle, hauler, saucer, or drone per provider for
  airships; and, where the default theme hangs a crystal under the
  citadel and each island, a communications array, its mast reaching
  into the rock or the keel and its head of dishes panning while the
  link is up, its feeds glowing with the link's state. Its sky is always a starfield, whatever the page's light or
  dark colors. Switching rebuilds the citadel, islands, terraces, and
  airships in place, keeping navigation and what agents are doing. The
  choice is a switch in the citadel's panel, kept in the browser
  (`skylands.worldTheme`). Checked in headless Chrome at every level in
  both themes, in light and dark, switching both ways with agents
  waiting on approval, and opening a page with the theme saved.
- goose is in progress, the third real agent. It comes as a binary per
  platform rather than from npm, so the daemon runs `goose acp` from its
  PATH, and an adapter's new Install hint says how to get it when it is
  missing. goose advertises session load, form questions, and session
  close, offers its mode and model as ACP config options by category, and
  reports token usage, so it needs nothing goose-specific beyond its
  adapter. Its default mode, auto, approves every tool call itself, so
  every session is set to approve, where it asks before every tool call
  its own settings don't always allow; smart_approve would let goose,
  partly by asking a model, decide which calls are reads. goose reports
  no kind for its tool calls, so the rules can't let its reads pass and
  every call waits on a person unless a rule or trust allows it. It
  reports no login, so preflight can't say whether a provider is
  configured; a session that won't open shows in the models check. An
  agent that isn't installed (its command not on PATH, or no Node for an
  agent from npm) is skipped, a new check status, rather than failed,
  while the host has another installed; with only the built-in fake
  agent, which an adapter's BuiltIn marks, it still fails. The daemon
  logs a skipped agent as not installed, at info rather than warn. Its
  airship is a taupe goose with a neck and an orange beak, and its ship
  in the sci-fi theme a swept-wing glider. Not yet run live: goose is
  not installed on the machine this was built on.
- A session's mode and model change while it runs (#40, first part). An
  adapter lists the modes that keep its agent asking before it acts, the
  first being where a session starts: Claude Code's default (Manual) and
  plan, Codex's read-only, and goose's approve and chat. StartSession
  takes one of them, which SessionStarted records. SetSessionMode and
  SetSessionModel change an idle session's mode or model through the
  agent's ACP config options, from the conversation's header or `session
  mode|model` on the host, and SessionModeChanged and SessionModelChanged
  record each change with who made it: a person, the agent, or the daemon.
  The daemon follows the agent's own current_mode_update and
  config_option_update, keeping apart the changes it asked for, so the
  header follows the agent changing its own model. A mode the adapter
  doesn't list is set back, and a session whose mode can't be set back is
  stopped. A restart brings a session back on its latest mode and model.
  The title's side session runs in the first listed mode, whatever the
  conversation's: run live, Claude Code in plan mode answered a title
  request with a name for a plan.
  The agent check reads each agent's listed modes, as it names them, with
  its models, which the command bar's Mode chip and the header list. The
  fake agent offers a plan mode, changes its own mode or model when told,
  and announces every change, so the guard and the record are tested end
  to end. Slash commands are the second part.
- Slash commands are done (#40, second part). The daemon keeps the
  commands each session's agent announces in available_commands_update,
  including while a session loads, and the commands fetch returns them less
  the ones the adapter withholds: commands that act outside the session's
  tool calls, such as Claude Code's /remote-control and /config and
  Codex's /logout. A prompt whose first word is a slash and a name is a
  command, and the daemon refuses one the adapter withholds or the agent
  didn't announce, since an adapter may pass on a command it leaves out of
  its list, as Claude Code's does /logout. StartSession refuses a withheld
  command at once; an unannounced first command ends the session once the
  agent is up, waiting a moment for its commands. The reply box lists the
  commands when a reply starts with a slash. The fake agent announces two
  commands a moment after a session opens and answers them, so the wait,
  the list, and both refusals are tested end to end. The withheld lists were
  checked against Claude Code's announced commands and Codex's adapter at
  their pinned versions; check them again when a pin moves.
- Signed prompts are done (#45). StartSession, SendPrompt, CommitChanges,
  MergeChanges, and OpenPullRequest through the citadel must be signed by
  an approver once the world exists, as decisions are: three new decision
  kinds, start, prompt, and change, which the shared vector covers. A start
  names its session, which the browser now picks, and the daemon refuses
  a session id it has seen; a prompt or a change names the session's
  revision, which counts its prompts, commits, merges, and pull requests,
  so a signature can't be used twice, and one made before another prompt
  is stale after it. PromptSent, ChangesCommitted, ChangesMerged, and
  PullRequestOpened record the approver and signature. Stopping,
  interrupting, and mode and model changes stay unsigned.
- Approving from a phone is done, apart from push (#54). `devworld up`
  and `devworld install` take `--url`, the address people open the
  citadel at, which the setup link names and `citadel.CheckPublicURL`
  checks (#55). An "Add an approver" link opens at the page's address,
  the citadel's `--url`, or another one typed in, since a browser keeps
  its approver key per address (#56). Behind `tailscale serve`, which
  keeps the browser's `Host` header, the sign-in cookie is `Secure` when
  a loopback proxy says the browser came over HTTPS, and a proxy that
  rewrites `Host` gets a refusal that says so (#57). Desktop
  notifications, turned on in the citadel's panel, announce a tool call
  waiting for approval or a question while the page is in the
  background, naming the agent, repository, and island but never the
  input (#58). A window narrower than 700 px or shorter than 500 px gets
  the phone view: only what's waiting, newest first, each item at its
  own `/inbox/...` link, and no 3D world, which is now a chunk a phone
  never downloads (#59). Chrome and Edge install the citadel as an app
  that shares the browser profile's approver key; Safari isn't offered
  it, since its installed apps keep separate storage (#60). Web Push to
  phones and closed pages is done (#53), below.
- Gemini CLI is the fourth real agent (#62). It runs from npm as
  `@google/gemini-cli@0.68.0` with `--acp`. It offers its mode through
  ACP's session modes and session/set_mode, which the daemon now sets and
  guards as it does a config option, keeping sessions in default or plan,
  and reads a `[MODE_UPDATE]` marker in its message chunks as a mode
  update, kept out of the conversation. It asks about tool calls without
  raw input, so for any agent that gives none, the request's title and
  content (an edit's diff) stand in as the input the approval card shows
  and the approval signs. /extensions, /restore, and /init are withheld,
  and a withheld command now covers its subcommands. Open: no model
  choice, since the ACP library doesn't read Gemini's models or send
  session/set_model; a session resumes by load only with
  `security.auth.selectedType` in Gemini's settings; Gemini retries 503s
  for minutes without a word over ACP; and preflight doesn't warn about
  Gemini's own allow rules.
- Removing what ended sessions leave behind is done from the citadel
  (#64, part of #43). An ended session's changes card offers Remove
  worktree when the worktree is clean, and the terrace panel lists ended
  sessions' worktrees with Remove them. Each removal is signed
  over the session's revision, recorded in WorktreeRemoved, and keeps a
  dirty worktree or a branch with unmerged commits, as `session remove`
  does without `--force`. The daemon runs `git worktree prune` in each
  registered repository at startup and after every prune. Open in #43:
  pruning on a schedule, forcing a removal from the citadel, and deleting
  orphaned `skylands/*` branches.
- On a terminal, devworld's log shows each line as the time of day, the
  level, the message, and the fields; a service or piped output keeps
  slog's dated key=value lines (#63).
- Packaging is done. A version tag runs goreleaser, which builds archives
  for Linux and macOS on amd64 and arm64 with the web client embedded,
  uploads them to a draft release, smoke-tests the Linux binary with
  `scripts/release-smoke.sh` (#61), and publishes the release only if
  that passes. The release then pushes the cask goreleaser wrote to
  skylands-ai/homebrew-tap, and the tag publishes docs.skylands.ai. A
  service from `install` runs the binary at its path on PATH, such as
  Homebrew's `bin/skylands`, and follows it: on a new binary it stops once
  no agent is mid-turn and exits failing, so launchd or systemd starts the
  new one, and on a binary gone for five minutes it removes its own
  definition (#65). v0.0.5 is the first release with the cask. Not yet
  run: a service following an upgrade under a real launchd or systemd.
- docs.skylands.ai has Getting Started, Concepts, and Operations pages
  (#62, #68, #69), and its Run on Your Own NATS Server page carries what
  docs/NATS.md says. CONTRIBUTING.md, CODE_OF_CONDUCT.md, and SECURITY.md
  are in the repository's root (#70).
- Web Push is done (#53). A citadel whose `--url` is HTTPS sends pushes,
  unless `--no-push`, with the world's VAPID key from the `skylands-push`
  bucket, encrypted for each browser (RFC 8291) by `internal/webpush` on
  the standard library. The Notifications switch, in the citadel's panel
  and at the foot of the phone's list, subscribes the browser at the
  citadel's `--url` only, signed with its approver key. Each island's
  mirror has a `push` consumer that starts at new events, which every
  such citadel pulls from: PermissionRequested and QuestionAsked push,
  once the projection has the event and while the item still waits,
  unless the event is older than the daemon's 30-minute request timeout,
  which the push's TTL ends with. A push to an approver whose page is in
  sight, kept in `skylands-presence` with a one-minute TTL, waits 30
  seconds. Revoking an approver, or a 404 or 410 from the push service,
  deletes its subscriptions. No push closes a notification; the page
  closes those that no longer wait. Not yet run: a push through a real
  push service.
- Terminals are done. A conversation switches between Chat and Terminal,
  as the inbox does between Open and All, and the first switch opens a
  shell on the session's island, in its worktree, as the host user's
  login shell in a pseudo-terminal (`internal/daemon/terminal`, on
  creack/pty), with xterm.js in the browser, in a chunk of its own.
  OpenTerminal and CloseTerminal are session commands, and
  TerminalOpened and TerminalClosed session events, so the inbox knows
  what is open; what goes in and out is plain NATS on the terminal's
  `in`, `size`, and `out` subjects, which no stream captures. The daemon
  keeps the newest 256 KB a terminal printed, which a browser reads by
  the terminal fetch and then follows live by byte offset, reading again
  on a gap, and publishes at most 1 MB a second, so a flood waits on the
  shell rather than the browser. Opening a terminal and every keystroke
  are signed (the `terminal` and `terminal-input` kinds, which the shared
  vector covers): each browser view counts its inputs, and the daemon
  takes them only in increasing order, checking each against the
  registry it holds, so the citadel can relay typing but not forge it.
  A terminal closes when a person closes it, its shell exits, the
  session ends, or its worktree is removed; a restart ends every shell,
  and the next daemon records them closed. A failed session keeps its
  terminals until they close. Checked in headless Chrome against a set-up
  world: opening, typing, Ctrl-C, a 20 KB paste in signed pieces,
  switching back and forth, a second terminal, closing, ending the
  session, a restart, and unsigned and forged input refused.

## After the MVP

### Milestone 6b: airships

Port the prototype's airship, flight, landing, sandbags, name tags, and the
agent panel, driven by session events. Buildings light blue or amber when a
session reads or edits them, and red when two sessions overlap. The agent
level in navigation follows one airship.

### Milestone 7: a working session

What it takes to use the citadel to work on Skylands itself, from the
user's side one feature: a conversation with an agent that lasts.

- **Multi-turn sessions.** The agent process stays alive after a turn. The
  session goes idle, waiting for a person, until SendPrompt continues it or
  StopAgent ends it. Each turn records PromptSent, the tool calls, and
  TurnEnded; SessionEnded marks the end of the whole session.
- **Conversations anywhere.** An inbox lists every session on every island
  in one table, selectable from anywhere, with the usual marks when
  something new happens in one: a dot for unread activity, a color for
  waiting on you, running, finished, or failed. The tab title carries the
  count of sessions waiting on you.
- **Reading the conversation.** An open conversation shows the whole
  exchange by turns as a chat, with a reply box and a stop button, and the
  agent's words and tool calls arrive as they happen.

### Milestone 8: approvals

The daemon records PermissionRequested instead of auto-allowing, rules settle
what they can, the citadel's beacon lights, and `request list`, `approve`,
and `deny` work locally over the socket. A per-session "trust this session"
switch is the stopgap. Signed approvals from a browser follow once approver
keys exist.

Done: a tool call the host's rules do not allow (anything but read, search,
and think) blocks the agent until a person answers. The daemon records
PermissionRequested with the exact input and the agent's options, then
PermissionGranted or PermissionDenied with who answered (local, citadel,
or trust), or PermissionExpired after 30 minutes, on stop, or on restart.
ApproveToolCall, DenyToolCall, and TrustSession arrive over NATS from the
citadel's API or over the socket from `request list|approve|deny`. The
browser shows a card in the conversation with Allow, Always allow, and
Deny, a trust switch in the header, a beam under the airship, the lit
beacon, and the waiting count. The fake agent asks before its edit, so
every test exercises the path. Signed approvals remain for the users
milestone; the rule language remains a kind allow-list for now.

### Milestone 9: recovery from a daemon restart, and merging a session's changes

A daemon bounces for many reasons, and the one running the session is often
the one being rebuilt. Before this milestone a restart marked every open
session failed, expired whatever was waiting on a person, and the
conversation could be read but not continued. After it:

- **The history survives.** The events are in the host's stream and the
  transcript is on disk, so the conversation, its title, its turns, its
  tool calls, and its approvals are all still there after a bounce, in the
  inbox and in the world, with a note of when the daemon restarted.
- **The conversation continues.** The daemon resumes the agent through ACP
  session load when the agent supports it, which Claude Code does, so the
  next prompt carries on with the agent's own memory of the conversation.
  When the agent cannot load a session, the daemon opens a new one and
  seeds it with the transcript, and says so in the conversation. Either
  way the session goes back to idle rather than failed, the airship
  returns to its terrace, and pending requests are asked again.
- **A restart is visible, not fatal.** AgentFailed is reserved for an agent
  that actually failed; a bounce records that the daemon restarted and what
  it resumed.
- **Merging a session's changes.** Sessions can run in the registered
  checkout or in a worktree, the conversation offers to commit the
  worktree and merge the branch or open a pull request, and `session`
  commands stop, remove, and prune worktrees and branches.

Done. For the restart: the daemon keeps the agent's own session id from
AgentSpawned, asks the agent to load it again after a restart, and seeds
a new session with the transcript when it cannot. Nothing is re-asked by
the daemon itself: the interrupted turn is continued with a prompt that
says what happened, and the agent asks again for what it still needs. An
idle session's agent comes back idle. The transcript gets a note at the
point of the restart, which the conversation shows between the messages.

For the changes: a session's branch is cut from whatever the checkout is
on, which SessionStarted records as the base. The command bar's Where chip
starts a session in the checkout instead, one at a time per repository.
CommitChanges, MergeChanges, and OpenPullRequest arrive over NATS from the
conversation's changes card, which reads where the changes stand from the
host through the changes fetch. A merge goes into the base in the checkout
and is aborted on conflict; a pull request is a push to origin and
`gh pr create`. ChangesCommitted, ChangesMerged, PullRequestOpened, and
WorktreeRemoved record each step, and the transcript gets a note.
`skylands session list|stop|remove|prune` work over the socket; remove and
prune keep a dirty worktree or an unmerged branch unless forced.

## What the MVP proves

1. `skylands devworld up` on a laptop, then opening the web app, shows the
   citadel with one island for that machine.
2. Starting `skylands daemon up` elsewhere makes a new island rise within a
   few seconds.
3. Stopping that daemon turns its island grey on a slack tether within about
   ten seconds. Restarting it brings the island back.
4. The world panel lists islands with last-seen times and the NATS server the
   citadel is connected to.

## The prototype

The mock UI was one HTML file of about 2,400 lines, since ported into `web/`
and removed: vanilla JavaScript in a single closure, three.js r128 loaded as a
global from a CDN, Google Fonts, and hard-coded hosts, agents, and queue data.
It already modeled reachability the way the design describes. Each island had
a drift value that eased toward one when its host was unreachable, and that
value drove the crystal, the tether material, and the sparks.

Two things it lacked for the MVP. Islands were built once from a fixed list, so
there was no path for a host to appear or leave. And island positions were
hand-placed rather than derived from the host.
## Milestones

Each milestone is demoable on its own and builds on the last.

### 1. Embedded NATS and a citadel that serves a page

- `natsembed` starts nats-server in-process with JetStream on and a leaf node
  listener. One account, no auth.
- `citadel/web` serves the embedded `web/dist` and a WebSocket that sends the
  connected NATS server's name, version, URL, cluster, and JetStream domain
  on connect and whenever the connection changes.
- `web/` is the Vite plus TypeScript client, with a `make web` target
  feeding `web/dist` into the Go embed. The prototype's sky, citadel, camera,
  labels, header, and world panel are ported into modules, pinned to three
  0.128.0, with the fonts bundled. The world panel shows the NATS facts.
- `skylands devworld up` starts the embedded server and the citadel. The
  daemon joins in milestone 2.

Demo: the browser shows the citadel in the sky, and the panel shows which
NATS server it is on.

### 2. Daemon presence over NATS micro

- The daemon connects through its embedded leaf node and registers the
  `skylands-daemon` micro service with host name, OS and architecture,
  version, and start time as metadata.
- A `--host` flag defaults to the machine's hostname until credentials carry
  identity.
- In `citadel/projections`, a reachability poller pings the service every two
  seconds, collects replies for one second, and marks a host unreachable
  after three misses while keeping its last state and last-seen time.

Demo: the citadel log prints islands going reachable and unreachable as a
second daemon starts and stops.

### 3. Stream the projection to the browser

- The WebSocket sends a full snapshot on connect, then deltas: host added,
  reachable, unreachable.
- Each host record carries what the prototype's panel code reads: id, name, a
  one-line description from the metadata, reachable, and last-seen. Geometry
  is not sent. The client derives it from the id.
- The browser renders a flat list first and reconnects with backoff.

Demo: the list updates live as daemons start and stop.

### 4. Port the prototype and make it live

Three parts.

**Move the rest of the file into the build without changing how it looks.**
Milestone 1 already ported the sky, citadel, camera, labels, and world panel
into `world/` and `panels/`, pinned to three 0.128.0 with the fonts bundled.
This step moves the island, terrace, building, airship, tether, and island
panel code the same way, mechanically, and adds a `data/` module in place of
the hard-coded constants. Upgrading three is a separate later step, because
r128 to current changes color management and light intensity units and will
need visual re-tuning.

**Replace the mock data with a store fed by the WebSocket.** The `HOSTS`,
`AGENTS`, and `QUEUE` constants become a store that starts empty.
`reachable`, `statusOf`, `connectedCount`, and the last-seen times read from
it. The agent, terrace, queue, and work order code stays in place and simply
has nothing to render, so it comes back as later features arrive. The
simulation drivers go: the laptop offline cycle, the NUC toggle, and the
dispatch simulation.

**Add islands dynamically with stable placement.** New `addIsland` and
`retireIsland` paths build or dispose an island group and tether when the
store changes, with the existing drift value providing the rise on arrival
and the drift and grey crystal on loss. Angle, distance, height, radius, and
seed derive from a hash of the host id, with a deterministic nudge when two
hosts land in the same slot, so every host keeps its place across reloads and
across citadels.

The world panel shows islands connected and the NATS server. The island panel
shows link state, last seen, and the daemon facts from the micro metadata.
Levels below island render nothing yet.

Demo: the MVP.

### 5. Hardening and the two-machine run

- An e2e test in `test/e2e` runs natsembed, a citadel, and fake daemons in
  one process and asserts the reachable and unreachable transitions. Unit
  tests cover the poller with a fake pinger and clock.
- Reconnect handling for the daemon's leaf remote, the citadel's NATS client,
  and the browser's WebSocket.
- The web server binds `127.0.0.1` by default until auth exists, with the
  `--http` flag to override.
- A README quickstart for the laptop plus second machine case.

## Decisions

- **Leaf node from day one.** The daemon could connect as a plain client for
  now, but the leaf node is its defining connectivity and reuses the same
  embed code as devworld. It is the one piece that can be swapped later
  without touching anything else.
- **Unreachable means grey, not gone.** The design keeps islands in place so
  people build spatial memory. Removing them is a one-line variant.
- **No auth means local binding.** The web server and the embedded NATS
  listeners default to `127.0.0.1` until credentials exist.
- **Keep three.js at r128 for the MVP.** The scene is tuned for it. Upgrading
  belongs in its own step after the MVP is visible.
- **Strip the simulation, keep the features.** Deleting the agent and work
  order code would save little and cost a rewrite later. Feeding it an empty
  store costs nothing.
- **Placement is a client concern.** The server sends identity and state. The
  client owns geometry, so the design's rule that an island keeps its place
  needs no server state.
- **Libraries.** nats-server v2 and nats.go on the Go side, with nats.go's
  micro package for the service. `coder/websocket` for the WebSocket. Vite
  and TypeScript for the client, with three.js added at milestone 4.

## Deferred past the MVP

NATS accounts and auth callout: a world on its own nats-server takes the
credentials its operator issues, as docs/NATS.md describes. World-wide
rules mirrored to every host; rules live on each host's repositories for
now. Work orders and dynamic dispatch. Compaction puffs and subagent leashes, which wait for their
events. Claude Code hooks, so terminal sessions appear on the island. The
three.js upgrade.

World setup, sign-in, signed approvals, the phone inbox, Web Push, and
packaging were deferred here and are done; the status above has them.
