---
title: Chat with an Agent
description: Chat with an Agent
---

You can create a chat session with an agent in a terrace (island repo) in one of two ways. You can either
select the terrace in the 3D world visualization and enter your initial prompt in the available text box,
or you can go directly to your inbox and click the _New_ button to create a new session.

When you create a new session, you'll need to choose the model provider (e.g. **Claude**, **Codex**, **Gemini**, etc) and
select the model you want to use for the session. You can change models in the middle of a session, but not providers (e.g. a Claude session will always be a Claude session).

The inbox's _New_ form asks for:

- the repository, from every repository registered on any island
- the agent, from the agents that repository's island can run
- the mode and the model, for an agent that offers a choice
- where the agent works: a worktree of its own, or the checkout itself
- the first prompt, with any files it mentions and images you attach

Press _Start_, or ⌘ Enter (Ctrl Enter on Linux and Windows), and the new conversation opens in the inbox. Only a browser that's an approver for the world can start a session. The form keeps your choices until you reload the page.

## Mention Files and Attach Images

In the command bar, the _New_ form, and the reply box under a conversation, you can point the agent at files and show it images.

**Mention a file.** Type `@` and a list of the repository's files appears, filtered as you type. Pick one with the arrow keys and Enter or Tab, or click it, and `@` and its path go in your prompt. For a new session the list comes from the registered checkout, and for an open session from the session's worktree. It has the tracked files, and untracked ones git doesn't ignore. A mention goes with the prompt only while its `@path` is still in the text, so deleting the text drops it.

The host decides how the agent gets each file, with the session's rules, as if the agent were reading it:

- If a rule denies reading the file, the prompt is refused, and the error names the rule.
- If reading it is allowed, and the agent takes a file's contents in a prompt, a text file up to 100 KB goes whole. The agent sees the file as it was when you sent the prompt.
- Otherwise the agent gets a link to the file and reads it itself, which asks like any other read.

A file outside the session's directory, including through a symbolic link, can't be mentioned. A prompt can mention up to 20 files.

**Attach an image.** Paste an image into the box, drop one on it, or press _Image_ and pick one. Each shows as a thumbnail you can remove before you send. PNG, JPEG, GIF, and WebP images up to 10 MB each can be attached, up to 5 per prompt. The image goes to the host that runs the agent as soon as you attach it, and the host keeps it once you send the prompt. An image you don't send is removed after an hour.

_Image_ is off for an agent that doesn't take images, and says why. Claude Code, Codex, and Gemini CLI take them.

Once a world has approvers, your signature on the prompt covers the files it mentions and the images it carries, so nothing can be added or swapped on the way to the host.

In the conversation, each prompt shows the files it mentioned and thumbnails of its images. A mention with a dashed outline went as a link. The thumbnails are fetched from the host while you look at them.

## From the Command Line

At the moment, the `skylands` CLI doesn't support creating new sessions or sending new text to an existing
session. This limitation will likely be removed in the near future.

Once you've started a new session, you'll see the agent's [airship](../../concepts/airships) hovering above the terrace. When the agent begins work on any file, you'll see the airship descend to that file's column.
