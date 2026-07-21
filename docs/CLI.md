# StackKit CLI Reference

> Last verified: 2026-07-11

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
the only public OSS kit surface for this release line. The installer also adds a
short `sk -> stackkit` symlink when the `sk` name is free — it never overwrites
an existing `sk` (e.g. `skim`). Opt out with `STACKKIT_SKIP_SK_SYMLINK=1`.
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
| `init [stackkit]` | Create a CUE-owned StackSpec. Explicit v0.6 compatibility builds also create the legacy output directory. |
| `prepare` / `prep` | Prepare local or SSH target: prerequisites, Docker checks, packaged OpenTofu check, spec validation, hardware checks. |
| `generate` / `gen` | Generate rollout artifacts from the spec and CUE contracts. |
| `plan` | Run an OpenTofu plan for the generated deployment. |
| `apply [plan-file]` | Apply generated infrastructure and optionally run verification. |
| `verify` | Run read-only post-deployment checks locally or over SSH. |
| `remove` | Destroy a StackKit deployment. |
| `status` | Show deployment state and service health. |
| `validate [file]` | Validate stack specs, CUE files, and generated OpenTofu output where present. |
| `resolve [file]` | Resolve canonical StackSpec v2 through the embedded CUE authority or return a typed v1 migration report. |
| `migrate [v1-spec-file]` | Classify v1, reconcile one explicit v2 draft, and optionally persist canonical migrated-v1 intent. |
| `addon` | List add-ons from the embedded CUE catalog; add/remove remains v0.6 compatibility-only until a governed v2 mutation contract exists. |
| `app` | v0.6 compatibility only: write optional customer-owned PaaS handoff metadata. |
| `break-glass` | Inspect and rotate break-glass recovery bundles. |
| `backup` | Configure, inspect, run, verify, restore, and migrate Kopia backups. |
| `cluster` | Manage multi-node cluster membership. |
| `compat` | Show published OS support evidence and run non-destructive host prerequisite diagnostics. |
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

Development and v0.7+ builds create a canonical Architecture v2
`stack-spec.yaml` directly from the selected product's embedded CUE
`Definition.authoring.initialSpec`. They do not discover local kit paths or
create an empty deployment directory, and they make no generation/apply
readiness claim. Topology belongs to the KitDefinition, observed host facts to
Inventory, and identity to a separate handoff.

Native Architecture v2 flags:

- `--name`
- `--domain` (required by Cloud Kit and Modern Home Lab)
- `--force`, `-f`
- `--non-interactive`

The following flags and local-path input are available only in an explicitly
versioned v0.6 compatibility binary:

- `--mode`
- `--compute-tier`
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

v0.6 compatibility Owner bootstrap modes:

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
| `host_conformance` / `network_env` | Local runtime-prerequisite and network context detection. `vps_compat` is emitted in parallel as a deprecated one-minor wire alias for existing orchestrators; it carries no server-provider compatibility claim. |
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

StackSpec v1 generates the compatibility OpenTofu/tfvars output. Architecture
v2 re-resolves the current Spec and Inventory, requires the exact canonical
plan at `<outputRoot>/.stackkit/resolved-plan.json`, authorizes only a
generation-ready plan, and atomically installs its complete heterogeneous
artifact set plus manifest and receipt beneath the plan-owned `outputRoot`.
It never falls through to the v1 generator.

The following shape flags belong to the v1 compatibility generator:

- `--output`, `-o`
- `--force`, `-f`
- `--fragments`

For Architecture v2, an explicit `--output` is accepted only when it resolves
to the exact plan-owned `outputRoot`; `--force` and `--fragments` are rejected.
Managed replacement is already transactional and generation strategy belongs
to the ResolvedPlan. `stackkit verify` validates the exact generated bytes,
manifest, and receipt before it reaches the still-explicit v2 verifier boundary.

Generated files are disposable outputs and must not be hand-edited.

### `stackkit plan`

On exact v0.6, this runs the StackKit-packaged OpenTofu plan against the
generated deployment directory.

On native v0.7, `stackkit plan` is a deterministic read-only inspection of the
current canonical ResolvedPlan and its verified generation manifest, receipt,
and artifact hashes. It reports the exact Spec, Inventory, KitDefinition, plan,
and renderer identity; generation and Apply readiness; and every governed Apply
blocker. It explicitly reports `infrastructureDiff: not-available` and
`executorInvoked: false`: the command does not initialize OpenTofu, contact a
host or provider, or mutate files. Use `--json` for the machine-readable
inspection consumed by the native MCP tool. `--out` and `--destroy` are rejected
on v0.7 because no governed infrastructure-diff executor exists yet. The native
MCP inspection remains available with its exact same-build CLI binding when the
MCP write gate is disabled; a missing workspace is rejected rather than created.

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
On the native v0.7 line, apply intent is classified before deploy logging,
rollout recording, telemetry, or tenant-event reporting starts. A managed job
without local intent performs only its required read-only Admin fetch first;
a fetched v1 document is rejected before local spec/bundle persistence and no
lifecycle event is posted. A missing unmanaged intent or any admitted local v1
document likewise leaves no `.stackkit` artifacts. Exact v0.6 retains its
compatibility flow.

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

Operates an already materialized local `kopia-agent` deployment. The public CLI
does not install or generate that container, and the presence of these commands
is not fresh-host deployment proof. Filesystem repository operations are
local-first; object-store targets remain part of deployment configuration.
The portable emergency export is modeled in `backup.resilience.emergencyExport`
and has a Kopia-independent manifest/runbook runner.

The Kopia-agent and Restic operations below are exact-v0.6 compatibility
commands. Development and v0.7+ builds reject configure, status, run, list,
restore, verify, migrate, and managed enroll before target creation, Docker,
Kopia, or network work because no CUE-governed native-v2 backup execution
contract exists yet. The read-only `backup init` checklist and
Kopia-independent `backup emergency-export` remain available without claiming
v2 backup readiness.

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

Fleet enrollment and controller operations are outside the public CLI contract.

### `stackkit validate [file]`

Validates `stack-spec.yaml` by default. It also validates CUE and generated OpenTofu output when those files are present.

### `stackkit migrate [v1-spec-file]`

Reads v1 only through the one-minor migration adapter. A projection-only run is
diagnostic and cannot authorize generation. To produce executable intent, pass a
complete explicit v2 draft with an explicit target kit:

```bash
stackkit migrate legacy.yaml \
  --target-kit basement-kit \
  --complete-with explicit-v2.yaml \
  --spec-output stack-spec.v2.json \
  --output .stackkit/migration-result.json
```

`--spec-output` always writes deterministic canonical JSON, regardless of the
report `--format`. The adapter owns `source.kind: migrated-v1` and its report
hash; callers must not pre-author migration lineage. Both files default to
fail-if-exists, and `--force` atomically replaces each destination through a
held filesystem root. The self-contained audit report is committed first; the
canonical spec is installed only after report publication succeeds, so an
in-place migration cannot replace the legacy source without its audit result.
Report and spec paths must differ, remain beneath the working directory, and
cannot both be stdout aliases. An in-place replacement of the legacy spec is
allowed only when `--force` is explicit and after the exact v1 source has been
read and resolved.

v0.6 remains the sole compatibility minor for first-party v1 execution. From
v0.7/M+1, raw v1 never falls through to the legacy implementation of `generate`,
`plan`, `apply`, `verify`, or the legacy remote verifier. Those commands return the shared typed
`migration_required` or `migration_blocked` error and retain the complete
migration report. Retry them with `--spec <stack-spec.v2.json>` after completion.

### `stackkit addon`

`stackkit addon list` is native on the v0.7 line. It reads only the CUE-bound
catalog embedded in the CLI; it never scans the checkout. With a validated
canonical v2 StackSpec, the list is filtered to its explicit `kit.slug` and
shows enabled selections. Without a spec it shows the product-wide catalog.
Catalog presence means only that an add-on contract exists for a kit; it is not
evidence that mutation, planning, generation, apply, or runtime execution is
ready.

`stackkit addon add` and `stackkit addon remove` remain available only in an
explicit v0.6 build. In v0.7 they fail before reading or writing a spec because
the current HA add-on requires a coordinated topology and availability
transition. Native mutation will require a catalog-declared authoring mode,
catalog-bound validation, and compare-and-swap-safe canonical spec persistence.

### `stackkit app`

This command is available only on the explicit v0.6 compatibility line. It
writes optional PaaS app handoff metadata to the legacy `stack-spec.yaml` and
does not make the app StackKit-owned.

Architecture v2 deliberately has no arbitrary image-to-module or image-to-route
mapping: StackKit-owned applications come from the governed CUE catalog, while
a future customer-workload desired-state contract belongs to TechStack. That
TechStack contract does not exist yet, so v0.7+ fails closed instead of claiming
that deployment ownership has already moved.

Subcommands:

- `app add <name>`

v0.6 compatibility flags for `app add`:

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

Resolves the current operating system and architecture against the published OS
compatibility evidence, then runs non-destructive diagnostics for local
container-host prerequisites such as namespaces, storage, bridge networking,
iptables, and cgroups. Host diagnostics do not certify or recommend a server
provider. StackKits does not publish provider pricing or provider-specific
server configuration.

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
- `kit upgrade` (also available top-level as `stackkit upgrade` — the kit namespace is the default for the everyday upgrade verb)
- `kit upgrade rollback` (also `stackkit upgrade rollback`)
- `kit verify`

These commands are for registry, release, lifecycle, and parity workflows. Admin API calls require the relevant endpoint/token configuration documented in [CONFIGURATION.md](CONFIGURATION.md).

`stackkit upgrade` is a top-level alias for `stackkit kit upgrade`: the kit is the default upgrade target, so you do not type `kit`. Upgrading a single tool/module (not the whole kit) stays under the explicit `module` namespace (e.g. a future `stackkit service upgrade`).

### `stackkit module`

Subcommands:

- `module release`
- `module verify-db`
- `module verify-version-bumps`

Use these for module contract hash release, DB parity checks, and the offline
merge-base guard that requires a strictly higher SemVer whenever a canonical
module contract changes. `verify-version-bumps` accepts exactly one of
`--baseline-ref` or `--baseline-tree`; new modules are allowed, but every
declared module version must be valid SemVer.
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

Exact-v0.6 compatibility only: posts locally captured wizard answers or
free-form intents to the Admin API. Native v0.7 rejects the command before
environment, answer-file, payload, or network access because the v1
`answers`/derived-context/compute payload is not a canonical v2 intent contract.
Use `--dry-run` to inspect the compatibility payload on an exact-v0.6 build.

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
