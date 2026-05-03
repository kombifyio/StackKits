# Backup Add-On (v2.0.0)

Encrypted, deduplicated backups for any StackKit. One engine, two surfaces:

| Surface | Who manages it | Where to look |
|---|---|---|
| **Self-Hosted** (Free) | The user, on their own host | Kopia Web UI under `backups.{{domain}}` or `stackkit backup` CLI |
| **SaaS** (Paid) | kombify Backup Controller | kombify-TechStack web dashboard |

A host runs in **one** of these modes — never both. The `agentMode.enabled`
flag in the addon config decides.

## Why Kopia (and not Restic anymore)

The v1 addon used Restic. v2 standardises on Kopia for three reasons:

1. **Built-in Web UI.** End users get a dashboard without us having to ship
   one ourselves.
2. **Repository Server.** Kopia's repo-server supports per-user ACLs, which
   is the natural fan-in point for the SaaS multi-tenant path.
3. **Same backends, faster dedup.** B2, Hetzner Storagebox, S3, SFTP — same
   options the user already had with Restic, just better throughput.

Restic users migrate via `stackkit backup migrate-from-restic` (one-shot,
non-destructive, keeps history intact). See
[ADR-0016-backup-single-engine-kopia](../../docs/ADR/ADR-0016-backup-single-engine-kopia.md).

## Quick start (self-hosted)

```bash
stackkit init base-kit
stackkit addon add backup
stackkit apply
```

Then open `https://backups.<your-domain>` (gated by login-gateway). The
first run prompts for an offsite target and an encryption passphrase.
Both are stored as SOPS+age secrets via `stackkit break-glass`.

For power-user workflows the same actions are available from the CLI:

```bash
stackkit backup init                                       # first-run checklist
stackkit backup run                                        # snapshot now
stackkit backup list                                       # show snapshots
stackkit backup list --json                                # JSON for scripting
stackkit backup restore <snapshot-id> --target /tmp/x      # restore
stackkit backup verify                                     # validate-provider ad-hoc
stackkit backup migrate-from-restic [--dry-run]            # one-shot v1→v2 import
```

See [`docs/CLI.md`](../../docs/CLI.md#stackkit-backup) for the full subcommand reference.

## Quick start (SaaS — Phase 4 scaffold)

Onboarding happens through the kombify-TechStack web UI. The dashboard
issues an enrollment token, which the local agent picks up via:

```bash
stackkit addon add backup
stackkit backup enroll --token <token-from-dashboard> --endpoint https://backup.kombify.io
```

After that, schedules, retention, and offsite targets are managed
centrally. The local Kopia Web UI is disabled in this mode.

> **Status note:** the controller endpoint is not operational yet — `enroll` returns a clear "not yet available" error until the Phase-4 follow-up PRs (Postgres driver, NATS, OIDC, live enrollment) land. The CLI surface is final; only the wire is missing. Track in [`docs/plans/2026-05-01-backup-rollout.md`](../../docs/plans/2026-05-01-backup-rollout.md).

## What runs on the host

| Service | When | Purpose |
|---|---|---|
| `kopia-agent` | always | Scheduled snapshots of Docker volumes |
| `kopia-ui` | self-hosted only | Web UI behind Traefik + login-gateway |
| `kopia-integrity` | always | Weekly provider validation + monthly restore drill |
| `restic-importer` | one-shot, only when `engine: restic-import` | Imports legacy Restic snapshots |

## Database consistency (handled automatically)

The addon detects which database engines run in the surrounding StackKit
and runs the correct pre-snapshot hook automatically. **Users do not
configure this.**

| Engine | Hook | Default for |
|---|---|---|
| SQLite | `sqlite3 .backup` to tmpfs | Vaultwarden, Jellyfin, Home Assistant, Stalwart, Gitea |
| Postgres | `pg_dump --format=custom` to tmpfs | Immich, Dokploy |
| Redis | `BGSAVE` + LASTSAVE poll | Immich cache |
| MariaDB | `mariadb-dump --single-transaction` | (defensive, user-added) |
| MongoDB | `mongodump` | (defensive, user-added) |

Detection rules live in `db-hooks.cue`. Adding a new app to the catalog
that uses one of these engines means appending one entry there — not
asking the user to configure a backup tool.

## 3-2-1 in practice

| Copy | Where | How it's enforced |
|---|---|---|
| Copy 1 | Live Docker volumes | The data itself |
| Copy 2 | Local Kopia repo (`/backup/kopia`) | `targets.local.enabled: true` |
| Copy 3 | Offsite (B2 / Hetzner Storagebox / S3) | `targets.offsite.enabled: true` + Object Lock |

The offsite leg is **immutable** for `immutability.retentionDays` (default
7) — ransomware on the host cannot delete history within that window.

## Files

| File | Role |
|---|---|
| `addon.cue` | User-facing configuration schema + service definitions |
| `db-hooks.cue` | Internal pre-snapshot hooks per DB engine |
| `integrity.cue` | Weekly provider validation + monthly restore drill |
| `restic-importer.cue` | One-shot migration from v1 (Restic) to v2 (Kopia) |

## Compatibility

- StackKits: `base-kit`, `dev-homelab`, `modern-homelab`, `ha-kit`
- Contexts: `local`, `cloud`, `pi`
- Required addons: none
- Conflicts: none

## See also

- [`docs/BACKUP-ARCHITECTURE.md`](../../docs/BACKUP-ARCHITECTURE.md) — full architecture
- [ADR-0016 — Single backup engine: Kopia](../../docs/ADR/ADR-0016-backup-single-engine-kopia.md)
- [`docs/plans/2026-05-01-backup-rollout.md`](../../docs/plans/2026-05-01-backup-rollout.md) — phase rollout
