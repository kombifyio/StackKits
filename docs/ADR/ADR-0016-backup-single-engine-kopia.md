# ADR-0016 — Single backup engine: Kopia

**Status:** Accepted (2026-05-01)
**Supersedes:** the implicit "Restic everywhere" decision baked into `addons/backup/addon.cue` v1.0.0.
**Pairs with:** [`docs/BACKUP-ARCHITECTURE.md`](../BACKUP-ARCHITECTURE.md), [`docs/plans/2026-05-01-backup-rollout.md`](../plans/2026-05-01-backup-rollout.md).

## Context

The v1 backup add-on shipped Restic with B2 / Hetzner-Storagebox / S3 targets and SOPS+age secrets. It works for the single-server self-hosted case but leaves three gaps that block the planned multi-tenant SaaS path:

1. **No native Web UI.** Restic is CLI-first. The self-hosted user has nothing to click.
2. **No native repository server with per-user ACLs.** Restic-REST exists but is a separate process with its own auth model. Multi-tenant fan-in across hundreds of homelab hosts would mean we own the auth surface.
3. **No DB consistency story.** Volume snapshots of running Postgres / SQLite are inconsistent; v1 had no pre-snapshot hook framework.

A first design draft attempted to plug those gaps by adding **parallel tools** — Kopia for the SaaS path, Litestream for SQLite, pgBackRest for Postgres, Borgmatic for low-tier hosts. The user rejected that draft on the grounds that backup must not require touching multiple tools. One engine, one setup.

## Decision

Standardise on **Kopia** as the only backup engine in `addons/backup/`, on every StackKit, in both the self-hosted and SaaS paths.

1. **Engine.** `#Config.engine` accepts `"kopia"` (default) or `"restic-import"` (transitional). No other values.
2. **Self-hosted UI.** The Kopia Web UI is exposed at `backups.{{domain}}` behind Traefik + login-gateway. The user manages everything from there or from `stackkit backup`.
3. **SaaS fan-in.** A new add-on `backup-repo-server/` deploys Kopia Repository Server with per-tenant users. The `stackkit-backup-agent` binary on each tenant host is a thin orchestrator around the local Kopia client; the controller drives schedules and reads status.
4. **DB consistency.** Pre-snapshot quiesce hooks (sqlite `.backup`, `pg_dump`, `BGSAVE`, `mariadb-dump`, `mongodump`) are **internal** to the addon (`db-hooks.cue`), driven by image- and volume-pattern detection of the deployed modules. Users do not configure them.
5. **Restic migration.** A one-shot importer (`addons/backup/restic-importer.cue`, CLI `stackkit backup migrate-from-restic`) imports every Restic snapshot into Kopia preserving original timestamps. Restic support is removed two minor releases after the importer ships.

## Why now

Two converging triggers:

- **kombify-TechStack Capability-Level 1+ (SaaS) is on the roadmap.** Multi-tenant backup is one of the first paid features. Picking the engine now avoids shipping a second migration once the SaaS path is live.
- **base-kit V6 default-app set is SQLite-heavy.** Vaultwarden, Jellyfin, Home Assistant, Stalwart, Gitea-default all use SQLite; Immich and Dokploy add Postgres. The current "snapshot the volume and hope" approach is statistically going to corrupt at least one of those during a backup window. The hooks need to land before the user count grows.

Doing both jobs (engine swap + hook framework) in one ADR keeps the surface small and avoids a second migration.

## Alternatives considered

### Keep Restic, add Restic-REST + bespoke SaaS layer

Cheap on day one — no engine swap. But:

- Still no built-in Web UI for the self-hosted user. We'd build one ourselves, taking on a UI surface that Kopia gives for free.
- Restic-REST's auth model is a single password per repo; multi-tenant means we mint per-tenant repos, which fragments dedup. Kopia Repository Server has per-user ACLs into the **same** physical repo with cross-user dedup intact.
- Migration cost is paid eventually anyway when SaaS lands.

Rejected: defers the same work and pays it twice.

### Keep Restic for self-hosted, use Kopia for SaaS

Two engines, two configs, two skill sets to support. Direct contradiction of the "one tool" requirement that triggered this redesign. Rejected.

### Kubernetes-native (Velero, Volsync)

ha-kit is Docker Swarm, not Kubernetes. modern-homelab and base-kit are plain Docker. Velero is k8s-only. Rejected.

### Proxmox Backup Server

Requires Proxmox. Our StackKits run on plain Ubuntu / Debian Docker hosts. Rejected.

### Borgmatic / Borg

Excellent on low-tier hardware (Pi). But:

- No built-in web UI.
- No first-class multi-host repo server.
- Adding it as a third option violates the one-engine constraint.

Rejected for the catalog. Users who already run Borg can keep using it outside the StackKits framework — we just don't bless it.

### Multiple DB-specific tools (Litestream, pgBackRest) as user-visible options

This was the rejected first draft. Rejected because the user explicitly required that backup must be configurable from one place. Litestream and pgBackRest survive only as **internal** options the addon may pick at apply time — not as user-facing choices.

## Consequences

### Positive

- One Web UI for self-hosted users; one dashboard (in TechStack) for SaaS users; never both.
- DB consistency lands as a side-effect of enabling the addon — no extra config.
- Repository Server is the same software users already run locally; one mental model.
- Cross-user dedup in the SaaS path keeps storage cost lower than per-tenant separate repos.

### Negative

- Existing v1 users have to migrate. Mitigated by the one-shot importer and a 2-release deprecation window.
- Kopia is younger than Restic (~2020 vs ~2014). We accept the maturity delta in exchange for the feature surface; the importer keeps escape-hatch open.
- The detection rules in `db-hooks.cue` need to be kept current as new apps land in the catalog. Each new module that uses one of the supported DB engines requires one entry there. CI will fail builds that add a Postgres/SQLite-using module without a matching hook entry (guard to be added in Phase 1 close-out).

### Neutral

- `addons/backup/addon.cue` jumps from v1.0.0 to v2.0.0. Within-major compatibility is broken intentionally; the importer is the upgrade path.

## Rollout

See [`docs/plans/2026-05-01-backup-rollout.md`](../plans/2026-05-01-backup-rollout.md). Phases 1-3 are complete. Phase 4 has landed as a scaffold and Phase 5 lives in kombify-TechStack.

## 2026-05-01 update — Phases 2-4 status

- **Phase 2 (CLI, ✅ done)** — `cmd/stackkit/commands/backup.go` ships the planned subcommand surface (`init`, `run`, `list`, `restore`, `verify`, `migrate-from-restic`, `enroll`). The `BackupEncryptionKey` Layer-3 slot in the break-glass bundle is optional, `omitempty`-serialised, and absent on hosts without the addon — bundles for those hosts stay byte-identical with v1.
- **Phase 3 (Repo-Server addon, ✅ done)** — `addons/backup-repo-server/` is auto-discovered by `cmd/stackkit/commands/addon.go`'s directory scan; no Go-side registry change was needed.
- **Phase 4 (Multi-tenant controller, 🟡 scaffold done)** — `internal/backup-controller/` defines the domain, the HTTP API, the cron scheduler, the audit log, and ships **in-memory** Store and JobQueue implementations. `cmd/stackkit-backup-agent/` ships the planned CLI but exits with a clear "not implemented" message. The Postgres driver, NATS wiring, OIDC middleware, real cron parser, and agent enrollment over the wire each land in their own follow-up PRs (Beads issues `StackKits-backup.4-pg`, `.4-nats`, `.4-oidc`, `.4-cron`, `.4-enroll`).

The **interfaces** in the scaffold are deliberately the contract: every follow-up swaps an implementation behind `Store` / `JobQueue` and, for the operator middleware, behind `Server.requireOperatorKey`. None of the handler bodies, route registrations, or domain types need to change again.
