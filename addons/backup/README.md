# Self-hosted Backup Add-On (v2.0.0)

The public backup contract standardizes encrypted, deduplicated local backups
on Kopia. Operators use an existing `kopia-agent` container through the
`stackkit backup` CLI and can expose Kopia's Web UI behind their own authenticated
reverse proxy.

## Release boundary

This release contains the self-hosted CUE contract, local CLI, database-hook
metadata, integrity policy, and Restic migration contract. The CLI operates an
already materialized `kopia-agent` deployment. It does not install that
container or prove that a fresh StackKit generation creates it; generator/apply
materialization requires its own release evidence before such a claim is made.

The local surface is:

- `init`, `configure`, `status`, `run`, `list`, `restore`, and `verify`
- `emergency-export` for a Kopia-independent manifest and restore runbook
- `migrate-from-restic` for the one-shot legacy migration

Fleet enrollment, tenant identity, and controller operation are not part of the
public command or CUE surface.

## Why Kopia

The v1 add-on used Restic. v2 standardizes on Kopia because it provides:

1. Client-side encryption, compression, deduplication, and retention policies.
2. A built-in Web UI for local operators.
3. B2, S3-compatible, SFTP, and Hetzner Storage Box targets.

Restic remains only as a one-shot migration input. It is not a second daily
backup engine.

## Local CLI

First verify that the target deployment already runs the expected container:

```bash
docker ps --filter name=kopia-agent
stackkit backup init
```

Then use the local operator commands:

```bash
stackkit backup configure --repo local:/backup/kopia
stackkit backup status
stackkit backup run
stackkit backup list
stackkit backup list --json
stackkit backup restore <snapshot-id> --target /tmp/stackkit-restore
stackkit backup verify
stackkit backup migrate-from-restic --dry-run
```

`configure` supports filesystem repositories mounted into `kopia-agent` using
`local:/path` or `filesystem:/path`. Object-store credentials stay in the
deployment configuration; the local CLI does not accept or persist them.

See [`docs/CLI.md`](../../docs/CLI.md#stackkit-backup) for flags and command
details.

## Portable emergency export

Kopia remains the primary operational engine. `resilience.emergencyExport`
models a complementary recovery path that does not require a healthy Kopia
repository:

```cue
resilience: emergencyExport: {
	mode:           "portable-archive"
	format:         "tar.zst.age"
	includeClasses: ["config", "secrets", "platform-state", "database", "documents", "serverless-config"]
	largeMediaMode: "manifest-only"
}
```

The current CLI command writes the portable manifest and restore runbook:

```bash
stackkit backup emergency-export --target /backup/emergency-export
```

Archive bytes, database dumps, encryption, and checksums still require the
deployment runner that consumes this contract. The CLI deliberately does not
claim those bytes were produced when it has only written metadata.

## Data classes

The contract resolves selected state classes into backup policy metadata.
Caches and generated data are excluded by default.

| Class | Default | Restore mode |
|---|---:|---|
| `config`, `secrets`, `platform-state`, `serverless-config` | included | file restore and pre-change snapshot |
| `database`, `telemetry-timeseries` | included | database-hook restore |
| `user-content`, `documents`, `photos` | included | volume restore |
| `large-media` | opt-in | volume restore; usually NAS or offsite-cost sensitive |
| `cache-generated` | excluded | regenerate |

Emergency export defaults large media to `manifest-only`: it records what
should exist and where it lived without silently copying multi-terabyte media.

## Database consistency

The public hook metadata in `internal/backuphooks/db-hooks.cue` describes the
pre-snapshot operation for each supported database engine:

| Engine | Hook |
|---|---|
| SQLite | `sqlite3 .backup` to temporary storage |
| PostgreSQL | `pg_dump --format=custom` |
| Redis | `BGSAVE` plus `LASTSAVE` polling |
| MariaDB | `mariadb-dump --single-transaction` |
| MongoDB | `mongodump` |

These are internal execution details, not additional backup tools users must
configure.

## 3-2-1 target posture

| Copy | Location | Contract |
|---|---|---|
| 1 | Live application volumes | Source data |
| 2 | Local Kopia repository, normally `/backup/kopia` | `targets.local.enabled: true` |
| 3 | User-owned B2, S3-compatible, or SFTP target | `targets.offsite.enabled: true` |

Offsite immutability defaults to seven days where the selected provider
supports object lock or file locking.

## Exported files

| File | Role |
|---|---|
| `README.md` | Public operator contract and limitations |
| `addon.cue` | Self-hosted configuration and service definitions |
| `integrity.cue` | Provider validation and restore-drill policy |
| `restic-importer.cue` | One-shot Restic-to-Kopia migration contract |

The add-on contract supports local, cloud, and Raspberry Pi contexts. Actual
runtime availability still depends on the generated deployment containing the
services and mounts described here.
