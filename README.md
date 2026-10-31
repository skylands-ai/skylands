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

## Quickstart

You need Go 1.26 and Node 22 or newer. Node builds the web client, which the
Go build embeds.

```sh
make web && make build
./bin/skylands devworld up
```

Open <http://127.0.0.1:8080>. `devworld up` runs an embedded NATS server, a
citadel, and a daemon for this machine in one process, so you get the castle
and one island.

### A second island on the same machine

Every island needs its own host name and data directory.

```sh
./bin/skylands daemon up --host other --data-dir /tmp/skylands-other
```

Its island rises within a few seconds. Stop it with Ctrl-C and the island
drifts out on a slack tether about six seconds later. Start it again and it
comes back.

### Register a repository

On the machine whose island should carry it:

```sh
./bin/skylands repo register ~/code/storefront
./bin/skylands daemon status
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
The terrace panel shows the agent, what it is reading or editing, and when it
finishes. Click the agent to read its response and the diff in its
worktree. Both stay on the host, under `transcripts/` and `worktrees/` in
the data directory, and the citadel fetches them only while you look.

Two agents are available. The built-in fake agent reads a few files and
edits one, so you can see the loop without any API keys. Claude Code runs
through its ACP adapter from npm, which `npx` fetches on first use, and needs
a Claude Code login on that machine. The model chip overrides the model for
that session; leave it empty for Claude Code's own default. A model alias
such as `fable` in your Claude Code settings may not work under the adapter,
so set a full model id in the chip if a session fails on its model. Until
approvals land, the daemon allows every tool call an agent asks for, so run
real agents only on repositories you would trust them with unattended.

### An island on another machine

Nothing is authenticated yet, so only do this on a network you trust. The
embedded server listens on localhost unless told otherwise.

On the machine running the world:

```sh
./bin/skylands devworld up --leaf-listen 0.0.0.0:7422
```

On the other machine, with `LAPTOP` as the first machine's address:

```sh
./bin/skylands daemon up --nats-url nats-leaf://LAPTOP:7422
```

The daemon keeps running while the world is unreachable and reconnects on
its own, as does the citadel if NATS restarts, and the browser if the
citadel restarts. While the browser can't reach the citadel, the world dims
and shows the last state it heard.

### Running the roles separately

`devworld up` is the same code as the separate roles, with NATS started for
you. Against your own nats-server, run `skylands citadel up --nats-url ...`
and `skylands daemon up --nats-url ...` on each host. The remaining
commands, such as `world init` and the request commands, print
"unimplemented" until their milestones land.

## Development

| Command | What it does |
| --- | --- |
| `make web` | Builds the web client into `web/dist` |
| `make build` | Builds `bin/skylands` with the embedded client |
| `make test` | Runs every Go test, including a whole-world test under `test/e2e` |
| `make lint` | Runs `go vet` and golangci-lint |
| `make install` | Installs `skylands` into your Go bin directory |

For client work, `cd web && npm run dev` serves the client with live reload
and proxies the WebSocket to a citadel running on port 8080.
