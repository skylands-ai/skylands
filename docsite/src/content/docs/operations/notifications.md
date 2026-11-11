---
title: Notifications
description: Hear about approvals and questions on a computer or a phone, with the citadel's page open or closed.
---

## Turn Them On

Open the citadel's panel, turn on Notifications, and allow them when the browser asks. On a phone, the switch is at the bottom of the list of what's waiting. Each browser has its own switch.

## What Notifies

The browser shows a notification when a tool call is waiting for approval and when an agent asks a question. An agent that has finished its turn and is waiting for your reply doesn't notify.

A notification names the agent, the repository, and the island, such as "Claude Code in storefront on workstation wants to run a command". It never includes the command or any other input.

Clicking a notification opens the item: on a phone, the item itself, and on a computer, its conversation. The notification closes once the request is answered, in that browser or anywhere else. If the page is closed then, it closes the next time the page opens.

## With the Page Closed

When the citadel's `--url` is HTTPS, notifications come as pushes from the citadel and reach the browser with the page closed. Chrome, Edge, and Firefox get them on computers and on Android, and so does Safari on the Mac. iPhones don't.

A browser gets pushes only at the citadel's `--url`. At another address, such as `127.0.0.1`, it gets notifications only while the page is open in a tab.

While the page is in sight in a browser, a push to that browser waits 30 seconds, and isn't sent if you answer in that time.

Pushes go through the browser vendors' push services, encrypted so the services can't read them. To keep the citadel from calling those services, start it with `--no-push`:

```sh
skylands devworld up --url https://<machine>.<tailnet>.ts.net --no-push
```

`skylands citadel up` takes the same flag.

## Where Browsers Allow It

Browsers show notifications only for a page opened over HTTPS, or at `localhost` or `127.0.0.1`. To give the citadel an HTTPS address, see [Open the World on a Phone](/operations/phone/).
