# Backup resilience

> Verified implementation: 2026-09-02. An archive receipt and a successful
> application recovery are separate evidence.

## Current behavior

| Operation | Implemented behavior | Evidence still needed |
| --- | --- | --- |
| Native v2 Kopia backup | Owner-authorized configure, journaled writer stop/resume, crash-consistent snapshot with CUE retention, signed snapshot anchor and staged restore; Full and Lite Core include selected `backup: true` standalone-Compose volumes | Database-specific application consistency, functional data/client restore checks and final live target evidence |
| Local backup schedule | Explicit Owner approval binds a systemd trigger to the exact CLI, Plan, Apply and CUE UTC schedule; ordinary backup journals own execution and retries | Final candidate timer, missed-run and restart evidence on a real systemd host |
| Emergency export | Explicit local sources streamed into an age-encrypted tar/gzip archive with manifest, SHA-256 checksums and restore runbook | Database/file consistency and complete source selection for each application |
| Emergency restore | Authenticates the full encrypted archive, validates every listed file, and publishes a new private staging directory | Database import, service activation, login and real client access |
| Off-host recovery | An owner-held archive can be decrypted on a replacement machine without the original host, Kopia or a Kombify account | Independent storage and separately retained age identity |

CUE owns backup policy. A schedule or restore interval is intent, not evidence
that a scheduler ran. The current local Kopia runtime is explicitly
`idle-until-owner-command`. Its exact source policy proves only the Core and
selected `backup: true` standalone-Compose volume set bound to the selected Full
or Lite Core Plan and node. External application storage, database-consistent
dumps, actual scheduled execution and functional application recovery each require
their own current evidence. The restore verifier checks Core and all selected
local application runtimes, including their actual HTTP health result; this is
not proof of database integrity, restored user content or client login.

The source policy resolves `backupPolicy.retention` from CUE and explicitly
sets and reads back the daily, weekly, monthly and yearly Kopia buckets.
Hourly and latest buckets are zero, so inherited Kopia defaults cannot widen
the selected policy. Ordinary snapshots may expire when a new snapshot is
created. Upgrade checkpoints and restore-activation safety snapshots are pinned
at creation and remain retained. Kopia changes a manifest's ID when its pins
change, so the runtime does not rewrite pins on an existing signed anchor.
Historical unprotected anchors are not retroactively described as protected.

An interrupted restore of an unprotected source blocks new snapshots that
could expire that source. Retry the restore with its reported operation ID. If
the approval expired, explicitly authorize a new restore of the same source;
only its successful signed result releases the older pending attempt. A
changed, incompatible source topology can be released with `stackkit backup
restore abandon <operation-id> --owner-approve`. The terminal decision is signed;
it preserves the original recovery evidence, touches no repository or live data,
and prevents reuse of that restore operation ID. Deleting the journal is not a
supported recovery step.

The [local schedule](../addons/backup/README.md#local-schedule) lowers the CUE
cadence into an explicitly enabled systemd timer. It invokes the same CLI backup
path under the existing lifecycle lock. `backup schedule status --json` shows
authorization, timer state and the last scheduled attempt separately. An active
timer without a completed signed snapshot is not evidence of protected data.

`stackkit backup status` exposes owner-authenticated snapshot and staged-restore
receipt timestamps, their age in seconds, and the original Plan hash. Only
receipts for the current owner, authority, repository and exact source-policy
digest and source set contribute. The original Plan hash and `currentPlan` flag
keep historical receipts distinct after another Apply. Missing history stays
`unverified`; an unreadable or invalid entry reports `history-incomplete` while
retaining the latest verifiable receipts. If the history directory cannot be
verified, `history-could-not-be-authenticated` leaves both dates unverified.
Neither condition turns repository readiness into protection. Future timestamps
report `clock-skew` with no usable age. A staged
restore is not an application activation or a client-access drill. These are
historical receipts, not a fresh check that their data is still retained.

## Portable emergency archive

`stackkit backup emergency-export` now creates actual encrypted bytes. The
former metadata-only command and planned `tar.zst.age` / `zip.age` formats are
replaced by one supported format, `tar.gz.age`, using the existing age dependency
and standard tar/gzip tooling.

Use an age X25519 public recipient from an owner-controlled recovery identity.
Keep its private identity away from the host and backup storage. Export accepts
only the public recipient; restore reads private identities from a local file.

```sh
stackkit backup emergency-export \
  --recipient age1YOUR_PUBLIC_RECIPIENT \
  --source config=/opt/stacks \
  --source documents=/srv/documents \
  --source database=/srv/database-dumps \
  --source large-media=/srv/media \
  --large-media-mode manifest-only \
  --target /backup/emergency-2026-09-01 \
  --json
```

The target must be a new directory whose parent exists, outside all sources.
The command rejects overlapping sources, missing included sources, symbolic
links, device nodes, sockets, and files that change while being copied. It never
overwrites an existing export. Sources are explicit `CLASS=PATH` selections;
there is no implicit scan of every Docker volume.

Supported classes: `config`, `secrets`, `platform-state`, `database`, `documents`,
`photos`, `large-media`, `serverless-config`, `user-content`,
`telemetry-timeseries`, `cache-generated`. A class labels a source; it does not prove
completeness or trigger a dump. The database example assumes a directory of
native dumps already created with the matching consistency workflow. Raw live
database files do not produce an application-consistent backup. This command
does not claim or perform automatic database quiescing.

Collect application configuration, required secrets, database dumps and user
files as a consistent unit. Keep application versions and deployment intent
with them. Media policy applies specifically to sources labeled `large-media`:

| Policy | Archive content |
| --- | --- |
| `manifest-only` (default) | Source mapping retained; media bytes absent |
| `include` | Media bytes encrypted and checksummed with other sources |
| `exclude` | Source mapping explicitly records exclusion; bytes absent |

Personal photo originals belong to `photos` and are included. Leave rebuildable
caches, transcodes and downloaded models out of the source set. Each omitted
dataset needs a separately verified recovery source before claiming full protection.

A successful export publishes three files together:

- `stackkit-emergency.tar.gz.age`: encrypted data and embedded manifest/runbook.
- `stackkit-emergency-export-manifest.json`: receipt with archive digest and exact
  source/entry coverage; it contains paths, not keys.
- `RESTORE.md`: recovery steps, also embedded in the encrypted archive.

## Recovery without the original host

Copy the archive to a replacement machine. Supply the independently retained
age identity and choose a new isolated directory:

```sh
stackkit backup emergency-restore \
  --archive /recovery/stackkit-emergency.tar.gz.age \
  --identity-file /recovery/private/recovery-key.txt \
  --target /recovery/staged-2026-09-01 \
  --json
```

No original owner custody, hosted login, Docker daemon or Kopia repository is
needed. The command authenticates the entire encrypted stream, including its
final tag, and compares extracted bytes and metadata with the embedded manifest.
Unsafe paths, links, duplicate entries, unlisted data, checksum mismatches and
truncated streams fail without publishing the target. The decompressed-byte
limit defaults to 512 GiB and includes archive metadata; set `--max-bytes`
explicitly for larger archives.

`contentVerified: true` means these checks passed. `applicationsVerified` stays
false. Files remain under `sources/0000`, `sources/0001`, and so on; the manifest
maps them to original sources. Staging uses private permissions and does not
activate services or restore privileged ownership, executable bits, devices or links.

Follow the embedded runbook to recreate matching versions, restore configuration
and secrets, import database dumps, assign replacement service identities, and
restore user data. Then check native login, content integrity, health and access
from intended client devices. Record that restore drill separately with its
timestamp and exact archive digest.

Commodity recovery uses `age --decrypt -i recovery-key.txt ARCHIVE`, followed by
standard tar/gzip inspection and extraction into a new isolated directory.
Use the embedded manifest to compare file checksums manually.

## Recovery objectives and failure domains

A single host has no host-failure availability guarantee. A local repository
helps recover deleted files. An independent off-host copy protects against host
or disk loss, and the age identity must survive that loss too. NAS storage in
the same household can separate disks and hosts while still sharing power,
network and physical-site risks.

Evaluate RPO, RTO, retention, data growth and restore age per application. A
successful snapshot does not prove those objectives. HA topology and quorum
are separate concerns; three nodes or a configured backup policy alone are not
live failover evidence.

Managed/provider-native backups remain Techstack-owned. Configuration and
receipts may be projected into StackKits, but must not become an account
requirement for owner-held local recovery.
