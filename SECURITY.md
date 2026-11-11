# Security Policy

## Reporting a Vulnerability

Don't open a public issue for a vulnerability. Report it privately from the repository's [Security tab](https://github.com/skylands-ai/skylands/security/advisories/new). Only the maintainers and you can see the report.

Include:

- the version, from `skylands version`
- which part is affected: the citadel, a daemon, the devworld, the CLI, the web client, or the release and install path
- the steps to reproduce it, or a proof of concept
- what an attacker can do with it

## What to Report

Skylands decides what agents may do and records who decided it. Report anything that breaks that, such as:

- an agent's tool call running without the approval the host's rules require, or a call a deny rule matches not being refused
- a signed decision that can be forged, replayed, or applied to a session or revision it wasn't made for, or a daemon accepting one it should refuse
- a citadel making a decision rather than passing one along
- reading or changing a session, transcript, rule, or repository through the citadel or the daemon's socket without the access that should take
- a release archive, the Homebrew cask, or an install step running code you didn't ask for

## Known Behavior

These are documented and aren't vulnerabilities on their own, though reports of ways to go further than the documentation says are welcome:

- The devworld's embedded NATS server asks for no credentials, so any daemon that can reach its leaf listener joins the world. See [Add Islands](https://docs.skylands.ai/operations/islands/).
- Anything NATS lets into the world's account is trusted as a daemon. See [Run on Your Own NATS Server](https://docs.skylands.ai/operations/own-nats/).
- The world's push key and approvers' push subscriptions are kept in the world's account, so any daemon can read them and send pushes to approvers' browsers. See [Notifications](https://docs.skylands.ai/operations/notifications/).
- An agent's own allow rules and hooks, such as those in Claude Code's or Gemini CLI's settings, let calls skip the host's rules. See [Agent Modes](https://docs.skylands.ai/operations/agent-modes/).
- Codex runs shell commands inside its read-only sandbox without asking.

## What Happens Next

A maintainer confirms the report and works out its scope, and may ask you for more information. Once it's confirmed, the report becomes a draft security advisory, where the maintainers and you discuss the fix and when to disclose it. If you've agreed on a date for public disclosure, keep to it.

The fix ships in a new release, and the advisory is published with it.

## Credit

The published advisory credits whoever reported the vulnerability, unless you ask not to be named. Skylands has no bug bounty.

## Supported Versions

Security fixes go into the latest release only. Upgrade with `brew upgrade`, or download the new release from the [releases page](https://github.com/skylands-ai/skylands/releases).
