---
title: Start From an Issue
description: Paste an issue's link into the command bar to start work on it, in the repository it belongs to
---

Paste a link to an issue into the command bar, such as `https://github.com/org/storefront/issues/12`,
and press Enter. Skylands finds the repository the issue is in, reads the issue on that repository's
island, and opens the _New_ form with a prompt written from it. Read it, change anything you want, and
press _Start_.

## Where the Work Goes

The work goes to a registered repository with a git remote that points to the issue's repository:
`origin`, `upstream`, or any other. A link places the work ahead of anything the prompt's words match.

- From the world, a repository on any island can take the work. From an island, it must be on that
  island, and on a terrace, it must be that terrace's repository.
- When more than one repository has a matching remote, such as a checkout of your fork with an
  `upstream` remote and a checkout of the original on another island, the _New_ form lists them first
  for you to choose.
- When the issue's repository is somewhere other than where you are, the command bar says where it is,
  with _Go there_ when it's in one place.
- When it's only on an island that can't be reached, the command bar says so.

Skylands reads links in GitHub's form, `https://<host>/<owner>/<repo>/issues/<number>`, on github.com
or a GitHub Enterprise host. It doesn't read other kinds of issue links yet.

The daemon records each repository's remotes when the repository is registered, when the daemon starts,
and every five minutes. A remote you add to a checkout counts within five minutes, or as soon as you
restart the daemon.

## What the Island Needs

The island reads the issue with `gh`, as the host's user. `gh` must be installed on the island and
signed in to the issue's host: `gh auth login`, with `--hostname` for a GitHub Enterprise host. The
island reads only issues in a repository the checkout's remotes point to.

## The Prompt

The _New_ form opens titled _Start on org/storefront#12_, with the repository chosen and the prompt
filled in:

```text
Resolve this issue.

Issue org/storefront#12: Prices round wrong
https://github.com/org/storefront/issues/12

Totals are off by a cent when a cart has more than one currency.
```

Anything you typed along with the link replaces _Resolve this issue._ The issue's comments aren't
included. The form says when the issue is closed.

Anyone who can open an issue can write its text, and the text reaches the agent only as part of the
prompt you sign, so read it before you start. Your signature on the start names the issue. If you
choose another repository in the form, the session starts from the prompt alone, without the issue.

The agent panel links to the issue a session was started from.

## Pull Requests

A pull request opened from the conversation, with its text written from the changes, ends with
`Closes org/storefront#12`, so merging it closes the issue. Nothing else from the issue goes into the
pull request. See [Sessions and Worktrees](../sessions).

## When No Island Has the Repository

When no island has a repository the issue is in, a dialog offers to clone it onto one:

1. Choose the island.
2. Check the clone URL. It's the repository's `https://` URL. Change it to an ssh URL, such as
   `git@github.com:org/storefront.git`, if the island clones over ssh.
3. Check **Clone into**, the directory in the island's clone root.
4. Click **Clone**.

The dialog waits while the island clones the repository. Once its terrace appears, the _New_ form opens
with the issue. If the clone fails, the dialog shows what git said. Only an approver can clone a
repository from the browser. See [Register a Repo](../../getting-started/register-repo) for the clone
root and what the island needs to clone without prompting.
