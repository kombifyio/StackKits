# Backup Rollout Plan

> Companion to [ADR-0016](../ADR/ADR-0016-backup-single-engine-kopia.md) and [BACKUP-ARCHITECTURE.md](../BACKUP-ARCHITECTURE.md).

Five phases. Each phase is shippable on its own. Phases 1-4 land in this
repo; Phase 5 lands in kombify-TechStack.

## Phase 1 — Engine swap + self-hosted UI ✅ landed 2026-05-01

**Scope**

- `addons/backup/addon.cue` rewritten for Kopia (`engine: "kopia"`),
  immutability config, restore-drill config, web-UI exposure config,
  agentMode config, retention policy, notify channels.
- `addons/backup/db-hooks.cue` (new) — internal pre/post-snapshot hooks
  for SQLite, Postgres, Redis, MariaDB, MongoDB. Built-in detection
  rules for Vaultwarden, Jellyfin, Home Assistant, Stalwart, Gitea,
  Immich (Postgres + Redis), Dokploy.
- `addons/backup/integrity.cue` (new) — weekly `validate-provider` job
  + monthly restore drill + heartbeat into the monitoring addon.
- `addons/backup/restic-importer.cue` (new) — one-shot v1→v2 migration
  service definition.
- `addons/backup/README.md` (new) — user-facing quickstart and engine
  rationale.
- `docs/ADR/ADR-0016-backup-single-engine-kopia.md` (new).
- `docs/BACKUP-ARCHITECTURE.md` (new).

**Out of scope for this phase**

- The CLI subcommand `stackkit backup` (Phase 2).
- Repository server addon (Phase 3).
- Multi-tenant controller (Phase 4).
- Web dashboard (Phase 5, in kombify-TechStack).

**Verification (manual, until E2E lands)**

1. `cue vet ./addons/backup/...` (run when CUE is installed in CI).
2. `stackkit init base-kit && stackkit addon enable backup && stackkit apply`.
3. Open `https://backups.<domain>` → Kopia Web UI prompts for password (TinyAuth gate first).
4. Force a snapshot of the Vaultwarden volume; stop the container; wipe the volume; restore from the snapshot; verify Vaultwarden login still works.

## Phase 2 — CLI surface ✅ landed 2026-05-01

**Scope**

- `cmd/stackkit/commands/backup.go` (new) — subcommand tree:
  - `stackkit backup init` — first-run checklist (no writes)
  - `stackkit backup run` — force snapshot now
  - `stackkit backup list [--json]` — show snapshots
  - `stackkit backup restore <snapshot-id> [--target <path>]`
  - `stackkit backup verify` — trigger `validate-provider` ad-hoc
  - `stackkit backup migrate-from-restic [--dry-run]`
  - `stackkit backup enroll --token <t> --endpoint <url>` — **Phase-4 stub** that errors clearly until the controller lands
- `cmd/stackkit/commands/backup.go` mounts itself on `rootCmd` via `init()` (matching the `break_glass.go` self-registration pattern).
- `internal/identity/backup_encryption.go` (new) — `BackupEncryptionKeyCredential` + `BackupEncryptionKeyGenerator`.
- `internal/identity/bundle.go` — extended `BreakGlassSection` with optional `BackupEncryptionKey` (yaml `omitempty` so existing bundles stay byte-identical when the addon is not in use). Restore-instructions doc updated with the Layer-3 (data-recovery) path.
- Operator-facing docs updated in `docs/CLI.md` (`stackkit backup` section).

**Out of scope for this phase**

- Real network call from `enroll` to the controller — Phase 4.
- Persisting the backup-encryption-key into the bundle automatically during `stackkit apply` — that wiring lands when the apply pipeline learns to materialise addon secrets (separate Beads issue).

## Phase 3 — Repository server addon ✅ landed 2026-05-01

**Scope**

- `addons/backup-repo-server/addon.cue` (new) — Layer-2 service deploying Kopia Repository Server with two Traefik routes (UI gated by login-gateway; agent route uses Kopia's own per-user auth).
- `addons/backup-repo-server/README.md` (new).
- No Go-side registration code change needed — `cmd/stackkit/commands/addon.go` discovers addons dynamically by scanning `addons/*/addon.cue`.

**Verification**

- Auto-discovery: `stackkit addon list` enumerates `backup-repo-server` once the directory exists.
- Multi-tenant smoke (manual): start the server, connect two test clients with different per-user credentials, snapshot lists must be disjoint.

## Phase 4 — Multi-tenant controller + agent (SaaS) 🟡 scaffold landed 2026-05-01

**What landed**

- `internal/backup-controller/` — domain model, interfaces, **in-memory** Store and JobQueue, HTTP API (stdlib `http.ServeMux`), minimal cron Scheduler, AuditLog, agent enrollment helpers.
- `internal/backup-controller/migrations/001_init.sql` — Postgres schema (planning artifact; no driver wired yet).
- `internal/backup-controller/{store,server,scheduler}_test.go` + new `queue_test.go`, `agent_test.go`, `audit_test.go` — unit tests for the in-memory implementations, the HTTP handlers (auth + happy path), the cron decision logic, the queue's backpressure / close semantics, agent enrollment, and the audit log's dual-sink behaviour.
- `cmd/stackkit-backup-agent/main.go` (new) — Linux-only Go binary with the planned CLI surface (`enroll`, `run`, `status`). Exits with a clear "not implemented" error until the controller listener is up.
- `cmd/stackkit-backup-controller/main.go` (new) — Linux-only Go binary that boots the controller package on `:8083` with the in-memory Store + JobQueue and runs the in-process scheduler. Lets the kombify-TechStack web UI integrate against a real endpoint immediately; the Postgres / NATS swap-ins land behind the same `Store` and `JobQueue` interfaces without any HTTP API change.
- `.goreleaser.yaml` — added `stackkit-backup-agent` and `stackkit-backup-controller` builds + standalone tarball archives.

**What is intentionally NOT yet landed (follow-up PRs)**

1. Replace `NewMemoryStore()` with a `pgx`-backed implementation that executes `migrations/001_init.sql`.
2. Replace `NewMemoryQueue()` with NATS JetStream (durable streams `backup.jobs`, `backup.status`).
3. Wire `cmd/backup-controller/main.go` (or extend `cmd/stackkit-server/main.go`) to host the HTTP server with TLS, request-ID, and rate-limit middleware (matching `internal/api/server.go`).
4. Swap the cron field parser in `scheduler.go` for `github.com/robfig/cron/v3`.
5. Implement OIDC middleware against PocketID for operator endpoints; remove the static `X-API-Key`.
6. Hash agent tokens with sha256 before persisting (the in-memory store stores them verbatim because it has no threat model).
7. Make the `stackkit backup enroll` subcommand actually call the controller.

**Estimated effort for the follow-up**: 2–3 sprints, broken into focused PRs (Postgres, NATS, OIDC, cron, agent enroll). The scaffold's interfaces mean each can land independently without breaking the others.

**Risk areas (re-confirmed during scaffold)**

- Storage isolation policy (per-tenant bucket vs. shared bucket with per-tenant keys) is still open. Default in code today: every tenant gets its own bucket. Revisit when the first cost-pressure customer asks.
- Job queue choice: NATS JetStream is the current preference because the rest of the kombify stack uses it.

## Phase 5 — Web dashboard (kombify-TechStack)

**Scope** (lives in kombify-TechStack repo, tracked here for visibility)

- Tenant onboarding flow.
- Host list with per-host backup status.
- Restore wizard (browse snapshots → pick files → restore target).
- Audit-log viewer with export.
- Stripe plan-tier enforcement.

**Estimated effort**: tracked separately in TechStack.

## Beads issues

To be created (Phase 1–4 close-out):

- `StackKits-backup.epic` — Backup v2 (Kopia) Epic
- `StackKits-backup.1` — Phase 1 ✅ DONE
- `StackKits-backup.2` — Phase 2: CLI subcommand ✅ DONE
- `StackKits-backup.3` — Phase 3: Repository Server addon ✅ DONE
- `StackKits-backup.4` — Phase 4: Multi-tenant controller 🟡 scaffold DONE
- `StackKits-backup.4-pg` — Phase 4 follow-up: pgx-backed Store (replaces `NewMemoryStore`)
- `StackKits-backup.4-nats` — Phase 4 follow-up: NATS JetStream Queue (replaces `NewMemoryQueue`)
- `StackKits-backup.4-oidc` — Phase 4 follow-up: OIDC operator auth via PocketID
- `StackKits-backup.4-cron` — Phase 4 follow-up: replace minimal cron parser with `robfig/cron/v3`
- `StackKits-backup.4-enroll` — Phase 4 follow-up: wire `stackkit backup enroll` to a live controller
- `StackKits-backup.5` — Phase 5: Web dashboard (cross-repo, kombify-TechStack)
- `StackKits-backup.ci-guard` — CI guard for `db-hooks.cue` coverage when new modules are added
