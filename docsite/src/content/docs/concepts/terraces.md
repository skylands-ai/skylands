---
title: Terraces
description: Terraces
---

The **terrace** is a visual representation of a _registered_ git repository. With an island running, you need to use
the `skylands repo ...` set of commands to register and interact with the repository. Agents can't start working
without a repository in which to run. 

Terraces appear in the 3D world view as squares with vertical columns, where the height of the column corresponds to the given file's relative size. In the default (sky) theme, terraces are surrounded by stanchions and thick "theater" rope, which will pulse with color when selected. In the space theme, terraces are surrounded by lit runway-style beacons.

The repository boundaries are enforced by the daemon process but can be expanded based on how much you trust a session
or approve the commands within it. By default, sessions can't escape the terrace boundary.