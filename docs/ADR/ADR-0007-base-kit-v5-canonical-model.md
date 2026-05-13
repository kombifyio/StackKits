# ADR-0007: Base Kit v5 Canonical Model

**Status:** Accepted
**Date:** 2026-03-16
**Resolves:** Base Kit drift between `stackkit.yaml`, `stackfile.cue`, `models.StackSpec`, and Terraform generation

---

## Context

The Base Kit currently has two competing shapes:

1. `base-kit/stackkit.yaml` expresses the product-facing v5 model:
   - use-cases
   - platform roles
   - compute-tier adjustments
   - add-on centric evolution

2. `base-kit/stackfile.cue` still exposes a variant-era v4 surface:
   - `deploymentMode`
   - `variant`
   - `computeTier`
   - a reduced service set centered around historical variants

At the same time, the Go CLI and generated Terraform already operate on a third shape: `pkg/models.StackSpec` and `cmd/stackkit/commands/generate.go`.

This split makes it too easy for docs, schema, and generation logic to drift independently.

## Decision

For Base Kit going forward:

- `pkg/models.StackSpec` is the canonical external input shape for deployment.
- `base-kit/stackkit.yaml` is the catalog and product-metadata description of Base Kit.
- `base-kit/stackfile.cue` is a transitional contract surface that must expose the v5 input concepts even while it still carries legacy compatibility fields.

The v5 concepts that must remain visible in `stackfile.cue` are:

- `mode`
- `runtime`
- `context`
- `compute.tier`
- `paas`
- `addons`
- `domain` and related access/TLS inputs

The legacy fields remain temporarily:

- `deploymentMode`
- `variant`
- `computeTier`

These fields are compatibility aliases only and must not define the future product model.

## Consequences

### Positive

- The Base Kit schema surface now matches the language used by the CLI and the v5 docs.
- Refactors in the generator can move toward one model instead of translating between unrelated shapes.
- Legacy CUE tests can continue to pass while the migration proceeds incrementally.

### Negative

- `stackfile.cue` remains a transitional schema for a while and is intentionally not yet fully clean.
- Some concepts are visible before they are fully wired through module-driven generation.

### Follow-up

- Move tfvars generation to one canonical bridge path.
- Replace variant-era service selection with module/use-case/add-on resolution.
- Retire the legacy compatibility aliases once module-driven Terraform generation is in place.
