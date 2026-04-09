# Deployment — kombify StackKits

> **Last Updated:** 2026-03-10
> **Version:** 1.0  
> **Component:** kombify StackKits (Infrastructure Templates)

---

## Quick Reference

| Environment | Method | Notes |
|------------|--------|-------|
| **Production Website** | VPS / Dokploy | CI/CD from `main` → Docker container on kombify-ionos |
| **Local** | Docker Compose | `docker compose up -d` |
| **Binary** | Go binary | `make build` → `./bin/stackkit` |

| Property | Value |
|----------|-------|
| **Production URL** | `https://stackkits.kombify.io` |
| **Container Registry** | `ghcr.io/kombiverselabs/stackkits-web` |

---

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────┐
│                     StackKits Architecture                   │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  ┌──────────────────────────────────────────────────────┐  │
│  │              CUE Schema Repository                    │  │
│  │  base/          - Core service definitions           │  │
│  │  base-kit/  - Single Environment Kit             │  │
│  │  ha-kit/    - High-Availability Kit              │  │
│  └──────────────────────────────────────────────────────┘  │
│                          │                                  │
│                          ▼                                  │
│  ┌──────────────────────────────────────────────────────┐  │
│  │              stackkit CLI (Go)                        │  │
│  │  - Validates CUE schemas                             │  │
│  │  - Generates Docker Compose / OpenTofu configs       │  │
│  │  - Publishes to artifact repository                  │  │
│  └──────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────┘
```

### Platform Position

```
┌─────────────────────────────────────────────────────────────┐
│                    kombify Platform                          │
├─────────────────────────────────────────────────────────────┤
│   ┌──────────────────┐     ┌──────────────────┐             │
│   │  kombify Cloud     │     │  kombify-TechStack   │             │
│   │  (Portal)        │     │  (Core API)      │             │
│   └────────┬─────────┘     └────────┬─────────┘             │
│            │   Uses templates from  │                        │
│            └────────────┬───────────┘                        │
│                         ▼                                    │
│            ┌──────────────────────┐                         │
│            │     StackKits        │◀── YOU ARE HERE         │
│            │  (CUE Templates)     │                         │
│            └──────────────────────┘                         │
└─────────────────────────────────────────────────────────────┘
```

---

## CI/CD Workflows

| Workflow | Trigger | What it does |
|----------|---------|--------------|
| `ci.yml` | All PRs and pushes | CUE validation, Go tests, build |
| `release.yml` | `v*` tag push | GoReleaser cross-compilation + GitHub Release |
| `generate-cue.yml` | Push to `main` | Regenerates CUE artifacts |
| `deploy-website.yml` | Push to `main` (docs/) | Deploys documentation site |

---

## VPS Deployment

### Domain Routing

Public `*.kombify.io` subdomains are routed through Cloudflare DNS to kombify-managed infrastructure. The public edge terminates TLS and forwards traffic to the active StackKits deployment.

| Property | Value |
|----------|-------|
| **Public URL** | `https://stackkits.kombify.io` |
| **Hosting** | kombify-managed infrastructure |
| **Reverse Proxy** | Managed edge proxy |
| **TLS** | Wildcard `*.kombify.io` via Let's Encrypt / Cloudflare DNS challenge |
| **DNS** | Cloudflare |

Routing changes are handled by maintainers through the active infrastructure configuration.

### Infrastructure

| Resource | Name | Purpose |
|----------|------|---------|
| Docker Container | stackkits-web | Documentation website |
| Container Registry | `ghcr.io/kombiverselabs` | Docker images |
| Database | kombify-DB (`kombify_me`) | kombify.me subdomain registry |

### Required GitHub Secrets

```
VPS_HOST              # VPS IP address
VPS_USER              # SSH user
VPS_SSH_KEY           # SSH private key
VPS_SSH_PORT          # SSH port
DOPPLER_TOKEN         # Doppler service token (secrets injection)
```

---

## Local Development

### Prerequisites

```bash
go 1.25+        # Required
cue 0.9+        # Required (go install cuelang.org/go/cmd/cue@latest)
make             # Required
docker           # Optional (for testing generated manifests)
```

### Build & Test

```bash
# Build CLI
make build
# → ./bin/stackkit

# Run tests
go test ./...

# Build with version info
go build -ldflags "-X main.version=v1.0.0" -o bin/stackkit ./cmd/stackkit
```

### Working with CUE

```bash
# Validate all schemas
cue vet ./...

# Validate specific stack
cue vet ./base-kit/...

# Evaluate a schema
cue eval ./base-kit/

# Export as YAML
cue export ./base-kit/ --out yaml

# Format CUE files
cue fmt ./...
```

### Docker

```bash
docker compose up -d --build
curl -s http://localhost:5280/api/v1/health
```

---

## Publishing Artifacts

StackKits artifacts are published to GHCR as OCI artifacts:

```bash
# Login to GHCR
echo $GHCR_TOKEN | docker login ghcr.io -u USERNAME --password-stdin

# Push as OCI artifact
oras push ghcr.io/kombiverselabs/stackkits/base-kit:v1.0.0 \
  --artifact-type application/vnd.kombify.stackkit.v1 \
  ./base-kit/:application/vnd.cuelang.cue
```

---

## Pre-Release Checklist

- [ ] All CUE schemas validate (`cue vet ./...`)
- [ ] Go tests pass (`go test ./...`)
- [ ] Documentation updated
- [ ] Version bumped in appropriate files
- [ ] CHANGELOG updated

---

## Troubleshooting

### CUE validation errors

```bash
# Get detailed error output
cue vet -c ./base-kit/...

# Check specific file
cue vet ./base-kit/stackfile.cue
```

### Import resolution issues

Ensure `cue.mod/module.cue` exists and has correct module path:

```cue
module: "github.com/kombifyio/stackkits"
```

---

## Cross-Repository Dependencies

| Repo | Dependency Type | Notes |
|------|-----------------|-------|
| kombify-TechStack | Consumer | Stack loads StackKit definitions |
| docs | Documentation | Published to docs site |
| kombify Cloud | Display | Shows available templates |

---

## Related Documentation

- [Architecture](./ARCHITECTURE.md)
- [kombify.me Integration Guide](./kombify-me-integration-guide.md)
- [ROADMAP](../ROADMAP.md)
- [ADR/](./ADR/) — Architecture Decision Records
