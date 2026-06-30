# StackKit CLI Reference

> Last verified: 2026-06-30

This page summarizes the implemented `stackkit` command surface. Cobra command definitions under `cmd/stackkit/commands/` are the source of truth.

## Installation

```bash
curl -sSL https://install.stackkit.cc | sh
stackkit version
```

The shared installer installs `stackkit`, `stackkit-server`, `stackkit-mcp`,
packaged OpenTofu, packaged Terramate, and the public kit catalog under
`~/.stackkits`, so `stackkit init basement-kit` works from a clean directory
without a repo checkout. Basement Kit is the verified beta one-click path and
the only public OSS kit surface for this release line.
Unpinned installer runs use the current stable GitHub `releases/latest`. To
test a prerelease such as `v0.4.5-beta.1`, export the pin before invoking the
installer:

```bash
export STACKKIT_RELEASE_VERSION=v0.4.5-beta.1
curl -sSL https://base.stackkit.cc | sh
```

For a single copy/paste command, pass the pin to the shell that executes the
installer:

```bash
env STACKKIT_RELEASE_VERSION=v0.4.5-beta.1 sh -c 'curl -sSL https://base.stackkit.cc | sh'
```

For local-server beta tests, run the command in the shell of the target server
itself, for example through SSH, the server console, or an on-server agent. The
default generated URLs use browser-native `*.home.localhost` names. They are
intended for the target server/local host context and do not create LAN-wide DNS
records. If testers need to open the services from another device, choose an
explicit domain/LAN-DNS path before treating the printed URLs as shared network
links.

For the full process taxonomy, including website prompting, one-line install,
direct CLI, on-server agents, SSH agents, local MCP fallback, protected remote
MCP day-2 target operation, automation levels, and individualization levels,
see [INSTALLATION_PROCESSES.md](INSTALLATION_PROCESSES.md).

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
| `--progress-jsonl` | | unset | Write redacted machine-readable rollout progress JSONL to a path, or `-` for stdout. |

## Primary Workflow

```bash
stackkit init basement-kit
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
| `app` | Write optional PaaS app handoff metadata for dev/customer-owned apps. |
| `break-glass` | Inspect and rotate break-glass recovery bundles. |
| `backup` | Configure, inspect, run, verify, restore, and migrate Kopia backups. |
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
- `--port`
- `--dry-run`
- `--skip-docker`
- `--skip-tofu`
- `--auto-fix`
- `--force`
- `--non-interactive`

`prepare` is the non-interactive TechStack prep contract when called as:

```bash
stackkit --progress-jsonl - prepare --non-interactive --host <ip> --user <ssh-user> --key <private-key> --port 22 --spec stack-spec.yaml
```

It validates the spec, connects to the remote target when `--host` is not
`localhost`, waits for apt/dpkg locks as a bounded `apt_wait` phase, installs
Docker on supported OS families, installs StackKit-packaged OpenTofu, installs
StackKit-packaged Terramate for `mode: advanced`, emits a telemetry handoff
status, and reports resource checks. Re-running on an already prepared host is
idempotent: existing Docker/OpenTofu/Terramate installations are detected and
reported as checked or already installed instead of creating a new VM or stack.

The same redacted event shape is written to `.stackkit/runs/<runId>/events.jsonl`
and streamed through `--progress-jsonl`. The schema is
[`schemas/stackkit-rollout-event.schema.json`](../schemas/stackkit-rollout-event.schema.json).
Prepare emits these stable phases for orchestrators:

| Phase | Meaning |
| --- | --- |
| `prepare` | Command lifecycle. |
| `spec.load` | StackSpec load and validation boundary. |
| `target.connect` / `target.inspect` | SSH connection and remote system inventory. |
| `vps_compat` / `network_env` | Local VPS and network context detection. |
| `resources.disk` / `resources.check` | Disk preflight plus CPU/RAM/disk resource evidence. |
| `apt_wait` | Bounded package-manager wait before Docker installation. |
| `docker.check` / `docker.install` / `docker.runtime` / `docker.dns` / `docker.prepull` | Docker readiness and optional image pre-pull. |
| `opentofu.check` | StackKit-packaged OpenTofu readiness or remote install. |
| `terramate.check` | StackKit-packaged Terramate readiness or remote install for Advanced mode. |
| `telemetry.handshake` | Whether OTLP/Sentry handoff configuration was supplied; OSS default is `skipped`. |
| `ports.check` | Remote HTTP/HTTPS port availability check. |

`apt_wait` failures use granular classes when possible:
`cloud_init_timeout`, `apt_lock_timeout`, `apt_process_timeout`,
`unattended_upgrade_timeout`, or the compatibility fallback
`apt_wait_timeout`.

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

Rollout telemetry is local-first by default. Remote traces are disabled unless
`OTEL_EXPORTER_OTLP_ENDPOINT` is supplied, and Sentry is disabled unless
`SENTRY_DSN` is supplied. When enabled, the CLI emits redacted rollout phase
spans and, on failed rollouts, a sanitized Sentry error event plus a local
`.stackkit/runs/<runId>/sentry-event.json` marker with event id/delivery status.
`SENTRY_AUTH_TOKEN` and `SENTRY_API_AUTH_TOKEN` are not accepted on target nodes.
Managed tenant spec envelopes may carry ingestion-only telemetry configuration,
but `stack-spec.yaml` never persists DSNs, OTLP endpoints, OTLP header values, or
Sentry API credentials.

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

### `stackkit backup`

Manages the Kopia backup add-on from the local host. Self-hosted backup
configuration is local-first; object-store targets remain part of the addon/Web
UI setup until public managed-backup enrollment is available.
The portable emergency export is modeled in `backup.resilience.emergencyExport`
and has a Kopia-independent manifest/runbook runner.

Common commands:

- `stackkit backup init` prints the first-run checklist.
- `stackkit backup configure --repo local:/backup/kopia` creates or reconnects the local Kopia filesystem repository inside `kopia-agent`.
- `stackkit backup status` checks whether the local Kopia repository is configured.
- `stackkit backup run` creates an ad-hoc snapshot of configured Docker volumes.
- `stackkit backup list [--json]` lists snapshots.
- `stackkit backup restore <snapshot-id> --target /tmp/stackkit-restore` restores one snapshot.
- `stackkit backup verify` runs `kopia repository validate-provider`.
- `stackkit backup emergency-export --target /backup/emergency-export` writes a portable export manifest and restore runbook without requiring a healthy Kopia repository. Use `--large-media-mode manifest-only|include|exclude` to control media handling.
- `stackkit backup migrate-from-restic [--dry-run]` runs the one-shot legacy importer.
- `stackkit backup enroll --token <token> --endpoint <url>` is the managed-service scaffold and returns a clear not-implemented error until the controller follow-ups land.

### `stackkit validate [file]`

Validates `stack-spec.yaml` by default. It also validates CUE and generated OpenTofu output when those files are present.

### `stackkit app`

Writes optional PaaS app handoff metadata to `stack-spec.yaml`. This is a
dev/handoff helper for customer-owned apps; it does not make the app
StackKit-owned, and `stackkit apply` records handoff state rather than
deploying or managing the customer app lifecycle.

Subcommands:

- `app add <name>`

Common flags for `app add`:

- `--image`
- `--kind` (`sveltekit` currently)
- `--port`
- `--host`
- `--auth` (`login-gateway` or `public`)
- `--health-path`
- `--env KEY=value`
- `--secret KEY=env:NAME|doppler:NAME|vault:NAME|file:PATH`

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

- `agent install-plan` prints a non-interactive Basement Kit rollout plan. Use `--json` for machine-readable output.
- `agent self-check` prints local binary, server, and MCP gate checks. Use `--json` for machine-readable output.
- `agent prompt <scenario>` prints copy-ready prompts. Use `--list` to see scenarios.
- `agent mcp-config` prints one `stackkit` MCP client connection for `generic`, `codex`, or `claude`.

Examples:

```bash
stackkit agent install-plan --json
stackkit agent prompt basekit-autonomous-rollout
stackkit agent mcp-config --client codex --mode docs,local,server
```

`stackkit-server` also mounts the native local MCP connector at `POST /mcp` and publishes local discovery at `GET /openmcp.json`. `stackkit-mcp` is the local stdio or loopback adapter for the same user-facing `stackkit` MCP connection and uses the same registration. Both runtime forms support `docs`, `local`, `server`, and optional `actions` modes. Write tools stay disabled unless `STACKKIT_MCP_ALLOW_WRITE=true` or `stackkit-server --mcp-allow-write` is set. MCP HTTP auth uses `STACKKIT_MCP_TOKEN` or `stackkit-server --mcp-token`. Non-loopback MCP access is a protected day-2 target posture, not the default first-install path.

The write-capable MCP tools execute local CLI-equivalent StackKits operations. `stackkit_apply` and `stackkit_rollout` use `--skip-platform-apps` by default so the connector manages StackKits rollout and evidence, not customer app or managed-serverless orchestration.

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
- [INSTALLATION_PROCESSES.md](INSTALLATION_PROCESSES.md)
- [API.md](API.md)
- [stack-spec-reference.md](stack-spec-reference.md)
- [agent/agents.md](agent/agents.md)
- [agent/stackkit-mcp.md](agent/stackkit-mcp.md)
