# kombify StackKits -- Product Overview

## One-Liner

CUE-defined infrastructure blueprints that deploy homelab and self-hosted stacks
with zero manual steps via a single `stackkit apply` command.

## Elevator Pitch

kombify StackKits lets you define your entire homelab infrastructure in CUE --
services, networking, security, identity -- and deploy it to a fresh server with
one command. The CLI validates your configuration against strict schemas,
generates OpenTofu and Docker Compose artifacts internally, and applies
everything automatically. No Terraform expertise required, no YAML drift, no
manual SSH sessions.

## Core Tasks

- **Define infrastructure in CUE.** All services, constraints, and defaults
  live in `.cue` files under `base/` and per-kit directories like `base-kit/`.
  CUE provides type safety and constraint validation that YAML cannot.
  (Source: `base/stackkit.cue`, `base-kit/services.cue`)

- **Validate before deploy.** `stackkit validate` and `cue vet` catch
  misconfiguration at authoring time -- wrong port types, missing required
  fields, incompatible settings. The API server also exposes validation
  endpoints. (Source: `internal/cue/validator.go`, `internal/api/handlers.go`)

- **Generate deployment artifacts.** `stackkit generate` reads CUE definitions
  and a user spec (`stack-spec.yaml`), renders OpenTofu `.tf` files and
  `terraform.tfvars.json`. These are disposable build outputs, never hand-edited.
  (Source: `cmd/stackkit/commands/generate.go`, `internal/cue/bridge.go`)

- **Deploy fully automatically.** `stackkit apply` runs OpenTofu (or Terramate
  in advanced mode) to provision Docker containers, networks, and volumes on
  the target server. No manual post-deploy steps.
  (Source: `cmd/stackkit/commands/apply.go`, `internal/iac/executor.go`)

- **Interactive initialization.** `stackkit init` walks users through StackKit
  selection, variant choice, compute tier, domain, and email via a terminal
  wizard -- or accepts `--non-interactive` flags for CI.
  (Source: `cmd/stackkit/commands/init.go`)

- **Composable add-ons.** 18 CUE-defined add-ons (monitoring, backup, VPN,
  media, smart-home, etc.) can extend any StackKit. Each add-on declares
  compatibility, services, and configuration.
  (Source: `addons/monitoring/addon.cue`, `addons/*/`)

- **REST API for integration.** A Go HTTP server exposes 14 endpoints for
  catalog browsing, spec validation, and artifact generation -- used by
  kombify-TechStack Web UI.
  (Source: `internal/api/server.go`, `internal/api/handlers.go`)

## Boundaries

- **Not a general IaC tool.** StackKits produces OpenTofu internally but never
  exposes it. Users do not write, read, or run Terraform/OpenTofu directly.

- **Not a runtime manager.** After `stackkit apply`, day-2 operations
  (monitoring, alerting, updates) are handled by the deployed services
  themselves (Uptime Kuma, Beszel) or by the kombify-TechStack control plane --
  not by the CLI.

- **Not Kubernetes.** Per ADR-0002, v1.x is Docker-only. K8s was explicitly
  excluded. Docker Swarm is planned for ha-kit only.

- **No incremental patching.** Every change regenerates from CUE and redeploys
  from scratch. There is no `stackkit update` or partial rollout.

- **No multi-cloud orchestration.** StackKits deploys to a single target
  environment per spec. Cross-cloud coordination is the responsibility of
  kombify-TechStack (Level 1+).

## Target Audience

- **Homelab enthusiasts** who want a reproducible, one-command setup for
  Traefik, Dokploy, monitoring, and identity services.

- **Self-hosting beginners** who need opinionated defaults without learning
  Terraform, Docker Compose, or networking internals.

- **DevOps teams** who want validated, schema-enforced infrastructure templates
  that can be versioned and shared.

- **AI agents and automation** that need deterministic, CLI-driven
  infrastructure operations (kombify-TechStack, kombify Sphere).

## Competitive Differentiation

- **CUE as source of truth** -- unlike Terraform modules or Helm charts, the
  configuration language itself validates constraints at authoring time.

- **Zero-manual-step deployment** -- competitors (Ansible playbooks, manual
  Docker Compose) typically require post-deploy configuration. StackKits does
  not.

- **Progressive capability model** -- works standalone (Level 0 CLI), as a
  REST API backend (Level 1), or as a managed agent (Level 2+), without
  changing the definition format.

- **Identity-first architecture** -- every StackKit includes LLDAP + Step-CA
  at Layer 1. Zero-trust is not an add-on; it is a requirement enforced by
  CUE schema validation. (Source: `base/layers.cue:383-393`)

## Current Status and Maturity

| Component | Status | Notes |
|-----------|--------|-------|
| base-kit | 85% production | E2E verified: 31 resources, 9 containers. Variants work. |
| modern-homelab | 0% (schema only) | Entirely K8s-based -- needs complete rewrite (TD-08) |
| ha-kit | 0% (scaffolding) | CUE schema exists, 8 explicit TODOs, no implementation |
| CLI (`stackkit`) | 95% | 12 commands functional including interactive init |
| CUE base schemas | 95% | ~2800 lines, production-quality |
| Service modules | 65% | 14 modules in `modules/` with integration tests |
| API server | 95% | 14 endpoints, API key auth, rate limiting, CORS, 42 tests |
| Add-on code generation | 0% | 18 CUE schemas exist but no runtime wiring |
| Node-context auto-detection | 0% | Schema defined, detection logic not implemented |
| Documentation | 50% | v4 docs current, cross-repo Mintlify docs still outdated |

(Sources: `docs/ROADMAP.md`, `TECHNICAL_DEBT.md`)

## Potential and Outlook (Next 6-12 Months)

Based on the roadmap (M0-M9 milestones in `docs/ROADMAP.md`):

- **M1 (IaC pipeline):** Complete CUE-to-OpenTofu bridge with modular `.tf`
  generation. Partially done (`internal/cue/bridge.go` rewritten).

- **M2 (Add-On + Context):** Wire add-on code generation and implement
  node-context auto-detection (local/cloud/pi). This replaces the variant
  system with composable extensions.

- **M3 (modern-homelab rewrite):** Docker multi-node with VPN overlay,
  replacing the K8s-based schemas that exist today.

- **M4-M5:** Documentation overhaul, cross-repo consistency (K1-K15 findings),
  CLI polish (add-on management commands).

- **M6+ (longer term):** ha-kit implementation with Docker Swarm, kombify
  Stack full integration, gRPC agent mode (Level 2), AI-assisted operations
  via kombify Sphere (Level 4).

## Marketing Description

kombify StackKits is a declarative infrastructure toolkit that turns CUE
definitions into fully deployed homelab environments. Define your services,
security policies, and network topology in type-safe CUE schemas, then run
`stackkit apply` to provision everything -- reverse proxy, identity provider,
PaaS platform, monitoring -- on a fresh server with zero manual steps. StackKits
ships with opinionated blueprints for single-server setups today, with hybrid
and high-availability patterns in development. Whether you use the standalone
CLI or integrate via the REST API, the workflow is the same: CUE in, running
stack out.
