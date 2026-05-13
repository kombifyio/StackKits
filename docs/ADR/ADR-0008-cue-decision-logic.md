# ADR-0008: CUE Decision Logic as Canonical Resolution Pipeline

**Status:** Proposed
**Date:** 2026-04-17
**Resolves:** Go CLI generation drifts from CUE contracts; no enforcement of per-module `requires`/`provides`; V5 Resolution Hierarchy exists only in prose

---

## Context

Architecture V5 (ADR-0007, accepted 2026-03-10) defined a 10-step Resolution Hierarchy that maps user input → context → defaults → overrides → compute-tier gating → add-ons → CUE unification → generate. Today only steps 1–6 and 10 are implemented in Go; steps 7–9 (compute-tier gating, add-on resolution, CUE unification + validation) are either partial or bypassed.

The effect is that CUE modules can declare contracts (`requires`, `provides`, `contexts`) but the Go CLI does not enforce them completely. A `stackkit.yaml` that violates a contract can produce a deploy artifact that then fails at runtime.

The 2026 CUE audit identified three missing components:

- **Phase 3** — Terraform fragment generation from CUE module outputs (not hand-coded YAML)
- **Phase 4** — Context-driven generation (Compose for `local`, cloud-native for `cloud`)
- **Phase 5** — Composition Engine that glues module outputs into one coherent deployment

Without these, V6 cannot ship. The out-of-the-box hardening modules (`security-baseline`, `admin-bootstrap`, `login-gateway`) need the Composition Engine to integrate into any StackKit; if they are hand-wired per StackKit, they drift.

Separately, user feedback (2026-04-17) clarified that earlier transcription artifacts referring to "Q-Logic" were speech-to-text errors. The concept has always been CUE-based decision logic. V6 formalizes the name.

## Decision

**CUE Decision Logic** is the canonical configuration-resolution pipeline for V6. The Go CLI binds to it via the `cuelang.org/go/cue` API. Contracts are enforced at `stackkit validate` time.

### What CUE Decision Logic is

A pipeline that resolves user intent to a deploy artifact using three CUE features:

1. **Unification** — merge `user-input ∪ stackkit-defaults ∪ context-defaults` into one config
2. **Disjunctions with defaults (`*`)** — express "exactly one of these tools; this one by default"
3. **Constraints** — enforce cross-field invariants (`requires`, `provides`, domain/context compatibility)

### What binds to Go

| Component | Path | Responsibility |
|-----------|------|----------------|
| `internal/cue/loader.go` | new | Load StackKit module + all per-module contracts via `cue` Go API |
| `internal/cue/resolver.go` | new | Implement V5 10-step Resolution Hierarchy as CUE unification + disjunctions |
| `internal/generate/composer.go` | new/refactor | Compose module outputs into Terraform fragments + Docker Compose |
| `internal/generate/context_driver.go` | new | Choose PaaS/TLS/runtime branches based on detected `context` |
| `tests/cue-binding/` | new | Contract-violation test harness: deliberately-broken input must fail |

### Enforcement Contract

After this ADR lands, any `stackkit.yaml` that:

- References a module not declared in any CUE contract
- Omits a field that a module `requires`
- Specifies values that fail a module's `provides` invariant
- Conflicts with the detected context (e.g., `paas: coolify` on `context: pi`)

must fail `stackkit validate` with a CUE-native error message that names the module and the constraint.

## Alternatives Considered

| Alternative | Why rejected |
|-------------|--------------|
| Keep Go-native validation, ignore CUE contracts | Contracts exist in 14 modules (Phases 1–2 done). Dropping them wastes the audit work and leaves V6 hardening modules unenforced. |
| Generate Go structs from CUE at build time, validate at compile time | Loses runtime context information (hardware tier, cloud metadata). Needs static inputs, which the CLI doesn't have. |
| Write a custom DSL for decision logic | We already have CUE with unification + disjunctions. Reinventing is cost without benefit. |
| Use JSON Schema + custom resolver | JSON Schema lacks unification and defaults with disjunctions; we'd rebuild what CUE already gives us. |

## Consequences

### Positive

- Contracts from the 14 modules become executable. A user cannot configure a combination that violates `requires`/`provides`.
- V6 hardening modules (`security-baseline`, `admin-bootstrap`, `login-gateway`) integrate through the Composition Engine, not by editing each StackKit's Terraform.
- Errors surface at validate time, not apply time. The test-user on bare Ubuntu does not debug Terraform failures.
- Future StackKits (HA, Modern Homelab evolutions) inherit enforcement automatically — no per-StackKit generator code.
- Unblocks the `schemas/wizard.cue` shared schema (ADR-0009 cross-ref) — the CLI wizard and TechStack web wizard both consume the same CUE source.

### Negative

- CUE Go API has a learning curve; team needs to build fluency. Mitigation: the concept work is done; remaining work is mostly binding, not design.
- Big refactor surface. Mitigation: V5 CLI stays working (feature-branched V6); rollout is gradual behind `STACKKIT_V6=1` or similar gate.
- CUE errors are not always beginner-friendly. Mitigation: error-translation layer in `internal/cue/errors.go` that maps CUE errors to user-facing messages.

### Follow-up

- CUE binding Phase 3 tickets (Q2/2026 priority 1)
- CUE binding Phase 5 tickets (Q3/2026 priority 2)
- `stackkit validate --explain` flag that walks the Decision Logic pipeline step-by-step for debugging
- Error-translation layer (UX polish; not blocking)

---

## Related

- [ARCHITECTURE.md](../ARCHITECTURE.md) - current CUE and generation architecture
- [ADR-0009](./ADR-0009-three-tier-provisioning.md) — Three-Tier Provisioning (Tier 1 depends on this binding)
- [ADR-0005](./ADR-0005-service-modules-as-atomic-unit.md) — Service Modules as Atomic Unit (the modules this ADR enforces)
