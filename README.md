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
Code, whose adapter comes from npm. `gh` is needed only to open pull
requests from a conversation. To check a machine, run:

```sh
skylands preflight
```

It reports git, Node, and npx with the versions and paths it found, starts
each agent over ACP without asking a model anything, reports whether Claude
Code is logged in as Claude Code itself sees it, checks `gh`, and asks the
daemon on the local socket which version it runs. Each problem comes with
what to do about it, and the command exits with status 1 when a check
fails.

The daemon makes the same agent checks itself, in its own environment,
when it starts and every ten minutes, and records which agents it can run
whenever that changes. The island's panel in the world lists those agents,
with each one's login and version, and `skylands daemon status` names them.
An agent the host can't run isn't listed; the daemon's log and
`skylands preflight` say why. Preflight also asks the daemon to check
again, which updates the island at once, and warns when this shell can run
an agent the daemon can't, as when a service has a different PATH.

## Quickstart

```sh
skylands devworld up
```

Open <http://127.0.0.1:8080>. `devworld up` runs an embedded NATS server, a
citadel, and a daemon for this machine in one process, so you get the castle
and one island.

### Keep it running

`up` runs in the terminal you start it from. To have it start when you log
in, restart if it fails, and keep running after the terminal closes, install
it as a service instead, with the same flags `up` takes:

```sh
skylands devworld install
```

On a machine that only hosts agents for a world running elsewhere, install
the daemon the same way: `skylands daemon install --nats-url ...`. Install
writes a systemd user unit on Linux or a launchd agent on macOS that runs
this `skylands` binary, gives it this shell's PATH so it finds npx, git, and
gh, waits for the island to come up, and prints where the logs are. Install
again to change the flags, restart the service after replacing the binary,
and `skylands devworld uninstall` or `skylands daemon uninstall` stops and
removes it, leaving the data directory alone. On Linux the service runs
while you are logged in, unless `loginctl enable-linger` keeps it running
from boot. A service gets only PATH from your shell, so an API key in your
environment does not reach it, and `skylands preflight` warns about that.

### A second island on the same machine

Every island needs its own host name and data directory.

```sh
skylands daemon up --host other --data-dir /tmp/skylands-other
```

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
the terrace, then a building. Each daemon has one local socket, so a second
daemon on the same machine needs `--socket` on both `daemon up` and these
commands.

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

Two agents are available. The built-in fake agent reads a few files and
edits one, so you can see the loop without any API keys. Claude Code runs
through its ACP adapter from npm, at the exact version pinned in
`internal/daemon/agents/agents.go`, which `npx` fetches on first use, and
needs a Claude Code login on that machine. The model chip overrides the model for
that session; leave it empty for Claude Code's own default. A model alias
such as `fable` in your Claude Code settings may not work under the adapter,
so set a full model id in the chip if a session fails on its model.

### Approvals

An agent does nothing to a repository without permission. The daemon's
rules let tool calls that only look pass: reads, searches, and thinking.
Everything else, an edit, a shell command, a fetch, waits for a person. A
waiting call lights the citadel's beacon, drops a yellow beam from the
airship, counts in the Inbox badge and the tab title, and shows in the
conversation as a card with the tool, the exact input the agent proposed,
and the answers the agent accepts: Allow, Always allow, Deny. "Trust this
session", in a conversation's header, lets that session's calls through
without asking from then on. A request nobody answers in 30 minutes
expires, and the agent is told it was cancelled; ending the session
cancels whatever it was asking.

On the host itself, `skylands request list` shows what is waiting, and
`skylands request approve <id>` and `skylands request deny <id>` settle it
over the daemon's socket, which works while the citadel is unreachable.
Every request and every answer is recorded in the session's history, with
who answered: the host's CLI, a browser through the citadel, or trust.
Browser approvals are not yet signed, so anyone who can reach the citadel
can approve; passkeys come with user enrollment.

### An island on another machine

Nothing is authenticated yet, so only do this on a network you trust. The
embedded server listens on localhost unless told otherwise.

On the machine running the world:

```sh
skylands devworld up --leaf-listen 0.0.0.0:7422
```

On the other machine, with `LAPTOP` as the first machine's address:

```sh
skylands daemon up --nats-url nats-leaf://LAPTOP:7422
```

The daemon keeps running while the world is unreachable and reconnects on
its own, as does the citadel if NATS restarts, and the browser if the
citadel restarts. While the browser can't reach the citadel, the world dims
and shows the last state it heard. A daemon restart does not end its
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
remove` do the same for a repository. The most specific matching rule
wins, a deny beats an allow among equals, and a session's own rule beats
one it copied from the repository.

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
you. Against your own nats-server, run `skylands citadel up --nats-url ...`
and `skylands daemon up --nats-url ...` on each host. `world init` and
`daemon enroll` print "unimplemented" until credentials land.

## Development

| Command | What it does |
| --- | --- |
| `make web` | Builds the web client into `web/dist` |
| `make build` | Builds `bin/skylands` with the embedded client |
| `make test` | Runs every Go test, including a whole-world test under `test/e2e` |
| `make lint` | Runs `go vet` and golangci-lint |
| `make install` | Installs `skylands` into your Go bin directory |
| `make snapshot` | Builds every release archive into `dist/` with goreleaser, publishing nothing |

For client work, `cd web && npm run dev` serves the client with live reload
and proxies the WebSocket to a citadel running on port 8080.

### Releasing

Push a version tag, such as `git tag v0.1.0 && git push origin v0.1.0`.
The release workflow builds the archives, uploads them to a draft release,
runs `scripts/release-smoke.sh` on the Linux binary, and publishes the
release only if that passes. The smoke test runs a world on its own ports
and checks that the binary serves the web client, and it runs the same way
on any binary: `scripts/release-smoke.sh dist/skylands_linux_amd64_v1/skylands`.
