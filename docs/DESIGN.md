# Skylands design document

## Summary

Skylands is a self-hosted system for running, watching, and approving coding agents from several providers across several machines, shown as a 3D world of floating islands. It ships as one Go binary with two roles. Every machine that hosts agents runs a daemon, `skylands`, which launches agents over the Agent Client Protocol (ACP), owns their event-sourced sessions, and buffers events through an embedded NATS leaf node. Citadels serve the web application, build the projections the UI reads, and relay approvals that users sign on their own devices, and any number of them can run because they're interchangeable. 

Both roles run on NATS with JetStream: `skylands devworld up` embeds a NATS server for a one-machine world, and larger worlds connect to an external NATS server. A world's scope is a NATS account, so anything holding credentials scoped to that account's subjects is a participant, and the subject namespace is the system's public API.

## Scope and users

Skylands is for developers who run enough parallel agents that terminal UIs have become confining, often across more than one model provider and more than one machine. The product does four jobs:

- Show every session, agent, and subagent across every host in one spatial view, with session history that can be replayed.
- Put the human decision loop first: an approval or a stuck agent is never more than one action away, from a desktop or a phone.
- Show what each agent is touching at the level of individual files, including when two sessions are working on the same file.
- Let developers start, pause, stop, and fork sessions on any connected host without opening a terminal there.

Skylands runs where its users choose. A solo developer runs the citadel and a daemon on one laptop. A team runs one citadel on an internal network or a public endpoint, with a daemon on every machine that hosts agents.

There is also room to build a paid, centrally hosted offering at `skylands.ai` where teams would provision a citadel and then connect any of their working machines to a central NATS endpoint (e.g. Synadia NGS).

## System components

Skylands ships as one Go binary with two roles, the citadel and the daemon, plus agent adapters and clients around them. Both roles run on NATS, which is infrastructure rather than part of Skylands. `skylands devworld up` runs a citadel, a daemon, and an embedded NATS server in one process for a one-machine world, and only the citadel serves a web application.

| Component | What it does | Where it runs | What it owns |
| --- | --- | --- | --- |
| NATS (nats-server) | Accepts every daemon's leaf connection and every citadel's client connection, stores the world's streams, and carries every message between them | Inside `skylands devworld up` for a one-machine world, otherwise nats-server on one machine or a cluster of three or more | The world's streams, including copies of every host's events and the citadel aggregates' events |
| Citadel (`skylands citadel up`) | Serves the UI, authenticates people, builds projections, relays signed approvals, and sends push notifications | A laptop, an internal server, or a public endpoint | Users, rules, budget, and work order aggregates, plus its own projections of every session |
| Daemon (`skylands daemon up`) | Runs agents, owns their sessions, enforces rules, verifies approval signatures, and buffers events while offline | Every machine that hosts agents | Session aggregates and session event streams for its host, plus agent worktrees |
| Agent adapters | Bridge each agent to the Agent Client Protocol (ACP), the JSON-RPC protocol the daemon speaks over stdio | Child processes of the daemon | Nothing; they're stateless bridges |
| Clients | The 3D world, a mobile approval inbox, and a local CLI | Browsers, phones, and the host's shell | Nothing; they read projections and send commands |

NATS here means `nats-server` with JetStream. `skylands devworld up` runs one in-process, and any other world runs nats-server itself, with the settings in [NATS.md](NATS.md), or uses a NATS cluster the team already runs.

Apart from the NATS server that `skylands devworld up` embeds, only the daemon embeds a NATS server: a leaf node with a local JetStream domain, so it keeps working while it can't reach the world's NATS server. The citadel embeds no server and connects as an ordinary NATS client. Agents (harnesses) communicate with the daemon via ACP.

![Daemons and citadels dial NATS, and browsers reach only a citadel](./topology.svg)

Every connection starts at the edge: daemons and citadels dial out to NATS, and browsers and phones reach only a citadel.

## The world metaphor

Every visible element in the 3D world stands for one thing in the system, and its position, motion, and color carry state or metadata. The hierarchy includes citadel, island, terrace, building, with airships as the agents working across buildings.

![Each level of the world is one level of the system](./world-hierarchy.svg)

### Places

| In the world | In the system |
| --- | --- |
| The sky | A world, which is one NATS account subject namespace (usually bounded by an account) |
| Citadel, at the center of the world | The citadel role; the castle looks the same whether one citadel is running or ten |
| Citadel crystal, under the castle | The world's NATS server or cluster; every tether attaches here |
| Citadel beacon, on the top tower | Lit and pulsing while anything in the world is waiting on a person |
| Island | One host running one daemon |
| Island crystal, under the island | The daemon as the citadel last saw it: lit while the host is reachable, grey while the citadel can't reach it |
| Tether | The daemon's leaf node connection to NATS |
| Sparks moving up a tether | Events flowing from the island to the citadel |
| Slack tether, island drifted outward, grey crystal | The citadel can't reach the host. A dropped network and a stopped daemon look the same from the citadel, so the island holds the last state it saw until the host returns |
| Burst of fast sparks | The host is reachable again, and the events its daemon held are syncing in order |
| Terrace | One repository on that host |
| Stanchions and colored rope | The repository boundary; the rope color identifies the repo everywhere it appears |
| Building | One tracked file; its height scales with file size |
| Amber building | An agent is editing the file |
| Blue building | An agent is reading the file |
| Red building with a "2 sessions" tag | Two different sessions are on the file at once, and their worktrees will collide at merge |
| Flag on a terrace, in the repo's rope color | A work order: grey while queued, solid while running, yellow when it needs a person, and a check when it's done |
| Bright spark running down a tether from the citadel | A work order being dispatched to that island |

### Agents

| In the world | In the system |
| --- | --- |
| Airship | One agent |
| Hull shape and color | The provider: a long orange envelope for Claude, a boxy green hull for Codex, a round blue hull for Gemini, and a purple four-sided hull for local models |
| Airship landed on a roof | The agent is working on that file |
| Takeoff, flight, and landing | The agent moving its attention from one file to the next |
| Flight altitude | Context fill; a fuller airship flies lower |
| Sandbags under the gondola | One bag per quarter of the context window used |
| Sandbags dropped with a puff, then a climb | Context compaction |
| Yellow beam over a hovering airship | The agent is waiting for a person to approve a tool call |
| Coral beam over a hovering airship | The agent stopped on an error and needs a decision |
| Leash line between two airships | A subagent tied to its lead |
| Name tag over an airship | The agent's name and what it's doing right now |

### Interface markers

| In the world | In the system |
| --- | --- |
| Number badge on an island or citadel label | Items waiting on a person there |
| White ring around an island or the citadel | The current selection |
| File-name tag on a building | The file is busy right now, or it's the selected file |

Clouds, trees, hills, ponds, and the waterfall are scenery and carry no data. Each island keeps a fixed place in the sky so people build spatial memory of where each host lives.

## Navigation and interaction

The world has five zoom levels, and every click goes one level deeper. The 3D scene is for knowing where to look; anything you read, such as transcripts, diffs, plans, and activity, appears in the flat side panel.

| Level | What the camera shows | What the panel shows |
| --- | --- | --- |
| World | Every island around the citadel | Islands connected, everything waiting on a person across the world, spend, and the NATS server the citadel is connected to |
| Island | The island from outside the ring, with the citadel behind it | Link state, daemon facts, terraces, airships by provider, and waiting items |
| Terrace | The repo up close, with name tags on airships, tags on busy files, and subagent leashes | Each agent's live action, waiting items for the repo, and the repo's activity feed |
| Agent | Follows one airship as it flies | Model, role, session, current action, any pending request, context, plan, and recent activity |
| File | One building up close | The path, who is on it, any overlap warning, and recent activity on that file |

### Controls

- Click goes one level deeper; double-click jumps straight to the deepest thing under the pointer.
- Esc goes up one level, and the breadcrumb in the header jumps to any level above.
- Drag orbits the camera, and scroll or pinch zooms.
- Right-drag, middle-drag, or Shift-drag pans, and so do two fingers on a touch screen. Orbit then turns around the new center. Anything that flies the camera, such as a click, Esc, or a crumb, drops the pan.
- The World crumb returns to the opening view.
- Next flare, or Space, flies straight to the next agent waiting on a person, at agent level.
- Approve, Deny, and Retry appear wherever a waiting item is listed, and act immediately.
- Pause and Resume hold an agent in place, and subagents link back to their lead.

### Work orders

Work starts as a work order, not as a chat with an agent someone picked. One command bar at the bottom of the screen takes the order, and its target comes from where you are.

Dispatch has two levels. The citadel picks the host, and the host's daemon picks the repo, because the daemon can search its own code and knows its own load.

| Where you are | What the bar does |
| --- | --- |
| World | Posts a work order to the citadel, which picks a host, and picks the repo too when the text names one or a keyword settles it |
| Island | Posts a work order to that host, and its daemon picks the repo |
| Terrace | Posts a work order for that repo |
| File | Posts a work order for that repo, with the file attached as context |
| Agent | Sends a message to that agent |

- The bar always shows the scope it's using and why. Where you are sets the scope: on an island, terrace, or file, that place is the scope even when the text names a different repo, and the Auto option shows what the text alone would pick so you can switch to it. At the world level, Auto takes the scope from the text when it names a host or repo or a keyword settles it, and otherwise says the citadel will pick the island.
- Picking a scope in the bar moves the camera to it, the same way clicking in the world sets the scope.
- The model, attempts, and budget chips default to Auto, one attempt, and the world's default cap, and each shows what Auto resolved to, or that the daemon will choose.
- Each attempt runs on a different provider. A model the target host can't run, such as a local model on a host without a GPU, is swapped for one it can, and the preview names the swap.
- Enter shows a one-line dispatch preview, and a second Enter launches the order.
- The citadel chooses the host by routing rules. For an order scoped to the world, it chooses when the order is submitted, considers only hosts it can reach, and records its reason on the order. For an order scoped to a host, the daemon chooses the repo and model under the same rules, which it holds as a projection, and the order shows the daemon's reason too. The lead agent breaks the work into subagents itself.
- The daemon finds the repo by searching its own repos, so no code leaves the host to make the choice. When nothing fits, it asks a person which repo to use instead of guessing, and that question leads the waiting list because the order can't start until someone answers it.
- When the citadel can't reach the target host, the preview says the order will queue, and the order waits until the host is reachable again.
- When Citadel can't accurately determine a target for the work order, it interactively prompts the user to select from among valid destinations.

Labels and name tags are real buttons, so every level is reachable by keyboard. Reduced-motion settings turn off flight animation, bobbing, and camera glides. The light theme renders a day sky and the dark theme renders dusk.

The phone client is a flat approval inbox rather than the 3D world. It lists waiting items with Approve, Deny, and Retry, and opens a waiting agent's request and recent activity.

## Walkthrough: putting work into the world

A person puts work into the world by starting a session on a terrace, steers it from the panel, and answers its requests from any device. This example follows one afternoon on the storefront repo, with the command each step sends and the events it produces.

1. **Bring a host into the world.** The owner copies the install command from the citadel's "Add a host" section and runs it on the workstation. The command names the world's owner key with `--world`, which the daemon pins, so it joins this world and no other. The workstation island rises into the sky, tethered to the citadel.
2. **Register the repo.** From the island's panel or the local CLI (`skylands repo register ...`), the person registers `~/code/storefront`. The daemon handles `RegisterRepo` and records `RepoRegistered` and `TerrainSnapshotted`, and a roped terrace appears with a building for every tracked file.
3. **Post a work order.** With the storefront terrace open, the person types the task into the command bar: _add multi-currency support to pricing_. The bar shows the scope it's assuming, **storefront on workstation**, and chips for Auto model, one attempt, and a $5 cap. Enter shows the dispatch preview, Claude Opus on storefront at workstation, and a second Enter launches it. The citadel handles `SubmitWorkOrder`: it records `WorkOrderSubmitted`, sends `StartSession` to the workstation's daemon, and records `WorkOrderDispatched`. The daemon creates a worktree, launches the agent over ACP, and records `SessionStarted`, `PromptSent`, and `AgentSpawned`. A spark runs down the tether, a flag in storefront's rope color plants on the terrace, and an orange airship lifts off from the flag and lands on the first file it reads.
4. **Watch the work spread.** Opus reads the pricing crate (a user repo), so those buildings light blue, then lands on `engine.rs` and lights it amber while it edits. It spawns two subagents to survey the catalog and payments crates, and they fly out on leash lines. Its plan fills in on the agent panel, and sandbags gather under it as its context grows.
5. **Race two models.** From the same terrace, the person posts a second work order for stock reservations in the cart and sets the attempts chip to two. The preview shows Codex and Claude Sonnet, one session each. The dispatcher starts both sessions in separate worktrees, and both airships work the cart crate side by side under one flag.
6. **Handle an overlap.** Codex lands on `search.rs` while one of Opus's subagents is already there. The building turns red with a "2 sessions" tag and the terrace feed logs the overlap, so the person pauses the subagent until Codex moves on. The daemon handles PauseAgent and records AgentPaused. The user can ignore this warning if they're ok with the overlap happening in different git worktrees (hoping for resolution/marge later).
7. **Answer a request from a phone.** Opus asks to run `cargo test -p storefront-pricing`, and no rule covers it. The daemon records `PermissionRequested`, Opus hovers over the tests building with a yellow beam, the citadel's beacon lights, and the person's phone gets a notification naming Opus and storefront. They open the inbox, read the exact command, and approve, which the phone signs with its approver key. The citadel relays `ApproveToolCall`, the daemon verifies the signature and records `PermissionGranted`, and the airship lands and runs the tests.
8. **Turn the decision into a rule.** From the same request, the person chooses to always allow `cargo test` in storefront. The citadel handles `AddRule` and records `RuleAdded`, every daemon projects it, and the next test run proceeds without asking.
9. **Steer an agent.** The person clicks Opus, so the command bar now targets Opus, and sends a follow-up: _keep single-currency pricing behind a feature flag_. The daemon handles `SendPrompt`, records `PromptSent`, and Opus's plan gains a step.
10. **Let it compact.** Opus passes 92% of its context. On its next takeoff it drops its sandbags with a puff and climbs, and the session records `ContextCompacted`.
11. **Review the result.** When Opus finishes its plan, the daemon records `SessionEnded`. The person opens the session's diff, which the daemon sends from the host on request, and takes the worktree's branch from there.
12. **Look back later.** Scrubbing the session's timeline replays the island from its events: the airships retrace their flights, buildings light and dim, and the beam rises and resolves.

## Worlds, membership, and subjects

A world is one NATS subject space (typically bounded via account), and membership in a world is holding credentials for that account. One NATS deployment can carry several worlds, and accounts keep them fully isolated. Revoking a participant's credentials removes it from the world.

### Participants

Every participant is a NATS user with permissions scoped to the subjects its role needs. Being a member grants no trust beyond those subjects.

| Participant | Can publish | Can receive |
| --- | --- | --- |
| Daemon | Events under its own island, and replies to commands for its own sessions | Commands for its own sessions, fetch requests for its own island, and rule events |
| Agent | Only within its own session's subtree | Only within its own session's subtree |
| Citadel web app | Commands to any session, and rule events | Every event in the world |
| Integration, such as a chat bot or CI runner | Only the commands its role grants | Only the events its role grants |

Browsers and phones never connect to NATS. They talk to the citadel web app, which lets them in once they sign in with their approver key.

### Subject namespace

The subject namespace is the public API, versioned by its first token. Events are captured by JetStream streams, and commands and fetches are request-reply that no stream captures, so the namespace itself keeps commands ephemeral.

| Subject | Carries | Persistence |
| --- | --- | --- |
| `v1.island.<host>.session.<session>.evt.<type>` | Session events | Captured on the host, then mirrored into the world's JetStream domain |
| `v1.island.<host>.session.<session>.cmd.<type>` | Commands to a session aggregate | Request-reply only |
| `v1.island.<host>.evt.<type>` | Host events, such as terrain snapshots and adapter availability | Captured on the host, then mirrored into the world's JetStream domain |
| `v1.island.<host>.fetch.<kind>` | On-demand reads, such as a diff or a file's contents | Request-reply only |
| `v1.island.<host>.session.<session>.live` | A session's transcript as it is written, for whoever reads the conversation | Plain NATS, captured by no stream |
| `v1.island.<host>.session.<session>.term.<terminal>.{in,size,out}` | A terminal's signed input, its size, and what its shell writes | Plain NATS, captured by no stream |
| `v1.world.<aggregate>.<id>.evt.<type>` | Events from citadel-owned aggregates, one subject per aggregate instance | Captured in the world's JetStream domain; rule and user events are mirrored into every daemon's domain |
| `v1.world.cmd.<type>` | Commands to citadel-owned aggregates, endpoints of the skylands-citadel micro service in one queue group | Request-reply only |
| `v1.world.query.<kind>` | Queries answered from the receiving citadel's projections | Request-reply only |
| `$SRV.PING.<service>` | Discovery of running citadels and reachable daemons through NATS micro | Request-reply only |

Commands to citadel-owned aggregates are endpoints of a NATS micro service, `skylands-citadel`, that every citadel registers. The endpoints share a queue group, so NATS delivers each command to one citadel and no citadel holds a role. The web API calls the same endpoints, and the CLI and integrations with the right credentials can call them directly. Daemons register `skylands-daemon` the same way, which is how citadels find out which hosts they can reach.

### Across worlds

Account exports and imports let one world share selected subjects with another, such as a read-only view of one island's events. A daemon can belong to more than one world by opening one leaf connection per account, and each world sees only what crosses its own connection.

## Where data lives

Every kind of data has one home and, where it needs one, a named copy.

| Data | Lives in | Written by | Copies |
| --- | --- | --- | --- |
| Session and host events | The host's stream, in its daemon's leaf JetStream domain | That host's daemon, and nothing else | One mirror per host in the world's JetStream domain, with the same sequence numbers |
| Rule, user, budget, and work order events | One stream per aggregate type in the world's JetStream domain, with one subject per aggregate instance | Any citadel, settled by a per-subject compare-and-set | Rule and user streams are mirrored into every daemon's domain |
| Citadel projections | Each citadel's memory | That citadel, from its own ordered consumers | None |
| Daemon projections: rules and approver keys | The daemon's memory | The daemon, from its local mirrors | None |
| Diffs, file contents, transcripts, and tool call records | The host's disk, in worktrees and agent session files | Agents and the daemon | None; a citadel holds fetched content only while someone views it |
| Requests' exact inputs, tool calls' whole outputs, and the files and images prompts gave the agent | The host's disk, under `blobs/` in the data directory, each file named by its SHA-256, which the session's events name | The daemon, as it records the request, the call's end, or the prompt | None, and nothing is removed |
| Images uploaded for a prompt not yet sent | The host's disk, under `uploads/` in the data directory, named by their SHA-256 | The daemon, from the pieces a citadel relays | None; the citadel keeps nothing, and the daemon removes an image no prompt named within an hour |
| Commands and queries | Nowhere; they're NATS request-reply | People, through a citadel, the CLI, or an integration | None |
| World record: its name and owner key, signed by the owner key | The `skylands-world` bucket in the world's JetStream domain | A citadel, once, when the owner sets the world up | Each daemon pins the owner key it names |
| Owner key | Only in the recovery file the owner downloads when setting the world up | The owner's browser, during setup | None |
| Approver keys | Private keys stay in each approver browser's IndexedDB, for the citadel's origin, and public keys travel in approver entries | People's browsers | The `skylands-approvers` stream in the world's JetStream domain |
| The world's push key (VAPID) and approvers' push subscriptions | The `skylands-push` bucket in the world's JetStream domain, each subscription under the approver that signed it | A citadel: the key once, create-only, and a subscription when its browser signs one | None |
| Pages in sight, for holding pushes | The `skylands-presence` bucket in the world's JetStream domain, each entry gone a minute after it was last refreshed | The citadel holding the page's WebSocket | None |
| What a terminal printed | The daemon's memory, the newest 256 KB, while the terminal is open | The terminal's shell | None; a browser showing it holds what it drew, and a citadel keeps nothing of what it relays |


Each role creates the streams it needs, idempotently. A daemon creates its host stream and its mirrors of the rule and user streams at startup. A citadel creates a host's mirror in the world's domain the first time it sees that host, and creating a mirror that already exists changes nothing.

Each host gets its own mirror rather than one world stream that sources every host. A mirror copies one stream and keeps its sequence numbers, while sourcing renumbers messages, and adding a host to a sourcing stream would mean editing one shared stream configuration. If a host's stream is ever deleted and recreated, it gets a new name, so old and new events never share sequence numbers.

## Event sourcing model

Sessions are event-sourced, and each daemon is the single writer of the session streams on its host. Commands are ephemeral: an aggregate handles each one, validates it, and produces events. Only events are stored, and replay reads only events.

### Aggregates

| Aggregate | Lives in | Handles commands | Produces events |
| --- | --- | --- | --- |
| Session | Daemon | StartSession, SendPrompt, RecordToolCall, RecordToolResult, RecordPlan, RecordUsage, RecordTokens, RecordCompaction, RecordPermissionRequest, ApproveToolCall, DenyToolCall, RetryAgent, PauseAgent, ResumeAgent, StopAgent, ForkSession, ExpirePendingRequests, OpenTerminal, CloseTerminal | SessionStarted, PromptSent, AgentSpawned, SessionTitled, ToolCallStarted, ToolCallCompleted, TurnEnded, PlanUpdated, UsageReported, TokensUsed, ContextCompacted, PermissionRequested, PermissionGranted, PermissionDenied, PermissionExpired, AgentFailed, AgentPaused, AgentResumed, SessionForked, SessionEnded, TerminalOpened, TerminalClosed |
| Host | Daemon | RegisterRepo, SnapshotTerrain, RecordFileChanges, RecordAdapterAvailability | RepoRegistered, TerrainSnapshotted, FilesChanged, AdapterAvailabilityChanged |
| Rules | Citadel | AddRule, RemoveRule | RuleAdded, RuleRemoved |
| Users | Citadel | EnrollUser, EnrollApproverKey, RevokeUser | UserEnrolled, ApproverKeyEnrolled, UserRevoked |
| Budget | Citadel | SetBudget | BudgetSet |
| WorkOrder | Citadel | SubmitWorkOrder, CancelWorkOrder | WorkOrderSubmitted, WorkOrderQueued, WorkOrderDispatched, WorkOrderCompleted, WorkOrderCancelled |
| Placement (processor) | Daemon | PlaceWorkOrder, ChooseRepo | WorkOrderPlaced, RepoChoiceRequested |

What an agent reports through its adapter enters the session aggregate as a Record command. The aggregate validates it against the session's state and produces the matching event, so every event in a session stream came from that stream's single writer.

While handling SubmitWorkOrder, the citadel records WorkOrderSubmitted. For an order scoped to a repo, it sends StartSession to that repo's daemon. For an order scoped to a host, it sends PlaceWorkOrder, and the daemon's placement processor chooses the repo and model, records WorkOrderPlaced with its reason, and sends StartSession to its own session aggregate. The citadel records WorkOrderDispatched when the daemon replies. Every session event carries its work order's id, so the orders board and each terrace's flags are projections across the sessions an order started.

### Projections

- Each citadel builds its own projections in memory: the world view, the timeline, the approval inbox, and spend against budgets. At startup it reads each stream from the beginning with an ordered consumer, which tracks its own position, so the citadel stores no positions of its own. Replay time is bounded by the size caps on host streams. A projection is a pure function of the streams, so every citadel converges on the same state without talking to the others.
- Reading a host's stream from the beginning takes the projections through every state its sessions were ever in, so a citadel sends browsers nothing about a host until it has read the events the host's mirror held when it began. As it starts, it also waits for the hosts it followed before, which it knows by their mirrors, until each has answered and been read, or as long as it takes to call a host unreachable. Meanwhile its snapshots say what it is still reading and leave those hosts out, and once nothing is being read it sends every browser a new snapshot. A browser that already showed the world keeps showing it under a veil until then, rather than each point in the history.
- A citadel that handled a command waits until its consumer delivers the event at the sequence in the append's ack before answering, so people see their own change. A browser stays on one citadel through its WebSocket, so it always sees its own writes, and an integration gets the outcome in the command's reply.
- Each daemon mirrors the world's rule and user streams into its own JetStream domain, so it can rebuild its projections across a restart even while it can't reach NATS. It projects rule events into its local rule set and enforces it there.
- Each daemon watches the world's approver entries and trusts only those that trace back to the world's owner key.

### Ordering

Ordering comes from JetStream, not from numbers Skylands assigns. The daemon is the only writer to its host's stream, so the stream sequence JetStream assigns on append orders every event on the host, and a session's events in order are that stream filtered to the session's subject. A mirror keeps the origin's sequence numbers, so an event has the same sequence on the host and in the world's domain, and the host plus that sequence identifies it everywhere. AgentSpawned references the event that spawned the agent by its stream sequence, which the daemon gets back in the publish ack. Nothing orders events by comparing clocks across hosts; timestamps are recorded for display only.

### Idempotency

- Every command carries an id from whoever sends it, and every event records the id of the command that produced it. An aggregate that receives a command id already in its history replies with the original outcome and produces nothing new. This doesn't rely on JetStream's duplicate window, which only covers a limited time.
- Each citadel-owned aggregate instance has its own subject. A citadel appends its events with `Nats-Expected-Last-Subject-Sequence` set to the last sequence it read for that subject. When two citadels handle commands for the same aggregate at once, one append succeeds, and the other citadel reloads the aggregate and handles its command again against the new state. Commands for different aggregates never contend.
- Side effects of events, such as push notifications, run from a durable consumer whose acks track progress, so no citadel keeps a record of what it has handled. A citadel that fails between sending a notification and acking it can send it twice, which is harmless, and using the event's identity as the notification's collapse key makes the phone replace the duplicate rather than show both.

### Restart

An agent process dies with its daemon. On restart, the daemon sends ExpirePendingRequests to each open session, which records PermissionExpired for anything that was waiting. It then resumes the session through ACP's session load when the agent supports it, and otherwise starts a new ACP session seeded from the session's history.

## The daemon

The daemon, `skylands daemon up`, turns one host into an island. It runs agents, owns their sessions, enforces rules, and keeps working while the citadel is unreachable.

### Connectivity

The daemon runs nats-server in-process as a leaf node, which is a NATS server that dials out to another NATS server or cluster and extends it. The leaf has its own JetStream domain, so the host's session and host streams live on the host, and a mirror in the world's JetStream domain copies them when the link is up. The leaf connects outbound over WebSocket on port 443, so hosts behind NAT or a corporate firewall need no inbound ports. When the world's NATS is a cluster, the leaf lists every server's URL and moves to another on its own when one goes away.

### Unreachable hosts

A citadel learns which daemons it can reach by pinging the skylands-daemon micro service, which uses request-reply and no heartbeat timestamps. When a daemon stops answering, the citadel can't tell a network failure from a stopped or crashed daemon, so the world shows one state for both. The island drifts out on a slack tether, its crystal goes grey, and everything on it holds the last state the citadel saw, along with when it last saw it. The cause shows only after the host returns: a daemon that kept running syncs the events it held, and a daemon that stopped reports its restart and expires anything that was waiting on a person. Decisions and work orders for the host wait until it's reachable again.

### Running agents

- The daemon launches each agent as a child process and drives it over ACP on stdio, using the community Go SDK `github.com/spachava753/acp-sdk`.
- Each agent type sits behind one adapter interface, so adding an agent type never changes the daemon core.
- At ACP initialization the daemon records each agent's capabilities, such as session load and session list, and uses only what the agent advertises.
- Each session runs in its own git worktree on a branch cut from the checkout's, unless it is started in the checkout itself, and subagents work in their lead's worktree. A session's changes are committed, merged into the checkout, or sent as a pull request from the conversation, and `skylands session` removes or prunes the worktrees and branches that ended sessions leave.
- Agents distributed through npm need Node on the host. The daemon checks at startup, records which agent types it can launch, and the world shows that on the island's panel.

### Claude Code hooks

The daemon configures Claude Code command hooks that forward hook payloads to it over its local Unix socket. Subagent start and stop events from those hooks drive the leash lines in the world. The same hooks make Claude Code sessions started by hand in a terminal appear on the island. Their permission requests can be answered remotely, within the hook's timeout.

### Terrain

When a repo is registered, the daemon snapshots its tracked files and their sizes, which become the terrace and its buildings. A terrace shows at most 64 buildings, an 8 by 8 grid, so each stays large enough to click. Until recently touched files can take priority, the daemon keeps the largest files, sorted by descending size and clamped at the limit, and records the total count alongside them. A file watcher records changes made through shell commands, and attributes them to the session whose worktree changed.

### Placement

A work order scoped to the host goes to the daemon's placement processor. It searches the host's repos, chooses the repo and the model under the world's routing rules, and records WorkOrderPlaced with the reason it gives people, such as a keyword it matched in a file path. When no repo fits, it records RepoChoiceRequested, and the order waits until someone picks a repo, which arrives as a ChooseRepo command.

### Rules and local control

The daemon evaluates its local rule set before anything reaches a person. A rule can approve or deny a tool call outright, and only what no rule settles becomes a PermissionRequested event. A deny is final: any matching deny rule refuses the call, whatever allow rule or trust would let it through. Otherwise the most specific matching rule wins. A local CLI talks to the daemon over its Unix socket: `skylands request list` shows what is waiting on a person, `skylands request approve` and `skylands request deny` settle it, and `skylands daemon status` reports the host's state. The daemon accepts local approvals from the OS user that owns it, which unblocks agents while the host is offline.

## NATS

Skylands runs on nats-server with JetStream, and NATS holds no Skylands logic. Daemons and citadels connect to whichever NATS server or cluster the world points at.

- `skylands devworld up` embeds nats-server in-process, alongside a citadel and a daemon. That server is the world's NATS for a one-machine world, and daemons on other machines can join it through its leaf node listener.
- A world that should outlive one machine runs nats-server on its own, on one machine or as a cluster of three or five. An even number adds nothing, because two servers stop accepting writes when either one fails.
- A team that already runs NATS uses its own cluster.

### Setting up a world

A world is set up in the browser. When a citadel starts and finds no world record in NATS, it prints a setup link with a one-time token in its fragment, and every page it serves leads to the setup page. Whoever opens the link names the world, and the page makes the world's owner key and this browser's approver key with nkeys.js. The owner key signs the world record and an entry enrolling the approver key, and the page offers a recovery file holding the owner key, encrypted with a passphrase if one is given. Only after the owner confirms saving it does the page send the signed record and entry, with the token. The citadel writes the record create-only, so the first setup wins and a second citadel skips setup, and the browser forgets the owner key. The recovery file later enrolls another browser on the same page, if every approver is lost.

Each daemon pins the world's owner key. devworld's daemon takes it from the world record, and every other daemon from `--world` in its install command, which the citadel shows under "Add a host". A daemon with no owner key refuses to start, and one whose pinned key differs from the world record's refuses to run.

The owner is responsible for NATS: its accounts, credentials, and the settings in [NATS.md](NATS.md), which `skylands devworld up` applies to its embedded server. A citadel checks the settings it can see from its connection when it starts, and reports any that are missing in its log and on the setup page.

Moving a world from `skylands devworld up` to its own nats-server is a configuration change. Start nats-server with the settings in [NATS.md](NATS.md) and the JetStream store directory `skylands devworld up` was using, at the same or a newer NATS version, then give daemons and citadels the new URLs.

### Streams and consumers

Because NATS holds no Skylands logic, citadels and daemons create the streams and consumers they need. They do it idempotently, at startup, so the same code runs against any NATS deployment.

### More than one server

A world runs on one NATS server or one cluster. Two servers that aren't routed together would each hold their own copy of the world, and daemons and citadels would split between them. Clustered JetStream refuses writes on any server that can't reach a majority of the others, so a partitioned server can't drift from the rest.

### What citadels show

The citadel panel shows the NATS server as it describes itself to the citadel's connection: its name and ID, its nats-server version, its cluster and how many servers are in it, its JetStream domain, the world's account, and the URL the citadel is connected through. A citadel can't tell whether that server runs inside `skylands devworld up` or on its own, and it doesn't need to.

## The citadel

The citadel is the world's web application and observer. It never writes session events; it reads them from the world's streams, projects them, and routes commands to the daemon that owns each session. Any number of citadels can run, and they're interchangeable. A citadel connects as an ordinary NATS client and embeds no server, because it has nothing to hold while disconnected: the world's streams live in NATS, and its projections can be rebuilt from them.

### Web application

- Serves the 3D client and the phone inbox, and lets a browser in once it signs in with its approver key, as [Signing in](#signing-in) describes.
- Keeps no sessions: a browser's sign-in is a statement it signed, which any citadel checks on every request. Browsers never connect to NATS.
- Streams projections to browsers over WebSocket and sends push notifications for waiting items, as [Push notifications](#push-notifications) describes.
- Routes each session command to its owning daemon by request-reply, and returns the daemon's answer.

### Push notifications

- A citadel whose `--url` is HTTPS sends Web Push, unless started with `--no-push`. It signs with the world's VAPID key and encrypts each push for its browser (RFC 8291), through the browser vendors' push services, with no Skylands-run service.
- A browser subscribes through the service worker, only at the citadel's `--url`, since a browser subscribes once per address. It signs the subscription, with the address, using its approver key, and the citadel keeps it under that approver.
- Each island's mirror has one durable `push` consumer, filtered to PermissionRequested and QuestionAsked, that every citadel sending pushes pulls from. A new consumer starts at new events, and the server removes one that no citadel has pulled from for an hour, so a citadel starting doesn't push the backlog.
- Before pushing, the citadel waits for its projection to take in the event, and pushes only while the item still waits. An event older than the daemon's 30-minute request timeout isn't pushed, and a push's TTL ends with the request.
- A push's topic comes from the event's identity, and its tag is the request's or question's, as the page's own notifications tag them, so a duplicate replaces the first.
- A page tells its citadel when it is in sight, and that citadel keeps an entry for its approver in `skylands-presence`. A push to an approver with an entry waits 30 seconds, with the event kept from the consumer meanwhile, and goes only if the item still waits then.
- No push closes a notification, since browsers expect every push to show one. The page closes the service worker's notifications for what no longer waits whenever it is open.
- Revoking an approver deletes its subscriptions, and so does a 404 or 410 from the push service.

### Deployment

The same binary and configuration run on a laptop, inside a company network, or on a public endpoint. Internal versus public hosting is a DNS and TLS choice. Citadels and NATS restart independently: restarting a citadel disconnects only its own browsers, and restarting one server in a NATS cluster moves its connections to the others. Restarting `skylands devworld up` restarts its embedded NATS server, so daemons buffer and resync as they do whenever they can't reach NATS.

### More than one citadel

Citadels have no roles. Each one does work as soon as it's available, a second citadel never doubles a result, and the world behaves the same with one citadel or ten. The reasons to start more are to serve the web app from somewhere else, such as your own laptop, or to keep it up while one restarts.

- Every citadel connects to NATS the same way, with citadel credentials.
- Work triggered by events, such as sending push notifications, runs from one durable JetStream consumer that every citadel pulls from, so one citadel at a time handles each event, and the consumer's acks track progress for all of them.
- Commands and queries are endpoints of the skylands-citadel micro service, so NATS hands each request to one citadel without any of them holding a role.
- Each citadel builds its own projections from the world's streams. Projections are derived, so every citadel's copy agrees.
- A command relayed by two citadels still produces its events once, because the receiving aggregate recognizes the repeated command id.
- An approval signed by an approver is valid through any citadel, because the daemon verifies the signature rather than trusting the relay.
- Stopping a citadel disconnects only its own browsers, and the shared consumer and queue groups hand its work to the others.

## Approvals and security

A daemon acts on an approval only when it carries a signature from an enrolled approver over the exact tool call. The same goes for answering a question, trusting a session, and setting rules, which approve in advance. The citadel relays decisions and can't create them.

![A daemon acts only on an approval signed for the exact call](./approval-flow.svg)

Rules settle most tool calls on the host, so only what no rule covers reaches a person.

### Signatures

- The approver's browser signs, with its approver key, the SHA-256 of the canonical JSON (RFC 8785) of a decision that starts `{v: 1, kind, world, host}`: the decision's kind, the world's owner key, and the host. `kind` keeps a signature over one kind from verifying as another:
  - `toolcall` adds `session`, `request`, `decision` ("allow" or "deny"), `option` (the option an approval chose, none for a denial), and `input` (the SHA-256 of the exact tool input).
  - `answer` adds `session`, `question`, `form` (the SHA-256 of the question's message and fields as the daemon recorded them, each property present), `declined`, and `answers` (the SHA-256 of the answers' canonical JSON, none for a decline).
  - `trust` adds `session`, `trusted`, and `revision`.
  - `session-rules` and `repo-rules` add `session` or `repo`, `rules` (the SHA-256 of the whole list as the daemon keeps it), and `revision`.
  - `start` adds `session` (an id the browser picks, which the daemon refuses once it has seen it), `repo`, `agent`, `mode`, `model`, `inCheckout`, and `prompt` (the SHA-256 of the prompt's text).
  - `prompt` adds `session`, `revision`, and `prompt`.
  - `change` adds `session`, `revision`, `action` ("commit", "merge", or "pull-request"), and `text` (the SHA-256 of a commit's message, or of a pull request's title and body as canonical JSON, none for a merge). A prompt's and a change's `revision` counts the session's prompts, commits, merges, and pull requests, which each moves on, so none can be sent twice.
  - `terminal` adds `session` and `terminal` (an id the browser picks, which the daemon refuses once it has seen it).
  - `terminal-input` adds `session`, `terminal`, `attach` (an id for the browser's view of the terminal), `seq` (that view's count of its inputs, from one), and `data` (the SHA-256 of the input's bytes). The daemon takes each view's inputs only in increasing order, so none can be sent twice.
- `internal/canonical` and `web/src/canonical.ts` write the same bytes, checked against the RFC's test vectors and a shared vector of every kind both must reproduce.
- Trust and rule lists can be set back and forth, and the citadel names the commands, so their decisions name the revision they replace: how many times that session's trust, that session's rules, or that repository's rules were set. The daemon refuses a change against any other revision, so an old signed change can't be sent again, and two browsers changing the same list conflict rather than overwrite each other.
- The daemon checks answers against the question's form before the signature: only its fields, a choice among its options, numbers as numbers and integers whole within ±2^53, booleans as booleans, an unanswered field left out rather than empty, and every required field answered. It records the answers, and gives them to the agent, as the canonical JSON that was signed.
- The daemon builds the same decision from its own record and verifies the signature over it, after reading the approver registry up to its latest record, so a revocation stored before a decision arrived counts against it. Only then does it record the decision (PermissionGranted or PermissionDenied, QuestionAnswered, SessionTrusted, SessionRulesSet, or RepoRulesSet) with the approver's key and the signature.
- Once the world exists, a decision through the citadel without a valid signature is refused, and so is a start, a prompt, a commit, a merge, a pull request, opening a terminal, or anything typed into one. Declining, turning trust off, and rule changes that only make things stricter are signed too. Interrupting, stopping, and changing a session's mode or model aren't signed, since none of them can make an agent act, but only a signed-in approver reaches them through the citadel, and `skylands session stop` stops an agent on its host.
- Decisions an approver signed before it was revoked stand; `skylands request history` shows them as signed by an approver that no longer approves. What it approved in advance doesn't: trust it turned on ends (SessionTrusted, by "revocation"), and a rule list it signed lapses (SessionRulesLapsed, RepoRulesLapsed), keeping its deny and ask rules while its allow rules ask, until an approver sets the list again. A session's copy of its repository's rules stands on the repository list's signature, which SessionStarted records. The daemon records a lapse as soon as it reads the revocation, and checks the signer again before trust or an allow rule lets any call through. Trust and rules set on the host never lapse.
- A deny rule is final: no allow rule, however specific, and no trust lets through a call a deny rule matches.
- The daemon answers an agent's permission request only with its "allow once" or "reject once" option. An "always" option would settle later calls inside the agent, where neither a deny rule nor a revocation reaches them, so a signed approval naming one is refused.
- `PermissionRequested` names the tool input by the SHA-256 of its exact bytes and its size, with a preview that is the whole input when it is at most 8 KB, so events stay small. The daemon keeps the whole input on the host under that hash, from when it records the request, and serves it while the request waits. The browser shows the whole input, from the preview or fetched, hashes it itself, and its approval names that hash; the daemon refuses an approval through the citadel whose hash isn't that of the input the agent proposed, so an input changed on its way to the person can't be approved.
- When a tool call ends, `ToolCallCompleted` names what it produced by the SHA-256 and size of a `ToolCallOutput`, the content the agent last reported and its raw output, as canonical JSON, which the host keeps whole beside the inputs. The call's record keeps only so much of each text, for showing. So from a signed decision, `skylands request history --show <request>` recovers the exact input the signature covers and what the call did with it, and checks both against their hashes.
- The first valid decision wins, and any later decision for the same request is rejected.
- A terminal runs whatever is typed into it, so the browser signs every keystroke, as a `terminal-input` decision. The daemon checks the opening against the whole registry, as it does a decision, and each keystroke against the registry as it already holds it, so typing waits on nothing, and a revocation it reads stops the next keystroke. Closing a terminal and changing its size aren't signed, since neither makes anything act.
- Approvals, answers, and repository rules set at the host through the local CLI are accepted from the OS user that owns the daemon.

### Keys

- Each world has an owner key, made in the owner's browser when the world is set up and kept only in the recovery file the owner downloads. It signs the world record and the entries that enroll approvers through setup or the recovery file.
- An approver adds another with a voucher: a one-time grant it signs, naming a claim key whose seed travels only in the link's fragment. The browser that opens the link signs its approver entry with that seed. A voucher works once, within 15 minutes of when it was stored, by the NATS server's clock.
- The owner key or any approver can revoke any approver, and a revocation is never undone. An approver approves only while it, and every approver back to the owner key that added it, isn't revoked, so revoking one removes everything added through it.
- The approver registry is the `skylands-approvers` stream in the world's JetStream domain. Each record is written once, to a subject of its own, and the stream takes no second message on a subject and refuses deletes and purges, so a revocation, once stored, can't be taken back. The registry is decided from the records in stream order, so every citadel and daemon reading the stream agrees on it. Each daemon reads it through its leaf node from the world's domain, `world` unless `--world-domain` names another.
- Each daemon pins the owner key and refuses to run in a world whose record names another.
- NATS decides which daemons and citadels join the world. The owner gives each one credentials for the world's NATS account, however they run NATS, and Skylands adds no enrollment of its own: whatever NATS admits to that account is trusted as a daemon.
- The citadel holds only a scoped signing key, which can mint credentials for limited roles such as integrations.
- A compromised citadel therefore can't enroll an approver, move a daemon into another world, or forge an approval.

### Signing in

- Once the world exists, a browser gets in through the citadel only by signing in with its approver key. The citadel names the world, the address the browser reached it at, and twelve hours from now by its own clock, and the browser signs them unchanged. The signed sign-in comes back as an HTTP-only, same-site cookie, named for the citadel's port, since a browser sends a host's cookies to every port on it.
- Every API call and WebSocket checks the cookie: signed by an approver that still approves, for this world and this address, and not expired. No citadel keeps a session, so any citadel of the world accepts the cookie, and a restart signs nobody out.
- Revoking an approver ends its sign-in: each citadel closes its WebSocket and refuses its cookie, and does the same for every approver added through it.
- Without signing in, a browser can do only what becoming an approver needs: set the world up with the setup link's token, read the world, its approvers, and a voucher whose claim key it holds, and enroll with an entry the owner key or a voucher's claim key signed.
- A request that changes something must come from the citadel's own page. One whose Origin names another site or port is refused, since the cookie alone can't tell them apart, and the WebSocket takes only the citadel's own origin.
- The page signs in whenever it opens the world or reconnects, and again when a request says its sign-in lapsed, so nobody signs in by hand. Setup, the recovery file, and an invite link each end by signing in with the new key. A browser that can't sign in is sent to the setup page, which says why and offers an invite link or the recovery file.
- Before the world exists there's no approver to sign in as, and the citadel lets every request through, as daemons take unsigned decisions until then. While a citadel can't tell whether the world exists, it lets no browser in.

### Waiting and cancelling

- An agent waits for a decision for as long as it takes, unless someone stops it.
- Stop cancels the agent's turn, and the daemon answers any open permission request as cancelled.
- Sessions started by hand in a terminal reach the world through Claude Code hooks, so remote decisions must arrive within the hook's timeout.
- Push notifications name who is waiting and where, but carry no tool input; the details load after sign-in.

## Data handling

The daemon decides what leaves the host. Session and host events carry metadata by default: tool names, file paths, commands, plans, context use, token counts, and decisions.

- Diffs, file contents, full transcripts, and what each tool call took in and gave back stay on the host until someone asks for them. The citadel requests them on demand through the island's fetch subjects, and the daemon answers or refuses based on its rules.
- NATS stores events. Each citadel derives its projections from them _in memory_, so no citadel holds data of its own, and it keeps fetched content only for as long as a person is viewing it.
- Event streams on each host are capped by size. When an offline host reaches the cap, the daemon stops starting new sessions rather than dropping events that haven't synced.
- Push notifications carry no tool input, file contents, or commands.

## Agents and adapters

Every agent runs over ACP, the Agent Client Protocol, so the daemon speaks one protocol to all of them. The first release supports five agent types.

| Agent | Connects through |
| --- | --- |
| Claude Code | The claude-agent-acp adapter, distributed through npm |
| Codex CLI | The codex-acp adapter, distributed through npm |
| Gemini CLI | Its native ACP mode, `gemini --acp`, through npm |
| goose | Its native ACP mode, `goose acp`, as installed on the host |
| Local models | OpenCode in ACP mode, pointed at Ollama |

Claude Code also reports through command hooks, which cover subagent lifecycle and sessions started by hand.

Any other agent on the [ACP agent list](https://agentclientprotocol.com/get-started/agents.md) joins through the same adapter interface without daemon changes. The daemon reads the ACP registry to fill the launch menu and to install agents on a host.

## Client and rendering

The 3D client is a browser application built on three.js and served by the citadel, along with its own pinned three.js build, so internal deployments need no internet access. It renders from projections the citadel streams over WebSocket, plus live updates as events arrive.

- Geometry is low-poly and flat-shaded, and every island, terrace, and building layout is seeded from stable ids, so a host looks the same on every visit.
- Labels, name tags, and file tags are DOM elements projected from 3D positions, which keeps them crisp and lets them be real buttons.
- Agent name tags and file tags appear only for the terrace being viewed, so the world view stays readable.
- Panels, scene, sky, and lighting share one set of CSS color tokens, with a day sky in the light theme and dusk in the dark theme.
- Reduced-motion settings turn off flight, bobbing, and camera glides, and a browser without WebGL gets a clear message instead of a blank stage.

## Prototypes and open items

Two prototypes show the design so far.

- [3D world prototype](https://claude.ai/artifact/UTjwRfHitMTRxWszMtGsw1): the castle citadel, islands, terraces with roped borders, airships that land and carry ballast, approvals, five-level drill-down, unreachable hosts, and work order dispatch.
- [2D design canvas](https://claude.ai/artifact/UvX38Kf5zToXcQFE8iSuER): the first flat island view, with the unit panel, timeline replay, and context fog.

Open items:

- [x] Choose the domain for Skylands: skylands.ai.
- [ ] Set default size caps for host event streams.
- [x] Design Web Push for approvers' phones and browsers (#53).

## Repository layout

```
skylands/
├── cmd/
│   └── skylands/              the one binary: devworld, citadel, daemon, and the local CLI
├── api/
│   └── v1/                    public: subjects, event and command types
├── internal/
│   ├── cli/                   subcommands, and the client for the daemon's socket
│   ├── citadel/               the citadel role
│   │   ├── projections/       in-memory views of the world
│   │   └── web/               HTTP and WebSocket server for browsers
│   ├── daemon/                the daemon role
│   │   ├── agents/            agents as child processes over ACP, and their adapters
│   │   ├── blobs/             exact inputs, outputs, and prompts' files and images, under their SHA-256
│   │   ├── fetch/             on-demand reads: transcripts, diffs, tool calls, files, and images
│   │   ├── host/              Host aggregate
│   │   ├── session/           Session aggregate
│   │   ├── socket/            local API for the CLI
│   │   ├── terminal/          a person's shell in a session's worktree, in a terminal of its own
│   │   └── terrain/           a repository's files as the world shows them
│   ├── approval/              approvers, signed decisions, and their verification
│   ├── canonical/             JSON in RFC 8785's canonical form, for signing
│   ├── es/                    event-sourcing kit over JetStream
│   ├── fakeagent/             scripted ACP agent for tests
│   ├── filelock/              the lock a daemon holds on its island while it runs
│   ├── natsembed/             in-process nats-server for devworld and leaf nodes
│   ├── preflight/             checks of what a host needs to run agents
│   ├── rules/                 rule language and evaluation
│   ├── service/               systemd and launchd services that follow their binary
│   ├── titles/                conversation titles from a side session
│   └── watchdog/              ends a daemon's agents if the daemon dies
├── web/                       citadel front end, built to web/dist and embedded
│   └── src/
│       ├── world/             the 3D scene and its themes
│       └── panels/
├── docsite/                   docs.skylands.ai, built with Starlight
├── docs/                      design notes and diagrams
├── scripts/                   release smoke test
├── test/
│   └── e2e/                   daemon, citadel, and embedded NATS in one test process
├── .github/workflows/         CI, releases, and the docs site
├── .goreleaser.yaml           release archives and the Homebrew cask
└── go.mod
```

### Sources

- [ACP agent list](https://agentclientprotocol.com/get-started/agents.md), for supported agents and adapters
- [spachava753/acp-sdk on pkg.go.dev](https://pkg.go.dev/github.com/spachava753/acp-sdk), for the Go ACP SDK
