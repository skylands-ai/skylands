---
title: Run on Your Own NATS Server
description: Run the citadel and daemons as separate roles against a nats-server you manage.
---

`skylands devworld up` runs a NATS server for you. To run NATS yourself, start the citadel and each daemon separately with `skylands citadel up` and `skylands daemon up`. The citadel and the daemons create the streams and buckets they need, so NATS needs only the settings on this page.

## What a World Needs from NATS

- **JetStream, with a domain.** Each daemon runs a leaf node with its own JetStream domain, named after its host, so the world's server needs a domain of its own. Daemons look for the world in the domain `world` unless `--world-domain` names another. Don't give the world's domain the same name as a host.
- **A leaf node listener the daemons can reach.** Each daemon dials it with `--nats-url nats-leaf://<host>:<port>`. Give the citadel the same address with `--leaf-url`, and "Add a host" shows it in the command. On a network you don't trust, give the listener TLS.
- **A client listener for the citadel.** The citadel connects with `--nats-url nats://<host>:<port>`. It's the only NATS client. Browsers talk to the citadel, not to NATS.
- **One account for the citadel and the daemons, with JetStream.** A daemon's leaf node joins the account of the credentials it dials with, and the citadel has to be in that same account.
- **Credentials for each role.** NATS decides who joins the world, and anything that can log in to the world's account is trusted as a daemon. Give each role its credentials file with `--creds`, and give credentials only to hosts you trust.

## Example Server Configuration

A nats-server configuration for a world at `skylands.example.com`:

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

This example has no accounts or credentials. Add them, however you run NATS, before you open the leaf node port to a network you don't trust.

## Start the Citadel

```sh
skylands citadel up --nats-url nats://127.0.0.1:4222 --creds citadel.creds \
  --http 0.0.0.0:8080 --url https://skylands.example.com \
  --leaf-url nats-leaf://skylands.example.com:7422
```

`--url` is the address people open the citadel at, and the links it prints use it. Put an HTTPS reverse proxy at that address that passes the `Host` header through, as [Other Reverse Proxies](/operations/phone/#other-reverse-proxies) describes. `--leaf-url` is the address daemons dial.

Until the world is set up, the citadel prints a setup link, and every page it serves leads to the setup page. Open the link and set the world up.

## Start Each Daemon

On each machine that runs agents, run the command under "Add a host" with that machine's credentials:

```sh
skylands daemon install --world O... \
  --nats-url nats-leaf://skylands.example.com:7422 --creds daemon.creds --host workstation
```

`skylands daemon up` takes the same flags, for a daemon you run in a terminal.

## What Skylands Creates

| What | Where | Written by |
| --- | --- | --- |
| Stream `island-<host>`, subjects `v1.island.<host>.>` | The host's domain | The host's daemon, its only writer |
| Mirror `island-<host>` | The world's domain | The citadel |
| Key-value bucket `skylands-world` | The world's domain | The citadel, once, when the world is set up |
| Stream `skylands-approvers`, subjects `v1.world.approvers.>` | The world's domain | The citadel, once for each approver, link, and revocation |

The `skylands-approvers` stream takes one message per subject and refuses deletes and purges. Daemons read it through their leaf nodes.

Everything else is request and reply on subjects under `v1.`, such as `v1.world.record`.

## What the Citadel Checks

Each time it connects, the citadel checks that JetStream is available to its account and that its server has a JetStream domain. A missing setting is logged as `citadel: NATS setting missing`, and the setup page lists it.

The citadel can't see the server's leaf node settings. A missing or unreachable leaf listener shows up as daemons that never connect. `skylands daemon status` on a host shows where its daemon is dialing.
