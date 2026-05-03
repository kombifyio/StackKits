# backup-controller

> Phase-4 SaaS control plane for the kombify-Backup add-on.
> See [`docs/BACKUP-ARCHITECTURE.md`](../../docs/BACKUP-ARCHITECTURE.md) and
> [ADR-0016](../../docs/ADR/ADR-0016-backup-single-engine-kopia.md) for the
> overall design and the engine decision.

## Status: scaffold

This package ships the **shape** of the controller — domain model, Store
and JobQueue interfaces, HTTP API, scheduler, audit log, agent
enrollment — with **in-memory implementations** of the persistence and
queue layers. Real Postgres + NATS JetStream wiring is a follow-up PR
and intentionally not part of this commit:

- pulling pgx and a NATS client into `go.mod` for a half-finished
  feature is the kind of premature commitment we want to avoid
- the in-memory implementations are real test doubles and a working
  single-process deployment target — not throwaway code
- `migrations/001_init.sql` defines the Postgres schema the future
  driver will execute. Treat it as the source of truth for the row
  shapes; when the driver lands, the structs in `types.go` map
  one-to-one

## Package layout

| File | Role |
|---|---|
| `types.go` | Domain model: `Tenant`, `Fleet`, `Host`, `Repo`, `Job`, `AuditEntry` |
| `store.go` | `Store` interface + in-memory implementation |
| `queue.go` | `JobQueue` interface + in-memory implementation |
| `server.go` | HTTP server (stdlib `http.ServeMux`) — operator and agent endpoints |
| `scheduler.go` | Walks Jobs on a tick, publishes due jobs onto the queue |
| `audit.go` | Audit log wrapper; writes to Store + slog |
| `agent.go` | `EnrollHost` helper — mints token, persists, audits |
| `migrations/001_init.sql` | Postgres schema (planning artifact) |

## HTTP API surface

```
POST   /api/v1/tenants                     create a tenant
GET    /api/v1/tenants                     list tenants
POST   /api/v1/tenants/{id}/fleets         create a fleet
GET    /api/v1/tenants/{id}/fleets         list fleets
POST   /api/v1/fleets/{id}/hosts           enroll a host (returns agent_token ONCE)
GET    /api/v1/fleets/{id}/hosts           list hosts
POST   /api/v1/tenants/{id}/repos          register a Kopia repo
POST   /api/v1/tenants/{id}/jobs           create a recurring job
GET    /api/v1/tenants/{id}/jobs           list jobs
GET    /api/v1/tenants/{id}/audit          last 100 audit entries

POST   /api/v1/agent/heartbeat             agent → controller (X-Agent-Token)
POST   /api/v1/agent/job-status            agent → controller (X-Agent-Token)

GET    /healthz                            liveness
```

Operator endpoints require the `X-API-Key` header. Phase 5
(kombify-TechStack) replaces the API key with an OIDC subject from
PocketID.

## Running locally

The controller is hosted by [`cmd/stackkit-backup-controller`](../../cmd/stackkit-backup-controller/),
which wires this package's `Server.Handler()` onto an HTTP listener
plus an in-process `Scheduler`:

```bash
BACKUP_CONTROLLER_API_KEY=$(openssl rand -hex 32) \
  go run ./cmd/stackkit-backup-controller --port 8083
```

In this scaffold the binary uses the in-memory `Store` and `JobQueue`,
so state is process-local. That is sufficient for the kombify-TechStack
team to integrate against a real endpoint while the Postgres / NATS
implementations land in follow-up PRs — neither swap requires
re-shaping the HTTP API.

The package's own unit tests are the second exercise path:

```bash
go test ./internal/backup-controller/...
```

The agent counterpart (`cmd/stackkit-backup-agent/`) ships a stub that
prints a clear "controller endpoint not configured" error so users can
see the planned surface; see that binary's README.

## What lands in the follow-up PR

1. Replace `NewMemoryStore()` with a `pgx`-backed implementation that
   executes `migrations/001_init.sql` against the configured database.
2. Replace `NewMemoryQueue()` with a NATS JetStream-backed
   implementation (durable streams `backup.jobs`, `backup.status`).
3. Add `cmd/backup-controller/main.go` (or extend
   `cmd/stackkit-server/main.go`) to host the HTTP server with TLS,
   request-ID, rate-limit middleware to match `internal/api`.
4. Swap the cron field parser in `scheduler.go` for a real cron lib
   (`github.com/robfig/cron/v3`) with full expression support.
5. Implement OIDC middleware against PocketID for operator endpoints.
6. Hash agent tokens with sha256 before persisting (the in-memory store
   stores them verbatim because it has no threat model).
