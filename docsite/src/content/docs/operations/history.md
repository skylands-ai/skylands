---
title: Signed Decisions and History
description: What approvers sign, how the daemon checks it, and how to audit decisions on a host.
---

## What Is Signed

An approver's browser signs each of these with its approver key:

- allowing or denying a tool call
- answering or declining a question
- trusting a session
- changing rules
- starting a session, and each prompt
- committing, merging, opening a pull request, and removing a worktree

Stopping or interrupting a session and changing its mode or model aren't signed.

## How the Daemon Checks

Before it acts on a decision, the daemon checks its signature against the world's approvers and against its own records. A citadel can pass decisions along, but it can't make one.

Trust and rule changes are signed over the version the browser showed. If that version has changed since, the decision is refused instead of overwriting the newer one, and an old decision can't be sent again.

A session start names the session, which the browser picks. Every other decision names the session's current revision, so none can be sent twice. A merge signed before another prompt, for example, is refused after it.

The session records who sent each decision: the host's CLI, a browser through the citadel, or trust.

## Audit from the Host

```sh
skylands request history
skylands request history <session>
skylands request history --show <id>
```

`request history` lists every tool call that waited on a person on the host, in all sessions or in one, oldest first, with the tool, the answer, when it was given, and who gave it. A decision made through the citadel carries its approver's signature, which is checked again against the world's approvers as they are now.

`--show` prints one decision whole: the answer and its signature, the exact input the request named, and what the tool call produced. The host keeps the input and output under `blobs/` in its data directory, named by their SHA-256, and `--show` checks each against the hash in the session's events. It fails if either doesn't match.

These commands read from the host's daemon, so they work while the citadel is unreachable.
