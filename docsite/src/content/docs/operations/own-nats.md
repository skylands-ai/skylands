---
title: Run on Your Own NATS Server
description: Run the citadel and daemons as separate roles against a nats-server you manage.
---

<!-- Stub. Sources: README before a6a1878, "Running the roles separately"; docs/NATS.md. -->

## What a World Needs from NATS

<!--
- port from docs/NATS.md, "What a world needs"
- one account for the citadel and the daemons; the account needs JetStream
- a JetStream domain for the world (default "world"; daemon flag --world-domain)
- anything that can log in to the world's account is trusted as a daemon
-->

## Example Server Configuration

<!-- port the example from docs/NATS.md, "An example" -->

## Start the Citadel

```sh
skylands citadel up --nats-url nats://<nats-host>:4222 --creds citadel.creds \
  --url https://<citadel-address> --leaf-url nats-leaf://<nats-host>:7422
```

<!--
- --url: address people open the citadel at
- --leaf-url: address daemons dial; shown under "Add a host"
- set the world up from the link it prints
-->

## Start Each Daemon

```sh
skylands daemon up --world O... --nats-url nats-leaf://<nats-host>:7422 --creds daemon.creds
```

<!--
- the command under "Add a host", plus --creds
-->

## What Skylands Creates

<!-- port from docs/NATS.md, "What Skylands creates" -->

## What the Citadel Checks

<!-- port from docs/NATS.md, "What the citadel checks" -->
