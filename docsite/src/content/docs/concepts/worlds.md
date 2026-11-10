---
title: Worlds
description: Worlds
---

The **world** is the outermost boundary in Skylands. A world contains a single citadel, multiple islands, and the terraces and agents that run on those islands. 

When you start a `devworld`, you get a single world. The data storage for that world is in a local JetStream store controlled by the internal NATS server started in-process.

If you want the boundary of a world to extend beyond a single workstation, then you will need to set up some NATS infrastructure. In the case of a central NATS cluster (or server), anything that can communicate within the world's _account space_ on that NATS
server will be considered within the world boundaries.

For more information on NATS clusters and setting up global access, check the operations guides. Keep in mind that NATS security
measures are taken _in addition to_ the approver and world owner security that comes with Skylands.