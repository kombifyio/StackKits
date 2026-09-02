---
name: family-vault
description: Operate a StackKits Photos (Immich) family vault through the Immich REST API after generate/apply. Use for albums, shares, owner bootstrap checks, and job health. Do not invent an Immich MCP; there is none.
---

# Family vault (Immich)

StackKits deploys digest-pinned Immich through the application adapter selected
in the resolved Plan. The Immich Full or Lite workload and its module-local
resource profile are explicit choices; the Core profile is not an application
size selector. This guide applies after that runtime exists.

## Owner setup

For an applied local `standalone-compose` Photos workload, prepare the private
workspace file `.stackkit/setup/owner.json` with `email`, `password`, and
`displayName`, then run:

```bash
stackkit setup photos --credentials-file .stackkit/setup/owner.json \
  --owner-approve --complete-onboarding --json
```

Use `--operation-id ID` to resume an interrupted operation. Protect the file
with `chmod 600` on Unix or a private Windows ACL (remove inherited access and
grant only the current account). The CLI verifies the requested owner through
`/api/users/me` and confirms the server configuration; it records no credential
values in the setup result, MCP arguments, or StackSpec. First use and client
login remain separate user steps. Coolify and Komodo workloads have no local
native setup runner yet and stay on their declared manual path.

Setup verifies the admitted Immich version before login and again after owner
readback. Its temporary session is closed before returning, including when
setup fails; a missing logout confirmation is reported as a failure. Sign in
normally in your browser or mobile client for ongoing use. The setup session is
never a reusable client credential.

## Do

- Use Immich REST (`/api`) with the owner token. Health is `/api/server/ping`.
- Treat albums, users, shared links, and job queues as the product contract. RIL write actions stay gated.
- Keep library data on the Immich persistent volume. Backup classification is on the Architecture v2 workload, not this skill.

## Do not

- Do not add a `product` MCP client for Immich.
- Do not call Coolify APIs to "fix" Immich. Coolify is the adapter; StackKits owns the workload bundle.
- Do not author Immich `configuration` files. The closed bundle is the runtime.
- Do not tunnel Immich product operations through `stackkit` MCP. `stackkit` owns lifecycle and the plan-bound owner setup; use Immich REST for product actions.
