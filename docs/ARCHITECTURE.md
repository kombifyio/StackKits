# Architecture — kombify StackKits

> Last verified: 2026-06-26

This is the current implementation overview for this repo. Normative product and module rules are summarized here and in accepted ADRs.

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
| CLI | `cmd/stackkit`, `internal/*` | Operator workflow: init, prepare, validate, generate, plan, apply, verify, update, registry, logs, and recovery commands. |
| API server | `cmd/stackkit-server`, `internal/api` | HTTP surface for catalog, schema, validation, generation preview, logs, capabilities, OpenAPI, and Direct Connect registry lifecycle. |
| CUE contracts | `base/`, `basement-kit/`, `cloud-kit/`, `modules/` | Schemas, defaults, constraints, module contracts, and deployment shape. |
| Composition/generation | `internal/cue`, `internal/composition`, `internal/iac`, `internal/tofu`, `internal/terramate` | Bind CUE/spec data into generated deployment artifacts and execution adapters. |
| Public docs | `README.md`, `docs/` | Homelab/BaseKit OSS documentation and CLI install contract. |
| Release automation | `.github/workflows`, `.goreleaser.yaml`, `scripts/public/` | CI, release, server image, private website validation, and curated Homelab/Basement Kit OSS mirror sync. The old `scripts/sync-public.sh` path is intentionally deprecated. |

## Core Data Flow

1. `stackkit init` creates a `stack-spec.yaml` from user intent.
2. `stackkit prepare` validates prerequisites, can install Docker on supported targets, and verifies the StackKit-packaged OpenTofu binary.
3. `stackkit validate` and generation paths bind the spec to CUE contracts.
4. `stackkit generate` writes generated rollout artifacts under `deploy/`.
5. `stackkit plan` and `stackkit apply` execute OpenTofu through the Go adapter.
6. After OpenTofu bootstraps the selected PaaS, `stackkit apply` consumes the generated platform manifest. StackKit may operate StackKit-owned system apps and StackKit-owned L3 application use cases through the platform adapter, but customer-owned user apps remain PaaS handoff metadata and are deployed, updated, and operated by the selected external PaaS tooling.
7. First-run setup is represented separately from deployment as setup-drop metadata. Local Base Node Hub routes are intentionally bootstrap-open until the operator clicks `Protect Base Hub` after owner setup; that action persists the protection setting and switches the local router behind TinyAuth. Public/non-local Base routes stay protected when TinyAuth is enabled. The default `bootstrapped` mode uses `automatic` setup for L1/L2 platform services and `on_demand` setup actions for L3 applications; `bare` forces manual setup and `advanced` is the Terramate Plus lifecycle mode with day-2 orchestration, Runtime Intelligence, Frontend Intelligence, and managed TechStack handoff surfaces.
8. `stackkit verify` performs read-only host checks and optional HTTP URL checks.
9. `stackkit-server` exposes the same catalog, validation, generation-preview, log, and registry concepts over HTTP and is deployed as a platform-managed system app in the normal Basement Kit path.

## Routing Ownership

StackKit does not own a separate router when the selected PaaS already includes one. For Coolify, generated StackKit routes must be served by Coolify's Traefik/proxy. In those environments, the PaaS router is the StackKit router. Dokploy has an integrated-router draft adapter, but it is not part of the production E2E standard until promoted.

Komodo is the first explicit exception: the initial `paas: komodo` contract uses exactly one StackKit-owned Traefik while Komodo owns Compose Stack deployment. The generated dashboard/status output and release evidence must label that routing ownership as StackKit-owned, not Komodo-owned.

StackKit must not add a second Traefik instance, an Nginx bridge container, a host-side proxy, or a browser/test-only forwarding workaround to make service URLs appear reachable. Such a path is a routing bypass, not production evidence. If StackKit later supports another PaaS without an integrated router, that adapter contract must explicitly include one StackKit-owned router and the generated dashboard/status output must label it as such.

## Current Technical Stack

| Area | Current source |
| --- | --- |
| Go | `go.mod` and `mise.toml`: `1.26.4` |
| CUE library | `cuelang.org/go v0.15.4` |
| CLI | Cobra `v1.10.2` |
| HTTP server | Go `net/http` with `ServeMux` |
| IaC engine | OpenTofu, packaged with StackKit release artifacts |
| Task runner | `mise.toml` |
| Public release checks | `scripts/release/*.mjs`, `.github/workflows` |

## StackKit Layers

Every StackKit resolves through the canonical layers:

- `foundation`: host bootstrap, security baseline, owner/break-glass, secrets bootstrap, base network, minimal telemetry, and preflight policy.
- `platform`: runtime, PaaS adapter, reverse proxy, DNS/TLS, identity provider, login gateway, service registration, logs, and health.
- `application`: user-facing use-case modules such as photos, vault, media, files, smart home, dev, and AI.

Layer definitions are enforced by CUE contracts.

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

`init`, `prepare`, `generate`, `plan`, `apply`, `verify`, `remove`, `status`, `validate`, `app`, `break-glass`, `backup`, `cluster`, `compat`, `doctor`, `kit`, `logs`, `module`, `registry`, `wizard`, `completion`, and `version`.

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
