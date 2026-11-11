---
title: Create world
description: Create the world
---

Creating the developer world is done automatically when Skylands launches in `devworld` mode the first time. All
worlds need at least one _[approver](/concepts/approvers)_. The first time you open [http://localhost:8080](http://localhost:8080), you will
see a page saying the world isn't set up yet. The first time the devworld starts, it prints a setup link, such as `http://127.0.0.1:8080/setup#...`, and `skylands daemon status` shows it again. Open that link to see instructions on saving the world's _recovery file_ and how to add your current browser as an approver. The way Skylands is currently designed prevents non-approving browsers from seeing any world data.