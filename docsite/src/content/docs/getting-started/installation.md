---
title: Installation
description: Install the skylands binary from a release or from source, and check a machine that will run agents.
---

Skylands is one binary, `skylands`, for Linux and macOS on amd64 and arm64.
The same binary runs every role: a one-machine world with `skylands devworld up`,
a citadel, and the daemon on each machine that hosts agents.

## From a release

Download the archive for your machine from the
[releases page](https://github.com/skylands-ai/skylands/releases). Archives
are named for the version, the system, and the architecture, such as
`skylands_0.1.0_linux_amd64.tar.gz` or `skylands_0.1.0_darwin_arm64.tar.gz`.
Unpack it and put `skylands` on your PATH:

```sh
tar xzf skylands_*_linux_amd64.tar.gz skylands
sudo install skylands /usr/local/bin/
```

:::note[macOS]
A binary downloaded with a browser is quarantined. Clear that before you run it:

```sh
xattr -d com.apple.quarantine skylands
```
:::

A release binary has the web client built in, so a citadel from it serves the
3D world with no other files.

## From source

You need Go 1.26 and Node 22 or newer. Node builds the web client, which the
Go build embeds. In a clone of the
[repository](https://github.com/skylands-ai/skylands):

```sh
make web && make install
```

`make install` puts `skylands` in your Go bin directory, and `make build` puts
it in `bin/` instead.

:::caution
`go install` alone builds a binary without the web client. That's fine for a
machine that only runs a daemon, but a citadel from it serves a page saying
the client is missing.
:::

## Check the install

```sh
skylands version
```

prints the version you installed, such as `skylands v0.1.0`.

## Machines that run agents

Every machine that hosts agents needs:

- **git**, for the worktree each session runs in.
- **Node 22 or newer**, for Claude Code and Codex, whose adapters come from npm.
- **goose**, on the PATH, only to run goose.
- **gh**, only to open pull requests from a conversation.

To check a machine, run:

```sh
skylands preflight
```

It reports git, Node, and npx with the versions and paths it found, and starts
each agent over ACP without asking a model anything. It says whether Claude Code
and Codex are logged in, and which models each offers that login. It also checks
`gh`, and asks the daemon on this machine which version it runs. Each problem
comes with what to do about it. The command exits with status 1 when a check
fails.

An agent that isn't installed, such as goose without `goose` on the PATH, or
Claude Code and Codex without Node, is skipped rather than failed, as long as
another agent is installed.
