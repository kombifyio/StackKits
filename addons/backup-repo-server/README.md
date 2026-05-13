# Backup Repository Server Add-On

> Layer-2 platform add-on. Deploys [Kopia Repository Server](https://kopia.io/docs/repository-server/) as the central fan-in for multi-host backups.

## When to enable this

| Scenario | Enable this addon? |
|---|---|
| Single self-hosted base-kit host | **No.** The local `addons/backup` is sufficient. |
| Two or more hosts you control, want one repo | Yes. Backup-Agents on each host point at this server. |
| kombify-Backup-SaaS multi-tenant control plane | **Required.** The Backup-Controller provisions per-tenant users on this server. |

## What it does (and does not do)

**Does:**
- Stands up Kopia Repository Server with its native multi-user ACL model
- Stores all snapshots in one physical backend (B2 / Hetzner-Storagebox / S3) with cross-user dedup
- Exposes two Traefik routes: a UI route gated by login-gateway, and an agent route that uses Kopia's own per-user credentials
- Ships with a sized server-side cache so warm clients don't re-download metadata on every snapshot

**Does not:**
- Provision tenants / fleets / users — that is the Backup-Controller's job (Phase 4)
- Run schedules — those live on the agents (driven by the controller in SaaS mode, by local cron in self-hosted multi-host mode)
- Hold backup data outside the storage backend — the server is stateless beyond config + cache

## Quick start (self-hosted multi-host, no SaaS)

```yaml
# stack-spec.yaml
addons:
  - traefik
  - backup-repo-server

backup-repo-server:
  storage:
    provider: b2
    b2:
      bucket: my-fleet-backups
      accountId: secret://b2/accountId
      accountKey: secret://b2/accountKey
  repositoryPassword: secret://kopia/repoPassword
```

Then on each host you back up:

```bash
stackkit addon enable backup
stackkit backup enroll --repo https://backup-repo-agent.<domain> --user host-a
```

(The `enroll` subcommand is tracked as a Beads follow-up. See [ADR-0016](../../docs/ADR/ADR-0016-backup-single-engine-kopia.md).)

## SaaS path

Do not configure this addon by hand in SaaS mode. The kombify-Backup-Controller (`internal/backup-controller/`, Phase 4) deploys the addon, manages users, and rotates credentials. You only see the kombify-TechStack web dashboard.

## Networking

| Route | Subdomain (default) | Auth | Used by |
|---|---|---|---|
| UI | `backup-repo.{{domain}}` | login-gateway (TinyAuth + PocketID) | Humans |
| Agent | `backup-repo-agent.{{domain}}` | Kopia per-user (server-internal) | `kopia-agent` / `stackkit-backup-agent` |

The agent route deliberately does **not** sit behind login-gateway — Kopia's own auth handles client identity, and forward-auth would break the protocol.

## Storage backend

Same provider set as `addons/backup` (B2 / Hetzner-Storagebox / S3). The two addons can reuse the same bucket or use different buckets — the per-host addon and the repo server do not need to coordinate, only the controller does.

For SaaS Business tier, set `storage.s3.objectLockEnabled: true`. The controller then sets per-tenant retention windows on top.

## See also

- [`docs/BACKUP-ARCHITECTURE.md`](../../docs/BACKUP-ARCHITECTURE.md)
- [ADR-0016 — Single backup engine: Kopia](../../docs/ADR/ADR-0016-backup-single-engine-kopia.md)
- [ADR-0016](../../docs/ADR/ADR-0016-backup-single-engine-kopia.md) - backup engine decision
- [Backup architecture](../../docs/BACKUP-ARCHITECTURE.md) - current SaaS backup surface
