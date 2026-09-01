---
name: family-vault
description: Operate a StackKits Photos (Immich) family vault through the Immich REST API after generate/apply. Use for albums, shares, owner bootstrap checks, and job health. Do not invent an Immich MCP; there is none.
---

# Family vault (Immich)

StackKits deploys digest-pinned Immich through the selected application adapter (Coolify on `standard`/`high`, standalone lite on `low`). This skill is product-user surface after that runtime exists.

## Do

- Use Immich REST (`/api`) with the owner token. Health is `/api/server/ping`.
- Treat albums, users, shared links, and job queues as the product contract. RIL write actions stay gated.
- Keep library data on the Immich persistent volume. Backup classification is on the Architecture v2 workload, not this skill.

## Do not

- Do not add a `product` MCP client for Immich.
- Do not call Coolify APIs to "fix" Immich. Coolify is the adapter; StackKits owns the workload bundle.
- Do not author Immich `configuration` files. The closed bundle is the runtime.
- Do not tunnel Immich through `stackkit` MCP. `stackkit` is lifecycle only (`init`, `generate`, `apply`, `status`).
