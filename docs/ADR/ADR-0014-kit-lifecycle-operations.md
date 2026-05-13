## ADR-0014: Kit Lifecycle Operations + DB-Driven Section Mapping

**Status:** Accepted
**Date:** 2026-04-27
**Resolves:** ADR-0012 introduced the lock mechanism for `sk_stackkit` rows
but left two gaps: (1) no audit trail of state changes (who imported,
when, did anyone bypass the lock?), and (2) the yaml↔group section
mapping was hardcoded in three places (TS forward + TS reverse + Go
fallback) and silently drifted between them. ADR-0014 closes both gaps.
**Cross-ref:** [ADR-0010](./ADR-0010-db-first-stackkit-registry.md) ·
[ADR-0011](./ADR-0011-legacy-admin-sk-sunset.md) ·
[ADR-0012](./ADR-0012-stackkit-kit-definition.md) ·
[ADR-0013](./ADR-0013-decision-vs-tool-logic-separation.md)

---

### Context

Two operational pain points emerged in the post-shipping review of the
kit pipeline:

1. **Lock has no observability.** The `enforce_sk_stackkit_lock` trigger
   in migration 000046 raises `EXCEPTION` on direct UPDATE/DELETE attempts
   against locked + cue-sourced rows. The legitimate bypass is the session
   variable `sk.kit_import_context = 'true'` set by the kit-import
   endpoint inside its transaction. But: nothing distinguishes a legitimate
   kit-import bypass from an operator running `psql` and setting the
   variable manually. No audit trail, no alert.

2. **Section mapping is duplicated three times.** The same yaml→group
   mapping (e.g. `useCases.photos → photo-management`) lived in:
   - `kit-import/+server.ts` as a `Record<string,string>` (forward)
   - `kit-export/+server.ts` as a separate `Record<string,string>` (reverse)
   - `internal/kitio/mapping.go` as a `map[string]string` (Go fallback)

   The three were intended to stay synchronized. They drifted in practice
   — `platform.tinyauth → forward-auth` was in Go but not in either TS
   variant. Adding a new mapping required three coordinated edits + redeploy.

### Decision

Two parallel changes, both data-driven where possible:

#### A. Audit log — `sk_stackkit_audit_log` table + AFTER triggers

Migration `000081_sk_stackkit_audit_log.up.sql` adds a new audit table
and `AFTER INSERT / UPDATE / DELETE` triggers on `sk_stackkit` that
populate it. Each row records:

- `stackkit_id`, `stackkit_slug` (denormalized — slug survives kit deletion)
- `action` ∈ {create, update, delete, unlock, relock}
  - unlock / relock are detected by `OLD.is_locked != NEW.is_locked`
- `actor` — `last_imported_by` from kit-import, or `current_user` for direct SQL
- `hash_before`, `hash_after` — `last_imported_hash`
- `was_locked_before`, `was_locked_after`
- `source_of_truth_before`, `source_of_truth_after`
- **`context_bypass`** — TRUE if `sk.kit_import_context = 'true'` was active
- `metadata` JSONB — extension point (unlock reason, request_id, etc.)
- `created_at`

The `context_bypass` flag is the key operational signal. A spike of
`context_bypass=TRUE` rows from `actor != 'cli'` warrants alerting:
someone is bypassing the lock from a psql session, not from the
official kit-import path.

Triggers are AFTER + best-effort: an audit-insert failure raises
`NOTICE` but does NOT block the parent operation. The kit pipeline
must not be hostage to audit table availability.

#### B. CLI surface for lifecycle ops

Three new `stackkit` subcommands replace the "reach for psql" recovery
flow:

```
stackkit kit list                  GET /api/v1/sk/registry/stackkits
                                   table or --json; columns include lock 🔒,
                                   source-of-truth, last-imported-by, hash
stackkit kit history --slug <kit>  GET .../audit?limit=20
                                   bypass-flagged rows show ✱ in the
                                   Bypass column
stackkit kit unlock --slug <kit>   POST .../unlock body={reason}
              --reason "..."       Sets is_locked=FALSE inside the bypass
              --yes                session, recorded as 'unlock' action
                                   in audit log; --reason is required
```

Two new admin endpoints back the CLI:

- `GET /api/v1/sk/registry/stackkits/{slug}/audit?limit=N` — paginated
  audit-log for one kit
- `POST /api/v1/sk/registry/stackkits/{slug}/unlock` — body `{reason}`
  (required); opens the bypass session and sets is_locked=FALSE

Both use `requireServiceKeyOrAdmin` so the CLI authenticates with HS256
service tokens.

#### C. DB-driven yaml mapping — `sk_service_group.yaml_section + yaml_key`

Migration `000082_sk_service_group_yaml_mapping.up.sql` adds two
columns to `sk_service_group`:

- `yaml_section TEXT` — `'foundation' | 'platform' | 'useCases' | ''`
  (empty = DB-only group, not surfaced in any kit yaml)
- `yaml_key TEXT` — the key inside that section (e.g. `'photos'` for the
  `photo-management` group, `'login-gateway'` for the `forward-auth` group)

A `CHECK (yaml_section IN ('', 'foundation', 'platform', 'useCases'))`
constraint keeps endpoints simple. Index on `(yaml_section, yaml_key) WHERE yaml_section <> ''`
for the export-side reverse lookup.

Endpoints `kit-import` and `kit-export` query this table at request
time (~30 rows, single query, no caching). Adding a new mapping is now
a regular `ALTER+UPDATE` migration without admin-api or stackkit-CLI
redeploy.

**Aliases** — A group like `forward-auth` accepts input from BOTH
`foundation.login-gateway` (canonical) AND `platform.tinyauth` (legacy
yaml duplicate in base-kit). The schema stores ONE canonical pair per
group in the columns; additional input aliases land in
`metadata.yamlAliases` as a JSONB array of `{section, key}`. Endpoints
read both:

```typescript
async function loadSectionMappings(): Promise<ForwardMaps> {
  const rows = await prisma.skServiceGroup.findMany({
    select: { slug, yamlSection, yamlKey, metadata }
  });
  for (const r of rows) {
    if (r.yamlKey && r.yamlSection) maps[r.yamlSection].set(r.yamlKey, r.slug);
    const aliases = r.metadata.yamlAliases ?? [];
    for (const a of aliases) maps[a.section].set(a.key, r.slug);
  }
}
```

`kit-export` uses canonical only (one preferred yaml location per group).

The Go-side `internal/kitio/mapping.go` keeps its hardcoded maps as
**offline fallback** for tests + roundtrip-only paths (where no DB is
available). A frozen JSON snapshot of the DB seed lives at
`internal/kitio/testdata/service_group_seed.json` and a sync test
(`seed_sync_test.go`) compares the two — drift fails CI.

### Alternatives Considered

| Alternative | Rejected because |
|---|---|
| Audit log via app-layer logging (Sentry/structlog), no DB table | Loses queryability ("which kits has alice unlocked?"); harder to alert on bypass spike |
| Trigger uses `RAISE EXCEPTION` if audit insert fails | Would let an audit-table outage halt the kit pipeline. Best-effort + NOTICE is the right tradeoff |
| Lock-state column instead of trigger | Requires every writer to remember the check; trigger guarantees it at DB level |
| Section-mapping as separate `sk_yaml_alias` table | Cleaner SQL but adds a table; with N=1 alias case (forward-auth), JSONB metadata is sufficient and idiomatic |
| Endpoints cache the section mapping in memory | Premature optimization at ~30 rows/req; would need invalidation on seed update |
| Drop `sk_stackkit.metadata.yamlAliases` and require all mappings via canonical column | Breaks platform.tinyauth case in real kit yamls; changing the yaml is a user-facing breaking change |

### Consequences

#### Positive

- **Audit-trail closes the operator-blind-spot** of ADR-0012. Every state
  change is queryable; bypass attempts are flagged.
- **CLI surface eliminates psql recovery path.** Operators no longer need
  `SET LOCAL "sk.kit_import_context"` know-how to fix a bad import.
- **Section mappings are single-source-of-truth** in DB. Adding a new
  service group is one SQL migration, no code redeploy.
- **Drift detection via sync test** — the seed-sync test fails CI on
  silent Go↔DB divergence. Already caught one case (platform.tinyauth
  alias).

#### Negative

- **Audit table grows monotonically.** No retention policy yet; at 1
  import/day per kit + 3 kits → ~1100 rows/year. Acceptable for years.
  Add retention when it's actually a concern.
- **Two-step regen for new mappings**: write SQL migration + update
  `testdata/service_group_seed.json` + Go map. The sync test enforces
  it but it's still three coordinated edits.
- **AFTER triggers add latency** to every sk_stackkit write. ~1ms per
  insert on local benchmarks. Not measurable in the kit-import path
  (which already does ~50ms of work).

#### Follow-up

- Add Sentry/observability hook to alert on
  `WHERE context_bypass = TRUE AND actor NOT LIKE 'cli%'` audit rows.
- Add audit-log retention policy (e.g. keep last 100 entries per kit,
  archive older to cold storage) once table size matters.
- Admin-UI page `/sk-stackkits/{slug}` showing kit detail + audit
  timeline + unlock workflow. Not needed for CLI ops but nice for
  operators.
- Phase-3 of section mapping: move group seed itself out of migration
  000046 into a separate seed file the admin-UI can edit without a
  schema migration. (Today: changing default-tool requires ALTER+UPDATE
  migration.)

### Implementation status (2026-04-27)

| Component | Status | Evidence |
|---|---|---|
| Migration 000081 (audit log + AFTER triggers) | ✅ Shipped | `kombify-DB/migrations/000081_sk_stackkit_audit_log.up.sql` |
| Migration 000082 (yaml_section + yaml_key + aliases backfill) | ✅ Shipped | `kombify-DB/migrations/000082_sk_service_group_yaml_mapping.up.sql` |
| Admin endpoint `GET /audit` | ✅ Shipped | `frontend/src/routes/api/v1/sk/registry/stackkits/[id]/audit/+server.ts` |
| Admin endpoint `POST /unlock` | ✅ Shipped | `frontend/src/routes/api/v1/sk/registry/stackkits/[id]/unlock/+server.ts` |
| Admin endpoints kit-import + kit-export DB-driven | ✅ Shipped | both `+server.ts` files refactored to use `loadSectionMappings()` |
| CLI `stackkit kit list / history / unlock` | ✅ Shipped | `cmd/stackkit/commands/kit_{list,history,unlock}.go` |
| Go-side seed-sync test | ✅ Shipped + green | `internal/kitio/seed_sync_test.go` + `testdata/service_group_seed.json` |
| Live admin redeploy | ⏳ Manual via `gh workflow run` (pending workflow_dispatch by operator) | — |
| Sentry alert on bypass spike | ⏳ Follow-up | — |
| Audit retention policy | ⏳ Not needed yet (year+ runway) | — |
| Admin-UI kit detail page | ⏳ Future ADR | — |
