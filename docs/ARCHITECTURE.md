# Architecture — kombify StackKits

> Last verified: 2026-05-17

This is the current implementation overview for this repo. Normative product and module rules live in [STACKKIT_GOLDEN_RULES.md](STACKKIT_GOLDEN_RULES.md), [STACKKIT_DEVELOPMENT_DECISION_GUIDE.md](STACKKIT_DEVELOPMENT_DECISION_GUIDE.md), and accepted ADRs.

## System Role

StackKits turns CUE-defined infrastructure contracts into deployable homelab environments:

```text
operator intent / TechStack intent
        |
        v
stack-spec.yaml or API request
        |
        v
CUE contracts + Go resolver/bindings
        |
        v
generated OpenTofu / tfvars / metadata
        |
        v
stackkit apply + stackkit verify
```

CUE is the technical contract source of truth. The kombify database mirrors registry and operations state, but it does not replace live CUE contracts.

## Major Containers

| Container | Location | Responsibility |
| --- | --- | --- |
| CLI | `cmd/stackkit`, `internal/*` | Operator workflow: init, prepare, validate, generate, plan, apply, verify, update, registry, logs, backup, and recovery commands. |
| API server | `cmd/stackkit-server`, `internal/api` | HTTP surface for catalog, schema, validation, generation preview, logs, capabilities, OpenAPI, and Direct Connect registry lifecycle. |
| CUE contracts | `base/`, `base-kit/`, `modern-homelab/`, `ha-kit/`, `modules/`, `addons/` | Schemas, defaults, constraints, module contracts, and deployment shape. |
| Composition/generation | `internal/cue`, `internal/composition`, `internal/iac`, `internal/tofu`, `internal/terramate` | Bind CUE/spec data into generated deployment artifacts and execution adapters. |
| Backup binaries | `cmd/stackkit-backup-agent`, `cmd/stackkit-backup-controller`, `internal/backup-controller` | Host backup and SaaS/controller integration surfaces. |
| Static website | `website/` | OSS landing page and CLI docs for `stackkit.cc`. |
| Release automation | `.github/workflows`, `.goreleaser.yaml`, `scripts/sync-public.sh` | CI, release, server image, website validation, and curated OSS mirror sync. |

## Core Data Flow

1. `stackkit init` creates a `stack-spec.yaml` from user intent.
2. `stackkit prepare` validates prerequisites, can install Docker on supported targets, and verifies the StackKit-packaged OpenTofu binary.
3. `stackkit validate` and generation paths bind the spec to CUE contracts.
4. `stackkit generate` writes generated rollout artifacts under `deploy/`.
5. `stackkit plan` and `stackkit apply` execute OpenTofu through the Go adapter.
6. After OpenTofu bootstraps the selected PaaS, `stackkit apply` consumes the generated platform manifest. StackKit may operate StackKit-owned system apps through the platform adapter, but user apps remain PaaS handoff metadata and are deployed, updated, and operated by the selected external PaaS tooling.
7. First-run setup is represented separately from deployment as setup-drop metadata. Local Base Node Hub routes are intentionally bootstrap-open until `protect_base_hub=true` is applied after owner setup, while public/non-local Base routes stay protected when TinyAuth is enabled. Other L1/L2 platform services use `automatic` setup and must be usable after rollout, while L3 apps use `manual` or `on_demand` depending on whether StackKits has a supported bootstrap drop.
8. `stackkit verify` performs read-only host checks and optional HTTP URL checks.
9. `stackkit-server` exposes the same catalog, validation, generation-preview, log, and registry concepts over HTTP and is deployed as a platform-managed system app in the normal BaseKit path.

## Current Technical Stack

| Area | Current source |
| --- | --- |
| Go | `go.mod` and `mise.toml`: `1.26.3` |
| CUE library | `cuelang.org/go v0.15.4` |
| CLI | Cobra `v1.10.2` |
| HTTP server | Go `net/http` with `ServeMux` |
| IaC engine | OpenTofu, packaged with StackKit release artifacts |
| Task runner | `mise.toml` |
| Website build | `website/package.json`, static Node build script |

## StackKit Layers

Every StackKit resolves through the canonical layers:

- `foundation`: host bootstrap, security baseline, owner/break-glass, secrets bootstrap, base network, minimal telemetry, and preflight policy.
- `platform`: runtime, PaaS adapter, reverse proxy, DNS/TLS, identity provider, login gateway, service registration, logs, and health.
- `application`: user-facing use-case modules such as photos, vault, media, files, smart home, dev, and AI.

Layer definitions are enforced by CUE contracts and explained in [STACKKIT_GOLDEN_RULES.md](STACKKIT_GOLDEN_RULES.md).

## API Surface

The API server registers endpoints in `internal/api/server.go`; the contract source is [../api/openapi/stackkits-v1.yaml](../api/openapi/stackkits-v1.yaml). The human summary is [API.md](API.md).

Public unauthenticated endpoints:

- `GET /health`
- `GET /api/v1/health`
- `GET /api/v1/openapi.yaml`

Protected endpoints cover:

- capabilities
- StackKit list/get/schema/defaults
- full and partial validation
- tfvars and preview generation
- deploy log list/get/stream
- Direct Connect registry register/deregister/heartbeat

## CLI Surface

The implemented top-level command groups are documented in [CLI.md](CLI.md):

`init`, `prepare`, `generate`, `plan`, `apply`, `verify`, `remove`, `status`, `validate`, `addon`, `backup`, `break-glass`, `cluster`, `compat`, `doctor`, `kit`, `logs`, `module`, `registry`, `wizard`, `completion`, and `version`.

## Source Of Truth Boundaries

| Concern | Source |
| --- | --- |
| Technical deployment contract | CUE files in this repo |
| Registry, catalog, lifecycle mirror | kombify database / Admin API |
| API wire shape | `api/openapi/stackkits-v1.yaml` plus server tests |
| CLI behavior | Cobra command definitions and tests |
| Architecture overview | `docs/ARCHITECTURE.md` |
| Active work | published roadmap and release notes |
| Roadmap read-view | `ROADMAP.md` |

Historical V5/V6 and CUE-audit planning content has been folded into ADRs, Beads, the architecture manifest, and this overview. Do not reintroduce standalone architecture-version or task-tracker Markdown files.
