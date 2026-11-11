---
title: Agent Modes
description: How each agent is kept asking before it acts, and what can skip the host's rules.
---

Skylands runs each agent only in modes where the agent asks before it acts. The daemon sets the mode through the agent's mode setting over ACP, and a session whose mode can't be set doesn't start. If an agent switches itself to another mode, the daemon switches it back, or stops the session if it can't. The conversation notes either one.

## Claude Code

Sessions run in Manual mode, or in Plan mode when you choose it, whatever `permissions.defaultMode` says in Claude Code's settings. Bypass mode is off for the session.

In Plan mode, Claude Code makes no changes until you approve its plan. Allow once on the plan's card moves the session to Manual.

### Allow Rules in Claude Code's Settings

Claude Code runs a call that matches its own allow rules without asking. That call never reaches the host's rules or a person, so the host's deny rules don't apply to it either. Claude Code reads allow rules from `permissions.allow` in:

- `~/.claude/settings.json`
- the repository's `.claude/settings.json`
- the repository's `.claude/settings.local.json`

`skylands preflight` lists the files that have allow rules. Remove the rules for calls you want Skylands to decide.

### Hooks

Claude Code runs hooks from the same settings files, from its managed settings, and from the plugins those enable. A `PreToolUse` or `PermissionRequest` hook can approve a tool call itself, skipping the host's rules. Every hook runs its command without approval, as the daemon's user, in the session's directory.

`skylands preflight` lists each file and enabled plugin that defines hooks, with their events. Remove or turn off the hooks you don't want Skylands sessions to run.

## Codex

Sessions run in Codex's read-only mode, so Codex asks before every edit. Shell commands run inside Codex's sandbox without asking. In the sandbox, a command can read the machine but can't write to it or reach the network.

The daemon adds instructions to each session, after any of your own in Codex's config, telling Codex to ask whenever a command needs to write or reach the network. If Codex still reports that the sandbox blocked a command, tell it to ask for escalated permissions. That request waits for the rules or a person like any other.

A command the sandbox stopped doesn't show in the conversation.

## Gemini CLI

The daemon runs Gemini CLI with `npx`, so the only thing it needs installed is Node. Sessions run in Default mode, where Gemini asks before every tool call its own policies don't allow, or in Plan mode, which is read-only, when you choose it. This holds whatever `general.defaultApprovalMode` says in Gemini's settings. The `autoEdit` and `yolo` modes are never allowed.

Gemini can switch itself into Plan mode, asking first, and back to Default once you allow a plan. The daemon follows both.

The `/extensions`, `/restore`, and `/init` commands aren't available in Skylands sessions.

Gemini's approval cards show each call's title and diff instead of its raw input, and that's what an approval signs. Reads pass without a request, as they do under the host's default rules.

Gemini uses the model in `GEMINI_MODEL`, or `model.name` in `~/.gemini/settings.json`, or `auto` when neither is set.

### Logging In

Set `GEMINI_API_KEY`, or log in with Google once by running Gemini CLI yourself. When Gemini isn't logged in, `skylands preflight` prints the exact command. An API key in your shell doesn't reach a [service](/operations/service/#environment).

After a daemon restart, Gemini reloads its own session only when `security.auth.selectedType` is set in `~/.gemini/settings.json`. Logging in with Google sets it. With only `GEMINI_API_KEY`, the session is rebuilt from its transcript instead.

### Gemini's Own Allow Rules

Calls that Gemini's own allow rules match skip the host's rules. Those rules are `tools.allowed` in Gemini's `settings.json` and allow rules in its policy files under `~/.gemini/policies`. `skylands preflight` doesn't check these.

## goose

Sessions run in goose's approve mode, where goose asks before every call its own settings don't always allow, or in chat mode, where goose runs no tools. goose's default mode isn't allowed.

goose doesn't report what kind each call is, so its reads don't pass on the default rules. Every goose call waits for a person unless a rule or "Trust this session" lets it through.
