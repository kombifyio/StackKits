---
adr: ADR-0051
title: The server MCP reaches the host through a secret-free host observation snapshot and an effect-classified host-exec spool
status: Accepted
date: 2026-10-16
last_verified: 2026-11-27
---

# ADR-0051: The server MCP reaches the host through a secret-free host observation snapshot and an effect-classified host-exec spool

**Decision owner:** Marcel (`D-2026-09-24-01`, `D-2026-11-20-06`, `D-2026-11-26-08`, `D-2026-11-27-09`, `D-2026-11-27-20`).
**Related:** ADR-0044 (Core binary mount in `stackkit-server`), ADR-0054 (Single Core engine & Coolify extensibility), `STACKKITS-PRODUCT-BOUNDARY-STANDARD.md`.

## Context

ADR-0044 runs `stackkit-server` as an unprivileged, capability-free Core container with an empty working directory. Giving `stackkit-server` either the Docker socket, a GET-only Docker socket-proxy (which still exposes container `Env` secrets via `/containers/<id>/json`), or a read-only workspace mount containing `.env` files would expose application secrets if the container or an agent credential were compromised.

At the same time, autonomous AI-agent self-healing and external agent operation require secret-free diagnostics and bounded reversible repairs aligned with the uniform open-default external-agent rule (`D-2026-11-26-08`, `D-2026-11-27-09`) and the six human-approval guards (`D-2026-11-20-06`).

## Decision

Two strictly separated host-interaction mechanisms replace both the read-only workspace `.env` mount and the GET-only Docker socket-proxy:

1. **Read-only host observation — Host-Side Secret-Free Observation Snapshot:**
   - A host-side oneshot (`stackkit diagnose --write-snapshot`, driven by `stackkit-watchdog.timer` and on-demand spool refresh) gathers typed host state (`core_health`, `app_health[]`, `ext_health`, `live_drift[]`, `tls_status`, `disk_ram_clock`, `backup_age`, `signatures[]`, `blast_radius`, plus redacted log tails for Coolify's `kombify-agent-apps` project) and writes a single redacted, secret-free JSON snapshot file mounted read-only into `stackkit-server`.
   - `stackkit-server` never mounts app `.env` files, custody directories, or any Docker socket / GET-only Docker proxy. Compromise of `stackkit-server` cannot read application secrets or container `Env` variables.
   - Failure-signature matching uses one versioned declarative table (`internal/diagnose/playbooks.cue`) shared across CLI, `/mcp`, Watchdog, and docs.

2. **Mutating operations & self-healing — Effect-Classified `host-exec` Spool & Watchdog:**
   - Mutating actions never execute inside `stackkit-server`. The server writes a signed job record (agent ID, operation ID, canonical argv, generation token, approval ID when required, digest) into a host-owned spool directory consumed by a host-side `stackkit host-exec` oneshot under the unified node `action_lease` (`action_lease_linux.go`, `confinedfs` flock + generation token).
   - **Open Default (Reversible, Non-Destructive Effects — No Per-Call Approval; User Can Narrow Anytime):**
     Aligned with `D-2026-11-26-08` and `D-2026-11-27-09`, the allowlisted reversible repair effects execute without per-call human approval by default:
     - `restart-service`
     - `reconcile-compose`
     - `reload-router`
     - `renew-tls`
     - `retry-appsetup`
     - `reconcile-coolify-proxy`
     All open-default repairs are strictly bounded by a circuit breaker (**max 2 auto-repairs per signature per hour**), generation fencing, durable receipts, unified `action_lease` (no agent or watchdog may clear an active lease), and user-configurable narrowing.
   - **Guarded / Excluded from Open Default (`D-2026-11-20-06` Human-Approval Guards):**
     - `gc-dangling-images` is **excluded** from open default (deletion is irreversible on an offline node and `internal/docker/client.go` also prunes build cache; requires explicit owner approval unless local pinned rollback recovery is proven).
     - Destructive writes, cost-bearing actions, volume/database wipes, clearing active leases/locks, rotating `APP_KEY` or credentials, restoring over live data, and widening ingress/firewall (including binding a new public domain) always require fresh explicit owner approval verified from `.stackkit/mcp/agents.json` before `host-exec` executes.
   - **Host-Side Watchdog (`stackkit-watchdog.timer`, 5-min oneshot):**
     Runs host-side under `action_lease` and the same circuit breaker. In Connected mode, uses a pinned fallback resolver if AdGuard dies; in Offline/Local-Only mode, uses only a local LAN/host fallback resolver or reports `degraded` — never public DNS. Stops immediately on stale evidence, disk-full past safe floor, missing/corrupt custody, clock skew beyond threshold, or tripped circuit breaker, emitting a redacted incident envelope over `/mcp` (or State Console offline).

## Explicit Security Trade-Off

Moving reversible non-destructive repairs from per-call owner approval to open default trades away the earlier property *"a leaked agent token cannot trigger any host restart/reconcile"* in exchange for autonomous self-healing under `D-2026-11-26-08` and `D-2026-11-27-09`. The blast radius of a leaked token is strictly bounded by:
- zero secret read access (redacted snapshot only, no `.env`/Docker `Env`/custody mounts),
- closed allowlist of reversible non-destructive effects,
- circuit breaker (max 2 auto-repairs/signature/hour) and active-lease refusal,
- durable tamper-evident receipts and instant agent revocation,
- mandatory fresh human approval (`D-2026-11-20-06`) for every destructive, cost-bearing, custody, lock-clearing, restore, or ingress-widening operation,
- user ability to narrow any open-default operation back to approval-required at any time.

## Consequences

- `stackkit-server` drops both the RO workspace `.env` mount and any GET-only Docker socket-proxy.
- Coolify `get_logs` (which requires `read:sensitive` in Coolify 4.7.5) is dropped from the Coolify MCP facade; container logs for `kombify-agent-apps` are served through this host-side redacted observation snapshot instead.
- Acceptance requires real Linux VM verification (`rack-kvm` fault-injection lane: AdGuard killed, disk full, clock skew, Coolify proxy drift, interrupted apply, revoked agent refusal, and approval-required guard enforcement).
