---
title: Installation
description: Install the skylands binary with Homebrew, from a release, or from source, and check a machine that will run agents.
---

Once developers get past the initial adoption stage and start using AI assistants and harnesses for real work (aka "in anger"), their needs change. Managing multiple agents working in multiple repositories from different locations and apps becomes a real pain and slows down the work. Developers end up herding terminal sessions and VPNs more than they orchestrate agents.

You can use _any agent_ you like, or mix and match however you choose. Skylands supports **Claude Code**, **Gemini**, **Codex**, and **Goose**, with more to come.

Skylands solves that problem by giving you a single interface through which you can dispatch agent work to any machine you have access to, from any browser you like. Tell your agents to start working on your dev box from your laptop and approve permission requests from your phone, all with a fun 3D interface that also supports a unified inbox for all your work.

Skylands gives you a _**secure**, **signed**_ log of everything your agents do and every approval or rejection you provide, no matter where the agent's running or what it's doing. Conversation history is available through a central inbox rather than hidden deep within some obscure sub-directory.


## Install with Homebrew

On macOS or Linux with [Homebrew](https://brew.sh), add the Skylands tap, trust it, and install the cask:

```sh
brew tap skylands-ai/tap
brew trust skylands-ai/tap
brew install --cask skylands
```

Homebrew won't load a cask from a third-party tap until you've trusted the tap.

On macOS, the cask clears the quarantine attribute for you.

`brew upgrade` installs new versions, and `brew uninstall --cask skylands` removes it. A service from `skylands daemon install` or `skylands devworld install` follows both on its own; see [Run as a Service](/operations/service/#upgrades-and-uninstalls).

If you've also installed from source, `which -a skylands` shows which copy your shell runs first.

## Install from a Release

Download the archive for your machine from the
[releases page](https://github.com/skylands-ai/skylands/releases). Archives
are named for the version, the system, and the architecture, such as
`skylands___VERSION___linux_amd64.tar.gz` or `skylands___VERSION___darwin_arm64.tar.gz`.
Unpack it and put `skylands` on your PATH:

```sh
tar xzf skylands___VERSION___linux_amd64.tar.gz skylands
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

## Install from Source

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

prints the version you installed, such as `skylands v__VERSION__`.

## Run the Preflight Check

Every machine that hosts agents needs:

- **git**, for the worktree each session runs in.
- **Node 22 or newer**, for Claude Code, Codex, and Gemini CLI, which come from npm.
- The agent binary, if there is one. For example, `goose` needs to be installed, whereas Claude, Codex, and Gemini can all be installed automatically via `npm`
- **gh**, only to open pull requests from a conversation.

To check a machine, run:

```sh
skylands preflight
```

It reports git, Node, and npx with the versions and paths it found, and attempts
to verify which agents are available by using [ACP](https://agentclientprotocol.com/get-started/introduction) to start and interrogate
them.

It also checks `gh`, and asks the daemon on this machine which version it runs.

**_At least one_** agent must respond to this probe for Skylands to work properly.
