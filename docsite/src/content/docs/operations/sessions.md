---
title: Sessions and Worktrees
description: Where a session works, how its changes reach your branch, and how to clean up after it.
---

## Where a Session Works

Each session works in its own git worktree, on a branch named `skylands/<session>` cut from whatever the checkout was on. Worktrees live under `<data-dir>/worktrees/<host>/<session>`.

A session can work in the checkout itself instead, when you choose that under Where in the command bar or the inbox's New form. Only one session at a time can work in a repository's checkout.

## Commit, Merge, or Open a Pull Request

When the agent isn't in the middle of a turn, the conversation shows where its changes stand and offers to:

- commit them
- merge the session's branch into the checkout, which is aborted if there's a conflict
- push the branch and open a pull request with `gh`

## Open a Terminal

To build, test, or look around where the agent works without asking it to, switch the conversation from Chat to Terminal. The first switch opens a shell on the session's island, in its worktree (or the checkout, for a session that works there), as your login shell on that host. Switch back and forth as you like: the terminal keeps running, and shows what it printed meanwhile when you come back.

A conversation can have more than one terminal. Use New terminal for another, and the numbered tabs to move between them.

A terminal stays open until you press Close terminal, exit its shell, or end the session. Ending a session closes its terminals, and so does removing a worktree, or restarting the daemon, which ends every shell it ran.

Only an approver's browser can open a terminal or type in one, once the world is set up. The browser signs the opening and every keystroke, and the daemon refuses anything else, so the citadel relays what you type but can't type for you. What a terminal prints isn't stored: the daemon keeps the newest 256 KB while it is open, for a browser that opens it, and nothing after it closes.

## Interrupt and End

To change course while the agent is working, press Interrupt in the conversation, or run:

```sh
skylands session cancel <id>
```

The agent stops and the session stays open, and your next reply steers it. Anything the turn was waiting on a person for is withdrawn.

When you're done with a conversation, press End session, or run:

```sh
skylands session stop <id>
```

## Clean Up

Ending a session leaves its worktree and branch in place.

From the citadel, use "Remove worktree" on an ended session's changes card, or "Remove them" under Worktrees in an island's or terrace's panel. Both need an approver's browser.

From the host:

```sh
skylands session list
skylands session remove <id>
skylands session prune
```

`session remove` deletes one ended session's worktree and branch. `session prune` does the same for every ended session and lists what it kept. Stop an open session before you remove it.

Removal keeps a worktree with uncommitted changes and a branch with commits its base doesn't have. `session remove --force` removes them anyway. The citadel can't force a removal.

## Change Mode or Model

```sh
skylands session mode <id> <mode>
skylands session model <id> <model>
```

Both work only on an idle session. The mode has to be one the agent's adapter lists, and those are the modes that keep the agent asking before it acts. A model the agent won't take is refused, along with a list of the models it offers. A session can't switch to another agent.

## When the Daemon Restarts

Open sessions don't end when the daemon restarts. When the daemon comes back up, it brings back each open session's agent, through the agent's own saved session when the agent can load one, or through a new session given the transcript. Claude Code and Codex can load their own sessions, and so can Gemini CLI with the setting in [Agent Modes](/operations/agent-modes/#logging-in).

A turn the restart cut short is picked up again. The conversation notes where the restart happened.

## Agent Processes

Each agent runs in its own process group. When a session ends or the daemon stops, the agent is asked to exit, and is stopped if it doesn't. Anything it started that's still running, such as a dev server or a test run, is stopped with it.

Ctrl-C or closing the terminal reaches only the daemon, which stops its agents this way. Open sessions resume when the daemon starts again.

If the daemon is killed or crashes, its watchdog, a small `skylands` process, ends whatever its agents left running.
