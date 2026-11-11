---
title: Sessions and Worktrees
description: Where a session works, how its changes reach your branch, and how to clean up after it.
---

<!-- Stub. Source: README before a6a1878, "An island on another machine". -->

## Where a Session Works

<!--
- its own git worktree, on a branch (skylands/<session>) cut from whatever the checkout was on
- or the checkout itself, when the command bar's Where chip says so; one at a time per repository
- worktrees live under <data-dir>/worktrees/<host>/<session>
-->

## Commit, Merge, or Open a Pull Request

<!--
- when the agent isn't mid-turn, the conversation shows where its changes stand
- commit; merge the branch into the checkout (aborted on conflict); or push and open a PR with gh
-->

## Interrupt and End

```sh
skylands session cancel <id>
skylands session stop <id>
```

<!--
- Interrupt in the conversation / `session cancel`: agent stops, session stays open, next reply steers it
- End session / `session stop`: done with the conversation
-->

## Clean Up

```sh
skylands session list
skylands session remove <id>
skylands session prune
```

<!--
- remove: a finished session's worktree and branch
- prune: all finished sessions
- both keep a worktree with uncommitted changes or a branch with commits its base lacks, unless --force (remove only)
- nothing prunes on a schedule yet (#43)
-->

## Change Mode or Model

```sh
skylands session mode <id> <mode>
skylands session model <id> <model>
```

<!--
- idle sessions only from the CLI
- provider can't change mid-session
-->

## When the Daemon Restarts

<!--
- open sessions don't end
- agent comes back through its own session when it can load one (Claude Code, Codex), otherwise a new session given the transcript
- a turn the restart cut short is picked up again
- conversation notes where the restart happened
-->

## Agent Processes

<!--
- each agent runs in its own process group
- session end or daemon stop: agent asked to exit, then stopped; whatever it started goes too (dev servers, test runs)
- Ctrl-C or closing the terminal reaches the daemon alone, which stops its agents this way; open sessions resume on next start
- daemon killed or crashed: its watchdog (a small `skylands` process) ends whatever's left
-->
