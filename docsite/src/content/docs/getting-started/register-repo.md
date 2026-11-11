---
title: Register a Repo
description: Register a repository on a host
---

Before agents can do any work on your daemon host island, a repository needs to be registered
on that specific host. You can do this with the following command:

```sh
skylands repo register /path/to/repository
```

Note that you're using the path to the _local checkout_ of the git repository and _not_ the 
URL as it exists in Github (or Github enterprise or other private Github alternatives). Agents will
work within that repository inside _work trees_.

Once you register a repository, it will appear on the host's island as a [terrace](../../concepts/terraces).