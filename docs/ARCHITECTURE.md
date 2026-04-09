# kombify StackKits -- Architecture

## Tech Stack

| Component | Technology | Version | Source |
|-----------|-----------|---------|--------|
| Language | Go | 1.25 | `go.mod:3` |
| Configuration | CUE | v0.15.4 (cuelang.org/go) | `go.mod:6` |
| CLI framework | Cobra | v1.10.2 | `go.mod:10` |
| IaC engine (internal) | OpenTofu | >= 1.6.0 | `internal/template/renderer.go:341` |
| IaC orchestrator (optional) | Terramate | >= 0.5.0 | `base-kit/stackkit.yaml:62` |
| Container runtime | Docker | 24.0+ | `README.md:26` |
| YAML parsing | gopkg.in/yaml.v3 | v3.0.1 | `go.mod:13` |
| SSH | golang.org/x/crypto | v0.47.0 | `go.mod:12` |
| HTTP server | Go stdlib net/http | 1.25 | `internal/api/server.go` |
| Testing | testify | v1.9.0 | `go.mod:11` |
| Task runner | Mise | >= 2024.11.1 | `.mise.toml:4` |
| Dev environment | Docker Compose | -- | `docker-compose.yml` |

## System Overview

```
+------------------------------------------------------------------+
|                     USER / KOMBIFY STACK UI                       |
|  stack-spec.yaml   or   REST API (POST /api/v1/validate, etc.)   |
+--------+--------------------------+------------------------------+
         |                          |
         v                          v
+------------------+     +----------------------------+
|  stackkit CLI    |     |  API Server (Go net/http)  |
|  cmd/stackkit/   |     |  internal/api/server.go    |
|                  |     |  14 endpoints, API key auth|
|  Commands:       |     +------------+---------------+
|  init            |                  |
|  validate        |                  |
|  generate  <-----+------ shared ---+
|  plan            |      packages
|  apply           |
|  destroy         |
|  status          |
+--------+---------+
         |
         | 1. Load spec
         v
+---------------------------+
|  config/loader.go         |
|  Parse stack-spec.yaml    |
|  Parse stackkit.yaml      |
|  Apply defaults           |
+--------+------------------+
         |
         | 2. Validate CUE
         v
+---------------------------+
|  internal/cue/            |
|  validator.go  Validate   |
|  bridge.go     CUE->TFVars|
|  generator.go  (planned)  |
+--------+------------------+
         |
         | 3. Render templates
         v
+---------------------------+
|  internal/template/       |
|  renderer.go              |
|  .tf.tmpl -> .tf files    |
|  terraform.tfvars.json    |
+--------+------------------+
         |
         | 4. Execute IaC
         v
+---------------------------+      +---------------------------+
|  internal/iac/executor.go |----->|  internal/tofu/           |
|  Unified Executor iface   |      |  OpenTofu CLI wrapper     |
|  OpenTofu or Terramate    |----->|  internal/terramate/      |
+--------+------------------+      |  Terramate CLI wrapper    |
         |                         +---------------------------+
         | 5. Provision
         v
+---------------------------+
|  Target Server            |
|  Docker containers        |
|  Networks, volumes        |
|  Traefik, Dokploy, etc.  |
+---------------------------+
```

## Core Components and Data Flow

A typical `stackkit apply` traverses these stages:

### Stage 1: Configuration Loading

`config.Loader` (`internal/config/loader.go`) reads two YAML files:
- `stackkit.yaml` -- kit metadata, variants, modes, requirements
- `stack-spec.yaml` -- user's deployment choices (kit name, variant, domain, network, nodes)

Defaults are applied if omitted: variant="default", mode="simple",
subnet="172.20.0.0/16", ssh.user="root", ssh.port=22.

### Stage 2: CUE Validation

`cue.Validator` (`internal/cue/validator.go`) loads CUE instances from the
kit directory, builds values via `cuelang.org/go`, and runs `value.Validate()`
with `cue.Concrete(true)`. Errors are collected into `models.ValidationResult`.

The spec is also validated against Go-side rules: required fields (`name`,
`stackkit`), enum values for network mode, context, and compute tier.

### Stage 3: Artifact Generation

Two paths exist:

1. **Template rendering** (`internal/template/renderer.go`): Walks `.tf.tmpl`
   files in `<kit>/templates/<mode>/`, executes Go templates with a
   `RenderContext` (spec + stackkit metadata + services), writes `.tf` files
   to `deploy/`.

2. **CUE bridge** (`internal/cue/bridge.go`): Converts StackSpec into a
   `TFVars` struct matching all OpenTofu variables, writes
   `terraform.tfvars.json`. Handles service enable flags, domain, subnet,
   TinyAuth config, Docker host.

### Stage 4: IaC Execution

`iac.Executor` (`internal/iac/executor.go`) is an interface with two
implementations:

- `OpenTofuExecutor` -- wraps `internal/tofu/` for direct `tofu init`,
  `tofu plan`, `tofu apply` execution. Default for "simple" mode.
- `TerramateExecutor` -- wraps `internal/terramate/` for orchestrated
  multi-stack execution. Used when mode is "terramate" or "advanced-terramate".

Both support: Init, Plan, Apply, Destroy, Validate, Output, DetectDrift, Refresh.

### Stage 5: State Management

After successful apply, `DeploymentState` is written to
`.stackkit/state.yaml` with status, timestamp, and variant info.
`stackkit status` reads this state plus live Docker container info
(`internal/docker/`) to display service health.

## Key Architectural Decisions

### Why CUE (not YAML/HCL/Jsonnet)?

CUE provides type-safe schemas with constraint validation at authoring time.
Unlike YAML, CUE catches invalid configurations before generation. Unlike HCL,
CUE schemas compose via unification -- extending a base service definition is
a type operation, not string interpolation. (Source: `base/stackkit.cue` --
`#BaseStackKit` embeds `#StackKitMetadata`, `#ServiceDefinition`, etc.)

### Why Docker-only for v1.x (ADR-0002)?

Docker Compose is universally available on homelab hardware. K8s adds
operational complexity (etcd, kubelet, CNI) that homelab users rarely need.
Docker Swarm is reserved for ha-kit (v1.2+). The `#PlatformType` enum in
`base/layers.cue:402` is `"docker" | "docker-swarm" | "bare-metal"` --
Kubernetes is intentionally absent.

### Why OpenTofu (not raw Docker Compose)?

OpenTofu provides state management, plan/apply lifecycle, and idempotent
resource creation. The Docker provider (`kreuzwerker/docker ~> 3.0`) manages
containers, networks, and volumes as resources. Users never interact with
OpenTofu; the CLI abstracts it entirely.

### Why a 3-Layer architecture?

The layered model enforces separation of concerns:
- **Layer 1 (Foundation):** System config, security, identity (LLDAP, Step-CA).
  Required and validated via `#Layer1Foundation` in `base/layers.cue:366-394`.
- **Layer 2 (Platform):** Container runtime, ingress (Traefik), PaaS (Dokploy),
  platform identity (TinyAuth/PocketID). Validated via `#Layer2Platform`.
- **Layer 3 (Applications):** User services deployed via the PaaS. Validated
  via `#Layer3Applications`.

The `#ValidatedStackKit` type at `base/layers.cue:474-486` embeds all three
layers, ensuring a complete kit satisfies all constraints.

### Why Traefik as default ingress?

Traefik auto-discovers Docker containers via labels, handles Let's Encrypt
certificates, and provides domain-based routing. All service URLs use
`<service>.<domain>` (e.g., `dokploy.stack.local`), never `localhost:PORT`.

## Core Features

### Interactive Init Wizard
Files: `cmd/stackkit/commands/init.go`, `cmd/stackkit/commands/prompt.go`
Discovers available StackKits by scanning for `stackkit.yaml` files, presents
selection menus for kit, variant, mode, compute tier. Generates `stack-spec.yaml`.

### CUE Schema Validation
Files: `internal/cue/validator.go`, `base/stackkit.cue`, `base/layers.cue`
Validates kit CUE against schemas and spec against Go-side rules.
Returns structured `ValidationResult` with errors and warnings.

### Artifact Generation
Files: `cmd/stackkit/commands/generate.go`, `internal/cue/bridge.go`,
`internal/template/renderer.go`
Renders OpenTofu files from Go templates. Generates `terraform.tfvars.json`
from StackSpec via CUE bridge.

### Dual IaC Backend
Files: `internal/iac/executor.go`, `internal/tofu/`, `internal/terramate/`
Unified `Executor` interface supporting OpenTofu (simple mode) and Terramate
(advanced mode with drift detection).

### REST API Server
Files: `internal/api/server.go`, `internal/api/handlers.go`, `cmd/stackkit-server/`
14 endpoints: health, capabilities, stackkit catalog (list, get, schema,
defaults, variants), validation (full + partial), generation (tfvars + preview).
Middleware: request ID, API key auth (constant-time comparison), per-IP rate
limiting, CORS, structured error responses, panic recovery.

### Service Modules
Files: `modules/*/module.cue`, `modules/*/reference-compose.yaml`
14 self-contained service modules (traefik, tinyauth, dokploy, lldap, step-ca,
pocketid, uptime-kuma, whoami, dozzle, dashboard, crowdsec, adguard-home,
unbound, socket-proxy). Each has CUE definition and integration tests.

### Deployment Status
Files: `cmd/stackkit/commands/status.go`, `internal/docker/`
Reads `.stackkit/state.yaml` and queries Docker daemon for live container
status. Supports `--json` output for machine consumption.

## Ecosystem Integration

### kombify Techstack (Control Plane)
The API server (`internal/api/server.go`) exposes REST endpoints consumed by
kombify Techstack (`techstack.kombify.io`). CORS is configured to allow
`https://kombify.io` origins. Techstack loads StackKit definitions, provides
a UI wizard, and calls the API for validation and generation.

> **Note:** `stack.kombify.io` is the marketing website (website-v2).
> `techstack.kombify.io` is the actual tool.

### kombify Sphere (SaaS)
Level 4 in the progressive capability model. StackKits provides the
infrastructure definitions; Sphere adds AI-assisted operations and
multi-tenant management. (Planned, not yet implemented.)

### Docker Daemon
The CLI communicates with Docker via `DOCKER_HOST` environment variable.
For development, this is `tcp://vm:2375` (Docker-in-Docker VM). For
production, it targets the local daemon or a remote host.

## Database and State Management

StackKits has no relational database. State is file-based:

- `stack-spec.yaml` -- user's deployment specification (YAML)
- `.stackkit/state.yaml` -- deployment state (YAML, `models.DeploymentState`)
- `deploy/terraform.tfstate` -- OpenTofu state (JSON, managed by OpenTofu)
- `deploy/terraform.tfvars.json` -- generated variables (JSON)

All persistent state belongs to OpenTofu. The `.stackkit/state.yaml` is
a convenience cache for `stackkit status`.

Data models are defined in `pkg/models/models.go`: `StackKit`, `StackSpec`,
`DeploymentState`, `ServiceState`, `ValidationResult`, `SystemInfo`.

## Authentication and Authorization

### API Server Auth
Optional API key authentication via `X-API-Key` header, configured with
`--api-key` flag or `STACKKITS_API_KEY` env var. Uses constant-time
comparison (`crypto/subtle`). Health and OpenAPI endpoints are exempt.
(Source: `internal/api/server.go:354-388`)

### Deployed Service Auth
Layer 1 mandates LLDAP + Step-CA (enforced in `base/layers.cue:385-393`).
Layer 2 provides TinyAuth (lightweight auth proxy) or PocketID (full OIDC).
Traefik integrates via `forwardAuth` middleware.

### SSH Access
`internal/ssh/client.go` handles SSH connectivity for remote deployments.
Key path and user are configured in `stack-spec.yaml` under `ssh:`.

## Deployment Model

### Development Environment
`docker-compose.yml` provides:
- `vm` -- Ubuntu Docker-in-Docker container (privileged) with ports 80/443/8080
- `orchestrator` -- Web UI with demo management + VM terminal (port 9000)
- `portal` -- Service dashboard (port 9001)
- `cli` -- StackKit CLI container targeting the VM via `DOCKER_HOST=tcp://vm:2375`
- `e2e` -- Test runner container
- `dns` -- Local dnsmasq for `.stack.local` domain resolution

### Production Deployment
1. User creates `stack-spec.yaml` (via `stackkit init` or kombify-TechStack UI)
2. `stackkit generate` produces OpenTofu files in `deploy/`
3. `stackkit apply` runs OpenTofu against the target Docker host
4. Services deploy into Docker containers on the target server

### Multi-Platform Builds
The Makefile supports cross-compilation:
- Linux amd64/arm64
- macOS amd64/arm64 (Darwin)
- Windows amd64

(Source: `Makefile:20-29`)

## Known Technical Debt

Source: `TECHNICAL_DEBT.md`, `docs/ROADMAP.md`

### Active Items

| ID | Severity | Description | Location |
|----|----------|-------------|----------|
| TD-08 | P1 | modern-homelab is entirely K8s-based, needs complete rewrite | `modern-homelab/` |
| TD-10 | P1 (partial) | CUE-to-Terraform bridge only generates basic tfvars, no modular .tf | `internal/cue/bridge.go` |
| -- | P2 | Add-on system has 18 CUE schemas but no code generation | `addons/*/` |
| -- | P2 | Node-context auto-detection schema exists but logic unimplemented | `base/context.cue` |
| -- | P2 | ha-kit has 8 explicit TODOs, Docker Swarm not wired | `ha-kit/` |
| -- | P3 | Cross-repo docs (Mintlify) still reference K8s (K1, K5, K6) | External |

### Resolved Items (Selected)

- TD-27: API arbitrary filesystem write -- fixed, uses temp dir (P0)
- TD-28: No API authentication -- added API key middleware (P0)
- TD-31: iac/terramate packages were dead code -- now wired to CLI (P1)
- TD-30: CLI generate did not use template renderer -- fixed (P1)
- TD-09: Services format inconsistency -- resolved (P1)
