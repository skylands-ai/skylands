# NATS settings for a Skylands world

`skylands devworld up` runs a NATS server for you with everything on this
page. Read on when you run nats-server yourself, with `skylands citadel up`
and `skylands daemon up` against it. Skylands keeps no logic in NATS: the
citadel and the daemons create the streams and buckets they need, so NATS
needs only the settings below.

## What a world needs

**JetStream, with a domain.** Every daemon runs a leaf node whose
JetStream domain is its host's name, and keeps its island's stream
there. The citadel mirrors each island's stream into the world's domain.
Without a domain on the world's server, a leaf node's JetStream and the
world's would be one and the same. devworld calls the world's domain
`world`, and daemons look for the world's approvers there unless
`--world-domain` names another. Name it anything that isn't also a host
name.

**A leaf node listener the daemons can reach.** Each daemon dials it with
`--nats-url nats-leaf://HOST:PORT`. Tell the citadel the same address
with `--leaf-url`, and the citadel's "Add a host" section shows it in the
install command. On a network you don't trust, give the listener TLS.

**A client listener for the citadel.** The citadel connects with
`--nats-url nats://HOST:PORT`. It is the only client; browsers talk to
the citadel, not to NATS.

**One account for the citadel and the daemons.** A daemon's leaf node
joins the account of the credentials it dials with, and the citadel
must be in that same account to reach the island's subjects and its
JetStream domain. The account needs JetStream.

**Credentials decide who joins.** NATS is what admits a daemon to the
world; Skylands adds no enrollment of its own, and anything NATS lets
into the world's account is trusted as a daemon. Issue credentials
however you run NATS, give each role its file with `--creds`, and give
them only to hosts you trust. devworld's embedded server asks for no
credentials, so any daemon that can reach its leaf listener joins: keep
that listener on localhost, or on a network you trust.

## An example

A nats-server configuration for a world reached at `skylands.example.com`:

```
server_name: skylands-world
listen: 127.0.0.1:4222

jetstream {
  store_dir: /var/lib/nats
  domain: world
}

leafnodes {
  port: 7422
}
```

Start the citadel beside it:

```sh
skylands citadel up --nats-url nats://127.0.0.1:4222 \
  --http 0.0.0.0:8080 --url https://skylands.example.com \
  --leaf-url nats-leaf://skylands.example.com:7422
```

Until the world is set up, the citadel prints a setup link, and every page
it serves leads to the setup page. Open the link, set the world up, and
copy the command under "Add a host" to each machine that should run
agents:

```sh
skylands daemon install --world O... \
  --nats-url nats-leaf://skylands.example.com:7422 --host workstation
```

## What Skylands creates

| What | Where | Written by |
| --- | --- | --- |
| Stream `island-HOST`, subjects `v1.island.HOST.>` events | The host's domain | The host's daemon, its only writer |
| Mirror `island-HOST` | The world's domain | The citadel |
| Key-value bucket `skylands-world` | The world's domain | The citadel, once, when the world is set up |
| Stream `skylands-approvers`, subjects `v1.world.approvers.>` | The world's domain | The citadel, once per record: approvers, their links, and revocations, each on a subject of its own. The stream takes no second message on a subject and refuses deletes and purges, so nothing written is taken back. Daemons read it through their leaf nodes |

The rest is request and reply on subjects under `v1.`, such as
`v1.world.record`, which daemons ask to learn the world they're in.

## What the citadel checks

Each time it connects, the citadel checks that JetStream is available to
its account and that its server has a JetStream domain. A missing
setting is logged as `citadel: NATS setting missing`, and the setup page
lists it with a link to this page. The citadel can't see a server's leaf
node settings from its client connection; a missing or unreachable leaf
listener shows as daemons that never connect, and `skylands daemon
status` on the host says where it is dialing.
