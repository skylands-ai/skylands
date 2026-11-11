---
title: Open the World on a Phone
description: Reach the citadel from a phone over HTTPS with tailscale serve.
---

<!-- Stub. Source: README before a6a1878, "Open the world on your phone". -->

## Serve the Citadel on Your Tailnet

```sh
tailscale serve --bg 8080
```

<!--
- why: a phone can't reach 127.0.0.1; installing the web app and notifications need HTTPS
- phone and laptop both on the tailnet; no public endpoint, no certificate to manage
- Tailscale asks to turn on HTTPS for the tailnet the first time
- serves https://<machine>.<tailnet>.ts.net until `tailscale serve --https=443 off`
- citadel keeps listening on 127.0.0.1, where Tailscale reaches it
- first page load can take ~30s while Tailscale gets its certificate
- macOS: the command ships with the app at /Applications/Tailscale.app/Contents/MacOS/Tailscale
-->

## Tell the Citadel Its Address

```sh
skylands devworld up --url https://<machine>.<tailnet>.ts.net
```

<!--
- --url is the address the links it prints (setup, invites) name
- devworld install takes the same flag
-->

## Make the Phone an Approver

<!--
- a browser keeps its key per address: a world set up at 127.0.0.1 needs that browser added again at the new address
  ("Add an approver", open the link in the same browser)
- phone: "Add an approver", scan the QR code; the link opens at --url ("Opens at" shows it)
-->

## The Phone Inbox

<!--
- phone shows only what's waiting on you, newest first: approvals and questions, with agent, repo, island
- tap one: exact input, Allow once or Deny, or the question's form; the phone's key signs the answer
- no 3D world on the phone
- any window narrower than 700px, or shorter than 500px, gets this view
- each item has its own link, /inbox/<island>/<session>/requests/<request>: opens the item on a phone, the conversation on a laptop
-->

## Other Reverse Proxies

<!--
- must pass the browser's Host header through (as tailscale serve does) and WebSocket upgrades
- through a proxy that rewrites Host, the citadel refuses what the page asks, and says why
-->
