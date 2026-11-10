---
title: Start the Devworld
description: Start the devworld
---

Skylands is one binary, `skylands`, for Linux and macOS on amd64 and arm64.
The same binary runs the devworld, the citadel web app, and daemons.

Starting a development world is basically a shortcut for starting a NATS server, starting a **citadel** server,
and starting a daemon process all within a single package. It makes developing locally with a single 
machine very easy.

```sh
skylands devworld up
```

This will then start the development world and host the citadel on [http://localhost:8080](http://localhost:8080).

To see the different options available to you, you can always add `--help` to whatever command you're interested in.