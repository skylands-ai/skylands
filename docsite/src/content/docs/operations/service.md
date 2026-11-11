---
title: Run as a Service
description: Start Skylands at login and restart it when it fails.
---

`skylands devworld up` and `skylands daemon up` run in the terminal you start them from. To have Skylands start when you log in, restart when it fails, and keep running after the terminal closes, install it as a service. `install` takes the same flags as `up`.

## Install the Devworld

```sh
skylands devworld install
```

## Install a Daemon

On a machine that only hosts agents for a world running somewhere else, install the daemon. The citadel's panel shows the whole command under "Add a host":

```sh
skylands daemon install --world O... --nats-url nats-leaf://<world-host>:7422
```

## What Install Does

On Linux, install writes a systemd user unit. On macOS, it writes a launchd agent. The service runs the `skylands` binary you ran install with, at the path your shell found it on, and gets your shell's PATH, so it finds `npx`, `git`, and `gh` where you do.

Install waits for the island to come up, then prints the service's definition file, the command it runs, and where its logs are. While the world isn't set up yet, it also prints the setup link.

The service is named `skylands-devworld` or `skylands-daemon`. Use `--name` to give it another name, and pass the same `--name` to `uninstall`.

## Logs and Restarts

|         | macOS                                                    | Linux                                       |
| ------- | -------------------------------------------------------- | ------------------------------------------- |
| Logs    | `tail -f ~/Library/Logs/skylands/skylands-devworld.log`  | `journalctl --user -u skylands-devworld -f` |
| Restart | `launchctl kickstart -k gui/$(id -u)/ai.skylands.devworld` | `systemctl --user restart skylands-devworld` |

For a daemon, use `skylands-daemon` and `ai.skylands.daemon`.

## Change Flags

Run install again with the new flags. It replaces the service and restarts it.

## Upgrades and Uninstalls

The service follows its binary. When the binary is replaced, for example by `brew upgrade`, the service restarts on the new one once no agent is working on a turn. A turn that's waiting for an approval is still a turn in progress, so the restart waits until it's answered and finished.

When the binary has been gone for five minutes, for example after `brew uninstall skylands`, the service removes its own definition and stops, again once no agent is working on a turn. The data directory stays.

A service installed by Skylands 0.0.4 or earlier doesn't follow its binary. Run install once more after upgrading.

## Uninstall

```sh
skylands devworld uninstall
skylands daemon uninstall
```

Uninstall stops the service and removes its definition. The data directory stays, with its sessions, transcripts, and worktrees.

## Linux: Keep It Running After You Log Out

A systemd user unit runs only while you're logged in. To start it at boot and keep it running after you log out:

```sh
loginctl enable-linger
```

Install prints this command when lingering is off.

## Environment

The service gets PATH from your shell and nothing else. An API key set in your shell, such as `ANTHROPIC_API_KEY`, doesn't reach it. Log the agent in instead, for example by running `claude` and using `/login`. `skylands preflight` warns when an agent's login comes from your shell's environment.
