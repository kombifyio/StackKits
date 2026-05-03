# ADR-0010: Database-First StackKit Registry

**Status:** Accepted
**Date:** 2026-04-18
**Resolves:** StackKits has no queryable source-of-truth for the operator; tool-catalog split (`admin_sk_tools` vs `content_stackkit_tools`); no tenant-deployment lineage; AI evaluations and CUE contracts drift because there is no common persistence layer
**Cross-ref:** [ADR-0008 CUE Decision Logic](./ADR-0008-cue-decision-logic.md) · [ADR-0009 Three-Tier Provisioning](./ADR-0009-three-tier-provisioning.md) · [Review doc](../plans/2026-04-18-db-first-review.md) · [Roadmap](../plans/2026-04-18-db-first-roadmap.md)

---

## Context

V5 (ADR-0007) and V6 (ADR-0008 + ADR-0009) anchored StackKits on CUE modules. CUE is authoritative for *contracts* (`requires`, `provides`, `contexts`, `compute_tiers`, `settings`). What CUE does **not** answer:

1. Which version of each module is currently released? What's its `contract_hash`?
2. Which tools exist in our catalog? Which ones are evaluated? Which ones are referenced by StackKit modules?
3. Which tenants deployed which StackKit-version? Which Postgres / Valkey instances were provisioned for them? Where are the secrets?
4. Which free-text wizard intents came in, and which are recurring enough to promote to a curated module (Tier-3)?

Two catalogs exist today: `admin_sk_tools` (rich: evaluation, changelogs, CVEs, AI assessment) and `content_stackkit_tools` (flat: name/layer/category/image). They never reconcile. The wizard produces answers that are resolved in-memory and then discarded. Tenant provisioning is manual.

The kombify platform uses Render Managed Postgres 17 + Valkey 8 across all services. kombify-Administration already owns the operator UI (SvelteKit 2 + Prisma + Auth0). kombify-DB is the single-repo migrations + seed-jobs + sqlc/Prisma home.

V6 needs a *cockpit*: a DB-backed registry that operators read from, the CLI synchronises to, and tenant provisioning drives off.

## Decision

Adopt a **hybrid, database-first registry** ("Variant B" from the review) as the V6 source of truth for catalog, composition, and tenant provisioning. CUE remains authoritative for module contracts.

### Split of authority

| Concern | Authority | Why |
|---|---|---|
| Module contracts (`requires`/`provides`/`contexts`/settings definitions) | CUE (`modules/*/contract.cue`) | ADR-0008 — unification + disjunctions + constraints belong in CUE |
| Module-version lineage, `contract_hash`, release timeline | Postgres (`sk_module_version`) | Operators need SQL queryability; CI must verify hash parity |
| Tool catalog (identity, evaluation, versions, CVEs) | Postgres (`sk_tool` + `sk_tool_version` + `sk_tool_evaluation`) | Single catalog eliminates the `admin_sk_tools` / `content_stackkit_tools` split |
| StackKit composition (which modules in which role) | Postgres (`sk_stackkit` + `sk_stackkit_module`) | Admin-UI needs to render and edit this; CUE stays the release spec for each module inside |
| Wizard runs + intents | Postgres (`sk_wizard_answer` + `sk_wizard_intent`) | Needed for Tier-2 routing (ADR-0009) and Tier-3 promotion |
| Tenant-deployment lifecycle + DB bindings | Postgres (`sk_tenant_deployment` + `sk_tenant_db_binding`) | Required for per-tenant DB provisioning; Render resource-IDs need a queryable home |
| Telemetry / pattern promotion | Postgres (`sk_intent_telemetry` + `sk_tier3_promotion`) | ADR-0009 §Tier-3 — evidence lives in SQL |

### Parity contract: `contract_hash`

Every `sk_module_version` row carries a `CHAR(64)` SHA256 of the canonical-JSON rendering of the module's CUE contract. This hash is:

1. Computed by `stackkit module release` from `cue eval -c modules/<slug>`.
2. Verified on every PR by `stackkit module verify-db` (CI guard).
3. Checked at `stackkit apply` time: the hash of the CUE bundle on disk must equal the hash of the version the tenant-deployment requested.

If the hashes diverge, the build fails. This is how DB-truth and CUE-truth stay in sync without either side overwriting the other.

### Table-prefix convention

All StackKits-owned tables use `sk_` (e.g. `sk_tool`, `sk_module`, `sk_stackkit`, `sk_wizard_answer`, `sk_tenant_deployment`). Legacy `admin_sk_*` and `content_stackkit_*` tables are kept read-only for one release cycle, then dropped.

This deviates from the existing `admin_sk_*` prefix intentionally: the new tables serve both Administration (operator UI) and StackKits (CLI) and are no longer Administration-internal. `sk_` is shorter and signals shared ownership. Added to [DATA-ARCHITECTURE.md](../../../kombify%20Core/standards/DATA-ARCHITECTURE.md) §Table prefixes.

### Cross-repo impact

| Repo | Change |
|---|---|
| `kombify-DB` | Migrations 000033–000037 (5 new migration pairs). Seed-job framework at `seed/modules/<slug>/`. Prisma + sqlc models regenerated. |
| `kombify-Administration` | New Prisma models for `sk_*`. New REST endpoints under `/api/v1/sk/*`. Admin-UI pages: `/sk-tools`, `/sk-modules`, `/sk-stackkits`, `/sk-tenants`, `/sk-intents`. Provisioner worker in `jobs/`. |
| `kombify-StackKits` | New CLI sub-commands: `stackkit module release`, `stackkit module verify-db`, `stackkit apply --tenant-deployment <uuid>`. Wizard posts answers to Admin API before resolution. |
| `kombify Core` | [DATA-ARCHITECTURE.md](../../../kombify%20Core/standards/DATA-ARCHITECTURE.md) (new) documents prefix convention, tenant-DB naming, Doppler path convention. |

### Per-tenant DB naming

`{module_slug}_{tenant_slug}` (e.g. `immich_acme`). Connection string stored in Doppler at `kombify-tenant-{tenant_id}/prd/{module_slug}_{engine}_url`. Render resource-id stored in `sk_tenant_db_binding.render_resource_id`.

### Multi-tenancy pattern

Unchanged from Administration's current approach: organization-based, application-layer filtering. No Postgres RLS, no schema-per-tenant, no separate DB cluster per tenant. Per-tenant *application* DBs (Immich, Vaultwarden, …) are separate Render managed instances; the `sk_*` registry itself is shared across tenants in the platform DB.

## Alternatives Considered

| Alternative | Why rejected |
|---|---|
| **Variant A — Full DB-First, generate CUE from DB rows** | Round-trip risk: two authorities for the same contract means any drift fork-bombs. ADR-0008 has only just started binding CUE to Go; adding DB-as-second-source before that binding is solid would destabilise. |
| **Keep CUE + flat `content_stackkit_*`** | Tool-catalog split stays unreconciled; no tenant-deployment lineage; operator UI stays read-only on a schema that's fine-grained for modules but opaque for operations. |
| **Move everything (including contracts) to Prisma JSONB** | Loses the CUE unification / disjunction / constraint semantics ADR-0008 depends on. Admin-UI becomes tolerant of contract-violating input. |
| **One giant `stackkit_state` JSONB blob per tenant** | Operator queryability dies. Alerting ("which tenants have module X at version Y") becomes impossible in SQL. Postgres indexes on blob shapes are a bad long-term bet. |

## Consequences

### Positive

- **Single catalog** — `sk_tool` unifies evaluation + StackKit catalog. Admin-UI has one tool page with all facets.
- **Contract-verified releases** — `contract_hash` in CI guarantees CUE-disk = DB = deployed. Silent drift becomes impossible.
- **Tenant lineage** — every managed DB, every seed run, every lifecycle transition is a queryable row. Ops can answer "which tenants run Immich v1.120.1" in one SQL.
- **Wizard telemetry** — every `stackkit init` captured. Tier-3 promotion gets its evidence channel.
- **CUE stays pure** — module authors edit `.cue` files; nothing else. DB is downstream.
- **Foundation for Tier-2** — when the kombify-AI intent contract lands (ADR-0009 follow-up), `sk_wizard_intent` is already waiting.

### Negative

- **Two surfaces per change** — a module change lands in CUE *and* needs a release CLI run. Mitigation: `stackkit module release` is one command, wired into CI.
- **Temporary duplication** — legacy `admin_sk_*` + `content_stackkit_*` tables remain read-only for one release cycle while callers migrate. Mitigation: deprecation warnings on old endpoints; hard-cutoff date in roadmap.
- **Render cost visibility matters** — per-tenant DBs multiply bills. Mitigation: cost dashboard + per-tenant quota (max 8 DBs by default) in Phase 4.
- **Admin-UI work is real** — five new admin pages. Mitigation: staged over Phases 1–5 (~14 weeks) with a policy that a migration only lands once its read UI ships.

### Follow-up

- Roadmap execution order is Phase 0 → 1 → 2 → 3 → 4 → 5 (dependency-linear; see [roadmap §9](../plans/2026-04-18-db-first-roadmap.md)).
- Phase 6 (upgrade to Variant A — split JSON columns into relations) is **conditional** and only triggers if JSONB query load becomes a bottleneck.
- `sk_module_compatibility` matrix, visual CUE-debug mode, drift detection are post-GA.
- TechStack wizard migration is **out of scope** for this ADR; TechStack will consume the same `/api/v1/sk/*` endpoints when it ports.

## Governance

- Migrations 000033–000037 land in `kombify-DB` and are applied to the Render platform DB only after ADR-0010 is Accepted.
- `stackkit module verify-db` becomes a required PR check in kombify-StackKits once Phase 2 is merged.
- Admin-UI pages for Phases 1–4 are gated by feature flags (`FEATURE_SK_*`) so partial rollouts are safe.

---

## Rollout Status (Re-verified 2026-04-26)

The original 2026-04-18 status table flattened "Done" across all 5 phases.
Re-audit (2026-04-26) shows the picture is more nuanced — the data + API + UI
layers are real; some operational pieces are still open. Granular status:

| Phase | Layer | Real status (2026-04-26) | Evidence |
|---|---|---|---|
| 0 | ADR + standards + Beads epics | ✅ Done | This ADR + roadmap + DATA-ARCHITECTURE.md |
| 1 | `sk_tool*` schema + backfill | ✅ Done | `kombify-DB/migrations/000036_sk_tool_catalog.up.sql` |
| 1 | `/api/v1/sk/tools` endpoints + `/sk-tools` UI | ✅ Done | `kombify-Administration/frontend/src/routes/api/v1/sk/tools/`, `(admin)/sk-tools/` |
| 2 | `sk_module*` schema | ✅ Done | `migrations/000037_sk_module_registry.up.sql` |
| 2 | `stackkit module release` + `verify-db` CLI | ✅ Done | `cmd/stackkit/commands/module.go`; `internal/cue/canonical.go`; dry-run verified for traefik (hash `793a71e8…`) |
| 2 | Admin endpoint `POST /api/v1/sk/registry/modules/{slug}/versions` | ✅ Done | `frontend/src/routes/api/v1/sk/registry/modules/[id]/versions/+server.ts` |
| 2 | CI guard `verify-db --strict` wired | ⚠️ Workflow file exists; unverified whether mandatory check is enforced on PRs | `.github/workflows/module-release.yml` |
| 3 | `sk_stackkit*` + `sk_wizard_*` schema | ✅ Done | `migrations/000038_sk_stackkit_composition.up.sql` |
| 3 | `/api/v1/sk/wizard/answers`, `/sk/stackkits` endpoints | ✅ Done | `frontend/src/routes/api/v1/sk/wizard/answers/+server.ts` |
| 3 | `stackkit wizard report` CLI | ✅ Done | `cmd/stackkit/commands/wizard.go` |
| 4 | `sk_tenant_*` schema | ✅ Done | `migrations/000039_sk_tenant_provisioning.up.sql` |
| 4 | River-based provisioner worker (Render + Doppler) | ✅ Done | `kombify-Administration/jobs/internal/jobs/provision_tenant_deployment.go` |
| 4 | Seed-job framework (`kombify-DB/seed/modules/{slug}/`) | ❌ Open — directory absent; provisioner notes "seeding dispatched out-of-band" but the dispatch target does not yet exist | — |
| 4 | `stackkit apply --tenant-deployment <uuid>` CLI flag | ⚠️ `cmd/stackkit/commands/tenant_spec_fetch.go` exists; integration with `apply.go` to wait for `db_ready` not yet audited | — |
| 5 | `sk_intent_telemetry` + `sk_tier3_promotion` schema | ✅ Done | `migrations/000040_sk_intent_telemetry.up.sql` |
| 5 | Clustering surface | ⚠️ Implemented as `POST /api/v1/sk/intents/cluster` endpoint, not as a nightly worker. Lower-effort but no off-hours batch run | `frontend/src/routes/api/v1/sk/intents/cluster/+server.ts` |
| 5 | `vector(1536)` enrichment | ❌ Open — column conditional-create works; no embedding worker fills it | — |

### Audit notes (2026-04-26)

- **Migration header bugs fixed**: 000038/039/040 had stale `Migration 000035/036/037` comments from an earlier renumbering. Corrected without semantic change.
- **Embedded registry (Phase A from commit `a63a81e`)** complements this ADR cleanly. The CLI's `internal/registry/AutoClient` switches between `EmbeddedClient` (baked-in `registry_snapshot.json`, OSS-safe for the public mirror `kombifyio/stackKits`) and `RemoteClient` (Admin API). DB-first does not displace the embedded snapshot — it feeds it (snapshot is generated from CUE + DB).
- **Open work tracked separately**: seed-job framework, CI guard hardening, embedding worker, end-to-end test against live Render Postgres. Each is a sub-issue under `stk-400` / `stk-500` rather than a Phase-level blocker.

All five phases merged to `codex/data-architecture-contracts` across kombify-DB, kombify-Administration, and kombify-StackKits `main`.

## Related

- [ADR-0008](./ADR-0008-cue-decision-logic.md) — CUE Decision Logic (authoritative for contracts)
- [ADR-0009](./ADR-0009-three-tier-provisioning.md) — Three-Tier Provisioning (Tier-2/Tier-3 storage defined here)
- [ARCHITECTURE_V6.md](../ARCHITECTURE_V6.md) — V6 target architecture
- [Review doc](../plans/2026-04-18-db-first-review.md) — Variant A vs B analysis
- [Roadmap](../plans/2026-04-18-db-first-roadmap.md) — Phase-level execution plan
