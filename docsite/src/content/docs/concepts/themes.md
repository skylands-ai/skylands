---
title: Themes
description: Themes
---

A theme changes how the 3D world is drawn, and nothing else: the same islands, terraces, airships, and waiting
requests, in another look. Each browser picks its own theme under **World theme** on the citadel's page.

Skylands comes with two built-in themes:

* **Default** - The citadel is a castle surrounded by floating islands in a bright blue sky.
* **Sci-Fi** - The citadel is a space port floating in space, surrounded by space stations. Ships are not airships, but rather space-themed ships.

## Theme packs

A **theme pack** adds another look to a world. A pack is data, not code: a `theme.json`, a `models.glb` of glTF models,
and a `LICENSE`. The world places every model, decides what a click on it means, and plays only the motions the format
offers, so a pack can't see host, repo, or file names, can't change what anything does, and can't reach the network.

Community packs, and the format for making your own, are in the
[skylands-ai/themes](https://github.com/skylands-ai/themes) repository. List them, and add one to your world by its
id, against the world's NATS server:

```sh
skylands theme search
skylands theme add paper-lanterns
```

`skylands devworld up` runs NATS at the command's default address; for a world on your own NATS server, pass `--nats-url`
and `--creds` as you do for `skylands citadel up`. A pack is stored in the world, so every citadel offers it at once.

* `skylands theme search [words]` lists the packs in the themes repository, or those matching every word.
* `skylands theme check <dir>` checks a pack exactly as a citadel will, without adding it.
* `skylands theme add <id, dir, .zip, or https URL>` checks a pack and adds it. Given an id, it downloads the pack the
  themes repository lists and refuses it unless its files have the sha256 listed there. `--sha256` refuses any pack
  unless its contents are the ones you expect, and a pack already in the world under the same id is replaced only with
  `--replace`.
* `skylands theme list` shows the world's packs and their hashes.
* `skylands theme remove <id>` takes a pack out of the world. Browsers drawing in it go back to Default.

If a pack can't be drawn in a browser, the world shows Default and the citadel's panel says why.
