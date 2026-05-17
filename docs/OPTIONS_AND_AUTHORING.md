# StackKit Options and Authoring Matrix

> Last verified: 2026-05-17

This page is the compact contract for adding or promoting StackKit options. CUE
is the technical source of truth; the kombify database mirrors catalog,
version, rollout, and lifecycle state.

## Roles

| Role | Meaning | Release requirement |
| --- | --- | --- |
| `default` | Enabled by the kit without user action. | Fresh-target smoke, first-run path, auth/routing, backup classification, upgrade notes, and registry/CUE hash parity. |
| `alternative` | Curated swap for a default in the same group. | Same generated contract shape as the default, documented migration/limits, and explicit user selection. |
| `optional` | Available but off by default. | CUE validation, generate path, documented enablement, and known gaps. |
| `addon` | Cross-cutting capability, not a user-facing use case. | Clear dependency surface, no hidden default activation, and tests for the generated fragments it adds. |

## Current BaseKit Platform Matrix

| Concern | Release value |
| --- | --- |
| Default PaaS | `coolify` |
| Explicit PaaS alternative | `dokploy` |
| Invalid normal PaaS values | `dockge`, `none` |
| Dockge status | Experimental/constrained Compose manager service only; not a normal Base/Modern/HA PaaS. |
| Low compute tier | Keeps the Coolify platform contract and gates heavier apps; it does not switch to Dockge. |

When the PaaS contract changes, update all of these together:
`base-kit/stackkit.yaml`, `base-kit/defaults.cue`, the Go resolver/validator,
`docs/stack-spec-reference.md`, `docs/CONCEPTS.md`, website installer copy, and
release archive smoke expectations.

## Authoring Flow

1. Define or update the CUE/module contract first under `modules/`, `addons/`,
   the relevant kit directory, or `base/`.
2. Classify the role in kit metadata with `role`, `defaultTool`, and
   `alternatives` where applicable.
3. Add resolver or generator code only when CUE cannot express the behavior
   yet; keep Go defaults aligned with the CUE contract.
4. Update docs and website copy only after the source contract is decided.
5. Add the narrowest tests that prove the changed layer, then broaden for
   release surfaces.

## Promotion Gates

| Promotion | Minimum tests |
| --- | --- |
| Experimental to optional | `cue vet`, module CUE validation, generate path, docs for known gaps. |
| Optional to alternative | Resolver/generator tests, compatibility with existing defaults, docs for migration and limits. |
| Alternative to default | Fresh-target smoke, release archive smoke, identity/secret checks, `stackkit verify` coverage, rollback/update notes. |
| Kit to release-ready | Public installer smoke, full archive validation, live BaseKit-style scenario evidence, and no HTML fallback on one-line endpoints. |

Modern Homelab and HA Kit currently remain alpha/scaffolding. They can stay in
the full archive and kit catalog, but they must not be presented as
production-ready one-click installers until their scenario matrix entries pass.

## Required Release Checks

For any option, installer, or kit-default change:

```bash
go test ./...
cue vet ./base/... ./base-kit/... ./modern-homelab/... ./ha-kit/...
mise run test:cue-binding
mise run test:website
```

For release packaging:

```bash
goreleaser release --snapshot --clean
bash scripts/release/validate-release-archives.sh dist
bash tests/e2e/test_live_installers.sh
```
