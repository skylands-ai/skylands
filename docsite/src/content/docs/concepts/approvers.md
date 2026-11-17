---
title: Approvers
description: Approvers
---

An **approver** is a _device_ that signs what you decide: a browser, or the CLI on a host. A daemon acts on a decision only when an approver signed it, whether it comes from the citadel or from the host's own CLI. When a running agent needs permission from a person, the request waits until an approver answers it. You can answer from the citadel, from the agent's session, or on the host.

Approvers are added with a short-lived invite link, the way streaming apps add a TV. An approver makes the link in Settings, under Approvers, with "Add an approver". Open it in the browser you want to add, or give it to the CLI on a host.

In the current version of Skylands, devices that aren't approvers can't see or interact with _any_ data in a world.

## Who Can Revoke an Approver

- The browser that set up the world, and any browser added with the recovery file, can revoke any approver.
- An approver added with an invite link can revoke itself and its descendants. It can't revoke the approver that invited it, or any other approver.

Revoking an approver also revokes its descendants. A revocation can't be undone: to bring a device back, add it again.

The same rules decide who can rename an approver.

## The CLI on a Host

`skylands request approve`, `deny`, and `answer`, and `skylands rule add` and `remove`, sign what you decide on the host the way a browser does in the world. Make the host's CLI an approver first, with an invite link:

```sh
skylands approver add 'https://skylands.example.com/setup#approver=...'
```

The invite box in Settings shows this command with the link already in it. The CLI asks for a passphrase, which encrypts its key, and asks for the passphrase again each time it signs. Agents on the host run as your user, so they can read the key's file, but they can't sign with it.

The CLI appears in the world's list of approvers with a CLI tag. Revoke it there like a browser. `skylands approver list` shows the worlds this host's CLI approves in.
