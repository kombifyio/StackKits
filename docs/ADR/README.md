# Architecture Decision Records

StackKits architecture decisions.

## Index

| ADR | Status | Summary |
|-----|--------|---------|
| [ADR-0001](ADR-0001-documentation-standard.md) | Accepted | Documentation standard |
| [ADR-0002](ADR-0002-docker-first-v1.md) | Accepted | Docker-first for v1 (no Kubernetes) |
| [ADR-0003](ADR-0003-paas-strategy.md) | Accepted | PaaS strategy |
| [ADR-0004](ADR-0004-ha-domain-policy.md) | Accepted | HA domain policy |
| [ADR-0005](ADR-0005-service-modules-as-atomic-unit.md) | Accepted | Service modules as atomic unit |
| [ADR-0006](ADR-0006-service-url-matrix.md) | Accepted | Service URL matrix |
| [ADR-0007](ADR-0007-base-kit-v5-canonical-model.md) | Accepted | Base Kit V5 canonical model |
| [ADR-0008](ADR-0008-cue-decision-logic.md) | Proposed | CUE Decision Logic as canonical resolution pipeline (V6) |
| [ADR-0009](ADR-0009-three-tier-provisioning.md) | Proposed | Three-Tier Provisioning (Curated / AI-Assisted / Promotion) (V6) |
| [ADR-0010](ADR-0010-db-first-stackkit-registry.md) | Accepted | DB-First StackKit Registry |
| [ADR-0011](ADR-0011-legacy-admin-sk-sunset.md) | Accepted | Legacy admin_sk_* sunset |
| [ADR-0012](ADR-0012-stackkit-kit-definition.md) | Accepted | StackKit Kit Definition (lock + canonical-hash) |
| [ADR-0013](ADR-0013-decision-vs-tool-logic-separation.md) | Accepted | Decision vs tool-logic separation |
| [ADR-0014](ADR-0014-kit-lifecycle-operations.md) | Accepted | Kit lifecycle operations |
| [ADR-0015](ADR-0015-layer-standard-canonicalization.md) | Accepted | Layer standard canonicalization |
| [ADR-0016](ADR-0016-backup-single-engine-kopia.md) | Accepted | Backup single-engine (Kopia) |
| [ADR-0017](ADR-0017-discovery-driven-module-proposals.md) | Accepted | Discovery-Driven Module Proposals (parallel to Tier-3 intent-frequency promotion) |
| [ADR-0018](ADR-0018-kit-update-lifecycle.md) | Accepted | Kit update lifecycle |
| [ADR-0019](ADR-0019-stackkit-operations-contract.md) | Accepted | StackKit operations contract with optional Pulumi executor |
| [ADR-0020](ADR-0020-basekit-multinode-dashboard.md) | Accepted | BaseKit multi-node and dashboard architecture |

## Template

```markdown
# ADR-NNN: Title

**Status**: Proposed | Accepted | Deprecated | Superseded
**Date**: YYYY-MM-DD

## Context
What is the issue motivating this decision?

## Decision
What change are we making?

## Consequences
What becomes easier or harder?
```
