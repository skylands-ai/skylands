<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/skylands-lockup-dark.png">
    <source media="(prefers-color-scheme: light)" srcset="docs/assets/skylands-lockup-light.png">
    <img alt="Skylands" src="docs/assets/skylands-lockup-light.png" width="600">
  </picture>
</p>

# Skylands

Skylands is a self-hosted system for running, watching, and approving coding
agents from several providers across several machines, shown as a 3D world
of floating islands. Every machine that hosts agents runs a daemon, which
makes it an island. A citadel serves the web application. Both run on NATS.

The design is in [docs/DESIGN.md](docs/DESIGN.md) and the current plan in
[docs/PLAN.md](docs/PLAN.md). Today the world shows the citadel and its
islands, with each island rising as its daemon connects and going grey when
the citadel can't reach it. Agents, repositories, and approvals come next.

## Install

Skylands is one binary, `skylands`, for Linux and macOS on amd64 and arm64.

**From a release.** Download the archive for your machine from the
[releases page](https://github.com/skylands-ai/skylands/releases), unpack
it, and put `skylands` on your PATH:

```sh
tar xzf skylands_*_linux_amd64.tar.gz skylands
sudo install skylands /usr/local/bin/
```

On macOS, a binary downloaded with a browser is quarantined, and
`xattr -d com.apple.quarantine skylands` clears it.

**From source.** You need Go 1.26 and Node 22 or newer. Node builds the web
client, which the Go build embeds.

```sh
make web && make install
```

`make install` puts `skylands` in your Go bin directory, and `make build`
puts it in `bin/` instead. `go install` alone builds a binary without the
web client, which is fine for a machine that only runs a daemon, but a
citadel from it serves a page saying the client is missing.

Every machine that runs agents needs git, and Node 22 or newer for Claude
Code and Codex, whose adapters come from npm. `gh` is needed only to open
pull requests from a conversation. To check a machine, run:

```sh
skylands preflight
```

It reports git, Node, and npx with the versions and paths it found, starts
each agent over ACP without asking a model anything, reports whether Claude
Code and Codex are logged in as each sees it and which models each offers
that login, looks for allow rules and hooks in Claude Code's own settings
and plugins, which let calls past the host's approvals (see
[Approvals](#approvals)), checks `gh`, and asks the daemon on the local
socket which version it runs.
Reading the models opens a session in the agent without prompting it,
which starts what a session needs, such as its MCP servers.
Each problem comes with
what to do about it, and the command exits with status 1 when a check
fails. An agent that isn't installed, such as goose without `goose` on
PATH, or Claude Code and Codex without Node, is skipped rather than failed
as long as another agent is installed; with none but the built-in fake
agent, it fails.

The daemon makes the same agent checks itself, in its own environment,
when it starts and every ten minutes, and records which agents it can run
whenever that changes. It reads the models at startup, when preflight asks
it to check, and for an agent whose login changed in between. The island's
panel in the world lists those agents,
with each one's login and version, and `skylands daemon status` names them.
An agent the host can't run isn't listed; the daemon's log and
`skylands preflight` say why. Preflight also asks the daemon to check
again, which updates the island at once, and warns when this shell can run
an agent the daemon can't, as when a service has a different PATH.

## Quickstart

```sh
skylands devworld up
```

`devworld up` runs an embedded NATS server, a citadel, and a daemon for this
machine in one process, so you get the castle and one island. The first time,
it prints a link to set the world up, apart from its log and in color in a
terminal:

```
 Set up this world  Open this link in a browser:

    http://127.0.0.1:8080/setup#...
```

Open it and set the world up, as the next section describes. After that, the
world is at <http://127.0.0.1:8080>, and that browser opens it signed in.

The world has two themes, switched from the citadel's panel: Default, a
castle and floating islands in a daytime sky, and Sci-fi, a space station
with asteroids for islands, communications arrays where the crystals hang,
towers lit by landing-strip lights for terraces, and starships for
airships, always in a starfield. A theme changes only how
the 3D world is drawn, never what it shows or the rest of the page, and the
choice is kept in your browser.

### Set up the world

The setup page names the world and makes you its owner. Your browser makes
the world's owner key and puts it in a recovery file for you to download,
encrypted with a passphrase if you give one. Save the file before you finish:
it is the only copy of the owner key, which never leaves your browser any
other way, and the browser forgets it once setup is done. The browser also
makes an approver key of its own, kept in its storage for the citadel's
address, which will sign your approvals.

Use the address the citadel prints. A browser keeps its key per address, so
to it `http://localhost:8080` and `http://127.0.0.1:8080` are different
places; the citadel sends `localhost` and `[::1]` on to the address it prints.
Don't set up in a private window, which forgets the key when it closes.
To set the world up at another address, such as this machine's name on
your network or an HTTPS address in front of the citadel, give
`devworld up` that address with `--url`, as in `--url
https://box.example.ts.net`; the setup link names it. Without `--http`
the citadel still listens on 127.0.0.1 only, so whatever serves that
address must reach it there, or listen more widely with `--http`.

Until the world is set up,
every page leads to `/setup`, and only the link's one-time code sets it up; a
citadel prints a new link each time it starts, and stops once the world
exists. The first setup wins: a second citadel on the same NATS finds the
world and offers no setup.

### Approvers

An approver is a browser that can approve what agents do in the world: the
one that set it up, and any it adds. The Approvers section of the citadel's
panel lists them. To add one, such as your phone, press "Add an approver":
it shows a QR code and a link, which work once, within 15 minutes, and make
whichever browser opens them first an approver, so share them only with
yourself. Opening a link in a browser that already approves leaves it
unused.

Any approver can revoke any other, or itself, and a revocation is never
undone. Revoking an approver also removes every approver added through it,
and the section says which before you confirm, so revoking a stolen laptop
removes anything the thief added with it. A revoked browser becomes an
approver again only with a new link or the recovery file, which on the
setup page, `/setup`, adds the browser it is opened in. The recovery file
is also how you get back in if no approver is left.

Once the world is set up, only approvers get in. A browser signs in to the
citadel with its approver key by itself, whenever it opens the world or
reconnects, so there's nothing to type and nothing to do after a restart.
A browser without a key, or whose key was revoked, gets the setup page
instead of the world, and revoking an approver closes the world in its
browser at once. Until the world is set up there's no approver to sign in
as, and the citadel lets anyone who reaches it in, so set it up before you
let others reach it.

### Keep it running

`up` runs in the terminal you start it from. To have it start when you log
in, restart if it fails, and keep running after the terminal closes, install
it as a service instead, with the same flags `up` takes:

```sh
skylands devworld install
```

On a machine that only hosts agents for a world running elsewhere, install
the daemon the same way, with the command under "Add a host" in the citadel's
panel: `skylands daemon install --world ... --nats-url ...`. Install
writes a systemd user unit on Linux or a launchd agent on macOS that runs
this `skylands` binary, gives it this shell's PATH so it finds npx, git, and
gh, waits for the island to come up, and prints where the logs are, and the
setup link while the world isn't set up yet. Install
again to change the flags, restart the service after replacing the binary,
and `skylands devworld uninstall` or `skylands daemon uninstall` stops and
removes it, leaving the data directory alone. On Linux the service runs
while you are logged in, unless `loginctl enable-linger` keeps it running
from boot. A service gets only PATH from your shell, so an API key in your
environment does not reach it, and `skylands preflight` warns about that.

### A second island on the same machine

Every island needs its own host name and data directory, and every daemon
outside devworld needs the world's owner key, which "Add a host" in the
citadel's panel shows:

```sh
skylands daemon up --world O... --host other --data-dir /tmp/skylands-other
```

The daemon pins that key, and refuses to start without one or to run in a
world whose owner key is different.

Its island rises within a few seconds. Stop it with Ctrl-C and the island
drifts out on a slack tether about six seconds later. Start it again and it
comes back.

### Register a repository

On the machine whose island should carry it:

```sh
skylands repo register ~/code/storefront
skylands daemon status
```

The daemon records the repository and a snapshot of its tracked files in
its own event stream, the citadel mirrors that stream, and a terrace with a
building per file appears on the island. Click the island, then "Fly in" on
the terrace, then a building. Each daemon has its own local socket and its
own island, so a second daemon on the same machine needs its own `--host`
or `--data-dir`, and `--socket` on both `daemon up` and these commands. A
daemon refuses to start on a socket, or an island in a data directory, that
another daemon is running.

### Run an agent

Open a terrace and type a task into the bar at the bottom of the world. The
order goes straight to that island's daemon, which checks out a git worktree
of the repository on a `skylands/<session>` branch and runs the agent there.
An airship rises from the terrace and lands on each file the agent reads or
edits, lighting the building blue or amber, and red when two sessions are
on the same file. The terrace panel shows the agent, what it is reading or
editing, and when it stops. Click the airship or the agent to follow it
and read the conversation and the diff in its worktree. Both stay on the
host, under `transcripts/` and `worktrees/` in the data directory, and the
citadel fetches them only while you look.

A session is a conversation. When the agent finishes a turn it waits, the
airship hovers with a "Waiting for you" tag, and the next prompt continues
it, so one session can carry a long piece of work the way a chat does.
The Inbox, next to World in the header, is where to hold those
conversations: one line per session on any island with its title, the
agent, the repository, its status, and when it last did anything, newest
first. The agent names the conversation itself: as the first turn starts,
the daemon asks it for a title in a side session that never touches the
conversation, and the first line of the prompt stands in until it answers.
It lists open conversations;
"All" shows finished ones too, and the choice is remembered. A
yellow mark is waiting on you, green is working, grey-blue is idle, a ring
is over, and a bold line with a blue dot has something you have not read.
A session waits on you while its agent has stopped for a reply you have
not read yet; once you have read it, it is idle until you answer. Open one
to read it as a chat, with the agent's words and tool calls arriving as
they happen, reply at the bottom, or end the session, which stops the
agent and keeps its worktree. Each tool call is a chip where it happened;
click one to see what the call did: the exact command or input, what came
back, such as a command's output or an edit's diff, and how long it took.
That record stays on the host beside the transcript and is fetched only
while you look. The world is paused while the inbox is
open, and "Show in world" flies back to the airship. The header's Inbox
badge and the tab title count the sessions waiting on you. Every answer
the agent gives comes back with what it cost in tokens, and the daemon
records it: a conversation's header shows the total for that session, the
top of the inbox shows the total across every conversation, and a
terrace's panel shows the total for its repository under the tracked
files, with the exact counts as a tooltip.

Four agents are available, and the agent chip lists the ones the island
can run. The built-in fake agent reads a few files and
edits one, so you can see the loop without any API keys. Claude Code runs
through its ACP adapter from npm, at the exact version pinned in
`internal/daemon/agents/agents.go`, which `npx` fetches on first use, and
needs a Claude Code login on that machine. The model chip lists the models
the chosen agent offers the island's login, as the island's daemon last
read them from the agent, overrides the model for that session, and keeps
your last choice for each agent; leave it on Agent's default for the
agent's own default, or pick Other model id to type one that isn't listed.
The daemon sets the model through the agent's own model setting over ACP,
so a model the agent doesn't take stops the session from starting, and the
error lists the ones it offers. A
model alias such as `fable` in your Claude Code settings may not work under the
adapter, so pick a full model id in the chip if a session fails on its model.

For an agent with more than one mode, a Mode chip beside the model chip
picks the mode the session starts in, as the agent names it: Claude Code's
Manual or Plan, or goose's approve or chat. Every mode it lists keeps the
agent asking before it acts, as [Approvals](#approvals) describes. While the
agent waits for your reply, the conversation's header has the same chips,
which change the session's mode and model from the next prompt on, and on
the host `skylands session mode <id> <mode>` and `skylands session model
<id> <model>` do the same. When the agent changes its own model, as one of
its commands can, the header follows. Each change is noted in the
conversation, and a session that comes back after a daemon restart is set
to its latest mode and model.

Typing `/` at the start of a reply lists the slash commands the agent
announced for the session, filtered as you type, with what each does; pick
one and the line under the box says what it takes. Your skills, plugins'
commands, a repository's custom commands, and MCP prompts are among them,
as the agent announces them on the island's machine. The daemon runs only
commands the agent announced, and refuses a prompt that starts with any
other, typed or picked, since an agent may still run a command it doesn't
list, such as Claude Code's `/logout`. It also refuses the ones that act
outside the session's tool calls, where no approval reaches: Claude
Code's `/remote-control`, `/config`, `/schedule`, `/ultrareview`, `/loop`,
`/design`, `/design-consent`, `/design-revoke`, `/usage-credits`, `/import`,
and `/auto-mode-setup`, and Codex's `/logout`. A prompt that starts with a
path, such as `/usr/bin`, is text.

Codex runs the same way, through its ACP adapter from npm at the version
pinned in `internal/daemon/agents/codex.go`, and needs a Codex login on that
machine: run `npx -y @agentclientprotocol/codex-acp@2.5.0 login`, or `codex
login` if you have the Codex CLI. An API key in the environment isn't
enough; log in with the key through `codex login --with-api-key`. Which
models Codex offers depends on the account, and the model chip lists the
ones it offers the island's.

goose runs as installed on the host: the daemon starts `goose acp` from its
PATH, so install goose
([installation](https://block.github.io/goose/docs/getting-started/installation))
and run `goose configure` to choose its model provider, which also decides
the models the chip lists. A service gets only PATH from your shell, so
install goose where that PATH finds it.

### Approvals

An agent does nothing to a repository without permission. The daemon's
rules let tool calls that only look pass: reads, searches, and thinking.
Everything else, an edit, a shell command, a fetch, waits for a person. A
waiting call lights the citadel's beacon, drops a yellow beam from the
airship, counts in the Inbox badge and the tab title, and shows in the
conversation as a card with the tool, the exact input the agent proposed,
and two answers: Allow once and Deny. An agent's "always allow" answer is
never given, because the agent would then stop asking, and neither a rule
nor revoking an approver could reach those calls. "Trust this session", in
a conversation's header, lets that session's calls through without asking
from then on, except what a deny rule refuses. A request nobody answers in 30 minutes
expires, and the agent is told it was cancelled; ending the session
cancels whatever it was asking.

Each agent may be in only the modes that keep it asking before it acts,
which the daemon sets through the agent's mode setting over ACP: a session
whose mode can't be set doesn't start. If the agent moves itself to any
other mode, the daemon sets it back, and stops a session it can't set
back; the conversation notes both.

The daemon sets every Claude Code session to its Manual mode, or to Plan
when you choose it, whatever `permissions.defaultMode` Claude Code's
settings name, and turns its bypass mode off for the session. Plan makes
no changes until you approve its plan, on a card whose Allow once answer
leaves Plan for Manual. In Manual, Claude Code asks before making changes, except a call that an
allow rule in its own settings matches: `permissions.allow` in
`~/.claude/settings.json`, or in the repository's `.claude/settings.json`
or `.claude/settings.local.json`. Claude Code runs such a call without
asking, so it reaches neither the host's rules nor a person, deny rules
included. `skylands preflight` names the files that hold allow rules;
remove the ones for calls Skylands should decide.

Claude Code also runs hooks: commands it starts on events, from the same
settings files, its managed settings, and the plugins they enable. A
`PreToolUse` or `PermissionRequest` hook can approve a tool call itself, so
the call reaches neither the host's rules nor a person, and every hook runs
its command with no approval at all, as the daemon's user, in the session's
directory. `skylands preflight` names each file and enabled plugin that
defines hooks, with its events; remove or turn off the ones Skylands
sessions shouldn't run.

Codex has a sandbox of its own, and the daemon sets every Codex session to
its read-only mode, through Codex's mode setting over ACP, so Codex asks
before every edit; a session whose mode can't be set doesn't start. A shell command runs inside that
sandbox without asking, where it can read the machine but not write to it
or reach the network. A command that needs more fails there unless Codex
asks to run it outside the sandbox. The daemon adds instructions to every
Codex session, after any of your own in Codex's config, telling it to ask
whenever a command needs to write or reach the network. If it still reports
a command blocked by the sandbox, tell it to ask for escalated permissions.
That request waits for the rules or a person like any other. A command the
sandbox stopped doesn't appear in the conversation, because Codex doesn't
report it.

goose approves every tool call itself in its default mode, so the daemon
sets every goose session to its approve mode, in which goose asks before
every tool call it hasn't been told in its own settings to always allow,
or to its chat mode when you choose it, in which goose runs no tools.
goose doesn't say what kind of call each one is, a read or an edit, so the
host's rules can't let its reads pass on their own: every goose call waits
for a person unless a rule or "Trust this session" lets it through.

On the host itself, `skylands request list` shows what is waiting, and
`skylands request approve <id>` and `skylands request deny <id>` settle it
over the daemon's socket, which works while the citadel is unreachable.
Every request and every answer is recorded in the session's history, with
who answered: the host's CLI, a browser through the citadel, or trust.
Answers from a browser are signed. Allowing or denying a tool call,
answering or declining a question, trusting a session, and changing rules
each sign the decision with the browser's approver key, and the daemon
checks the signature, against the world's approvers and against its own
record, before it acts, so a citadel can pass decisions along but can't
make one. Trust and rule changes are signed over the version of them the
browser showed, so one made since is refused rather than overwritten, and
an old one can't be sent again. What sets an agent working or changes the
repository is signed the same way: starting a session, each prompt, and
committing, merging, or opening a pull request. A start names the session,
which the browser picks, and the rest name the session's revision, which
each of them moves on, so none can be sent twice, and a merge signed
before another prompt is refused after it. The session records who sent
each. Stopping or interrupting a session, and changing its mode or model,
aren't signed: none of them can make an agent act. Only an approver's
browser gets into the world at all, as [Approvers](#approvers) describes.
`skylands request history` lists every decision on tool calls on the
host, with who made it and whether its signature still verifies, and
`skylands request history --show <id>` prints one decision whole: its
signature, the exact input the request named, and what the tool call
produced. The host keeps both whole under `blobs/` in the data directory,
named by their SHA-256, and the command checks each against the hash the
session's events name.

Revoking an approver doesn't undo what it already decided, but it ends
what it approved in advance: a session it trusted asks again, and a rule
list it set keeps its deny and ask rules while its allow rules ask, until
another approver approves the list again from its rules sheet. The same
goes for a session's copy of a repository's rules. Rules set on the host
with `skylands rule` never lapse.

### An island on another machine

devworld's NATS server asks for no credentials, so any daemon that can
reach its leaf listener joins the world: only do this on a network you
trust. The embedded server listens on localhost unless told otherwise. A
world on your own nats-server admits daemons by the credentials you give
them; see [docs/NATS.md](docs/NATS.md).

On the machine running the world:

```sh
skylands devworld up --leaf-listen 0.0.0.0:7422
```

On the other machine, with `LAPTOP` as the first machine's address and the
owner key from "Add a host" in the citadel's panel, which shows the whole
command:

```sh
skylands daemon up --world O... --nats-url nats-leaf://LAPTOP:7422
```

The daemon keeps running while the world is unreachable and reconnects on
its own, as does the citadel if NATS restarts, and the browser if the
citadel restarts. While the browser can't reach the citadel, the world dims
and shows the last state it saw. A daemon restart does not end its
sessions: on the way back up it brings each open session's agent back,
through the agent's own session when it can load one (Claude Code can) and
otherwise through a new session given the transcript, and a turn the
restart cut short is picked up again. The conversation notes where the
restart happened.

A session works in a git worktree of its own, on a branch cut from
whatever the checkout was on, or in the checkout itself when the command
bar's Where chip says so. When the agent is not mid-turn, the conversation
shows where its changes stand and offers to commit them, merge the branch
into the checkout, or push it and open a pull request with `gh`. On the
host, `skylands session list` shows every session, `session stop` ends
one, `session remove` deletes a finished session's worktree and branch,
and `session prune` does that for all of them, keeping any worktree with
uncommitted changes or branch with unmerged commits unless `--force` is
given.

Rules settle what an agent may do before anyone is asked. Reads, searches,
and thinking pass by default; everything else asks a person in the
conversation, the inbox, or `skylands request` on the host. When the agent
asks, the approval card can keep the answer as a rule, for that session or
for every session on the repository, with a pattern it proposes from the
request: `allow execute go test *`, `allow edit worktree`, `deny execute
rm -rf *`. The rules sheet on a terrace and behind a conversation's Rules
button lists and edits them, and on the host `skylands rule list|add|
remove` do the same for a repository. A deny is final: a call any deny
rule matches is refused, whatever allows it and whether or not the session
is trusted. Otherwise the most specific matching rule wins, an ask beats
an allow among equals, and a session's own rule beats one it copied from
the repository. To allow only some commands, write the allow rules alone:
everything else asks.

When an agent needs something only you can tell it, such as Claude Code
asking a clarifying question, the question arrives in the conversation as
a card with its choices and a box for anything else, and in the inbox, the
beacon, and `skylands request list` like an approval. Answer it there, or
with `skylands request answer <id> field=value`, or decline it.

To change course while the agent is working, press Interrupt in the
conversation, or run `skylands session cancel <id>` on the host. The agent
stops, the session stays open, and your next reply steers it. End session
is the way out when you are done with the conversation.

Each agent runs in a process group of its own. When its session ends, or
the daemon stops, the agent is asked to exit and then stopped if it
doesn't, and whatever it started that is still running goes with it, such
as a dev server or a test run its shell tool left behind. Ctrl-C on a
daemon in a terminal, or closing the terminal, reaches the daemon alone,
which stops its agents this way, and its open sessions are resumed when it
starts again. A daemon that dies without stopping them, killed or crashed,
leaves that to its watchdog: a small `skylands` process, the same binary,
that the daemon tells about each agent and that ends whatever is still
running once the daemon is gone.

### Running the roles separately

`devworld up` is the same code as the separate roles, with NATS started for
you. Against your own nats-server, configured as
[docs/NATS.md](docs/NATS.md) describes, run `skylands citadel up --nats-url
... --url ... --leaf-url ...`, set the world up from the link it prints, and
run the command under "Add a host" on each host. `--url` is the address
people open the citadel at, and `--leaf-url` the one daemons dial.

## Development

| Command | What it does |
| --- | --- |
| `make web` | Builds the web client into `web/dist` |
| `make docs` | Builds the documentation site in `docsite/` into `docsite/dist`; it needs Node 22.23.4 or newer |
| `make build` | Builds `bin/skylands` with the embedded client |
| `make test` | Runs every Go test, including a whole-world test under `test/e2e` |
| `make lint` | Runs `go vet` and golangci-lint |
| `make install` | Installs `skylands` into your Go bin directory |
| `make snapshot` | Builds every release archive into `dist/` with goreleaser, publishing nothing |

For client work, `cd web && npm run dev` serves the client with live reload
and proxies the WebSocket and the API to a citadel running on port 8080.
The dev server is an address of its own, so once that world is set up, a
browser signs in there only after an invite link, with its host changed to
the dev server's, or the recovery file makes it an approver there too.

### Releasing

Push a version tag, such as `git tag v0.1.0 && git push origin v0.1.0`.
The release workflow builds the archives, uploads them to a draft release,
runs `scripts/release-smoke.sh` on the Linux binary, and publishes the
release only if that passes. The smoke test runs a world on its own ports
and checks that the binary serves the web client, and it runs the same way
on any binary: `scripts/release-smoke.sh dist/skylands_linux_amd64_v1/skylands`.
