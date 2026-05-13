# stackkit-backup-controller

Host binary for the kombify Backup-Controller HTTP API.

## Status: Phase-4 scaffold

This binary boots the controller defined in
[`internal/backup-controller/`](../../internal/backup-controller/) on a
real network port. It lets the kombify-TechStack web UI integrate
against a real endpoint while the **Postgres-backed Store** and **NATS
JetStream Queue** land in follow-up PRs. The HTTP API surface is the
contract; the backing implementations swap out behind the existing
`Store` and `JobQueue` interfaces without changing routes or handlers.

What runs today:

- HTTP server on `:8083` (configurable) hosting all routes from
  `Server.Handler()`
- In-memory `Store` (process-local — restarting the binary loses state)
- In-memory `JobQueue` (channel-backed)
- In-process `Scheduler` walking the job table once per minute and
  publishing due jobs onto the queue
- Audit log mirrored to slog

What does **not** run yet (follow-up PRs):

- Postgres persistence (`migrations/001_init.sql` is the schema)
- NATS JetStream durable queue
- OIDC operator auth (today: static `X-API-Key`)
- Live agent enrollment from `stackkit backup enroll`

## Usage

```bash
# Smallest viable invocation:
BACKUP_CONTROLLER_API_KEY=$(openssl rand -hex 32) \
stackkit-backup-controller

# All flags:
stackkit-backup-controller \
  --port      8083 \
  --api-key   $KEY \
  --log-level info \
  --scheduler=true
```

### Environment variables

| Var | Replaces flag | Notes |
|---|---|---|
| `BACKUP_CONTROLLER_PORT` | `--port` | Integer. Invalid values fall back to the flag default. |
| `BACKUP_CONTROLLER_API_KEY` | `--api-key` | Required. Empty value exits with a clear error. |
| `BACKUP_CONTROLLER_LOG_LEVEL` | `--log-level` | `debug` / `info` / `warn` / `error`. |

### Disabling the in-process scheduler

`--scheduler=false` lets you run multiple controller replicas behind a
leader-election lock without each one emitting duplicate job
dispatches. Default `true` because the most common deployment is a
single-replica systemd unit.

## API surface

See [`internal/backup-controller/README.md`](../../internal/backup-controller/README.md)
for the full route table. Operator routes require `X-API-Key`; agent
routes require `X-Agent-Token`. `GET /healthz` is unauthenticated.

## Distribution

Linux-only (amd64 + arm64), shipped as a standalone tarball via
goreleaser (`backup-controller` archive). Deployed on the kombify-
TechStack SaaS infrastructure, not on customer hosts.

## See also

- [`docs/BACKUP-ARCHITECTURE.md`](../../docs/BACKUP-ARCHITECTURE.md)
- [ADR-0016 — Single backup engine: Kopia](../../docs/ADR/ADR-0016-backup-single-engine-kopia.md)
- [ADR-0016](../../docs/ADR/ADR-0016-backup-single-engine-kopia.md)
- [docs/BACKUP-ARCHITECTURE.md](../../docs/BACKUP-ARCHITECTURE.md)
