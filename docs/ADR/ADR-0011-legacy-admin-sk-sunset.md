## ADR-0011: Legacy `admin_sk_*` Sunset Strategy

**Status:** Accepted
**Date:** 2026-04-26
**Resolves:** ADR-0010 created `sk_*` tables and unified the catalog, but
several kombify-Administration endpoints (Dashboard, Categories, Compliance,
Tool-CRUD, Tool-Evaluation, Search/Discovery) still read+write the legacy
`admin_sk_*` tables. The 000036 backfill copied data once but is not kept
in sync, so the operator UI shows different counts depending on which page
they look at.
**Cross-ref:** [ADR-0010 DB-first registry](./ADR-0010-db-first-stackkit-registry.md) ·
[kombify-DB DEBT_REVIEW_2026-04-21](../../../kombify-DB/docs/DEBT_REVIEW_2026-04-21.md)

---

### Context

The 2026-04-26 audit (see ADR-0010 §"Rollout Status (Re-verified 2026-04-26)")
found two parallel data worlds in kombify-Administration:

| Layer | Legacy table | New `sk_*` table | Status |
|---|---|---|---|
| Tool catalog | `admin_sk_tools` | `sk_tool` | Both populated; backfill in 000036 was one-shot |
| Tool versions | — | `sk_tool_version` | New only |
| Tool evaluations | `admin_sk_evaluation_runs` | `sk_tool_evaluation` | Both populated; backfill in 000036 was one-shot |
| StackKits | `admin_sk_stackkits` | `sk_stackkit` | Both populated; new is dominant |
| Categories | `admin_sk_categories` | *(missing)* | Only legacy |
| Search runs | `admin_sk_search_runs` | *(missing)* | Only legacy |
| Discovery results | `admin_sk_discovery_results` | `sk_discovery_result` | Both exist; usage split |
| Standards | `admin_sk_standards` | `sk_standard` | Both populated |
| Tool ↔ standard | `admin_sk_tool_standards` | `sk_tool_standard` | Both populated |
| Stackkit ↔ standard | `admin_sk_stackkit_standards` | `sk_stackkit_standard` | Both populated |
| Tool changelogs | `admin_sk_tool_changelogs` | *(missing)* | Only legacy |
| Tool alternatives | `admin_sk_tool_alternatives` | *(missing)* | Only legacy |
| Tool crawl data | `admin_sk_tool_crawl_data` | *(missing)* | Only legacy |
| Evaluation decisions | `admin_sk_evaluation_decisions` | *(missing)* | Only legacy |

Endpoint usage today:

- **NEW (`prisma.skTool` / `skModule` / `skStackkit` / `skWizardAnswer`)**
  `/api/v1/sk/registry/*`, `/api/v1/sk/wizard/*`, `/api/v1/sk/tenants/*`,
  `/api/v1/dashboard/stats`
- **LEGACY (`prisma.adminSk*`)**
  `/api/v1/sk/tools/*` (single-tool CRUD + AI assessment),
  `/api/v1/sk/dashboard`, `/api/v1/sk/categories/*`, `/api/v1/sk/compliance/*`,
  `/api/v1/sk/search/*`

Operator impact: a tool created in `/sk-tools` (Tool-Evaluation UI) lands in
`admin_sk_tools` and *does not appear* in StackKit Composition (which reads
from `sk_tool`). New evaluation scores update `admin_sk_tools.overall_score`
but `sk_tool.overall_score` stays frozen at the 000036-backfill value.

This is not a bug — both schemas work in isolation — but it makes the
operator's mental model brittle. ADR-0010 implicitly assumed legacy would be
sunset within "one release cycle" (line 97). That cycle has passed without
action.

### Decision

Adopt a **two-track sunset** rather than a single big-bang cutover:

1. **Track A — Bridge (immediate, ~1 week):** Sync trigger from `admin_sk_*`
   to `sk_*` for Tool, Stackkit, Standard tables. Operator-facing pages keep
   working; StackKit Composition starts seeing new tools within seconds.

2. **Track B — Endpoint cutover (incremental, ~6 weeks):** Migrate Admin
   endpoints from `prisma.adminSk*` to `prisma.sk*` table-by-table, in
   dependency order. Legacy tables become read-only mirrors, then dropped.

The two tracks run in parallel: Track A unblocks the operator
inconsistency immediately; Track B does the proper cleanup.

### Track A: Sync triggers (Migration 000041)

For each `admin_sk_*` row written, upsert the equivalent `sk_*` row.
Implemented as `AFTER INSERT OR UPDATE` triggers — invisible to existing code.

```sql
-- 000041_sync_legacy_admin_sk.up.sql (sketch)

CREATE OR REPLACE FUNCTION sync_admin_sk_tool_to_sk_tool() RETURNS TRIGGER AS $$
BEGIN
  INSERT INTO sk_tool (slug, name, description, vendor, category, website,
                       repo_url, license, license_spdx, image_ref, logo_url,
                       tags, is_evaluation_only, overall_score, latest_version, metadata)
  VALUES (
    NEW.slug, NEW.name, COALESCE(NEW.description,''), '',
    COALESCE((SELECT name FROM admin_sk_categories WHERE id=NEW.category_id), ''),
    COALESCE(NEW.website,''), COALESCE(NEW.github_url,''),
    COALESCE(NEW.license,''), COALESCE(NEW.license_spdx,''), '',
    COALESCE(NEW.logo_url,''), COALESCE(NEW.tags, '{}'::text[]),
    true, NEW.overall_score, COALESCE(NEW.latest_version,''),
    jsonb_build_object('legacy_admin_sk_tool_id', NEW.id::text,
                       'ai_summary', COALESCE(NEW.ai_summary,''),
                       'ai_assessment', COALESCE(NEW.ai_assessment, '{}'::jsonb),
                       'github_stars', NEW.github_stars,
                       'status_legacy', NEW.status::text)
  )
  ON CONFLICT (slug) DO UPDATE SET
    overall_score = EXCLUDED.overall_score,
    latest_version = CASE WHEN sk_tool.latest_version='' THEN EXCLUDED.latest_version ELSE sk_tool.latest_version END,
    metadata = sk_tool.metadata || EXCLUDED.metadata,
    updated_at = NOW();
  RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE TRIGGER tr_sync_admin_sk_tool_to_sk_tool
  AFTER INSERT OR UPDATE ON admin_sk_tools
  FOR EACH ROW EXECUTE FUNCTION sync_admin_sk_tool_to_sk_tool();

-- Analog: admin_sk_stackkits -> sk_stackkit, admin_sk_standards -> sk_standard,
-- admin_sk_tool_standards -> sk_tool_standard, admin_sk_evaluation_runs -> sk_tool_evaluation.
```

**Reverse direction is NOT triggered.** `sk_*` writes from the new endpoints
do not propagate back to `admin_sk_*`. Once a tool flows through `sk_tool`,
it is by definition ahead of the legacy table — back-syncing would race the
operator's edits in the legacy UI.

### Track B: Endpoint cutover (planned, no migration yet)

Per surface, the cutover work is:

| # | Endpoint | New Prisma calls | Net new models needed in `sk_*` |
|---|---|---|---|
| B1 | `/api/v1/sk/dashboard` | `skTool.count`, `skStackkit.count`, `skStandard.count`, `skToolEvaluationRun.findMany` | (none — partial cutover already possible; categories + search-runs left on legacy with marker) |
| B2 | `/api/v1/sk/tools/[id]` (CRUD + `/ai`) | `skTool` + `skToolEvaluation` | (none) |
| B3 | `/api/v1/sk/categories/*` | `skCategory` (new) | **Need** `sk_category` (Migration 000042) |
| B4 | `/api/v1/sk/compliance/*` | `skTool`, `skStandard`, `skToolStandard` | (none) |
| B5 | `/api/v1/sk/search/*` | `skSearchRun` (new), `skDiscoveryResult` | **Need** `sk_search_run` (Migration 000043) |
| B6 | Tool changelog/alternatives/crawl | `skToolChangelog` (new) etc. | **Need** Migration 000044 (3 tables) |

After B1-B6: drop `admin_sk_*` tables in Migration 000045.

### Sequencing

| Week | Track A | Track B |
|---|---|---|
| 1 | Migration 000041 sync triggers | B1 partial dashboard cutover |
| 2 | Verify drift = 0 in production | B2 tools/[id] cutover |
| 3 | — | B3 sk_category migration + endpoint |
| 4 | — | B4 compliance cutover |
| 5 | — | B5 sk_search_run migration + endpoint |
| 6 | — | B6 tool aux tables + drop legacy |

### Implementation status (2026-04-26)

The 1-day push covered Track A in full plus the high-value Track-B work.
Updated state:

| Task | Status | Evidence |
|---|---|---|
| **Track A — Migration 000041 sync triggers** | ✅ Shipped | `kombify-DB/migrations/000041_sk_sync_triggers.up.sql` |
| **B1 Dashboard cutover** | ✅ Shipped | `frontend/src/routes/api/v1/sk/dashboard/+server.ts` reads `prisma.skTool/skCategory/skStackkit/skStandard/skDiscoveryRun/skModule/skWizardAnswer/skToolEvaluation` |
| **B2 Tools/[id] + /ai cutover** | ✅ Shipped | `prisma.skTool` with metadata-expansion compatibility shim |
| **B3 Categories cutover (000042 + endpoint)** | ✅ Shipped | `kombify-DB/migrations/000042_sk_category.*.sql` + `frontend/src/routes/api/v1/sk/categories/[id]/+server.ts` |
| **B4 Compliance cutover (5 endpoints)** | ✅ Shipped | `prisma.skStandard/skToolStandard/skStackkitStandard` everywhere under `/sk/compliance` and `/sk/standards` |
| **B6 Tool-Aux migration 000043 + endpoints** | ✅ Shipped | `sk_tool_changelog/alternative/crawl_data` migration + `frontend/src/routes/api/v1/sk/tools/[id]/{changelogs,alternatives,crawl-data}/+server.ts` |
| **B5 Search/Discovery cutover (10 endpoints)** | ⏳ DEFERRED | See "Deferred work" below |
| **stackkit_tool / stackkit_health workflows (4 endpoints)** | ⏳ DEFERRED | See "Deferred work" below |
| **Tool workflow endpoints (8 endpoints — crawl, github, batch, status, seed-data)** | ⏳ DEFERRED | See "Deferred work" below |
| **Migration 000045 drop legacy** | 📦 PREPARED, NOT ARMED | `kombify-DB/docs/pending-migrations/000045-drop-legacy-admin-sk.md` (Markdown wrapper outside `migrations/` — copy SQL body into a real migration file when arming) |

### Deferred work (Track-B continued)

The following endpoints still call `prisma.adminSk*`. Track A sync triggers
keep their data consistent with `sk_*`, so this is *code cleanliness* rather
than a functional bug.

**Search / Discovery (10 endpoints)** — `frontend/src/routes/api/v1/sk/search/**`
deferred because:
- `sk_discovery_run.source` is a free-form `String`; `admin_sk_search_runs.source`
  is the typed `admin_sk_search_source` enum. The `toSearchSourceEnum` helper
  in `lib/server/sk-discovery-executor.ts` is tightly coupled to the legacy enum.
- `sk_discovery_run` does not carry `categoryId` (FK to `admin_sk_categories`);
  `category` is a string. Cutover requires endpoint contract change.
- `sk_discovery_result` lacks the `isPromoted` boolean; promotion state is
  inferred from `promotedToolId IS NOT NULL` and `dismissedAt`. Search-queue
  filtering would change semantics.

**StackKit ↔ Tool relation (4 endpoints)** —
`frontend/src/routes/api/v1/sk/stackkits/[id]/{+server.ts,health,tools/**}`
deferred because:
- `admin_sk_stackkit_tools` is a direct `stackkit_id ↔ tool_id` join.
- `sk_*` has no equivalent: tools are referenced by modules
  (`sk_module.primary_tool_id`), and modules are composed into stackkits
  (`sk_stackkit_module`). Cutover needs an architectural decision: should the
  Admin UI display modules-then-tools, or do we add a denormalized
  `sk_stackkit_tool` view?

**Tool workflow endpoints (8 endpoints)** — `tools/[id]/status`,
`tools/[toolId]/crawl/**`, `tools/[toolId]/github/**`, `tools/batch/**`,
`tools/seed-data`, `tools/[toolId]/stackkits` deferred because:
- These use `admin_sk_tool_status` enum + `crawl_status` + `evaluation_status`
  fields that have no first-class equivalent on `sk_tool`.
- Crawl + GitHub workflows write to `admin_sk_tool_crawl_data` + run external
  fetches — porting them needs the workflow code refactored, not just a
  Prisma swap.

### Migration 000045 arming criteria

Drop migration is intentionally suffixed `.disabled`. To arm:

1. `cd kombify-Administration/frontend && grep -rln "prisma\.adminSk" src --include="*.ts" | wc -l` returns `0`.
2. SK_DRIFT_RECONCILIATION.md health-check returns `drift = 0` for all entities for **7 consecutive days** in production.
3. Migration 000041 has been deployed > 7 days.
4. `pg_dump` of all `admin_sk_*` tables stored in `kombify-DB/snapshots/`.

When all four hold: copy the SQL body from
`kombify-DB/docs/pending-migrations/000045-drop-legacy-admin-sk.md` into
`.up.sql`, add a `.down.sql` (rebuild from snapshot), commit, deploy.

### Alternatives Considered

| Alternative | Rejected because |
|---|---|
| Big-bang cutover (rewrite all endpoints, drop legacy in one migration) | High risk: operator workflow breaks in lockstep with deploy. Catalog data is operationally important. |
| Keep both schemas forever (no sunset) | Brittle mental model; eventual data drift; double-write logic accumulates technical debt. |
| Reverse direction triggers (`sk_*` → `admin_sk_*`) | Race condition with legacy UI edits; defeats purpose of sunset. |
| Drop `admin_sk_*` immediately, force endpoints to refactor | Blocks daily Tool-Evaluation work for the team during refactor. |

### Consequences

#### Positive
- Operator inconsistency closes within 1 week (Track A).
- Endpoint cutover is incremental and reversible (Track B).
- Final state: single `sk_*` schema; `admin_sk_*` removed in ~6 weeks.

#### Negative
- Sync triggers add write-amplification (small — admin_sk_tools sees ~1 write/day in practice).
- Three new migrations needed (`sk_category`, `sk_search_run`, `sk_tool_changelog`/`alternative`/`crawl_data`).
- Six endpoint refactors needed in kombify-Administration.

#### Follow-up
- Track A must ship **before** Track B endpoint cutovers begin. Otherwise
  a half-cutover endpoint reads stale `sk_*` data.
- Drift-check script lives at `kombify-DB/docs/SK_DRIFT_RECONCILIATION.md`
  — operator-runnable until legacy tables are dropped.

### Governance

- Migration 000041 (Track A) is reviewed by both StackKits and Administration owners.
- Each Track-B PR must update the table in §"Track B" of this ADR.
- Migration 000045 (drop legacy) requires explicit sign-off from Tool-Evaluation owner.
