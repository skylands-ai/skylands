---
title: Desktop Notifications
description: Hear about approvals and questions while the citadel's page is in the background.
---

## Turn Them On

Open the citadel's panel, turn on Notifications, and allow them when the browser asks.

## What Notifies

The browser shows a notification when a tool call is waiting for approval and when an agent asks a question. An agent that has finished its turn and is waiting for your reply doesn't notify.

A notification names the agent, the repository, and the island, such as "Claude Code in storefront on workstation wants to run a command". It never includes the command or any other input.

Clicking a notification opens the conversation. The notification closes once the request is answered, in that browser or anywhere else.

## Where Browsers Allow It

Browsers show notifications only for a page opened over HTTPS, or at `localhost` or `127.0.0.1`. To give the citadel an HTTPS address, see [Open the World on a Phone](/operations/phone/).

Notifications come from the citadel's page, so the page has to be open in a browser tab. A closed page doesn't get them, and neither does a phone.
