---
name: wiki-first
description: Consult the installed personal IT Wiki before planning or changing this server, or when looking up its services and relationships.
---

Read the person's IT facts through the server's StackKits MCP `wiki_read` tool.
Start with `view: pages`, search the relevant service with `view: search`, then
read its page with `view: page` and `pageSlug`. Use `view: graph` for dependencies.
Cite the page and generation returned by the Wiki when it informs a change.

If the optional Wiki is absent or unavailable, report that fact before using
other evidence. The Board carries current discussion; the Wiki carries IT facts.

The server resolves the reader credential from its own custody. Keep credentials
out of tool arguments and generated client configuration. A replica is read-only;
changes belong to its fenced writer. Account replication remains off until the
Gateway manifest-and-files route is available for the person's grant.
