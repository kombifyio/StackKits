# kombify StackKits — Project Context

> Standalone context document for AI assistants, new contributors, and onboarding.
> Last Updated: 2026-02-26

---

## What is kombify StackKits?

kombify StackKits is the **declarative Infrastructure-as-Code framework** for the kombify ecosystem built by Kombiverse Labs. It provides CUE-based infrastructure templates ("StackKits") that define complete service deployments — from Docker containers to networking, storage, and monitoring. The `stackkit` CLI generates and applies OpenTofu configurations from CUE definitions.

**Core idea:** A StackKit IS its CUE definitions. Users define what they want (services, networks, storage), and the framework generates all deployment artifacts (OpenTofu/Terraform, Docker Compose, scripts). The `stackkit` CLI handles the full lifecycle: generate, validate, apply, and manage.

## Tech Stack

| Layer           | Technology                | Notes                                         |
| --------------- | ------------------------- | --------------------------------------------- |
| Schema/Config   | CUE                       | Constraint-based configuration language       |
| CLI             | Go 1.24 + Cobra           | `stackkit` command-line tool                  |
| IaC Generation  | OpenTofu / Terraform      | Generated from CUE definitions                |
| Orchestration   | Terramate                 | Multi-stack orchestration (advanced mode)      |
| Container Runtime| Docker + Docker Compose   | Service deployment target                     |
| Reverse Proxy   | Traefik                   | Domain-based routing for all services         |

## Architecture

### Three-Layer Model

```
Layer 3: StackKits (e.g., base-kit)
    │     Combines platforms + add-ons into deployable packages
    │     Defines service configurations, networks, volumes
    │
Layer 2: Platforms (e.g., docker, incus)
    │     Platform-specific deployment abstractions
    │     Docker Compose generation, Incus profiles, etc.
    │
Layer 1: Core
          CUE schema definitions, type system, constraints
          Service interface, network model, storage model
```

### Pipeline: CUE → IaC → Deployment

```
StackKit CUE Definitions
    │
    ├── stackkit validate    → CUE constraint checking
    ├── stackkit generate    → OpenTofu HCL generation
    │       │
    │       ├── Simple Mode  → Plain HCL files
    │       └── Advanced Mode → Terramate-orchestrated stacks
    │
    ├── stackkit apply       → Execute on target server
    │       │
    │       ├── Docker Compose up
    │       ├── OpenTofu apply
    │       └── Post-deploy health checks
    │
    └── stackkit status      → Runtime state inspection
```

### Service Model

Each service in a StackKit is defined by CUE with:
- **Container image and version** (pinned for reproducibility)
- **Environment variables** (with defaults and constraints)
- **Network configuration** (Traefik labels, ports, domains)
- **Storage** (volumes, bind mounts, permissions)
- **Dependencies** (service ordering, health checks)
- **Add-on configuration** (monitoring, backup, etc.)

## Repository Layout

```
kombify StackKits/
├── cmd/stackkit/             # CLI entrypoint (main.go)
├── pkg/                      # Public Go packages
│   ├── cli/                  # Cobra command implementations
│   ├── cue/                  # CUE evaluation and unification
│   ├── generator/            # IaC generation (OpenTofu, Compose)
│   ├── executor/             # Deployment execution
│   └── validator/            # Schema validation
├── core/                     # Layer 1: Core CUE schemas
│   ├── service.cue           # Service interface definition
│   ├── network.cue           # Network model
│   ├── storage.cue           # Storage model
│   └── constraints.cue       # Global constraints
├── platforms/                # Layer 2: Platform implementations
│   ├── docker/               # Docker/Compose platform
│   └── incus/                # Incus/LXD platform
├── stackkits/                # Layer 3: StackKit definitions
│   └── base-kit/         # Canonical homelab StackKit
│       ├── services/         # 31 service definitions
│       │   ├── traefik.cue
│       │   ├── nextcloud.cue
│       │   ├── vaultwarden.cue
│       │   ├── gitea.cue
│       │   ├── jellyfin.cue
│       │   └── ... (26 more)
│       └── addons/           # 17 add-on definitions
│           ├── monitoring.cue
│           ├── backup.cue
│           └── ... (15 more)
├── docs/                     # Documentation
└── .github/workflows/        # CI/CD pipelines
```

## Key Concepts

### StackKit

A StackKit is a complete, deployable infrastructure package defined entirely in CUE. The `base-kit` StackKit includes 31 services and 17 add-ons covering a full homelab setup (reverse proxy, cloud storage, password manager, media server, Git hosting, monitoring, etc.).

### CUE Unification

CUE's unification model allows layered composition:
- Core schemas define the base types and constraints
- Platform schemas add deployment-specific fields
- StackKit schemas combine services with platform bindings
- User overrides (`kombination.yaml`) customize the final configuration
- Conflicts are detected at unification time, not at deployment time

### Simple vs. Advanced Mode

- **Simple Mode:** Generates plain OpenTofu HCL files. Suitable for single-server deployments.
- **Advanced Mode:** Uses Terramate for multi-stack orchestration. Suitable for complex, multi-server setups with dependency ordering.

### Service Definitions

Each service (e.g., `nextcloud.cue`) defines:
```
service: nextcloud: {
    image:   "nextcloud:29"
    domain:  "cloud.stack.local"
    ports:   [{internal: 80}]
    volumes: [{name: "data", path: "/var/www/html"}]
    env: {
        NEXTCLOUD_ADMIN_USER:     string | *"admin"
        NEXTCLOUD_TRUSTED_DOMAINS: string
    }
    depends_on: ["traefik", "mariadb"]
}
```

### Add-Ons

Add-ons are optional service groups that can be enabled per-StackKit:
- **Monitoring:** Prometheus + Grafana + node-exporter
- **Backup:** Restic + scheduled snapshots
- **Logging:** Loki + Promtail
- **Security:** CrowdSec + fail2ban integration

### Traefik Routing

All services use Traefik for reverse proxy with domain-based routing. Services are accessed via `service.stack.local` domains, never via `localhost:PORT`.

## Current Status

**Done:** Core CUE schema, Docker platform, `base-kit` StackKit with 31 services and 17 add-ons, `stackkit` CLI (validate, generate, apply, status), simple mode generation, Traefik integration.

**In Progress:** Advanced mode (Terramate orchestration), Incus platform, service health checks, add-on dependency resolution.

**Planned:** Kubernetes platform, cloud StackKits (Azure, AWS), StackKit marketplace, community contributions, version pinning and update channels.

## Dependencies

| Dependency          | Relationship                                    |
| ------------------- | ----------------------------------------------- |
| CUE                 | Configuration language for schema definitions   |
| OpenTofu            | Generated IaC execution engine                  |
| Terramate           | Multi-stack orchestration (advanced mode)        |
| Docker              | Container runtime for service deployment         |
| Traefik             | Reverse proxy for domain-based routing          |
| kombify-TechStack       | Consumes StackKits via Unifier Engine            |

## Key Documentation

| Document                              | Purpose                          |
| ------------------------------------- | -------------------------------- |
| [README.md](../README.md)             | Quick start and overview         |
| [CLAUDE.md](../CLAUDE.md)             | AI assistant instructions        |

## Development Commands

```bash
go run ./cmd/stackkit validate ./stackkits/base-kit   # Validate CUE definitions
go run ./cmd/stackkit generate ./stackkits/base-kit   # Generate OpenTofu configs
go run ./cmd/stackkit apply ./stackkits/base-kit      # Deploy to target server
go test ./...                                              # Run Go tests
cue eval ./core/...                                        # Evaluate core CUE schemas
```

## Conventions

- **A StackKit IS its CUE definitions.** All deployment artifacts are GENERATED — never hand-edit generated files.
- **NEVER use localhost URLs.** All service URLs use domain-based routing (e.g., `service.stack.local`).
- **NEVER use ports 3000 or 3001.** External/host ports must avoid 3000/3001 (dev tool conflicts). Use 4000/4001 instead.
- **Language:** All documentation and code comments in English
- **Commits:** Conventional Commits format (`feat:`, `fix:`, `docs:`, etc.)
- **Go style:** `golangci-lint` with shared config from kombify Core/standards
- **Deployment:** `stackkit generate` → `stackkit apply` on a fresh server. NO manual intervention, NO incremental patches.
