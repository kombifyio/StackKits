# Development — kombify StackKits

## Fundamental Rule

**CUE defines the StackKit. Everything else is generated output.**

- Edit `.cue` files to change anything about a StackKit
- Run `stackkit generate` to produce artifacts (never edit them)
- Run `stackkit apply` to deploy (fully automated — zero manual steps)
- Never write or edit Terraform/OpenTofu files, Docker Compose output, or deployment scripts
- Never manually run commands on target servers

## Prerequisites

- [Go](https://go.dev/) 1.25+
- [CUE](https://cuelang.org/) CLI 0.9+
- [Docker](https://docs.docker.com/get-docker/) (optional)
- Make

## Getting Started

```bash
# Clone
git clone https://github.com/kombifyio/stackKits.git
cd stackKits

# Build CLI
make build
# → ./bin/stackkit

# Run tests
make test

# Validate CUE schemas
cue vet ./...
```

## Project Structure

```
cmd/stackkit/         # CLI entry point + commands
internal/             # Go internal packages (cue, docker, validation, ...)
pkg/models/           # Public Go models
base/                 # Core CUE schemas (imported by all kits)
base-kit/         # Single Environment Kit
ha-kit/           # High-Availability Kit
modern-homelab/       # Multi-Node Kit (planned)
docs/                 # Documentation
website-v2/           # Documentation site (SvelteKit)
cue.mod/              # CUE module dependencies
```

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `STACKKITS_PORT` | `5280` | API server port |
| `STACKKITS_DATA_DIR` | `./data` | Data directory |
| `STACKKITS_LOG_LEVEL` | `info` | Log verbosity |

See [SETTINGS-CLASSIFICATION.md](SETTINGS-CLASSIFICATION.md) for the full settings taxonomy.

## Common Tasks

```bash
make build            # Build binary → ./bin/stackkit
make test             # Run Go tests
cue vet ./...         # Validate all CUE schemas
cue eval ./base-kit/  # Evaluate a schema
go test ./...         # Run tests directly
```
