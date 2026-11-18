---
title: Register a Repo
description: Register a repository on a host, from the browser or the host's CLI
---

Before agents can do any work on your daemon host island, a repository needs to be registered
on that specific host. Once you register a repository, it appears on the host's island as a
[terrace](../../concepts/terraces). Agents work within that repository inside _work trees_.

You can register a repository from the browser, by cloning it onto the host or by naming a
checkout already there, or with the `skylands` CLI on the host itself.

## From the Browser

Select the island in the world. Its panel ends with **Add a repository**, a field that takes a
clone URL or a path. Only an approver can add a repository, and your browser signs the addition
with its approver key. The terrace's panel then shows who added it.

### Clone a Repository

1. Paste the repository's clone URL, such as `https://github.com/org/storefront.git` or
   `git@github.com:org/storefront.git`.
2. **Clone into** fills in with the directory `git clone` would use. Change it if you want the
   clone somewhere else in the clone root, such as `org/storefront`.
3. Click **Clone**.

The island's panel lists the clone while it runs, and the terrace appears once it's done. If the
clone fails, the island's panel shows what git said. Clone again into the same directory to retry.

The daemon clones as the host's user, with that user's git credentials, so the host must already
be able to clone the URL without being asked anything. Git can't prompt for a password, a
passphrase, or whether to trust a new ssh host key. Before cloning a private repository, check
that `git clone <url>` works in a terminal on the host without a prompt: set up a credential
helper, such as with `gh auth setup-git`, load your ssh key into the ssh agent, and accept the
git server's host key once.

Clone URLs are `https://`, `ssh://`, or `user@host:path`. The daemon refuses other kinds, such as
`http://`, `file://`, and local paths, and an `https://` URL with a user name or password in it.

### Register a Checkout

To register a repository that's already on the host, type the path of its checkout and click
**Add**. A path is relative to the clone root, or absolute inside it.

### The Clone Root

Everything added from the browser must be inside the daemon's **clone root**: each clone, each
checkout, and the whole repository a checkout belongs to. Symbolic links are followed before
the check. This bounds which directories a remote approver can point agents at. The island's
panel shows the clone root, and so does `skylands daemon status` on the host.

The clone root is the directory you start `skylands devworld up` or `skylands daemon up` in. To
use another directory, pass `--clone-root`, or set `SKYLANDS_CLONE_ROOT`:

```sh
skylands devworld up --clone-root ~/code
```

A service doesn't start in the directory you install it from, so
[`install`](../../operations/service) gives it the clone root of the directory you run `install`
in, or the one `--clone-root` or `SKYLANDS_CLONE_ROOT` names.

## From the CLI

The CLI is the lower-level way to register a repository. Run it on the host, with the path of a
checkout already there:

```sh
skylands repo register /path/to/repository
```

It takes any repository the host's user can read, inside the clone root or not, and needs no
approver, since only someone with a shell on the host can run it. It doesn't clone: for a
repository that isn't on the host yet, run `git clone` first, then register the checkout.
