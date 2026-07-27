# Self-hosted Backup Add-On (v2.0.0)

The public backup contract standardizes encrypted, deduplicated local backups
on Kopia. The standalone Basement core renders and applies the pinned,
idle-until-owner-command `kopia-agent`; operators control the native v0.8
repository and snapshot path through the `stackkit backup` CLI.

## Release boundary

This release contains the self-hosted CUE contract, the Basement renderer and
local Apply owner for `kopia-agent`, native configure/status/run, a verified
isolated staging restore, database-hook metadata, integrity policy, and the
Restic migration contract. Fresh-host Day-2 release evidence, live restore
cutover/boot proof, schedules, database quiesce execution, and restore drills
remain separate gates and are not implied here.

The local surface is:

- `init`, `configure`, `status`, `run`, `list`, `restore`, and `verify`
- `emergency-export` for a Kopia-independent manifest and restore runbook
- `migrate-from-restic` for the one-shot legacy migration

`configure`, `status`, `run`, and the isolated `restore` are native v2.
`list`, `verify`, and `migrate-from-restic` remain exact-v0.6 compatibility
commands and fail closed on native-v2 workspaces until their lifecycle slices
land.

Fleet enrollment, tenant identity, and controller operation are not part of the
public command or CUE surface.

## Why Kopia

The v1 add-on used Restic. v2 standardizes on Kopia because it provides:

1. Client-side encryption, compression, deduplication, and retention policies.
2. A built-in Web UI that may be exposed later as an opt-in convenience behind
   PocketID and TinyAuth; it is not the v0.8 lifecycle authority.
3. B2, S3-compatible, SFTP, and Hetzner Storage Box targets.

Restic remains only as a one-shot migration input. It is not a second daily
backup engine.

## Local CLI

First materialize and verify the standalone Basement deployment:

```bash
stackkit init --owner-source=local
stackkit validate
stackkit generate
stackkit apply
stackkit verify
```

Then establish the owner-local repository and create a snapshot:

```bash
stackkit backup configure --json
stackkit backup status --json
stackkit backup run --operation-id first-owner-snapshot --json
stackkit backup list
stackkit backup list --json
stackkit backup restore sha256:<snapshot-anchor-id> \
  --owner-approve \
  --operation-id first-owner-restore \
  --json
stackkit backup verify
stackkit backup migrate-from-restic --dry-run
```

The native path accepts no repository, container, source, exclusion, or
password override. Those values come from the exact generated CUE policy and
owner-local custody. Object-store targets remain a future/explicit deployment
contract; they are not silently selected by the local CLI.

Restore likewise accepts neither a raw Kopia snapshot ID nor a target path. It
loads the signed snapshot anchor, records an owner-signed recovery anchor,
verifies all snapshot files, and extracts into a deterministic directory inside
the dedicated `kopia-restore-staging` volume. It verifies the snapshot again
after extraction and records an owner-signed result only after the current live
Stack, PocketID Owner binding, services, and probes still pass verification.
The staging volume is canonically excluded from subsequent snapshots, and a
persisted staged receipt prevents retries after post-verification failure from
repeating the repository restore.
This proves isolated recovery bytes without writing into live Docker volumes;
it does not activate those bytes or prove that services boot from them.

StackKits remains the standalone lifecycle authority. Techstack can provide an
optional Orchestrator UI/RIL over the exact published binary and versioned
results. kombify Cloud can provide user-approved identity sync convenience for
the local PocketID/TinyAuth plane, but neither service is required for backup or
restore authorization.

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
