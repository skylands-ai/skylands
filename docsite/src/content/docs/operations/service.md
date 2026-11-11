---
title: Run as a Service
description: Start Skylands at login and restart it when it fails.
---

<!-- Stub. Source: README before a6a1878, "Keep it running". -->

## Install the Devworld

```sh
skylands devworld install
```

<!--
- takes the same flags as `devworld up`
- `up` runs in the terminal you start it from; install keeps it running after the terminal closes
-->

## Install a Daemon

```sh
skylands daemon install --world O... --nats-url nats-leaf://<world-host>:7422
```

<!--
- the whole command is under "Add a host" in the citadel's panel
- for a machine that only hosts agents for a world running elsewhere
-->

## What Install Does

<!--
- Linux: systemd user unit; macOS: launchd agent
- runs this `skylands` binary
- gives it this shell's PATH, so it finds npx, git, gh
- waits for the island to come up, prints where the logs are
- prints the setup link while the world isn't set up yet
- --name sets the service name (defaults skylands-devworld / skylands-daemon)
-->

## Change Flags or Upgrade

<!--
- install again to change flags
- install again after replacing the binary, to restart it
-->

## Uninstall

```sh
skylands devworld uninstall
skylands daemon uninstall
```

<!--
- stops and removes the service; the data directory stays (sessions, transcripts, worktrees)
-->

## Linux: Run Before Login

```sh
loginctl enable-linger
```

<!--
- otherwise a user unit runs only while you're logged in
-->

## Environment

<!--
- a service gets only PATH from your shell; an API key in your environment doesn't reach it
- `skylands preflight` warns about that
-->
