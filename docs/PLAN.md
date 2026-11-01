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
  means everything inside the session's worktree. The most specific
  matching rule wins, a deny beats an allow among equals, a session's own
  rule beats one copied from its repository, and no match asks. A
  repository's rules (RepoRulesSet) are copied into each session it
  starts (SessionStarted carries them) and the session can change its
  copy (SessionRulesSet). The approval card proposes a rule from the
  request and keeps it for this session or for every session on the
  repository; the same rules sheet shows on the terrace and behind the
  conversation's Rules button. `skylands rule list|add|remove` manage a
  repository's rules on the host. A refusal by a rule is recorded as a
  request denied by "rule", so the conversation shows it.

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
switch is the stopgap. Signed approvals from a browser follow once passkeys
exist.

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

The mock UI is `docs/Skylands world.html`: one file of about 2,400 lines,
vanilla JavaScript in a single closure, three.js r128 loaded as a global from
a CDN, Google Fonts, and hard-coded hosts, agents, and queue data.
Reachability is already modeled the way the design describes. Each island has
a drift value that eases toward one when its host is unreachable, and that
value drives the crystal, the tether material, and the sparks.

Two things it lacks for the MVP. Islands are built once from a fixed list, so
there is no path for a host to appear or leave. And island positions are
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
`reachable`, `statusOf`, `connectedCount`, and the last-heard times read from
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
shows link state, last heard, and the daemon facts from the micro metadata.
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

World init, accounts, credentials, and auth callout. Passkeys, user sessions,
and signed approvals. World-wide rules mirrored to every host; rules live
on each host's repositories for now. Push notifications and the phone inbox. Work
orders and dynamic dispatch. Compaction puffs and subagent leashes, which
wait for their events. Claude Code hooks, so terminal sessions appear on the
island. The three.js upgrade. Packaging and goreleaser.
