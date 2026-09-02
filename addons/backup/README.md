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
cutover/boot proof, database-specific consistency and restore drills
remain separate evidence and are not implied here.

The local surface is:

- `init`, `configure`, `status`, `run`, `list`, `restore`, and `verify`
- `emergency-export` and `emergency-restore` for encrypted portable data and
  checksum-verified isolated recovery without Kopia
- `migrate-from-restic` for the one-shot legacy migration

`configure`, `status`, `run`, and the isolated `restore` are native v2.
`list`, `verify`, and `migrate-from-restic` remain exact-v0.6 compatibility
commands and fail closed on native-v2 workspaces until their lifecycle slices
land.

Fleet enrollment, tenant identity, and controller operation are not part of the
public command or CUE surface.

## Configuration compatibility

The add-on reuses the legacy backup class and policy definitions from
`foundation/observability.cue`. Existing class coverage, cron strings and
retention field names remain compatible with their Go readers and portable
archives. Native ResolvedPlan backup intent uses the separate versioned
`#BackupPolicyV1` contract in `foundation/architecture_v2.cue`, including UTC
cadence and bounded retention. It cannot be substituted into a legacy config
without migrating that reader and its data. Neither configuration format alone
proves a scheduler ran, a database is consistent, or an application recovered.

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
stackkit backup restore abandon first-owner-restore \
  --owner-approve \
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

To close a pending or staged restore whose source is no longer available, use
the exact operation ID with `--owner-approve`. This records signed terminal
abandonment without invoking Docker or Kopia, deleting repository data, or
touching live volumes. Any later restore must use a new operation ID.
A successful staging restore proves isolated recovery bytes without writing
into live Docker volumes; it does not activate those bytes or prove that services
boot from them. An abandonment receipt proves only the terminal Owner decision.

StackKits remains the standalone lifecycle authority. Techstack can provide an
optional Orchestrator UI/RIL over the exact published binary and versioned
results. kombify Cloud can provide user-approved identity sync convenience for
the local PocketID/TinyAuth plane, but neither service is required for backup or
restore authorization.

See [`docs/CLI.md`](../../docs/CLI.md#stackkit-backup) for flags and command
details.

## Local schedule

The generated policy carries `backupPolicy.schedule`: daily, hourly or weekly
UTC cadence with bounded jitter. On an observed active systemd host, enable the
timer after configuring and checking the repository:

```bash
stackkit backup schedule enable --owner-approve --json
stackkit backup schedule status --json
stackkit backup schedule disable --owner-approve --json
```

The service executes the exact packaged CLI through the normal backup lifecycle.
Its signed approval binds the current Plan and Apply, policy, workspace and spec
paths, operating-system account, CLI bytes and unit files. Run these commands as
the account that owns the local custody; installing system units also requires
permission to write `/etc/systemd/system` and control its manager. StackKits does
not switch accounts or obtain privileges automatically.

A CUE cadence alone grants no scheduler authority. A changed Plan, Apply, CLI or
unit leaves scheduled execution blocked until the Owner approves the current
binding. Each UTC slot has a stable operation ID; an interrupted snapshot
resumes its existing journal. Failed starts retry after 60 seconds with at most
three starts per hour; every attempt is bounded to 14 minutes. The timer catches
up after a missed slot, while the signed journal prevents a second successful
snapshot for the same slot. Disabling revokes dispatch before stopping the
timer; a failed revocation still attempts to stop the timer and reports the
incomplete result. Reapproval archives the previous signed grant. With unchanged
runtime authority it preserves the interrupted operation and its original
slot approval; a changed runtime can start a new operation only after the old
journal proves no unresolved quiescence. Every reapproval records its actual
approval time. Timer state and the last signed snapshot are separate status fields.
Fresh-host execution and a missed-run/restart exercise remain live evidence to
be collected on the final candidate.

## Portable emergency export

Kopia remains the primary operational engine. `resilience.emergencyExport`
models a complementary recovery path that does not require a healthy Kopia
repository:

```cue
resilience: emergencyExport: {
	mode:           "portable-archive"
	format:         "tar.gz.age"
	includeClasses: ["config", "secrets", "platform-state", "database", "documents", "serverless-config"]
	largeMediaMode: "manifest-only"
}
```

The CLI creates encrypted bytes, checksums, a manifest and restore runbook:

```bash
stackkit backup emergency-export --recipient age1YOUR_PUBLIC_RECIPIENT \
  --source config=/opt/stacks --target /backup/emergency-export-new
```

Sources are explicit `CLASS=PATH` selections. Automatic database dumps and
CUE-selected source execution remain separate integration work; source labels
alone do not prove consistency or full application coverage. Follow
[Backup resilience](../../docs/BACKUP-RESILIENCE.md) for owner identity custody,
media exclusions and independent staged restore.

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
