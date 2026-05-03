# ADR-0015 — Layer Standard Canonicalization (Foundation / Platform / Application)

**Status:** Accepted (2026-04-27)
**Supersedes nothing**; clarifies + enforces ADR-0012 (`sk_layer`) + ARCHITECTURE_V6 §4 across DB, YAML, Go, TS, and UI.
**Pairs with:** kombify-DB migration 000084, kombify-Administration spec 2026-04-27-admin-stackkit-lifecycle-ui-design.md.

## Context

ADR-0012 introduced `sk_layer` with the canonical layer slugs `foundation` / `platform` / `application` (+ `hardening`, `observability`). ARCHITECTURE_V6 §4 settled the same names as the "Three Modules of mandatory hardening" plus "BaseKit Application scope".

But the actual implementation lagged behind on the **third** layer name:

- `base-kit/stackkit.yaml` used the V5 top-level key `useCases:` (not `application:`)
- `sk_service_group.yaml_section` CHECK constraint allowed `'useCases'`, with 10 rows seeded under that value (migration 000082)
- Go `internal/kitio/types.go`, `mapping.go` named the field `UseCases`, the map `UseCaseToGroup`, the kind `SectionUseCases`
- TS `kit-import` + `kit-export` endpoints parsed/emitted `useCases` keys
- The cross-language canonical-hash fixture used `useCases` as a property name; the anchor hash baked that in

This drift had **not yet** corrupted any data — the term was self-consistent across the broken surfaces. The risk was that the admin UI being built on top of these surfaces (specifically the kit-detail page in Phase 3 of the lifecycle UI) would surface the legacy term as user-facing labels: "useCases" instead of "Application". Cementing the V5 name in operator-facing UI would make every future rename even more expensive.

## Decision

Canonicalize on **Foundation / Platform / Application** as the only acceptable spelling for the third layer across DB, YAML, Go, TS, tests, and UI.

This means:

1. **DB** — `sk_service_group.yaml_section` CHECK constraint allows `('', 'foundation', 'platform', 'application')`. The 10 rows previously stamped with `'useCases'` are rewritten to `'application'` in a single TX migration.
2. **YAML** — `base-kit/stackkit.yaml`'s top-level `useCases:` key becomes `application:`. Other kits (modern-homelab, ha-kit) didn't have this section yet, so they're unaffected.
3. **Go** — every `UseCases`, `UseCaseDef`, `UseCaseToGroup`, `SectionUseCases`, `resolveUseCases`, `isUseCaseEnabled` is renamed to its `Application` equivalent. JSON/YAML tags follow.
4. **TS** — `kit-import` + `kit-export` request/response shapes use `application` as the section key. The forward + reverse maps' bucket names and the `'foundation' | 'platform' | 'application'` discriminator union both update.
5. **Cross-language hash anchor** — regenerated. The fixture's `useCases` key becomes `application`, the SHA256 changes accordingly, both Go-side and TS-side anchors are updated in the same commit.
6. **CI guard** — a regression test in the admin frontend (`tests/unit/sk-no-usecases-regression.test.ts`) recursively scans `src/routes/api/v1/sk` + `src/lib/server/sk` for live `useCases` literals (allowing narrative comments), failing the build if a future PR re-introduces the term.

## Why now

The trigger was Phase 0 of the admin StackKit lifecycle UI rollout (spec 2026-04-27-admin-stackkit-lifecycle-ui-design.md). That spec needed three Layer Cards labelled with the canonical names; rendering "useCases" in a UI card was unacceptable. Phase 0 became the rename-everything-in-lockstep commit chain.

Doing the rename **before** the UI lands was non-negotiable: shipping a UI on top of the legacy term would have pulled in dependents (admin operators, screenshots in docs, training material) and made the eventual rename a coordinated multi-week project instead of a single afternoon.

## Alternatives considered

**A) Keep `useCases` everywhere as a yaml-section name; only the layer enum says `application`.**
The DB row `yaml_section='useCases'` would map to layer slug `'application'` via `sk_layer`. This was the path of least change but the name in the YAML / Go / TS code paths is what humans read. The asymmetry would confuse every new contributor.

**B) Bilingual support — accept both `useCases` and `application` on parse, normalize internally.**
Tempting because it's backward-compatible. But the only kit that uses the term is `base-kit`, and base-kit is also the kit we control. There are no third-party kits depending on the V5 yaml shape. Bilingual support would be permanent overhead with no compensating user.

**C) Use `applications` (plural) instead of `application`.**
ADR-0012 + ARCHITECTURE_V6 §4 say `application` (singular, matching `foundation` / `platform`). Plural breaks the parallelism.

The chosen approach (full canonical rename) won because:
- The rename is purely mechanical
- The test suite catches divergence cross-language
- A single migration + a single commit per repo can do it atomically
- The CI regression test prevents future drift

## Consequences

**Positive**

- Admin UI labels match documentation match DB enum match Go/TS code. Zero translation needed for new contributors.
- The cross-language canonical-hash anchor test now covers `application` keys, which exercises a slightly different sort-position than `useCases` would have (alphabetical ordering across `application < platform`, while `foundation < platform < useCases`).
- Future "what does Layer X mean?" questions point to a single source: ARCHITECTURE_V6 §4.

**Negative**

- Anyone with a local checkout from before 2026-04-27 needs to `git pull` + re-run `prisma generate` + `go build`. Existing in-flight branches that touched any of the renamed identifiers will conflict.
- The migration DOWN script exists but is not idempotent against partial re-application — running it on a DB that has already been renamed and then rolled forward again would loop. Mitigated by the migration's sanity assertion ("expected 0 rows with `useCases` after UPDATE").

**Neutral**

- `login-gateway` placement (currently under `foundation:` in YAML, but ARCHITECTURE_V6 §4 lists it under L2 Platform) is a **separate** drift, intentionally not touched here. It moves a real selection between layers, not just renames a key. Tracked as a follow-up.

## Implementation

Single atomic Phase 0 across three repos, each with a paired commit:

- kombify-DB `3c2e592` `feat(db): rename yaml_section useCases -> application (Phase 0)` — migration 000084
- kombify-StackKits `c60b9e2` `refactor(kitio): rename useCases -> application (Phase 0 layer-standard)` — 21 files
- kombify-Administration `ad332f2` `fix(sk): rename useCases -> application in kit endpoints (Phase 0)` — 4 SK files

Cross-language hash anchor regenerated:
- Old: `b1e5815355939e0da5d568298467a4782a2fe2074a130493704cbf34f9628155`
- New: `e0a2b8779d83ec126d24fbf9c9401ec28f1fe5e2acdecd73270ac761a4c624c8`

Regression guard: `kombify-Administration/frontend/tests/unit/sk-no-usecases-regression.test.ts`.

## References

- ADR-0012 — sk_stackkit kit definition (introduces `sk_layer`)
- ADR-0014 — kit lifecycle operations (depends on canonical layer names)
- ARCHITECTURE_V6 §4 "Out-of-the-Box Hardening Baseline"
- kombify-DB migration 000084
- kombify-Administration spec 2026-04-27-admin-stackkit-lifecycle-ui-design.md
