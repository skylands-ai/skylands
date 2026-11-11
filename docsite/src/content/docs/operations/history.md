---
title: Signed Decisions and History
description: What approvers sign, how the daemon checks it, and how to audit decisions on a host.
---

<!-- Stub. Source: README before a6a1878, "Approvals". -->

## What Is Signed

<!--
- with the browser's approver key: allowing or denying a tool call, answering or declining a question, trusting a session, changing rules
- also what sets an agent working or changes the repository: starting a session, each prompt, commit, merge, opening a PR
- not signed: stopping or interrupting a session, changing mode or model (none can make an agent act)
-->

## How the Daemon Checks

<!--
- checks the signature against the world's approvers and its own record before it acts
- a citadel can pass decisions along but can't make one
- trust and rule changes are signed over the version the browser showed: a newer one is refused, not overwritten; an old one can't be replayed
- a start names the session (the browser picks it); the rest name the session's revision, so none can be sent twice
- a merge signed before another prompt is refused after it
- the session records who sent each
-->

## Audit from the Host

```sh
skylands request history
skylands request history <session>
skylands request history --show <id>
```

<!--
- every tool-call decision on the host: tool, answer, when, who
- signatures re-checked against the world's approvers as they stand now
- --show: one decision whole: answer, signature, exact input, the tool call's output
- input and output kept whole under blobs/ in the data directory, by SHA-256; checked against the hashes in the session's events; fails on mismatch
- works while the citadel is unreachable
-->
