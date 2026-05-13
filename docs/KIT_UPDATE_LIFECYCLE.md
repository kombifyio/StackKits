# Kit-Update-Lifecycle — North-Star Reference

> **Canonical landing page** for the StackKits update model. Engineers and operators asking "how does kit-update work?" should find this first.
> **ADR:** [ADR-0018](ADR/ADR-0018-kit-update-lifecycle.md). Phase execution is tracked in Beads and summarized in [ROADMAP.md](../ROADMAP.md).

## TL;DR

- **Three phases** capture the lifecycle: Phase 1 (Single-Node Base Kit, **LIVE**), Phase 2 (Multi-Node Rolling, planned), Phase 3 (Auto-Promotion via demand-signal, planned).
- **Three pillars** carry every update: **Dual-Level Channels** (kit + module, `edge`/`beta`/`stable`), **Compatibility Resolver** (server-side, fallback `stable > beta > edge`), **Atomic Snapshot** (Kopia + tfstate-copy, mandatory pre-apply).
- **Surfaces involved per upgrade**: CUE contracts -> Render-managed Postgres (`sk_*` registry) -> kombify-Administration channel-promotion endpoints -> StackKits CLI -> Kopia + OpenTofu on the node -> Admin server-side mirror (`sk_node_deployment`).
- **Production status (2026-05-08)**: Migrations 000107-109 applied to Render; Admin endpoints + UI shipped; CLI sub-commands `kit upgrade`, `kit upgrade rollback`, `doctor --check-updates` shipped; operator runbooks shipped. VM-Smoketest + coverage hebung still pending.
- **Non-negotiable invariants**: CUE is module-contract SSoT, DB is version+channel+lineage authority, `contract_hash` is the parity gate, Kopia repo is mandatory pre-condition (no `--skip-snapshot`), every channel-promotion is audit-evented.

## Lifecycle Diagram

```
+---------------------+   +----------------+   +-------------------+   +-------------------+
| Discovery (ADR-0017)|-->| Eval / Curate  |-->| Catalog: sk_module|-->| Module Release    |
| (kombify-Agents)    |   | (engineer)     |   | sk_module_version |   | (channel = edge)  |
+---------------------+   +----------------+   +-------------------+   +-------------------+
                                                                                 |
                                                                                 v
+---------------------+   +-------------------+   +-------------------+   +-------------------+
| Operator: stackkit  |<--| Resolver         |<--| Channel Promotion |<--| Kit Composition   |
| kit upgrade         |   | /v1/sk/compat/   |   | (Admin UI -> API) |   | sk_stackkit row  |
|                     |   | resolve          |   | edge -> beta ->   |   | (sk_kit_modules)  |
+---------------------+   +-------------------+   | stable            |   +-------------------+
        |                                          +-------------------+
        v
+---------------------+   +-------------------+   +-------------------+   +-------------------+
| Pre-flight:         |-->| Atomic Snapshot   |-->| tofu apply        |-->| PATCH Admin       |
| Kopia configured?   |   | (Kopia + tfstate  |   | (new templates)   |   | sk_node_deployment|
| contract_hash diff? |   | + manifest.yaml)  |   |                   |   | (server mirror)   |
+---------------------+   +-------------------+   +-------------------+   +-------------------+
        |                          |
        v                          v
   ABORT on fail            Rollback path:
                            kit upgrade rollback
                            -> tfstate restore
                            -> kopia restore
```

Responsibilities:
- **Discovery -> Catalog**: kombify-Agents (ADR-0017) + Engineer curation.
- **Catalog -> Composition -> Promotion**: kombify-Administration registry handlers + Admin UI (channel-promote button).
- **Resolver**: Admin server-side, computed from `sk_kit_module_compat` view.
- **CLI flow**: StackKits CLI on the node (Kopia + OpenTofu, atomic snapshot).
- **Mirror**: Best-effort PATCH back to Admin so the multi-node fleet view is current.

## Three Pillars

### 1. Dual-Level Channels

Both `sk_stackkit` (Phase-1 kit-version carrier) and `sk_module_version` carry a `release_channel` (`edge`/`beta`/`stable`). Promotion is **manual** today (Admin-UI button) and **always audit-evented** (`sk_stackkit_audit_log.action='channel_promote'`, `target_kind in {'stackkit','module'}`, `actor`, `reason`). See [ADR-0018 Section 1](ADR/ADR-0018-kit-update-lifecycle.md#1-dual-level-release-channels). Auto-promotion is explicitly Phase 3.

### 2. Compatibility Resolver

When an operator chooses `--kit-channel=stable`, the resolver picks each module-version using the fallback hierarchy `stable > beta > edge`, with explicit `--module-channel=<c>` override. Each picked module carries a `reason in {'matched','fallback','override'}` annotation that the CLI surfaces before apply. Server endpoint: `GET /api/v1/sk/compat/resolve`. Source view: `sk_kit_module_compat`. See [ADR-0018 Section 2](ADR/ADR-0018-kit-update-lifecycle.md#2-compatibility-resolver).

### 3. Atomic Snapshot (Kopia + tfstate)

Every `stackkit kit upgrade` performs a two-stage snapshot before `tofu apply`:

- **Stage 9a (Kopia)**: snapshot every persistent volume, tag `pre-update-<ts>-<old-kit-version>`.
- **Stage 9b (tfstate)**: copy `deploy/terraform.tfstate` to `.stackkit/snapshots/<ts>-<old-kit-version>/state.tfstate`.
- **Stage 9c (Manifest)**: write `manifest.yaml` containing `kopia_snapshot_id`, `tofu_state_path`, old/new versions, channel-map.

If 9a or 9b fail, `tofu apply` is **refused**. Kopia repo configuration is a **mandatory pre-condition**, no override flag exists. See [ADR-0018 Section 3](ADR/ADR-0018-kit-update-lifecycle.md#3-atomic-snapshot-vor-apply-tofu--kopia).

## Surfaces

| Surface | Files / Endpoints | Role |
|---|---|---|
| **CUE** | [`base/iac-defaults.cue`](../base/iac-defaults.cue), [`base/tool_categorization.cue`](../base/tool_categorization.cue), [`base-kit/stackfile.cue`](../base-kit/stackfile.cue) | Module-contract source-of-truth. `#IaCDefaults`, `#ToolType`, `#ToolCategory`. |
| **DB** | `sk_stackkit`, `sk_module_version`, `sk_node_deployment`, view `sk_kit_module_compat`, audit `sk_stackkit_audit_log` (kombify-DB) | Version + channel + lineage authority. Migrations 000107-109 (LIVE on Render). |
| **Admin API** | `frontend/src/lib/server/sk/channel-service.ts` (kombify-Administration), routes `/api/v1/sk/registry/stackkits/[id]/channel`, `/api/v1/sk/registry/modules/[id]/versions/[vid]/channel`, `/api/v1/sk/compat/resolve`, `/api/v1/sk/node-deployments` | Channel promotion + resolver + node-deployment mirror. |
| **CLI** | [`cmd/stackkit/commands/kit_upgrade.go`](../cmd/stackkit/commands/kit_upgrade.go), [`cmd/stackkit/commands/kit_upgrade_rollback.go`](../cmd/stackkit/commands/kit_upgrade_rollback.go), [`cmd/stackkit/commands/doctor.go`](../cmd/stackkit/commands/doctor.go) | Operator-facing entry points. |
| **Snapshot** | [`internal/snapshot/atomic.go`](../internal/snapshot/atomic.go), [`internal/snapshot/kopia.go`](../internal/snapshot/kopia.go) | Kopia wrapper + atomic-snapshot orchestration. |
| **Resolver client** | [`internal/registry/channel_resolver.go`](../internal/registry/channel_resolver.go) | CLI-side client for `/v1/sk/compat/resolve`. |
| **State** | `pkg/models/DeploymentState` (`KitVersionID`, `KitSemver`, `KitChannel`, `LastSnapshotDir`) | Node-local SSoT, mirrored to Admin best-effort. |
| **Runbooks** | [`docs/runbooks/kit-upgrade.md`](runbooks/kit-upgrade.md), [`docs/runbooks/kit-rollback.md`](runbooks/kit-rollback.md) | Operator procedure. |

## Phase Roadmap

| Phase | Scope | Status | Reference |
|---|---|---|---|
| **Phase 1** | Single-Node Base Kit. Channels, resolver, atomic snapshot, CLI, runbooks. | **LIVE** (Render migrations 000107-109 applied; Admin endpoints + UI shipped; CLI shipped; runbooks shipped). | ADR-0018 |
| **Phase 2** | Multi-Node Rolling Update (`stackkit cluster upgrade`). Master-First, Worker-Drain, per-node atomic snapshots, cancel-safe stop. | Planned. Requires Phase-1 production hardening + BreakGlass cluster topology shipped. | Beads |
| **Phase 3** | Auto-Promotion via demand-signal (`edge -> beta -> stable`). Optional AI-assisted channel-mismatch resolution. | Planned. Requires Phase-2 + ADR-0017 Phase 4b demand-signal definition. | Beads |

Phase 1 still has open follow-ups (VM-Smoketest, test-coverage hebung) tracked in [ADR-0018 Implementation Status](ADR/ADR-0018-kit-update-lifecycle.md#implementation-status-kit-update-phase-1).

## Operator Quick-Start

```bash
# 1. Pre-condition: configure Kopia (mandatory, ADR-0018 section 3)
stackkit backup configure --repo=local:/var/lib/kopia

# 2. See what is available
stackkit doctor --check-updates

# 3. Upgrade to the current stable
stackkit kit upgrade --to=channel:stable --auto-approve

# 4. If something regresses, roll back
stackkit kit upgrade rollback --auto-approve
```

Details, failure modes, and timing expectations: [`docs/runbooks/kit-upgrade.md`](runbooks/kit-upgrade.md), [`docs/runbooks/kit-rollback.md`](runbooks/kit-rollback.md).

## Cross-Repo Surfaces

- **kombify-DB** — `sk_*` tables and the migrations that introduced them:
  - [`migrations/000107_sk_release_channels.up.sql`](https://github.com/KombiverseLabs/kombify-DB/blob/main/migrations/000107_sk_release_channels.up.sql) — `release_channel` + `released_at` on `sk_stackkit` + `sk_module_version`, audit triggers, `target_kind` on `sk_stackkit_audit_log`.
  - [`migrations/000108_sk_node_deployment.up.sql`](https://github.com/KombiverseLabs/kombify-DB/blob/main/migrations/000108_sk_node_deployment.up.sql) — server-side mirror of node deployment state.
  - [`migrations/000109_sk_compatibility_resolver_view.up.sql`](https://github.com/KombiverseLabs/kombify-DB/blob/main/migrations/000109_sk_compatibility_resolver_view.up.sql) — `sk_kit_module_compat` view.
- **kombify-Administration** — Admin endpoints + UI. See [`docs/STACKKITS_CHANNEL_PROMOTION.md`](https://github.com/KombiverseLabs/kombify-Administration/blob/main/docs/STACKKITS_CHANNEL_PROMOTION.md) for the endpoint surface and UI pages.

## Architectural Invariants

- CUE is the source-of-truth for module-contracts; DB stores hashes + lineage but never overrides CUE.
- DB is the authority for versions, channels, and lineage; `release_channel` is queryable on every version row.
- `contract_hash` is the parity gate — apply aborts on drift between DB-recorded hash and on-disk CUE.
- Kopia-repo is a mandatory pre-condition for `kit upgrade`; there is no `--skip-snapshot` override.
- Channel promotion is always audit-evented (`action='channel_promote'`, plus `target_kind`, `actor`, `reason`).
- Resolver fallback order is fixed: `stable > beta > edge`. Operator override only via explicit `--module-channel=<c>`.
- Phase-1 is single-node only. Multi-node deployments must wait for Phase 2 or upgrade per node manually.
- Atomic-snapshot is two-stage (Kopia + tfstate) plus a manifest. Both stages must succeed before apply runs.
