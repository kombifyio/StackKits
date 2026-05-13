# StackKit CLI Reference

> Last verified: 2026-05-09

This page summarizes the implemented `stackkit` command surface. Cobra command definitions under `cmd/stackkit/commands/` are the source of truth.

## Installation

```bash
curl -sSL https://raw.githubusercontent.com/kombifyio/stackKits/main/install.sh | bash
stackkit version
```

Build from source:

```bash
go build -o build/stackkit ./cmd/stackkit
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
- `--owner-source`
- `--owner-email`
- `--owner-username`
- `--owner-display-name`
- `--recovery-passphrase-hash`
- `--output`, `-o`
- `--force`, `-f`
- `--non-interactive`

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

## Related Docs

- [CONFIGURATION.md](CONFIGURATION.md)
- [TESTING.md](TESTING.md)
- [API.md](API.md)
- [stack-spec-reference.md](stack-spec-reference.md)
