---
title: Open the World on a Phone
description: Reach the citadel from a phone over HTTPS with tailscale serve.
---

To use Skylands from a phone, the citadel needs an HTTPS address the phone can reach. [Tailscale](https://tailscale.com) gives it one on your tailnet, with no public endpoint and no certificate to manage. The phone and the machine running the citadel both need to be on the tailnet.

## Serve the Citadel on Your Tailnet

On the machine running the devworld:

```sh
tailscale serve --bg 8080
```

The first time, Tailscale asks you to turn on HTTPS for the tailnet. The citadel is then at `https://<machine>.<tailnet>.ts.net` until you run `tailscale serve --https=443 off`. The first page load at that address can take about 30 seconds while Tailscale gets its certificate. The citadel itself keeps listening on 127.0.0.1.

On macOS, the `tailscale` command comes with the app, at `/Applications/Tailscale.app/Contents/MacOS/Tailscale`.

## Tell the Citadel Its Address

Start the devworld with the HTTPS address, so the links it prints and shows use it:

```sh
skylands devworld up --url https://<machine>.<tailnet>.ts.net
```

`skylands devworld install` takes the same flag.

## Make the Phone an Approver

A browser keeps its approver key per address. If you set the world up at `127.0.0.1`, add that browser again at the new address: press "Add an approver" and open the link in the same browser.

To add the phone, press "Add an approver" and scan the QR code with the phone. The link opens at the citadel's `--url`, which "Opens at" shows.

## The Phone Inbox

On a phone, the citadel shows only what's waiting on you, newest first: tool calls waiting for approval and questions, each with its agent, repository, and island. It doesn't show the 3D world.

Tap an item to see the exact input and answer with Allow once or Deny, or to fill in a question's form. The phone's approver key signs your answer.

Any window narrower than 700 pixels or shorter than 500 pixels gets this view, including a phone held sideways.

Each item has its own link, in the form `/inbox/<island>/<session>/requests/<request>`. On a phone, it opens the item. On a laptop, it opens the item's conversation.

To get notifications on the phone, turn on Notifications at the bottom of the list. See [Notifications](/operations/notifications/).

## Other Reverse Proxies

Any reverse proxy works if it passes the browser's `Host` header through unchanged and supports WebSocket upgrades, as `tailscale serve` does. If a proxy rewrites `Host`, the citadel refuses the page's requests and says why.
