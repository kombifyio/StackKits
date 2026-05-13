# Backup Architecture

> Status: Phases 1-3 landed 2026-05-01; Phase 4 landed as **scaffold** (in-memory Store + JobQueue, no Postgres/NATS yet; follow-up PRs are tracked in Beads).
> See [ADR-0016](ADR/ADR-0016-backup-single-engine-kopia.md) for the engine decision.

## TL;DR

- One engine: **Kopia**.
- One configuration surface per host: either the local **Kopia Web UI** (self-hosted, free) or the **kombify-TechStack dashboard** (SaaS, paid). Never both.
- Database consistency is **automatic** — pre-snapshot hooks live inside the addon and pick the right strategy by detecting which DB engines are deployed.
- 3-2-1 by default: live volumes + local Kopia repo + one offsite leg with Object Lock.

## The two surfaces

```
                    [ EINE Engine: Kopia ]
                            │
            ┌───────────────┴───────────────┐
            ▼                               ▼
   ╔════════════════════╗         ╔══════════════════════════╗
   ║  Self-Hosted-Pfad  ║         ║  SaaS-Pfad               ║
   ║  (Free, lokal)     ║         ║  (Paid, kombify-TechStack)║
   ╠════════════════════╣         ╠══════════════════════════╣
   ║ - Kopia Web-UI auf ║         ║ - kombify-TechStack-UI   ║
   ║   eigenem Host     ║         ║   (Multi-Tenant)         ║
   ║ - stackkit-CLI     ║         ║ - Backup-Controller (Go) ║
   ║ - User wählt eige- ║         ║ - stackkit-backup-agent  ║
   ║   nen Storage      ║         ║   pro Host               ║
   ║   (B2/Storagebox)  ║         ║ - Per-Tenant-Bucket      ║
   ║ - Eigener Encryp-  ║         ║ - Plan-Tiers, Stripe     ║
   ║   tion-Key         ║         ║ - Audit-Log              ║
   ╚════════════════════╝         ╚══════════════════════════╝
```

`#Config.agentMode.enabled` flips a host between the two surfaces. The
addon enforces mutual exclusion: agent mode forces `webUI.enabled = false`
so a host cannot be driven from two places at once.

## Self-hosted path

### Components on the host

| Container | Source | Role |
|---|---|---|
| `kopia-agent` | `kopia/kopia:0.18` | daemonset, runs scheduled snapshots |
| `kopia-ui` | `kopia/kopia:0.18` | Web UI, single instance on the main node |
| `kopia-integrity` | `kopia/kopia:0.18` | weekly `validate-provider` + monthly restore drill |
| `restic-importer` | `ghcr.io/kombify/restic-kopia-importer:0.1` | one-shot, only when migrating from v1 |

The Web UI is exposed at `https://{{webUI.subdomain}}.{{domain}}` (default
`backups.{{domain}}`) behind Traefik, and is **always** gated by the
login-gateway middleware (TinyAuth + PocketID forward-auth). The
`#WebUIConfig.authRequired` field is hard-wired to `true` to prevent a
foot-gun config that would expose the snapshot list to the public internet.

### Storage layout

```
local repo         /backup/kopia                      → Copy 2 (3-2-1)
offsite repo       b2://...  | sftp://...  | s3://... → Copy 3 (3-2-1)
                                                       Object Lock on offsite
```

`local.enabled = true` is the default; offsite is opt-in but strongly
recommended. Object Lock retention is `immutability.retentionDays`
(default 7) — even a host fully compromised by ransomware cannot delete
history within the lock window.

### Encryption & key custody

- Repo encryption: AES-256, Kopia-native, password-derived.
- Password storage: SOPS+age, in the same secrets layer the rest of the
  StackKit uses.
- The break-glass bundle gains an **optional** Layer-3 slot for the
  Kopia repository passphrase (`internal/identity.BackupEncryptionKeyCredential`,
  serialised as `breakGlass.backupEncryptionKey` with `yaml:",omitempty"`).
  Bundles for hosts without the addon stay byte-identical with v1; bundles
  for hosts with the addon enabled carry the passphrase + a free-text
  `repositoryHint` so a recovery operator who only has the bundle can
  re-attach the offsite repo. Without this layer, "lost host" = "lost
  backups", which defeats the purpose.

### Database consistency (automatic)

The addon looks at every container in the generated docker-compose at
apply time and matches them against the patterns in `db-hooks.cue`. When
a match wins, the corresponding pre-snapshot hook is wired into the
Kopia snapshot policy for the volume that container mounts.

| Engine | Strategy | Rationale |
|---|---|---|
| SQLite | `sqlite3 .backup` to tmpfs | Atomic, no second tool; output file is what Kopia snapshots |
| Postgres | `pg_dump --format=custom` to tmpfs | Custom format restores cleanly via `pg_restore`; no role/tablespace surprises |
| Redis | `BGSAVE` + poll `LASTSAVE` | Fast, in-place; cache-only Redises (Immich) skip the wait |
| MariaDB | `mariadb-dump --single-transaction --routines --events` | Defensive — most StackKits don't ship MariaDB but users add it |
| MongoDB | `mongodump` against admin user | Defensive — same rationale |

The user does not configure these. Adding a new module to the catalog
that uses one of these engines means adding **one entry** to
`db-hooks.cue`. A CI guard (Phase 1 close-out) refuses PRs that add a
Postgres/SQLite-using module without a corresponding hook.

### Integrity & restore drills

| Job | Frequency | What it does | Failure path |
|---|---|---|---|
| `validate-provider` | weekly (Sun 05:00) | Reads a 2% sample of repo data and re-verifies hashes | `#NotifyConfig` channels |
| Restore drill | monthly (1st 04:00) | Picks a random snapshot, restores into tmpfs, hashes against manifest | `#NotifyConfig` channels |
| Heartbeat | every snapshot | Pushes pass/fail signal into `addons/monitoring` | Stuck cron is visible as service-down in monitoring |

The restore drill is the **only** thing that catches silent corruption
between the application and the backup tool. It is on by default for a
reason: backups that have never been restored are wishful thinking.

## SaaS path (Phases 3-4)

The local engine and storage layout do not change. What changes is the
control plane:

- **Backup-Controller** (`internal/backup-controller/`) — Go service:
  REST API on stdlib `http.ServeMux`, Tenant / Fleet / Host / Repo / Job
  / AuditEntry domain, in-memory `Store` and `JobQueue` today, with
  Postgres + NATS JetStream wiring planned as a follow-up PR. The
  package's `README.md` enumerates exactly what is in the scaffold and
  what is not.
- **`stackkit-backup-agent`** (`cmd/stackkit-backup-agent/`) — separate
  Linux-only binary in goreleaser. The Phase-4 scaffold validates the
  CLI surface (`enroll`, `run`, `status`) and exits with a "not
  implemented" message until the controller listener is up.
- **`backup-repo-server`** addon (`addons/backup-repo-server/`) deploys
  Kopia Repository Server, which accepts connections from many tenants,
  each with a per-user ACL into the same physical storage. Cross-user
  dedup keeps storage cost low.
- **Storage isolation** defaults to one bucket per tenant (clear billing
  boundary). The shared-bucket / per-tenant-encryption-key option is on
  the roadmap as a cost-saver tier; not in scope for Phase 4.

### Phase 4 scaffold details

The current implementation choice prioritises **compilable, testable
code** over premature dependency commitment. Concrete consequences:

| Aspect | Today (scaffold) | Follow-up PR |
|---|---|---|
| `Store` impl | `NewMemoryStore()` — RWMutex-protected maps | `pgx`-backed, runs `migrations/001_init.sql` |
| `JobQueue` impl | `NewMemoryQueue(buffer)` — buffered channel | NATS JetStream durable streams |
| Cron parser | 5-field, no ranges/lists/steps | `github.com/robfig/cron/v3` |
| Operator auth | static `X-API-Key` header | OIDC against PocketID |
| Agent token storage | verbatim in memory | sha256-hashed in Postgres |
| Controller process | `cmd/stackkit-backup-controller` boots the package on `:8083` with the in-memory backends and an in-process scheduler | same binary, same HTTP API; only the Store / Queue implementations change |
| `stackkit backup enroll` | parses flags, errors out | live HTTP call to `/api/v1/fleets/{id}/hosts` |

The HTTP API surface (`Server.Handler()`) is the contract. The follow-up
PRs swap implementations behind the existing interfaces and do not
require revisiting handler shapes or the route table.

```
[kombify-TechStack Web UI]
        │ OIDC (PocketID) + REST
        ▼
[backup-controller] ──► [Postgres: tenants, fleets, hosts, jobs, audit]
   │   │                          ▲
   │   └─► [NATS JetStream] ◄─────┘
   │           ▲ pull
   │     ┌─────┴──────┬─────────┐
   │     ▼            ▼         ▼
   │  [agent host A] [host B] [Swarm-Node N]    stackkit-backup-agent
   │     │            │         │
   │     ▼            ▼         ▼
   └─► [Kopia Repository Server] ─► [S3 / B2 / Hetzner Storagebox]
         (multi-tenant, per-user ACL)              per-tenant bucket
```

### Tenant model (Postgres)

```
tenants(id, name, plan, created_at)
fleets(id, tenant_id, name, region)
hosts(id, fleet_id, hostname, agent_token, last_seen, stackkit_kind)
repos(id, tenant_id, kind, endpoint, credentials_ref)
jobs(id, tenant_id, host_id, repo_id, schedule, last_run, status)
backup_audit(id, tenant_id, actor, action, resource, ts, payload_jsonb)
```

`stackkit_kind` records which StackKit (`base-kit`, `modern-homelab`,
`ha-kit`) a host runs so the controller can pick a correct hook profile
without re-discovering it on the host.

### Plan tiers (illustrative, owned by TechStack)

| Tier | Hosts | Offsite | Retention | Object Lock | Audit export |
|---|---|---|---|---|---|
| Free | 1 | local only | default | — | — |
| Pro | up to 5 | B2 / Storagebox | 30 days | — | — |
| Business | unlimited | own bucket | configurable | yes | yes |

Stripe wiring lives in kombify-TechStack and is out of scope here.

## What the user explicitly does not touch

| Thing | Why |
|---|---|
| Restic config | Default is Kopia. Existing repos go through `migrate-from-restic` once. |
| Litestream / pgBackRest containers | DB consistency is internal to the addon. |
| Borg / Borgmatic | One engine. |
| A second dashboard | Self-hosted = Kopia UI; SaaS = TechStack UI. |
| Hook detection rules | They ship with the catalog and update with new modules. |

## Open questions tracked elsewhere

- Storage isolation policy for SaaS (per-tenant bucket vs. shared bucket
  with per-tenant keys) — decision in Phase 4 design.
- Job queue choice (NATS vs. River) — current preference NATS because
  the rest of the kombify stack uses it.
- Restic deprecation timeline — current preference: importer for two
  minor releases of the addon, then remove.

These are tracked in Beads as backup follow-ups.
