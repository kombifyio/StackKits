# StackKit CLI Reference

> Last verified: 2026-05-17

This page summarizes the implemented `stackkit` command surface. Cobra command definitions under `cmd/stackkit/commands/` are the source of truth.

## Installation

```bash
curl -sSL https://install.stackkit.cc | sh
stackkit version
```

The shared installer installs `stackkit`, `stackkit-server`, `stackkit-mcp`, packaged OpenTofu,
and the public kit catalog under `~/.stackkits`, so `stackkit init base-kit`
works from a clean directory without a repo checkout. BaseKit is the verified
beta one-click path; Modern Home Lab and HA Kit are packaged as
alpha/scaffolding definitions until their rollout matrices graduate.

Build from source:

```bash
go build -o build/stackkit ./cmd/stackkit
go build -o build/stackkit-mcp ./cmd/stackkit-mcp
./build/stackkit version
```

## Global Flags

| Flag | Short | Default | Purpose |
| --- | --- | --- | --- |
| `--verbose` | `-v` | `false` | Enable verbose output. |
| `--quiet` | `-q` | `false` | Suppress non-essential output. |
| `--chdir` | `-C` | `.` | Change working directory before running. |
| `--spec` | `-s` | `stack-spec.yaml` | Spec file path; `kombination.yaml` is accepted as a read alias when the default is missing. |
| `--context` | | auto | Override node context: `local`, `cloud`, or `pi`. |
| `--no-log` | | `false` | Disable structured deploy logging. |

## Primary Workflow

```bash
stackkit init base-kit
stackkit prepare
stackkit generate
stackkit plan
stackkit apply --verify
stackkit verify --http --json
```

## Top-Level Commands

| Command | Purpose |
| --- | --- |
| `init [stackkit]` | Create a deployment spec and initial output directory. |
| `prepare` / `prep` | Prepare local or SSH target: prerequisites, Docker checks, packaged OpenTofu check, spec validation, hardware checks. |
| `generate` / `gen` | Generate rollout artifacts from the spec and CUE contracts. |
| `plan` | Run an OpenTofu plan for the generated deployment. |
| `apply [plan-file]` | Apply generated infrastructure and optionally run verification. |
| `verify` | Run read-only post-deployment checks locally or over SSH. |
| `remove` | Destroy a StackKit deployment. |
| `status` | Show deployment state and service health. |
| `validate [file]` | Validate stack specs, CUE files, and generated OpenTofu output where present. |
| `addon` | Manage add-ons in `stack-spec.yaml`. |
| `backup` | Operate local Kopia backup flows and controller enrollment stubs. |
| `break-glass` | Inspect and rotate break-glass recovery bundles. |
| `cluster` | Manage multi-node cluster membership. |
| `compat` | Run a non-destructive VPS compatibility check. |
| `doctor` | Run local diagnostics for common StackKit issues. |
| `agent` | Emit agent-native install plans, prompts, self-checks, and MCP config. |
| `kit` | Import, export, list, verify, upgrade, rollback, history, roundtrip, and unlock kit definitions. |
| `logs` | List and read structured deploy logs. |
| `module` | Release module versions and verify DB parity. |
| `registry` | Manage the embedded registry snapshot. |
| `wizard` | Report wizard answers and free-form intents to the Admin API. |
| `completion` | Generate shell completions. |
| `version` | Print version, commit, build date, Go version, and OS/arch. |

## Command Details

### `stackkit init [stackkit]`

Creates `stack-spec.yaml`. Without arguments it runs the interactive wizard.

Common flags:

- `--mode`
- `--compute-tier`
- `--domain`
- `--local-dns`
- `--local-name`
- `--admin-email`
- `--owner-bootstrap-mode`
- `--owner-source`
- `--owner-email`
- `--owner-username`
- `--owner-display-name`
- `--recovery-passphrase-hash`
- `--recovery-material-ref`
- `--output`, `-o`
- `--force`, `-f`
- `--non-interactive`

Owner bootstrap modes:

| Mode | CLI shape | Notes |
| --- | --- | --- |
| `auto` | `--owner-bootstrap-mode auto --owner-source cloud --recovery-material-ref techstack://...` | SaaS/TechStack handoff. Does not require `--owner-email` or `--owner-username`; Cloud profile resolution happens outside the CLI. |
| `custom` | `--owner-bootstrap-mode custom --owner-source local --owner-email ... --owner-username ... --recovery-passphrase-hash ...` | Self-hosted explicit Owner. The hash is persisted; plaintext is never stored in `stack-spec.yaml`. |
| `none` | `--owner-bootstrap-mode none` | Explicitly skip Owner bootstrap for OSS/BYOS or manually managed identity. |

### `stackkit prepare`

Checks and installs prerequisites when allowed, validates the spec, and reports resource readiness.

Common flags:

- `--host`
- `--user`
- `--key`
- `--dry-run`
- `--skip-docker`
- `--skip-tofu`
- `--auto-fix`
- `--force`

### `stackkit generate`

Generates OpenTofu/tfvars output. The default output directory is `deploy/`.

Common flags:

- `--output`, `-o`
- `--force`, `-f`
- `--fragments`

Generated files are disposable outputs and must not be hand-edited.

### `stackkit plan`

Runs the StackKit-packaged OpenTofu plan against the generated deployment directory. Generate first if artifacts are missing or stale.

### `stackkit apply [plan-file]`

Applies generated infrastructure. If the deploy directory is missing or empty, generation runs first.

Common flags:

- `--auto-approve`
- `--tenant-deployment`
- `--admin-endpoint`
- `--admin-token`
- `--verify`
- `--verify-http`
- `--verify-strict`

Managed tenant mode uses `--tenant-deployment <uuid>` on a VM or job created
from the Admin SaaS flow. If no local `stack-spec.yaml` exists, the CLI fetches
`GET /api/v1/sk/tenants/deployments/{id}/spec`, validates that the returned
deployment envelope matches the requested id, writes `stack-spec.yaml` plus
`.stackkit/tenant-bindings.json`, and then runs the normal apply pipeline. Use
`STACKKIT_ADMIN_ENDPOINT` and the deployment-scoped `STACKKIT_BOOTSTRAP_TOKEN`;
`STACKKIT_ADMIN_URL`, `--admin-token`, and `STACKKIT_ADMIN_TOKEN` remain
fallbacks for older operator jobs. The same token path is used to report
`healthy` or `failed` back to the Admin deployment record.

Unless `--no-log` is set, rollout evidence is written under
`.stackkit/runs/<runId>/` next to the structured log. Managed tenant applies
also post phase progress to Admin when
`POST /api/v1/sk/tenants/deployments/{id}/events` is available; unsupported
event endpoints degrade safely and the final lifecycle `PATCH` remains.

### `stackkit verify`

Runs read-only checks against an applied workspace.

Common flags:

- `--json`
- `--http`
- `--strict`
- `--host`
- `--user`
- `--key`
- `--port`
- `--remote-dir`

HTTP verification treats `2xx`, `3xx`, `401`, and `403` as reachable because authenticated services are expected.

### `stackkit remove`

Destroys the generated deployment with OpenTofu and updates `.stackkit/state.yaml`.

### `stackkit status`

Reads local deployment state and reports service health from generated outputs and runtime checks.

### `stackkit validate [file]`

Validates `stack-spec.yaml` by default. It also validates CUE and generated OpenTofu output when those files are present.

### `stackkit addon`

Subcommands:

- `addon list`
- `addon add <addon-name>`
- `addon remove <addon-name>`

### `stackkit backup`

Subcommands:

- `backup init`
- `backup run`
- `backup list`
- `backup restore <snapshot-id>`
- `backup verify`
- `backup migrate-from-restic`
- `backup enroll`

`backup enroll` is a scaffolded controller path until the controller endpoint is operational.

### `stackkit break-glass`

Subcommands:

- `break-glass list`
- `break-glass show-bundle <node>`
- `break-glass rotate`

Rotation is marked as a later phase in the command help.

### `stackkit cluster`

Subcommands:

- `cluster join-token`

Cluster command coverage expands with the multi-node workstream.

### `stackkit compat`

Runs a non-destructive VPS compatibility check for CPU, memory, disk, Docker readiness, and networking assumptions.

### `stackkit doctor`

Runs local diagnostics for common StackKit issues. `--check-updates` adds Admin API-backed update discovery when endpoint and token configuration are present.

### `stackkit agent`

Read-only helpers for Coding Agents and Assistants. These commands do not create rollout logs or mutate deployment state.

Subcommands:

- `agent install-plan` prints a non-interactive BaseKit rollout plan. Use `--json` for machine-readable output.
- `agent self-check` prints local binary, server, and MCP gate checks. Use `--json` for machine-readable output.
- `agent prompt <scenario>` prints copy-ready prompts. Use `--list` to see scenarios.
- `agent mcp-config` prints `stackkit-mcp` client configuration for `generic`, `codex`, or `claude`.

Examples:

```bash
stackkit agent install-plan --json
stackkit agent prompt basekit-autonomous-rollout
stackkit agent mcp-config --client codex --mode docs,local,server
```

`stackkit-mcp` is a separate installed binary. It defaults to stdio transport, supports `docs`, `local`, `server`, and optional `actions` modes, and keeps write tools disabled unless `STACKKIT_MCP_ALLOW_WRITE=true`.

### `stackkit kit`

Subcommands:

- `kit import`
- `kit export`
- `kit list`
- `kit history`
- `kit roundtrip`
- `kit unlock`
- `kit upgrade`
- `kit upgrade rollback`
- `kit verify`

These commands are for registry, release, lifecycle, and parity workflows. Admin API calls require the relevant endpoint/token configuration documented in [CONFIGURATION.md](CONFIGURATION.md).

### `stackkit module`

Subcommands:

- `module release`
- `module verify-db`

Use these for module contract hash release and DB parity checks.
Admin API auth follows the kit commands: `SERVICE_AUTH_SECRET` mints the
preferred `X-Kombify-Service-Auth` token, with `STACKKIT_ADMIN_TOKEN` or
`KOMBIFY_ADMIN_API_KEY` only as legacy Bearer fallbacks.

### `stackkit registry`

Subcommands:

- `registry snapshot`
- `registry bake-from-cue`
- `registry info`

`snapshot` fetches from the internal Admin API. `bake-from-cue` creates the OSS-safe fallback snapshot from local CUE modules.

### `stackkit logs`

Subcommands:

- `logs list`
- `logs [run-id]`

Structured deploy logs live under `.stackkit/logs` unless configured otherwise.

### `stackkit wizard`

Subcommands:

- `wizard report`

Posts locally captured wizard answers or free-form intents to the Admin API. Use `--dry-run` to inspect the payload without sending it.

### `stackkit completion [bash|zsh|fish|powershell]`

Generates shell completion scripts from Cobra.

### `stackkit version`

Prints version, commit, build date, Go version, and target OS/arch.

## Files Created by the CLI

| Path | Created by | Purpose |
| --- | --- | --- |
| `stack-spec.yaml` | `init` | Deployment spec. |
| `kombination.yaml` | TechStack/user import | Read alias when `stack-spec.yaml` is missing. |
| `deploy/` | `generate` | Generated rollout artifacts. |
| `deploy/*.tf` | `generate` | Generated OpenTofu resources. |
| `deploy/terraform.tfvars.json` | `generate` | Sensitive generated values. |
| `deploy/.terraform/` | `plan`/`apply` | Provider cache and state internals. |
| `.stackkit/state.yaml` | `apply`/`remove` | Deployment state. |
| `.stackkit/logs/` | most commands | Structured deploy logs. |
| `.stackkit/runs/<runId>/` | most commands | Rollout evidence bundle with metadata, events, and summary. |

## Related Docs

- [CONFIGURATION.md](CONFIGURATION.md)
- [TESTING.md](TESTING.md)
- [API.md](API.md)
- [stack-spec-reference.md](stack-spec-reference.md)
- [agent/agents.md](agent/agents.md)
- [agent/stackkit-mcp.md](agent/stackkit-mcp.md)
