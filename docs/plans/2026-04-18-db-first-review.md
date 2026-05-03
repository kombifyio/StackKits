# StackKits Database-First Review & Data-Model Concept

> **Date:** 2026-04-18
> **Status:** Draft — Discussion Proposal
> **Author:** Architecture review (autonomous)
> **Purpose:** Evaluate how much of the StackKits knowledge (modules, layers, tools, contracts, decision logic, wizard, provisioning) should live in Postgres (Prisma + sqlc + pgvector) in kombify-Administration, and propose two concrete schemas plus a migration path. Includes per-tenant database-provisioning derivation.

---

## 0. TL;DR

StackKits today is **code-first, data-thin**. The CUE modules carry the real semantics (requires/provides, contexts, tiers, perma/flexible settings, disjunctions), the Go CLI half-reads them (CUE-AUDIT Phase 3 still pending), and kombify-Administration has a **very flat** mirror (`content_stackkits`, `content_stackkit_tools`, 6 more tables, mostly free-text and JSON) that does **not** reflect the richness. The Admin-UI lists StackKits like wiki entries with tag soup; it cannot be the canonical source without schema work.

Two concrete proposals:

- **Variant A — Full DB-first** (`~22 tables`): every concept from CUE (layers, modules, contracts, tool-roles, disjunctions, contexts, compute tiers, settings-classification, wizard questions, three-tier provisioning, per-tenant DB specs) is modeled in Postgres. CUE becomes a generated artifact from DB. Heavy up-front, max observability, max tooling leverage.
- **Variant B — Balanced middle path** (`~12 tables`): DB owns the **catalog + evaluation + decisions + telemetry + tenant-provisioning**. CUE modules stay in the repo and are registered in the DB as immutable, versioned contracts (hash + metadata). Decision logic stays in CUE; DB mirrors outcomes. Much lower migration cost; preserves CUE's mathematical guarantees.

**Recommendation: Variant B, with an explicit upgrade path to A for specific domains** (starting with tool-catalog and wizard-telemetry). Rationale at the bottom.

**Per-tenant DB provisioning**: both variants support it via a dedicated `tenant_database_spec` + `tenant_database_binding` table pair. A StackKit deployment produces a materialized per-tenant list of databases + seed content; the admin-center becomes the orchestrator that provisions them on Render before the StackKit actually installs.

---

## 1. What I found

### 1.1 StackKits repo (kombify-StackKits)

**Where data lives today:**

| Concept | File / Location | Format |
|---|---|---|
| StackKit identity | `base-kit/stackkit.yaml`, `modern-homelab/stackkit.yaml`, `ha-kit/stackkit.yaml` | YAML |
| Layer concept | `base/layers.cue` | CUE |
| Module contract (requires/provides/contexts/permaSettings/flexibleSettings) | `modules/<name>/*.cue` | CUE |
| Tool-role (default/alternative/optional/addon) | `modules/<name>/*.cue`, `base-kit/services.cue` | CUE |
| Disjunctions (Traefik \| Caddy) | `modules/<name>/*.cue` with `*` default marker | CUE |
| Context / compute-tier defaults | `base/context.cue`, Go `internal/detect/` | CUE + Go (drift) |
| Wizard schema | `schemas/wizard.cue` | CUE |
| Resolution Hierarchy (10 steps) | docs/CONCEPTS.md + Go `internal/wizard/` | prose + Go (not CUE-bound) |
| Service catalog | `pkg/catalog/` Go + `base-kit/services.cue` | duplicated (drift, per CUE-AUDIT) |
| Instance stack-spec | `stack-spec.yaml` (per-deployment) | YAML |
| Generated artefacts | `build/…/main.tf`, `docker-compose.yml` | generated |
| Three-Tier Provisioning (Tier 1/2/3) | ADR-0009, no runtime representation yet | docs-only |

**Hard facts** (from CUE-AUDIT-AND-PLAN.md, own read):

- 14 curated modules, 4 layers, ~6 BaseKit use cases (V6 scope).
- CUE-Audit identified **11 gaps** between CUE and Go; 6 concepts are "completely ignored" by Go today (module dependencies, context overrides, settings classification, layer validation, deployment mode partial, resource limits parallel).
- Go CLI produces terraform + compose from hand-wired templates, not from CUE composition. Phase 3–5 of ADR-0008 is *pending*.
- Module contracts are declared in CUE but not enforced at `stackkit validate` time — they fail at runtime.

### 1.2 kombify-Administration

Prisma schema at `frontend/prisma/schema.prisma` (1687 lines). Relevant blocks:

**Existing `content_stackkit_*` tables** (lines 1014–1156):

```text
StackKit                     (content_stackkits)                       — id, name, description, version, state, tags
StackKitToolEntry            (content_stackkit_tools)                  — id, name, layer, category, website, image       <- GLOBAL TOOL CATALOG
StackKitTool                 (content_stackkit_tool_assignments)       — M:N, purpose, required, config JSON
StackKitSetting              (content_stackkit_settings)               — key, name, type, defaultValue JSON
StackKitPattern              (content_stackkit_patterns)               — name, content Text, tags                        <- free-text Markdown
StackKitDecision             (content_stackkit_decisions)              — ADR-style; status enum; alternativesConsidered JSON
StackKitValidationRule       (content_stackkit_validation_rules)       — expression String (free-text rule)
StackKitAuditLog             (content_stackkit_audit_logs)             — table/record/oldValues/newValues/changedFields
```

**Existing `AdminSk*` tables** (tool-evaluation domain):

```text
AdminSkCategory / AdminSkTool / AdminSkEvaluationRun / AdminSkStandard
AdminSkToolStandard / AdminSkSearchRun / AdminSkDiscoveryResult
AdminSkToolCrawlData / AdminSkToolChangelog / AdminSkToolAlternative
```

**Key observations:**

1. **Two parallel tool catalogs:** `StackKitToolEntry` (global, flat, used for stackkit composition) and `AdminSkTool` (richer, used for evaluation, crawling, changelog, alternatives). These two must be consolidated — today they are separate islands.
2. **No layer/context/tier structure:** the existing `StackKit*` tables have `layer` only on `StackKitToolEntry` and via a `StackKitLayerType` enum. No `Context`, no `ComputeTier`, no `ToolRole`, no `Dependency`, no `Disjunction`.
3. **Settings are a flat KV store:** `StackKitSetting` has `key/name/type/defaultValue` but no `classification (perma|flexible)`, no `visibility (user-facing|internal)`, no `validation regex/range`, no `scope (module-level|stackkit-level)`.
4. **Patterns are free-text Markdown:** `StackKitPattern.content String @db.Text`. Patterns are not structured and cannot be composed or queried.
5. **Validation rules are free-text expressions:** `StackKitValidationRule.expression String`. No engine evaluates them. Admin-UI shows them, no code enforces them.
6. **No evaluation-run history on StackKit level:** only tools are evaluated (`AdminSkEvaluationRun`). StackKits themselves have no scoring or health metric.
7. **No telemetry, no tier-3 promotion pipeline:** ADR-0009 describes it; DB has zero rows for it.
8. **No tenant-provisioning spec:** no table maps a StackKit to "here are the DBs, buckets, secrets you need for tenant X".

### 1.3 kombify-DB standards

- Postgres 17, pgvector + uuid-ossp, Prisma for TS, sqlc for Go, golang-migrate for raw SQL.
- UUID primary keys (`@default(uuid())`), snake_case tables, @map() field names.
- Domain prefixes: `admin_*`, `admin_sk_*`, `content_*`, `vector_*`, unprefixed platform core.
- Multi-tenancy: **organization-based at application layer**, no RLS, no schema-per-tenant. One shared schema, everyone queries with org_id filters.
- pgvector at 1536 dim (OpenAI text-embedding-3-small).
- Migration: golang-migrate up/down pairs, `bun x prisma migrate deploy` for Prisma side, `mise run dev` for local bootstrap.
- Render Managed Postgres 17 (Frankfurt), Doppler for secrets.

---

## 2. Design principles

Six principles that both variants obey:

1. **CUE mathematical guarantees must not be lost.** Whatever lives in DB must either (a) be projected from CUE or (b) be data for which CUE would be overkill (telemetry, history, observations, user-input captures).
2. **DB is the catalog, CUE is the contract.** The admin-center manages "what tools exist, what categories, what standards, what evaluation results, what decisions, what rollout telemetry". CUE continues to enforce "what combination is valid".
3. **One tool catalog.** Collapse `StackKitToolEntry` (currently 0-field evaluation context) into the richer `AdminSkTool` lineage. There must be one row per tool, with evaluation, alternatives, changelog, contract-version, and module-binding in one graph.
4. **Explicit over JSON.** Where a concept has a fixed shape (layer, context, role, tier, requires/provides, perma/flexible), it is a column or a relation, not a JSON blob. JSON is reserved for genuinely variable payloads (a specific module's per-instance overrides).
5. **Append-only for history, mutable for catalog.** Catalog rows (tools, modules, settings) are mutable with versioning. Events (evaluation runs, decisions, telemetry, provisioning outcomes) are append-only.
6. **Tenant provisioning is a derived artifact.** When a user deploys a StackKit, the admin-center *materializes* a deterministic list of databases, buckets, secrets, and seed jobs from the StackKit + chosen modules + tenant profile. That materialization is the hand-off to the provisioning engine (Render API + Doppler + Terraform).

---

## 3. Variant A — Full DB-First (~22 tables)

The DB becomes the executable source of truth for the StackKits domain. CUE is a generated artifact; the Go CLI reads from DB at build time (or from generated CUE, for offline builds).

### 3.1 Entity map

```
                         ┌───────────────┐
                         │ sk_stackkit   │ (catalog)
                         └──────┬────────┘
             ┌──────────────────┼──────────────────┬──────────────┐
             ▼                  ▼                  ▼              ▼
      ┌────────────┐     ┌──────────────┐   ┌────────────┐  ┌────────────┐
      │sk_layer    │     │sk_wizard_...│    │sk_rollout_ │  │sk_tenant_  │
      └─────┬──────┘     └──────┬──────┘    │policy      │  │db_binding  │
            │                   │           └────────────┘  └────────────┘
     ┌──────┴──────┐            │                                  │
     ▼             ▼            ▼                                  │
┌─────────┐  ┌──────────┐  ┌──────────┐                            │
│sk_module│  │sk_module │  │sk_wizard_│                            │
│         │  │_binding  │  │question  │                            │
└────┬────┘  └────┬─────┘  └──────────┘                            │
     │            │                                                │
 ┌───┼────┬───────┼─────────┬──────────┬──────────┐                │
 ▼   ▼    ▼       ▼         ▼          ▼          ▼                ▼
sk_ sk_ sk_    sk_module_ sk_module_ sk_module_ sk_module_    sk_tenant_db_
tool con- dis-  requires  provides   setting   compose_frag  spec (template)
cat- text  j.                                                      ▲
log                                                                 │
                                                           ┌────────┴──────┐
                                                           │sk_tool_catalog│
                                                           │(unified w/    │
                                                           │admin_sk_tool) │
                                                           └───────────────┘
```

### 3.2 Tables (summary, not final DDL)

**Catalog & structure (8 tables):**

| Table | Purpose | Key columns |
|---|---|---|
| `sk_stackkit` | Canonical StackKit definition | id, slug, name, version, lifecycle_state, description, summary, default_wizard_answer_id, latest_contract_hash, created_at, updated_at |
| `sk_layer` | Foundation / Platform / Application / Hardening | id, slug (`foundation`/`platform`/`application`/`hardening`), position, description |
| `sk_context` | local / cloud / pi | id, slug, detection_hint, description |
| `sk_compute_tier` | low / standard / high | id, slug, min_cpu, min_ram_mb, min_disk_gb |
| `sk_tool_catalog` | Unified tool catalog (merges StackKitToolEntry + AdminSkTool) | id, slug, name, vendor, category_id, website, repo, license, maturity_level, image_ref, vector_embedding, metadata |
| `sk_tool_version` | Tracked tool versions | id, tool_id, semver, released_at, changelog_url, is_recommended, cve_flags |
| `sk_module` | Curated CUE module registered in DB | id, slug, layer_id, trust_level (curated/ai-generated/community), latest_version, cue_source_path, contract_hash |
| `sk_module_version` | Immutable version snapshot | id, module_id, version, cue_content (text), contract_hash, compose_template (text), terraform_template (text), created_at |

**Module contracts (5 tables):**

| Table | Purpose | Key columns |
|---|---|---|
| `sk_module_requires` | "this module requires X" | id, module_id, required_module_id (or required_capability), is_hard, reason |
| `sk_module_provides` | "this module provides capability Y" | id, module_id, capability_slug, description |
| `sk_module_context_support` | which context/tier combos are allowed | id, module_id, context_id, compute_tier_id, is_supported, note |
| `sk_module_tool_binding` | which concrete tool+version this module ships (one module = one primary tool + optional companions) | id, module_id, tool_version_id, role (primary/sidecar/companion) |
| `sk_module_disjunction` | "exactly one of: dokploy | coolify | dockge" | id, slot_slug (`paas`, `auth`, `reverse-proxy`), module_id, is_default |

**Settings & variability (2 tables):**

| Table | Purpose | Key columns |
|---|---|---|
| `sk_setting_definition` | Typed parameter that can be configured (module- or stackkit-scoped) | id, owner_type (module/stackkit), owner_id, key, data_type, classification (`perma`/`flexible`), visibility (user/admin/internal), default_value JSON, validation_rule, layer_scope |
| `sk_setting_override` | Per-stackkit / per-tenant overrides | id, setting_id, scope (stackkit/tenant), scope_id, value JSON, reason, created_by |

**Composition (2 tables):**

| Table | Purpose | Key columns |
|---|---|---|
| `sk_stackkit_module` | which modules this StackKit includes | id, stackkit_id, module_id, role (default/alternative/optional/addon), position |
| `sk_stackkit_layer_config` | per-layer config overrides | id, stackkit_id, layer_id, config JSON |

**Wizard & intent (3 tables):**

| Table | Purpose | Key columns |
|---|---|---|
| `sk_wizard_question` | 4-question (+ freeText Q5) catalog from `schemas/wizard.cue` | id, slug, order_index, label, question_type (multi/choice/text), schema_json, validation_json |
| `sk_wizard_answer` | a completed wizard submission | id, tenant_id, stackkit_id, answers_json, derived_context_id, derived_tier_id, created_at |
| `sk_wizard_intent_freetext` | Tier-2 free-text captures (pending AI contract) | id, wizard_answer_id, text, classification, routed_to_ai_at, ai_proposal_id |

**Three-tier provisioning & telemetry (3 tables):**

| Table | Purpose | Key columns |
|---|---|---|
| `sk_tier2_proposal` | AI-assisted proposals | id, wizard_intent_freetext_id, proposed_module_cue (text), proposed_compose (text), trust_level, approval_status, approved_by, applied_at |
| `sk_tier2_telemetry` | per-intent counter for promotion | id, pattern_hash, pattern_label, intent_description, count, window_start, window_end |
| `sk_tier3_promotion` | log of intents that became curated modules | id, pattern_hash, promoted_to_module_id, decided_by, decided_at, rationale |

**Tenant provisioning (4 tables):**

| Table | Purpose | Key columns |
|---|---|---|
| `sk_tenant_database_spec` | template: "StackKit X needs these DBs per tenant" | id, stackkit_id, module_id, db_engine (postgres/valkey/sqlite/redis/s3), db_name_template, schema_name, owner_role, extensions, seed_job_slug, notes |
| `sk_tenant_database_binding` | materialized per tenant | id, tenant_id, spec_id, actual_db_url_secret_ref (Doppler), provisioned_at, status |
| `sk_tenant_deployment` | a StackKit rollout to a tenant | id, tenant_id, stackkit_id, wizard_answer_id, lifecycle_state, applied_contract_hash, render_resource_group |
| `sk_tenant_deployment_event` | append-only lifecycle events | id, deployment_id, event_type, payload JSON, created_at |

**Misc:**

- `sk_tool_evaluation_*` — keep and evolve existing `AdminSk*` tables under `sk_tool_*` namespace (tool-catalog unification).
- Audit log table carries forward (maybe rename `sk_audit_log`).
- Vector embeddings stay in `vector_embeddings` with `sourceType='stackkit'|'module'|'tool'`.

### 3.3 What CUE becomes

CUE files are **generated from DB** in Variant A:

- `scripts/dbproject-to-cue.go` reads DB, emits `modules/<slug>/contract.cue` (generated, do not edit).
- Hand-edits to CUE are forbidden in this variant; edits happen via admin-center (SvelteKit) or directly in DB.
- The Go CLI can read either the generated CUE (offline) or the DB (online mode).
- `sk_module_version.cue_content` is the canonical blob; `cue eval` still validates at `stackkit validate` time.

### 3.4 Pros / Cons

**Pros:**
- Full tool-level and module-level observability in one place.
- AI agents, dashboards, what-if tools can query the catalog without parsing CUE.
- Tier-3 promotion pipeline has first-class data.
- Per-tenant provisioning is derivable; no glue scripts.
- Evaluation, changelog, CVE, compatibility matrix — all queryable SQL.
- Supports `stackkit intent "..."` with strong context (AI has rich features to reason about).

**Cons:**
- Big migration: ~22 tables, major Go+TS refactor to read from DB.
- Double-maintenance window during cutover (CUE authored + DB pulled ↔ DB authored + CUE generated).
- CUE pattern-matching guarantees become dependent on our code-gen correctness.
- Admin-UI work is substantial: SvelteKit has today flat tables; we'd need rich forms for disjunctions, requires/provides editing, contract-aware setting types.
- Unproven: no one on the team has built a CUE-gen-from-DB pipeline before; risk of bugs in contract round-trips.

---

## 4. Variant B — Balanced middle path (~12 tables)

**Principle:** DB owns **catalog, evaluation, wizard telemetry, decisions, per-tenant provisioning**. CUE modules stay in the repo. The DB registers each CUE module-version with a hash and metadata (which layer, which tools, which contexts, which compute tiers, which settings). The Go CLI continues to read CUE; but every module-version is also reflected in DB for querying, visualization, evaluation, and tenant provisioning.

### 4.1 Tables (12)

**1. Unified tool catalog** (replaces both `StackKitToolEntry` and `AdminSkTool`)

```text
sk_tool               — id, slug, name, vendor, category, website, repo, license, maturity,
                        image_ref, metadata JSON, vector_embedding, created_at, updated_at
sk_tool_version       — id, tool_id, semver, released_at, is_recommended, cve_flags JSON
sk_tool_evaluation    — id, tool_id, run_id, score, dimensions JSON (extensibility/stability/etc.),
                        model_ref, prompt_version, evaluated_at
```

**2. Module registry** (the DB indexes CUE modules, does not own them)

```text
sk_module             — id, slug, layer (enum foundation/platform/application/hardening),
                        trust_level (curated/ai-generated/community), primary_tool_id FK,
                        cue_source_path, metadata JSON, created_at, updated_at
sk_module_version     — id, module_id, version, contract_hash (SHA256 of CUE files),
                        requires JSON, provides JSON, contexts JSON (array), compute_tiers JSON,
                        setting_classification JSON (perma vs flexible),
                        disjunction_slot TEXT NULL, is_default BOOL, registered_at
```

> `contract_hash` is computed by `stackkit` CLI at module-version time and pushed to Admin via `POST /api/v1/sk/modules/<slug>/versions`. It is immutable and references the exact CUE content that was released.
> The JSON columns are **indexes for query**, not contracts: the CUE is still authoritative; the JSON is what you query when rendering the admin-UI or deriving tenant DBs.

**3. StackKit catalog & composition** (replaces / extends `content_stackkit_*`)

```text
sk_stackkit           — id, slug, name, version, lifecycle_state (DRAFT/ALPHA/BETA/GA/DEPRECATED),
                        description, summary, latest_contract_hash, metadata JSON
sk_stackkit_module    — id, stackkit_id, module_id, module_version_id, role (default/alternative/optional/addon),
                        position, notes
sk_stackkit_decision  — id, stackkit_id, title, status, context, decision, consequences,
                        alternatives_considered JSON, related_adr_ref, decided_at
                        (≈ keep existing table, add adr_ref)
```

**4. Wizard & intent** (data; CUE schema stays the truth for shape)

```text
sk_wizard_answer      — id, tenant_id, stackkit_id, answers JSON (validated against schemas/wizard.cue),
                        derived_context, derived_compute_tier, created_at
sk_wizard_intent      — id, wizard_answer_id, intent_text, status (captured/routed/applied/rejected),
                        ai_proposal JSON NULL, approved_by, applied_at
                        (replaces the "free-text Q5 pending AI" capture)
```

**5. Three-tier telemetry & promotion** (new, doc-aligned)

```text
sk_intent_telemetry   — id, pattern_hash, pattern_label, count, window_start, window_end,
                        last_sample_at, promotion_candidate BOOL
sk_tier3_promotion    — id, pattern_hash, promoted_module_id, decided_by, decided_at, rationale
```

**6. Tenant provisioning** (the piece the user explicitly asked for)

```text
sk_tenant_db_spec     — id, module_version_id, db_engine (pg/valkey/redis/s3/sqlite),
                        db_name_template, schema_name, owner_role, extensions JSON,
                        seed_job_slug NULL, notes
sk_tenant_deployment  — id, tenant_id, stackkit_id, stackkit_version, wizard_answer_id,
                        applied_contract_hash, lifecycle_state, render_resource_group, started_at
sk_tenant_db_binding  — id, deployment_id, spec_id, actual_db_name, db_url_secret_ref (Doppler path),
                        provisioned_at, status (pending/ready/failed/retired)
```

**Total: 12 tables** (`sk_tool`, `sk_tool_version`, `sk_tool_evaluation`, `sk_module`, `sk_module_version`, `sk_stackkit`, `sk_stackkit_module`, `sk_stackkit_decision`, `sk_wizard_answer`, `sk_wizard_intent`, `sk_intent_telemetry`, `sk_tier3_promotion`, `sk_tenant_db_spec`, `sk_tenant_deployment`, `sk_tenant_db_binding` — OK, 15 if you count all three tenant-provisioning tables separately; call it "~12–15").

### 4.2 What CUE stays

- Every file currently in `modules/`, `base/`, `schemas/` stays.
- `stackkit validate` still enforces contracts via CUE.
- CUE Decision Logic (ADR-0008) is unaffected; Phase 3–5 binding proceeds as planned.
- When a module-version is released (via `stackkit module release <slug>@<semver>`), a new `sk_module_version` row is created with `contract_hash` and the extracted metadata JSON. This is the only point of contact.

### 4.3 Pros / Cons

**Pros:**
- Migration cost is **bounded**: 12–15 tables, most of them reshape what already exists (`content_stackkit_*`, `admin_sk_*`).
- CUE keeps its mathematical guarantees; no round-trip bugs.
- Admin-UI can finally show: catalog, evaluation runs, module dependency graphs, tenant deployments, telemetry — without parsing CUE.
- Per-tenant DB provisioning flows through a clean 3-table pipeline (`spec → deployment → binding`).
- Tier-3 promotion has structured telemetry.
- Tool-catalog unification removes current duplicate tables.
- Doesn't block ADR-0008 Phase 3 work; they are orthogonal.

**Cons:**
- JSON columns (`requires`, `provides`, `contexts`) are **convenient, not normalized** — some queries (e.g., "which modules require capability X") are `jsonb @>` instead of joins. Acceptable trade-off if query patterns are catalog-display not graph-traversal.
- CUE and DB can diverge if the `contract_hash` pipeline is broken. Mitigation: CI fails if `stackkit module release` skipped.
- The `stackkit intent` Tier-2 consumer still needs structured catalog info for the AI — JSON is OK but slightly clunkier than relations.

---

## 5. Per-tenant database provisioning (common to A and B)

This is the bit the user called out as "possibly highest complexity". Both variants implement it identically, with three tables:

### 5.1 Data model

```text
sk_tenant_db_spec                  ◄── per module-version, template
  (which DBs a module needs; names, extensions, seeds)

sk_tenant_deployment               ◄── per tenant x stackkit, runtime
  (a rollout — aggregates wizard answer, stackkit version, contract hash)

sk_tenant_db_binding               ◄── materialization
  (actual DB name, actual Render resource id, actual Doppler secret path)
```

### 5.2 Flow

1. **Author** adds a module `immich@1.100`. The module's `contract.cue` declares it needs a Postgres database + Redis. The release CLI inserts:
   - `sk_module_version.id=M` with `contract_hash=H`.
   - Two `sk_tenant_db_spec` rows keyed to `M`: `{engine: postgres, name_template: "immich_{tenant}", extensions: [pgvector,uuid-ossp], seed_job_slug: "immich-schema-init"}` and `{engine: redis, name_template: "immich_redis_{tenant}"}`.

2. **Tenant deploys** a StackKit containing `immich@1.100` via wizard.
   - Admin-center creates `sk_tenant_deployment{tenant_id=T, stackkit_id=S, applied_contract_hash=H, lifecycle_state=PROVISIONING_DB}`.
   - For each `sk_tenant_db_spec` linked to included module-versions: create `sk_tenant_db_binding{deployment_id=D, spec_id=…, status=PENDING}`.

3. **Provisioner worker** (new service or job in kombify-Administration):
   - Reads all PENDING bindings for the deployment.
   - Calls Render API to create a managed Postgres / Valkey / other (based on `engine`).
   - Stores resulting connection string in Doppler at `kombify-tenant-<T>/prd/<slug>_DB_URL`.
   - Runs seed job (a Go job with sqlc queries per seed-job-slug — e.g., `immich-schema-init` creates tables, `vaultwarden-init` inserts bootstrap config).
   - Updates binding to `status=READY`.

4. **Deployment handoff**: once all bindings are READY, `sk_tenant_deployment.lifecycle_state=PROVISIONING_DB → DEPLOYING_STACKKIT`. `stackkit apply` runs with env vars pointing to the created DBs (loaded from Doppler path).

5. **Teardown**: on `stackkit destroy`, bindings go `READY → RETIRED` after Render-resource deletion + Doppler secret purge.

### 5.3 What sits in code vs what sits in DB

| Concern | DB | CUE | Code |
|---|---|---|---|
| "immich needs postgres+redis" | `sk_tenant_db_spec` (generated from CUE at module release) | `modules/immich/databases.cue` (source of truth in Variant B) | — |
| Actual Render API call | — | — | `kombify-Administration/jobs/provision_tenant_db.go` |
| Schema seed | seed_job_slug column | — | `kombify-DB/seed/modules/<slug>.go` (sqlc generated) |
| Secret storage | `db_url_secret_ref` (Doppler path) | — | Doppler CLI call |

### 5.4 Naming & multi-tenancy strategy

Recommendation: **separate Render Postgres DB per tenant** (not schema-per-tenant, not row-level). Reasons:

- Module schemas (immich, vaultwarden, authentik) are defined independently of kombify tenants — letting them be full-owners of their DB avoids cross-schema migration pain.
- Simpler to back up / restore per-tenant.
- Render supports multiple managed DBs per project; pricing is per-DB but manageable.
- RLS would require every upstream module to cooperate with an org_id — they won't.

Exceptions: for lightweight tools (`uptime-kuma`, `whoami`, `socket-proxy`), share a single admin-owned DB via namespaced tables (or skip DB entirely).

---

## 6. Migration path (from today → Variant B, then optionally A)

### Phase 0 — Decide + document (1 week)

- Accept this doc (or revised version).
- Open ADR-0010 "DB-First Module & Tenant Registry" — cross-ref ADR-0008/0009.
- Add `sk_*` prefix convention to `kombify Core/standards/DATA-ARCHITECTURE.md` (or create if missing).

### Phase 1 — Tool-catalog unification (2–3 weeks)

- Migration `000030_sk_tool_catalog_unify.up.sql`: create `sk_tool`, `sk_tool_version`, `sk_tool_evaluation`; backfill from `AdminSkTool` + `StackKitToolEntry`; FK-redirect all references; deprecate old tables (keep for one release).
- Admin-UI: single "Tools" page.
- Impact: StackKits composer no longer uses a parallel tool list.

### Phase 2 — Module registry (2–3 weeks)

- Migration `000031_sk_module_registry.up.sql`: create `sk_module`, `sk_module_version`.
- Extend `stackkit` CLI: add `stackkit module release <slug>@<ver>` command. It computes `contract_hash`, extracts JSON (requires/provides/contexts/compute-tiers/setting-classification), POSTs to Admin API.
- CI job: on every commit to `main`, walk `modules/`, call release for anything newly versioned.
- Admin-UI: module browser with module-version history + contract-hash diff.

### Phase 3 — StackKit composition + wizard telemetry (2 weeks)

- Migration `000032_sk_stackkit_composition.up.sql`: replace `content_stackkit_*` with `sk_stackkit`, `sk_stackkit_module`, `sk_stackkit_decision`; add `sk_wizard_answer`, `sk_wizard_intent`.
- `stackkit init` records wizard answers via API.
- Admin-UI: StackKit browser, "Modules in this kit", "Telemetry".

### Phase 4 — Tenant provisioning (3–4 weeks) — **the most operationally impactful phase**

- Migration `000033_sk_tenant_provisioning.up.sql`: `sk_tenant_db_spec`, `sk_tenant_deployment`, `sk_tenant_db_binding`.
- New job: `kombify-Administration/jobs/provision_tenant_db.go` — Render API + Doppler CLI + seed jobs.
- Seed jobs per module in `kombify-DB/seed/modules/`.
- Contract: `stackkit apply` against a tenant now waits for deployment.lifecycle_state=READY before starting.

### Phase 5 — Three-tier telemetry (ongoing)

- Migration `000034_sk_intent_telemetry.up.sql`: `sk_intent_telemetry`, `sk_tier3_promotion`.
- Wizard `intentFreeText` feeds telemetry even before the Tier-2 AI contract exists.
- Monthly review in admin-center flags candidates for promotion.

### Optional — Phase 6 (→ Variant A)

If and only if the JSON-column trade-offs in Variant B become painful (which happens if we need rich queries like "show me every module that requires capability X, filtered by compute tier Y, across stackkit-versions Z…Z'"), we can split the JSON into relations:

- `sk_module_requires`, `sk_module_provides`, `sk_module_context_support`, `sk_module_disjunction`, `sk_setting_definition`, `sk_setting_override`.
- This is additive (we keep the JSON columns as mirror during transition).
- Decision trigger: when admin-UI hits >3 queries-per-second on a `jsonb @>` operator, or when we can't express a product-management question in SQL.

---

## 7. Big picture

```
┌───────────────────────────────────────────────────────────────────────┐
│                       kombify-Administration (Postgres 17)            │
│                                                                       │
│  ┌─────────────┐   ┌──────────────┐   ┌─────────────────────────┐    │
│  │ sk_tool     │──▶│ sk_module    │──▶│ sk_stackkit              │    │
│  │ sk_tool_    │   │ sk_module_   │   │ sk_stackkit_module       │    │
│  │ version     │   │ version      │   │ sk_stackkit_decision     │    │
│  │ sk_tool_    │   │ (JSON: req/  │   │                          │    │
│  │ evaluation  │   │ prov/ctx/tier│   │                          │    │
│  └─────────────┘   │ /settings)   │   └──────────┬───────────────┘    │
│                    └──────┬───────┘              │                     │
│                           │                      ▼                     │
│                           │             ┌──────────────────┐           │
│                           │             │ sk_wizard_answer │           │
│                           │             │ sk_wizard_intent │           │
│                           │             └──────────┬───────┘           │
│                           │                        │                    │
│                           ▼                        ▼                    │
│                 ┌───────────────────┐   ┌────────────────────┐         │
│                 │ sk_tenant_db_spec │──▶│ sk_tenant_         │         │
│                 │ (per module-ver)  │   │ deployment         │         │
│                 └───────────────────┘   │ sk_tenant_db_      │         │
│                                         │ binding            │         │
│                                         └─────────┬──────────┘         │
│                                                   │                    │
│   ┌───────────────────┐                           │                    │
│   │ sk_intent_        │ ← monthly review →        │                    │
│   │ telemetry / tier3 │                           │                    │
│   │ _promotion        │                           │                    │
│   └───────────────────┘                           │                    │
└─────────────────────────────────────────────────────┬──────────────────┘
                                                      │ via Render API + Doppler
                                                      ▼
┌───────────────────────────────────────────────────────────────────────┐
│                         Tenant infrastructure                          │
│  ┌──────────┐    ┌──────────┐    ┌──────────┐    ┌──────────┐         │
│  │postgres  │    │postgres  │    │valkey    │    │s3 bucket │         │
│  │immich_T  │    │vault_T   │    │immich_T  │    │media_T   │         │
│  └──────────┘    └──────────┘    └──────────┘    └──────────┘         │
│       ▲               ▲               ▲               ▲                │
│       └───────────────┴──────────┬────┴───────────────┘                │
│                                  │                                     │
│                  ┌───────────────┴──────────────────┐                  │
│                  │  StackKit running on tenant host │                  │
│                  │  (bare Ubuntu / Hetzner / etc.)  │                  │
│                  │  configured by stackkit apply    │                  │
│                  │  reading DB_URLs from Doppler    │                  │
│                  └──────────────────────────────────┘                  │
└───────────────────────────────────────────────────────────────────────┘

                    ┌────────────────────────┐
                    │ kombify-StackKits repo │
                    │                        │
                    │ modules/*/*.cue        │◄── source of truth for contracts
                    │ base/*.cue             │
                    │ schemas/wizard.cue     │
                    │ Go CLI (stackkit)      │
                    │ goreleaser binary      │
                    └────────────┬───────────┘
                                 │
                    "stackkit module release <slug>@<ver>"
                                 │  (CI automation)
                                 ▼
                    Admin POST /api/v1/sk/modules/...
                    (inserts sk_module_version with contract_hash)
```

### 7.1 Recommendation

**Go with Variant B.**

Reasons:
1. Variant A's round-trip risk (CUE-from-DB-from-CUE) is higher than the value add, given that CUE's guarantees are the entire reason we chose it.
2. 12–15 tables is a scope we can ship across 4 quarters without freezing feature work.
3. We get everything the user asked for:
   - Catalog-driven StackKit planning in admin-center: ✅ (`sk_tool`, `sk_module`, `sk_stackkit`).
   - Evaluation + history + evidence: ✅ (`sk_tool_evaluation`, `sk_stackkit_decision`, `sk_intent_telemetry`).
   - Per-tenant DB derivation: ✅ (`sk_tenant_db_spec` / `deployment` / `binding`).
   - AI-assisted + Tier-3 promotion: ✅ (`sk_wizard_intent`, `sk_intent_telemetry`, `sk_tier3_promotion`).
4. Phase 6 upgrade path to Variant A is fully additive; if pain emerges, we split JSON → relations domain by domain.

**Do NOT go with Variant A right now** because:
- CUE-AUDIT Phase 3–5 is still pending; adding a second "CUE from DB" generator on top of an unfinished CUE binding multiplies risk.
- Admin-UI doesn't have the form patterns (disjunction editor, requires/provides graph) built; those are 2–3 quarters of UX work.

### 7.2 Next concrete steps (if B is agreed)

1. **This week:** open ADR-0010 "Database-First StackKit Registry (Variant B)". Link to this doc.
2. **Next 2 weeks:** unify `AdminSkTool` + `StackKitToolEntry` → `sk_tool`. Migration 000030. One admin-UI update.
3. **Following 2 weeks:** module registry + `stackkit module release` CLI command. Migration 000031. CI wiring.
4. **Q3 2026:** tenant-provisioning pipeline (migration 000033 + Render/Doppler provisioner job). This is the most user-facing payoff — the moment we can say "a user signs up → StackKit rolls out → their DBs are provisioned, seeded, and wired up automatically".
5. **Ongoing:** wizard telemetry & Tier-3 promotion table; keeps Tier-2 AI path viable once the kombify-AI contract lands.

### 7.3 Risks & mitigations

| Risk | Mitigation |
|---|---|
| `contract_hash` drift: CUE on disk ≠ DB | CI job `stackkit module verify-db` fails the build if any module-version is not registered in DB with matching hash |
| Per-tenant DB explosion (cost + operational complexity) | Start with lightweight modules sharing a single admin-owned DB; per-tenant DB only for stateful use-case modules (immich, vaultwarden, home-assistant). Configurable per `sk_tenant_db_spec`. |
| JSON-column queries too slow | Phase 6 upgrade (additive). Monitor `pg_stat_statements` on the `sk_module_version.requires` column queries. |
| Admin-UI lags behind DB | Build admin-UI MVP for each table **in the same migration PR**. No migration merges without read UI. |
| Tenant provisioning is expensive to reverse | Soft-delete bindings; actual Render resource deletion is gated behind a 7-day retention policy in admin-center. |
| Doppler path sprawl (one secret path per tenant-module-db) | Standard naming: `kombify-tenant-{tenant_id}/prd/{module_slug}_{engine}_{purpose}`. Documented in the provisioner job. |

---

## 8. Open questions for the product/architecture call

1. **Per-tenant DB strategy**: full per-tenant Postgres, or shared-DB + schema-per-tenant for most tools, with per-tenant DB only for "heavy" ones? Need cost model.
2. **Module-release CLI ownership**: lives in StackKits repo (`stackkit module release`) and posts to Admin API — or lives in a CI-only job? I recommend CLI; it keeps the loop tight for module authors.
3. **Admin write-back**: should admin-center be able to *edit* module registry data (e.g., change a module's displayed name), or is DB strictly read-only-from-CUE? I recommend read-only-from-CUE for module data; fully editable for stackkit-composition, decisions, evaluations, telemetry.
4. **Tool-catalog ownership**: StackKits tools are a subset of all tools kombify evaluates. Does a single `sk_tool` table serve both, or do we need `sk_tool` + `admin_evaluated_tool` with a FK? I recommend one table with a `is_stackkit_catalog` boolean flag.
5. **Wizard schema evolution**: when `schemas/wizard.cue` changes, how do historical `sk_wizard_answer` rows remain queryable? I recommend a `wizard_schema_version` column on `sk_wizard_answer` + a `sk_wizard_schema_snapshot` table for archived versions.

---

## 9. Appendix — What did NOT make it into this concept (and why)

- **Row-level security (RLS) for multi-tenancy**: the existing kombify pattern is application-layer org_id filtering; RLS would be a platform-wide change, not a StackKits-local decision.
- **Schema-per-tenant on the admin DB**: rejected; all tenants share `kombify-io` admin DB with `tenant_id` columns. Per-tenant isolation is at *tenant-deployed DB* level, not admin-DB level.
- **Versioned settings with branching**: YAGNI. Settings-override is per-stackkit and per-tenant; branching settings across tenant forks is not a business need today.
- **Graph database for module dependencies**: rejected. Postgres + recursive CTE handles 14–50 modules fine. Neo4j is over-engineering.
- **Event sourcing for deployments**: `sk_tenant_deployment_event` covers 95% of the audit need; a full ES implementation is not required until we have >1k deployments/day.
- **Replacement of CUE**: not in scope. CUE stays. Variant A only *generates* CUE from DB; Variant B keeps CUE authoritative.
