---
title: Rules and Approvals
description: Decide which tool calls pass on their own and which wait for a person.
---

<!-- Stub. Source: README before a6a1878, "Approvals" and "An island on another machine". -->

## Defaults

<!--
- an agent does nothing to a repository without permission
- reads, searches, thinking pass; edits, shell commands, fetches wait for a person
-->

## Answering a Request

<!--
- a waiting call: citadel beacon lights, yellow beam under the airship, Inbox badge and tab title count
- card in the conversation: tool, exact input, Allow once / Deny
- the agent's own "always allow" is never given (the agent would stop asking; rules and revocation couldn't reach those calls)
- unanswered after 30 minutes: expires, agent told it was cancelled
- ending the session cancels whatever it was asking
-->

## Trust a Session

<!--
- "Trust this session" in the conversation header
- lets that session's calls through from then on, except what a deny rule refuses
-->

## Keep an Answer as a Rule

<!--
- the approval card can keep the answer as a rule: for the session, or every session on the repository
- proposes a pattern from the request
- rules sheet: on a terrace, and behind a conversation's Rules button
-->

## Rules from the Host

```sh
skylands rule list
skylands rule add skylands allow edit worktree
skylands rule add skylands allow execute "go test *"
skylands rule add skylands deny execute "rm -rf *"
skylands rule remove <repo> <number>
```

<!--
- rules set on the host never lapse (see Revoked approvers)
-->

## How Rules Combine

<!--
- a deny is final: any matching deny refuses, whatever allows it, trusted or not
- otherwise the most specific match wins
- ask beats allow among equals
- a session's own rule beats one copied from the repository
- to allow only some commands: write only the allow rules; everything else asks
-->

## Questions

```sh
skylands request answer 7Fq2 question_0=a.go
skylands request answer 7Fq2 --decline
```

<!--
- e.g. Claude Code asking a clarifying question
- card with its choices and a box for anything else
- shows in the inbox, the beacon, and `request list`, like an approval
-->

## Requests from the Host

```sh
skylands request list
skylands request approve <id>
skylands request deny <id>
```

<!--
- over the daemon's socket; works while the citadel is unreachable
-->

## Revoked Approvers

<!--
- revoking doesn't undo what it already decided
- ends what it approved in advance: a session it trusted asks again
- a rule list it set keeps deny and ask rules; its allow rules ask, until another approver approves the list again from the rules sheet
- same for a session's copy of a repository's rules
-->
