---
title: Chat with an Agent
description: Chat with an Agent
---

You create a chat session with an agent from a terrace (island repo). Select the terrace in the 3D world
visualization and enter your initial prompt in the command bar at the bottom of the page. Once it's started,
the session shows in your inbox too.

When you create a new session, you'll need to choose the model provider (e.g. **Claude**, **Codex**, **Gemini**, etc) and
select the model you want to use for the session. You can change models in the middle of a session, but not providers (e.g. a Claude session will always be a Claude session).

At the moment, the `skylands` CLI doesn't support creating new sessions or sending new text to an existing
session. This limitation will likely be removed in the near future.

Once you've started a new session, you'll see the agent's [airship](../../concepts/airships) hovering above the terrace. When the agent begins work on any file, you'll see the airship descend to that file's column.
