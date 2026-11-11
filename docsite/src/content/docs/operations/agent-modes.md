---
title: Agent Modes
description: How each agent is kept asking before it acts, and what can skip the host's rules.
---

<!-- Stub. Source: README before a6a1878, "Approvals". -->

## Modes Skylands Allows

<!--
- each agent may be only in modes that keep it asking; set through the agent's mode setting over ACP
- a session whose mode can't be set doesn't start
- agent moves itself to another mode: daemon sets it back; stops a session it can't set back; conversation notes both
-->

## Claude Code

<!--
- Manual mode, or Plan when chosen; whatever permissions.defaultMode says; bypass mode off for the session
- Plan: no changes until you approve its plan; Allow once leaves Plan for Manual
-->

### Allow Rules in Claude Code's Settings

<!--
- permissions.allow in ~/.claude/settings.json, or the repo's .claude/settings.json / .claude/settings.local.json
- Claude Code runs a matching call without asking: reaches neither the host's rules nor a person, deny rules included
- `skylands preflight` names the files that hold allow rules; remove the ones Skylands should decide
-->

### Hooks

<!--
- from the same settings files, managed settings, and the plugins they enable
- PreToolUse / PermissionRequest hooks can approve a call themselves
- every hook runs its command with no approval, as the daemon's user, in the session's directory
- `skylands preflight` names each file and enabled plugin that defines hooks, with its events
-->

## Codex

<!--
- read-only mode, so Codex asks before every edit
- shell commands run inside Codex's sandbox without asking: can read the machine, can't write or reach the network
- daemon adds instructions (after your own in Codex's config): ask whenever a command needs to write or reach the network
- if it still reports a command blocked by the sandbox, tell it to ask for escalated permissions
- a command the sandbox stopped doesn't show in the conversation; Codex doesn't report it
-->

## Gemini CLI

<!--
- runs `npx -y @google/gemini-cli@<pinned> --acp`; nothing to install but Node
- Default mode (asks before every tool call its policies don't allow), or Plan (read-only) when chosen
- whatever general.defaultApprovalMode in Gemini's settings says; autoEdit and yolo never allowed
- Gemini moves itself into Plan (enter_plan_mode, which asks first) and back to Default once a plan is allowed; the daemon follows both
- withheld commands: /extensions (installs and runs code), /restore (rewrites files from a checkpoint), /init (writes GEMINI.md itself)
- approval card: Gemini sends no raw input, so the card shows its title and diff, and that's what an approval signs
- reads pass without a request (Gemini's own default policy), as they do under the host's default rules
- no model choice from Skylands yet: it runs on GEMINI_MODEL or model.name in ~/.gemini/settings.json, else "auto"
- login: GEMINI_API_KEY, or log in with Google once (`npx -y @google/gemini-cli@<pinned>`); an API key in your shell doesn't reach a service
- after a daemon restart, Gemini reloads its own session only when security.auth.selectedType is set in ~/.gemini/settings.json
  (a Google login sets it); with only GEMINI_API_KEY, the session is seeded from the transcript instead
- Gemini's own allow rules skip the host's rules: tools.allowed in settings.json, allow rules in its policy files (~/.gemini/policies)
  (preflight doesn't warn about these yet)
-->

## goose

<!--
- default mode approves every call itself, so: approve mode (asks before every call not always-allowed in goose's own settings), or chat mode (runs no tools)
- goose doesn't say what kind of call each is: reads can't pass on rules alone; every call waits unless a rule or trust lets it through
-->
