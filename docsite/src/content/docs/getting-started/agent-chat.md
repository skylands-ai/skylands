---
title: Chat with an Agent
description: Chat with an Agent
---

You can create a chat session with an agent in a terrace (island repo) in one of two ways. You can either
select the terrace in the 3D world visualization and enter your initial prompt in the available text box,
or you can go directly to your inbox and click the _New_ button to create a new session.

When you create a new session, you'll need to choose the model provider (e.g. **Claude**, **Codex**, **Gemini**, etc) and
select the model you want to use for the session. You can change models in the middle of a session, but not providers (e.g. a Claude session will always be a Claude session).

At the moment, the `skylands` CLI doesn't support creating new sessions or sending new text to an existing
session. This limitation will likely be removed in the near future.

Once you've started a new session, you'll see the agent's [airship](../../concepts/airships) hovering above the terrace. When the agent begins work on any file, you'll see the airship descend to that file's column.
