---
title: Add Islands
description: Run more daemons, on this machine or on others, in one world.
---

<!-- Stub. Source: README before a6a1878, "A second island on the same machine" and "An island on another machine". -->

## The World's Owner Key

<!--
- every daemon outside devworld needs it: "Add a host" in the citadel's panel shows it, with the whole command
- the daemon pins the key; refuses to start without one, or in a world with a different owner key
-->

## A Second Island on the Same Machine

```sh
skylands daemon up --world O... --host other \
  --data-dir /tmp/skylands-other --socket /tmp/skylands-other/daemon.sock
```

<!--
- each island needs its own --host, --data-dir, and --socket
- --socket doesn't follow --data-dir: it defaults to ~/.skylands/daemon.sock, which devworld's daemon already holds
  (the pre-a6a1878 README command left it out)
- CLI commands for that island need the same --socket
- island rises within a few seconds
- Ctrl-C: island drifts out on a slack tether ~6s later; start again and it comes back
-->

## An Island on Another Machine

On the machine running the world:

```sh
skylands devworld up --leaf-listen 0.0.0.0:7422
```

On the other machine:

```sh
skylands daemon up --world O... --nats-url nats-leaf://<world-host>:7422
```

<!--
- MUST be a caution aside before this ships: devworld's NATS server asks for no credentials,
  so any daemon that can reach the leaf listener joins the world. Trusted networks only.
- the embedded server listens on localhost unless told otherwise
- credentials: run your own nats-server (Run on Your Own NATS Server)
-->

## When the Connection Drops

<!--
- daemon keeps running while the world is unreachable, reconnects on its own
- citadel reconnects if NATS restarts; browser reconnects if the citadel restarts
- while the browser can't reach the citadel, the world dims and shows the last state it saw
-->
