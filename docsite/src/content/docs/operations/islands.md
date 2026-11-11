---
title: Add Islands
description: Run more daemons, on this machine or on others, in one world.
---

Each island is a daemon. The devworld runs one for you, and you can add more on the same machine or on other machines. They all join the same world.

## The World's Owner Key

Every daemon outside the devworld needs the world's owner key, given with `--world`. "Add a host" in the citadel's panel shows the key, along with the whole command to start a daemon.

The daemon pins the key the first time it starts. After that, it won't start without a key, and it won't join a world whose owner key is different.

## A Second Island on the Same Machine

Each island needs its own `--host`, `--data-dir`, and `--socket`:

```sh
skylands daemon up --world O... --host other \
  --data-dir ~/.skylands-other --socket ~/.skylands-other/daemon.sock
```

`--socket` doesn't follow `--data-dir`. Without it, the daemon tries `~/.skylands/daemon.sock`, which the devworld's daemon already holds.

CLI commands for that island need the same `--socket`:

```sh
skylands daemon status --socket ~/.skylands-other/daemon.sock
```

The island shows up in the world within a few seconds. About six seconds after you stop the daemon, its island drifts out on a slack tether. It comes back when you start the daemon again.

## An Island on Another Machine

:::caution
The devworld's NATS server doesn't ask for credentials. Any daemon that can reach its leaf listener joins the world, so only open the listener on a network you trust. To admit daemons by their credentials, [run your own NATS server](/operations/own-nats/).
:::

The devworld's NATS server listens on localhost unless you tell it otherwise. On the machine running the world, have it listen on an address the other machine can reach:

```sh
skylands devworld up --leaf-listen 0.0.0.0:7422
```

On the other machine, run the command from "Add a host", with `<world-host>` as the first machine's address:

```sh
skylands daemon up --world O... --nats-url nats-leaf://<world-host>:7422
```

## When the Connection Drops

A daemon keeps running while it can't reach the world, and reconnects on its own. Its events wait on the host until the connection is back.

The citadel reconnects when NATS restarts, and the browser reconnects when the citadel restarts. While the browser can't reach the citadel, the world dims and shows the last state it saw.
