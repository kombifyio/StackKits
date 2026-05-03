# ADR-0009: Three-Tier Provisioning (Curated / AI-Assisted / Promotion)

**Status:** Proposed
**Date:** 2026-04-17
**Resolves:** Long-tail infrastructure requests have no home; AI-generated infrastructure has no trust model; curated modules grow by gut-feel, not evidence
**Cross-ref:** kombify-AI ADR-014 (Spacelift Intent Agent)

---

## Context

The 14 curated CUE modules cover the common homelab shape (Platform + 6 BaseKit Use Cases + HA + Modern Homelab variants). They do not cover:

- Niche requests ("I want Bitwarden for my team" → Vaultwarden exists, but what about a Stalwart-integrated groupware stack?)
- Rapidly-changing upstream tools (new self-hosted AI apps ship weekly)
- Per-user customizations that are below the bar for a curated module (custom reverse-proxy rules, one-off integrations)

TechStack's wizard (4 questions: goals, access, users, login) cannot capture this long tail without becoming a 50-question form, which defeats its purpose.

kombify-AI has an Intent Agent (ADR-014, accepted 2026-04) — a Spacelift Intent-based Apache 2.0 Go MCP that translates natural-language intent into infrastructure changes. It has a four-level trust model (Explore → Provision → Modify → Destroy) with approval gates.

V6 needs a way to:

1. Let curated modules stay the default (safe, reviewed, tested)
2. Let AI handle long-tail requests with appropriate trust gates
3. Let evidence from AI usage inform which modules to curate next

## Decision

Three tiers of provisioning, with clear boundaries.

### Tier 1 — Curated (StackKits CUE Modules)

- Source: `modules/*/` + `stackkit.yaml` + `stackfile.cue`
- Trust: Full. Every module is reviewed, tested, admin-center gated.
- Execution: `stackkit validate && stackkit apply`. No AI in the loop.
- Coverage: BaseKit V6 scope (Platform + 6 Use Cases), Modern Homelab, HA Kit, curated add-ons.
- Default for: Every user. `stackkit init --interactive` uses Tier 1 only.

### Tier 2 — AI-Assisted (kombify-AI Spacelift Intent Agent) — **Contract pending**

- Source: kombify-AI Spacelift Intent Agent (ADR-014 on the kombify-AI side).
- **Status in V6:** Design placeholder. The existing kombify-AI Brain is an internal chat-routing service built around `IntentCategory`; it does not currently expose a StackKits-intent endpoint, and no inter-repo contract has been agreed. Tier 2 remains off the critical path until that contract exists.
- Trust model (target, per ADR-014): Per-action. Four-level trust model applies.
  - `Explore` — read-only analysis; no approval needed.
  - `Provision` — add new resources; user approval required.
  - `Modify` — change existing resources; user approval + preview diff.
  - `Destroy` — remove resources; double confirmation.
- Target execution flow (pending contract):
  - User triggers via TechStack wizard's "anything else?" step, or `stackkit intent "..."` CLI command (Phase 4).
  - TechStack / StackKits authenticate with Auth0 M2M (non_interactive client) and call the kombify-AI intent endpoint — **shape TBD by kombify-AI**.
  - Intent Agent proposes CUE-compatible module additions or `docker-compose` fragments.
  - User reviews proposal, approves, then StackKits CLI writes the result to `stackkit.yaml` / add-on files.
- Coverage: Long-tail requests not covered by Tier 1.
- Default for: Opt-in only. No user is pushed into Tier 2 by default. Free-text wizard input is captured but not routed until the contract exists.

### Tier 3 — Promotion

- Source: Telemetry from Tier 2 usage (anonymized intent patterns).
- Trigger: An intent pattern appears ≥N times in a rolling window (initial N=50/month, tunable).
- Action: Engineering team reviews the pattern, designs a curated module, writes CUE contract + reference compose, submits via ADR or module-PR.
- Outcome: What was Tier 2 becomes Tier 1. Users who previously used Tier 2 for this intent automatically get the curated module on their next `stackkit upgrade`.
- Governance: Product owns the promotion decision. Engineering owns the module implementation. Admin-center tracks the promotion history.

## Boundary Rules

| Rule | Why |
|------|-----|
| Tier 1 is the default. `stackkit init` never calls Tier 2. | Test-users must reach production-ready without AI. |
| Tier 2 can only propose; StackKits CLI writes. | CLI owns the CUE/YAML; AI is a suggestion engine, not an editor. |
| Tier 2 output must be CUE-valid (CUE Decision Logic enforces). | Prevents AI from producing silently-broken configs. |
| Tier 3 promotion is a product review, not automatic. | Avoids cargo-cult curation from skewed telemetry. |
| Tier 2 destroy-tier actions require double confirmation. | Aligns with ADR-014 trust model. |

## Cross-Repository Ownership

| Repository | Tier 1 | Tier 2 | Tier 3 |
|------------|--------|--------|--------|
| StackKits | Curated CUE modules, Go CLI, wizard schema | Consumes Brain API (`stackkit intent "..."`) | Writes curated modules from promoted patterns |
| kombify-AI | — | Brain API, Intent Agent (ADR-014), trust-level enforcement | Emits promotion telemetry |
| TechStack | — | Wizard "anything else?" → Brain API call | — |

## Alternatives Considered

| Alternative | Why rejected |
|-------------|--------------|
| Let AI write to `stackkit.yaml` directly | Violates CUE Decision Logic enforcement; user loses the reviewed-module guarantee. |
| No AI tier; all non-curated requests become feature requests | Too slow. Long-tail by definition is too varied to curate up-front. |
| One monolithic Intent API with no trust levels | ADR-014 already defines the trust model; discarding it means rebuilding safety from scratch. |
| Let TechStack embed AI directly (not via kombify-AI) | Violates `kombify Core/standards/AI-ARCHITECTURE.md` — internal tools use `go-common/ai` or (as decided for TechStack) delegate to kombify-AI. |

## Consequences

### Positive

- Clear ownership: StackKits owns modules, kombify-AI owns intent translation, TechStack owns the UX.
- Long-tail requests have a home without inflating the curated catalog.
- Evidence-driven curation: Tier 3 ensures curated work is informed by actual usage, not guesses.
- Test-user safety: Tier 1 default means bare-Ubuntu users never encounter AI in the install path.
- AI safety: ADR-014 trust levels cap blast radius.

### Negative

- Three surfaces to maintain (curated modules, Intent Agent, promotion pipeline). Mitigation: each tier has a clear owner and no overlap.
- Tier-2 transport contract does not exist yet. Mitigation: Tier 2 is opt-in, off the V6 critical path. TechStack Phase 2 and StackKits Phase 4 are blocked on a kombify-AI-owned ADR for the intent contract; both capture free-text input without routing until that contract lands.
- Promotion process adds product-management overhead. Mitigation: initial cadence is monthly, not per-request.

### Follow-up

- **kombify-AI (blocking for Tier 2):** publish a cross-repo ADR defining the homelab-intent contract (endpoint, auth, request/response schema, trust-level mapping). No code in StackKits or TechStack calls kombify-AI until this lands.
- StackKits Phase 4 (Q1/2027): `stackkit intent "..."` CLI command — consumer of the above contract.
- TechStack Phase 2: wizard "anything else?" routing — consumer of the above contract.
- Promotion pipeline: define telemetry schema (`intent_patterns` table) and monthly review process.

---

## Related

- [ARCHITECTURE_V6.md §3](../ARCHITECTURE_V6.md) — Three-Tier Provisioning
- [ADR-0008](./ADR-0008-cue-decision-logic.md) — CUE Decision Logic (Tier 1 enforcement layer)
- kombify-AI `backend/internal/intent/` — Spacelift Intent Agent implementation (ADR-014)
- `kombify Core/standards/AI-ARCHITECTURE.md` — AI provider standards
