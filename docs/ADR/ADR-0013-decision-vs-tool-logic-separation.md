## ADR-0013: Decision-Logik (DB) vs Tool-Logik (Files), DB-less Runtime, Validation Pipeline

**Status:** Accepted
**Date:** 2026-04-27
**Resolves:** Operator wants to (a) author kit definitions in the admin UI
backed by a queryable DB, (b) keep the existing CUE module contracts and
templates as the source of truth for tool behavior, (c) deploy the
generated artifacts as a standalone, DB-less runtime, and (d) validate
the bidirectional fidelity (CUE ↔ DB) before going live.
**Cross-ref:** [ADR-0010 DB-first registry](./ADR-0010-db-first-stackkit-registry.md) ·
[ADR-0011 admin_sk_* sunset](./ADR-0011-legacy-admin-sk-sunset.md) ·
[ADR-0012 StackKit kit definition](./ADR-0012-stackkit-kit-definition.md)

---

### Context

ADR-0010 established `sk_*` as the database-first registry. ADR-0012
extended `sk_stackkit` + child tables to fully represent kit definitions
(2-level architecture: layer/group + kit-specific selections). This ADR
makes the broader system architecture explicit:

1. **Decision-Logik** (which module for which group, tier sizing, mode,
   variant) is data — it lives in the DB, is editable in the admin UI
   (when not locked), and is queryable for operations questions like
   "which kits use traefik?".
2. **Tool-Logik** (how a module behaves: ports, volumes, healthchecks,
   service contracts, terraform templates) is code — it lives in
   `modules/*/module.cue`, `base-kit/services.cue`, `base-kit/templates/`,
   versioned in git, validated by CUE schema, contract-hashed (ADR-0010
   Phase 2).
3. **Generated Output** is the build artifact — concrete `stackkit.yaml`,
   `stackfile.cue`, `terraform.tfvars.json`, `*.tf`, `docker-compose.yml`
   produced from (1) × (2) by `stackkit generate` / `stackkit kit export`.
4. **DB-less Runtime** — the deployed stack runs the generated artifacts
   only. It has no admin-DB connection. This is what makes
   `stackkit install` + kombify.dev validation possible without coupling
   tenant deployments to the Admin DB lifecycle.

### Decision

Adopt the four-layer architecture above as the canonical mental model.
Two new operational artifacts cement it:

#### A. Reverse-Pfad: `kit-export` (re-instated from ADR-0012 out-of-scope)

ADR-0012 originally deferred the DB→files reverse path. It is now in
scope because validation is impossible without it. The reverse path is:

```
sk_stackkit + 8 children   (Decision-Logik)
        │
        ▼ stackkit kit export --slug X --output DIR --format all
        │
   ┌────┴───────────────────────────────────────────────────────┐
   ▼                ▼                  ▼                    ▼
stackkit.yaml   stackfile.cue    kit.tfvars.json       kit-overview.
                services.cue                            compose.yml
   (yaml form    (CUE form of    (kit-level TF        (kit composition
    of the kit)   the kit)        contract surface)    in compose form)
```

**One-way only**: file → DB never auto-flows. The reason: CUE files are
PR-reviewed; DB writes have no PR review. Auto-syncing back would break
the review boundary.

#### B. Validation-Pipeline (Test-Scaffolding)

Until the pipeline has been live-validated end-to-end, the following
test surface ships as **temporary scaffolding** and can be retired once
production confidence is established:

1. `internal/kitio/mapping_test.go` — section-mapping invertibility (unit).
2. `internal/kitio/roundtrip_test.go` — local roundtrip on
   `base-kit/stackkit.yaml`: yaml → KitDef → yaml → KitDef structural
   equivalence; cosmetic yaml drift acceptable, critical fields (name,
   version, modes, useCases, foundation, platform, computeTiers, features)
   must match.
3. `internal/kitio/roundtrip_live_test.go` — full DB roundtrip:
   POST kit-import → GET kit-export → diff → POST kit-import (no-op).
   Skipped without `STACKKIT_ADMIN_ENDPOINT`.

**Acceptance criteria:**

- All `internal/kitio` tests green.
- `cue eval` succeeds on the reconstructed `stackfile.cue` and
  `services.cue` for base-kit.
- Re-import of `kit-export` output is a no-op (hash unchanged).
- Critical-field diff = 0 for base-kit.

#### C. DB-less Runtime Property

The deployed stack:

- Has no Admin-DB connection string.
- Reads only from the generated artifacts in its working directory.
- Is fully reproducible from the input pair (DB-state at export time +
  module CUE contracts at deploy time).

This decouples tenant lifecycle from Admin DB lifecycle and lets
`stackkit install` work even when the Admin API is offline.

### Alternatives Considered

| Alternative | Rejected because |
|---|---|
| Bidirectional sync (DB → CUE auto) | Fork the source of truth; CUE PR-review boundary breaks |
| Skip reverse path entirely | Then we cannot validate that DB representation is lossless; ADR-0012 out-of-scope was wrong |
| Embed full module CUE contracts in DB (`sk_module_version.cue_source`) | Already done in ADR-0010 Phase 2 for audit; not used as source of truth — CUE files in git remain authoritative |
| Tenant runtime queries Admin DB live | Couples tenant uptime to Admin DB; violates DB-less principle |
| Test scaffolding stays forever | Production-confidence test should be `verify-db --strict` (per ADR-0010 Phase 2), not the kitio roundtrip — that retires once unnecessary |

### Consequences

#### Positive

- **Single mental model**: Decision-Logik vs Tool-Logik is a clear line developers + operators can both reason about.
- **Authoring + deployment decoupled**: Admin UI can stay locked / unlocked independently of running tenants.
- **Validatable**: roundtrip tests prove DB representation is lossless before any live traffic.
- **Reversible**: kit-export gives operators a "what does the DB say this kit is?" answer in standard formats.

#### Negative

- **Two surfaces per kit edit**: Admin UI write + post-validation re-export. Mitigated by the lock + `kit-import` path being the single legit author.
- **Test scaffolding is not free** to maintain. Mitigated by explicit retirement criteria above.
- **Generated output is build-time only**: changes require regenerate + redeploy. This is by design (DB-less runtime).

#### Follow-up

- `stackkit install` workflow + kombify.dev dev-server deployment.
- Live-promotion ("freigegeben" → controlled lock-toggle) — likely ADR-0014.
- Modern-homelab + HA-kit roundtrip after base-kit live-validates.
- Retire the kitio roundtrip tests when `verify-db --strict` covers the same property at production fidelity.

### Implementation status (verified 2026-05-13)

| Component | Status |
|---|---|
| Reverse library `internal/kitio/` (9 files) | ✅ Shipped |
| Tests (3 test files, all green) | ✅ Shipped |
| Live-API roundtrip test (env-gated) | ✅ Shipped |
| GET `/api/v1/sk/registry/stackkits/{slug}/kit-export` | ✅ Shipped |
| CLI `stackkit kit export` + `stackkit kit roundtrip` | ✅ Shipped |
| ADR-0012 amendment: reverse re-instated | ✅ Shipped |
| Live `base-kit`, `modern-homelab`, and `ha-kit` verify against Admin DB | ✅ Strict verification passed through the canonical Admin API verifier |
| `stackkit install` + kombify.dev deployment | ⏳ Future ADR-0014 |
| Lock-toggle live-promotion workflow | ✅ Admin release/unlock surface shipped; release-readiness QA tracked in Beads |

The live verification target is `kombify_admin` behind the Admin API. The
central `kombify` database is not the authoritative source for `sk_*` kit
definition reviews.
