---
title: Rules and Approvals
description: Decide which tool calls pass on their own and which wait for a person.
---

An agent doesn't change a repository without permission. Each tool call it wants to make is checked against the rules on its host, and a call no rule allows waits for a person.

## Defaults

Reads, searches, and thinking pass without asking. Everything else waits for a person, including edits, shell commands, and fetches.

## Answering a Request

When a call is waiting, the citadel's beacon lights, a yellow beam drops from the agent's airship, and the count shows in the Inbox badge and the tab title. The conversation shows a card with the tool, the exact input the agent proposed, and two answers: Allow once and Deny.

Skylands doesn't offer an agent's own "always allow" answer. To let calls through without asking, use a rule or trust the session.

A request nobody answers within 30 minutes expires, and the agent is told it was cancelled. Ending a session cancels anything it's waiting on.

## Trust a Session

"Trust this session", in the bar under the conversation's header when it shows Chat, lets that session's calls through without asking from then on. Deny rules still apply to a trusted session.

## Keep an Answer as a Rule

The approval card can keep your answer as a rule, either for that session or for every session on the repository. The card proposes a pattern from the request, such as `go test *` for a shell command.

The rules sheet lists and edits rules. Open it from a terrace, or with the Rules button in a conversation. A session copies its repository's rules when it starts.

## Rules from the Host

```sh
skylands rule list
skylands rule add <repo> <allow|ask|deny> <kind> [pattern]
skylands rule remove <repo> <number>
```

For example:

```sh
skylands rule add skylands allow edit worktree
skylands rule add skylands allow execute "go test *"
skylands rule add skylands deny execute "rm -rf *"
```

`<number>` is the rule's number in `rule list`.

## How Rules Are Written

A rule has a decision (`allow`, `ask`, or `deny`), a tool call kind, and an optional pattern. The kinds are `read`, `edit`, `delete`, `move`, `search`, `execute`, `think`, `fetch`, `other`, and `*` for any kind.

Patterns are globs where `*` matches anything:

- For `execute`, the pattern matches the command, such as `go test *`.
- For `fetch`, it matches the URL.
- For the other kinds, it matches the path. `worktree` means everything inside the session's worktree.

## How Rules Combine

- A deny is final. A call that matches any deny rule is refused, whatever allows it and whether or not the session is trusted.
- Otherwise, the most specific matching rule wins. A rule that names a kind is more specific than one for `*`, and a rule with a pattern is more specific than one without.
- When two rules are equally specific, a session's own rule beats one it copied from the repository. If they come from the same place, an ask beats an allow.
- A call no rule matches waits for a person.

To allow only certain commands, write allow rules for those commands and nothing else. Every other command asks.

The same rules decide a file you mention with `@` in a prompt, as a `read` of that file. A deny refuses the prompt. An allow, including the host's default for `read`, sends the file's contents with the prompt when the agent takes them. Anything that would ask sends a link, and the agent's own read of it asks. See [Mention Files and Attach Images](/getting-started/agent-chat/#mention-files-and-attach-images).

## Questions

When an agent needs something only you can tell it, such as Claude Code asking a clarifying question, the conversation shows a card with its choices and a box for anything else. Questions show in the inbox, on the beacon, and in `skylands request list`, the same as approvals.

To answer from the host, give one `field=value` for each field that `request list` shows. A field with choices takes one of them, or several separated by commas when it allows more than one.

```sh
skylands request answer 7Fq2 question_0=a.go
skylands request answer 7Fq2 --decline
```

`--decline` tells the agent you won't answer.

## Requests from the Host

```sh
skylands request list
skylands request approve <id>
skylands request deny <id>
```

These talk to the daemon over its local socket, so they work while the citadel is unreachable.

## Revoked Approvers

Revoking an approver doesn't undo decisions it already made. It does end what it approved in advance:

- A session it trusted asks again.
- A rule list it set keeps its deny and ask rules, and its allow rules ask instead, until another approver approves the list again from the rules sheet. The same goes for a session's copy of a repository's rules.

Rules added with `skylands rule` on the host aren't affected.
