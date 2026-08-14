# StackKit CLI Reference

> Last verified: 2026-07-27

This page summarizes the implemented `stackkit` command surface. Cobra command definitions under `cmd/stackkit/commands/` are the source of truth.

## Installation

```bash
curl -sSL https://install.stackkit.cc | sh
stackkit version
```

The shared installer installs `stackkit`, `stackkit-server`, `stackkit-mcp`,
packaged OpenTofu, packaged Terramate, and the public kit catalog under
`~/.stackkits`, so `stackkit init basement-kit` works from a clean directory
without a repo checkout. Basement Kit is the verified standalone path; Cloud Kit
exposes the same account-free intent bootstrap with an explicit domain and ships
as a preview, while Modern Homelab is alpha scaffolding. The installer also adds a
short `sk -> stackkit` symlink when the `sk` name is free — it never overwrites
an existing `sk` (e.g. `skim`). Opt out with `STACKKIT_SKIP_SK_SYMLINK=1`.
Unpinned installer runs use the GitHub `releases/latest` of
`kombifyio/stackKits`. To test a prerelease, export the exact candidate tag
before invoking the installer:

```bash
export STACKKIT_RELEASE_VERSION=v0.17.0-beta.1
curl -sSL https://base.stackkit.cc | sh
```

For a single copy/paste command, pass the pin to the shell that executes the
installer:

```bash
env STACKKIT_RELEASE_VERSION=v0.17.0-beta.1 sh -c 'curl -sSL https://base.stackkit.cc | sh'
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
| `--correlation-id` | | unset | Bind one validated caller correlation ID to the collision-resistant local rollout run and its events. |

## Primary Workflow

```bash
stackkit init --owner-source=local
stackkit validate
stackkit generate
stackkit apply
stackkit verify --json
```

`init` defaults to `basement-kit`. `prepare` remains an optional host-conformance
step. `plan` is used only when the selected Product Apply executor is OpenTofu;
the v0.8 Basement default executes Compose directly.

## Top-Level Commands

| Command | Purpose |
| --- | --- |
| `init [stackkit]` | Create a CUE-owned StackSpec. Published v0.6 binaries also created a legacy output directory; current source uses native v2 plus local Owner custody. |
| `prepare` / `prep` | Prepare local or SSH target: prerequisites, Docker checks, packaged OpenTofu check, spec validation, hardware checks. |
| `generate` / `gen` | Generate rollout artifacts from the spec and CUE contracts. |
| `plan` | Run an OpenTofu plan for the generated deployment. |
| `apply [plan-file]` | Apply generated infrastructure and optionally run verification. |
| `verify` | Run read-only post-deployment checks locally or over SSH. |
| `drift` | Detect native-v2 local drift read-only; reconciliation remains fail-closed until its required authority exists. |
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
| `agent` | Emit agent-native install plans, prompts, self-checks, and MCP config. |
| `kit` | Public release list, verify, and deprecated upgrade alias; import/export and registry maintenance exist only in `stackkit-publisher`. |
| `logs` | List and read structured deploy logs. |
| `registry` | Inspect the embedded CUE-derived registry snapshot. |
| `completion` | Generate shell completions. |
| `version` | Print version, commit, build date, Go version, and OS/arch. |

Publisher-only `module`, registry-maintenance, DB, and Admin
operations are not part of this public command tree. They compile only into the
private `stackkit-publisher` executable.

## Command Details

### `stackkit init [stackkit]`

Development and v0.7+ builds create a canonical Architecture v2
`stack-spec.yaml` directly from the selected product's embedded CUE
`Definition.authoring.initialSpec`. They do not discover local kit paths or
create an empty deployment directory, and they make no generation/apply
readiness claim. Topology belongs to the KitDefinition and observed host facts
to Inventory. The local PocketID Owner projection and `ownerRef`/step-ca
lifecycle authority are initialized as local custody outside StackSpec; an
optional Techstack or kombify Cloud projection may propose user-approved
identity data but cannot replace that authority.

For a published build, `init` first resolves its exact embedded SemVer through
the public Release Index, verifies the index, trusted root, archive, SBOM and
attestations, and proves that the archive's canonical `stackkit` executable is
byte-identical to the running binary. Only then may it persist StackSpec or
Owner custody. The verified trust set and receipt are installed atomically
under `.stackkit/releases/`; a damaged exact cache or substituted binary fails
without a network fallback. The literal `dev` build and GoReleaser
`-devel` snapshots run only as development artifacts and do not claim
published-release authority.

The published-init executable proof currently has a kernel-bound source on
Linux (`/proc/self/exe`) and Windows (the loader-locked running image). A
released Darwin build fails closed during `init` until an equivalently safe
current-process-image source is implemented. This is the support boundary of
the released-init bootstrap proof, not a general claim about every v0.8 command
or artifact on those operating systems.

Native Architecture v2 flags:

- `--name`
- `--domain` (required by Cloud Kit and Modern Home Lab)
- `--expected-spec-hash` (required for an intentional replacement)
- `--non-interactive`
- `--owner-source=local`
- `--owner-email`
- `--owner-username`
- `--owner-display-name`

The following init flags belong to the exact-v0.6 compatibility surface. They
are not part of the native v0.8 workflow and do not restore the removed v1
generator:

- `--force`, `-f`
- `--mode`
- `--compute-tier`
- `--local-dns`
- `--local-name`
- `--admin-email`
- `--owner-bootstrap-mode`
- `--owner-source=cloud`
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

Provider creation, credentials, SSH transport, and host package lifecycle are
outside the standalone StackKits boundary. Current native-v2 `prepare` therefore
fails closed instead of inferring or mutating a host. Techstack or another
external host owner may perform those operations and hand StackKits observed
Inventory plus an execution-channel binding. The former remote preparation
flags and phase events describe immutable v0.6 artifacts only.

### `stackkit generate`

Current-source `generate` accepts canonical StackSpec v2 only. It resolves the
current Spec and optional Inventory, atomically writes the
exact canonical plan to `<outputRoot>/.stackkit/resolved-plan.json`, authorizes
only that generation-ready plan, and atomically installs its complete
heterogeneous artifact set plus manifest and receipt beneath the plan-owned
`outputRoot`. A separate `stackkit resolve --output ...` step is not required
for the normal workflow. `resolve` remains a read-only inspection command
unless an explicit output is requested. Generation never falls through to the
retired v1 generator.

`--output`/`-o` remains a valid native override only when it resolves to the
exact plan-owned `outputRoot`. The following deprecated flags remain parseable
only to return a structured denial; they never select a compatibility generator:

- `--force`, `-f`
- `--fragments`

For Architecture v2, an explicit `--output` is accepted only when it resolves
to the exact plan-owned `outputRoot`; `--force` and `--fragments` are rejected.
Managed replacement is already transactional and generation strategy belongs
to the ResolvedPlan. `stackkit verify` validates the exact generated bytes,
manifest, and receipt before it reaches the still-explicit v2 verifier boundary.

Generated files are disposable outputs and must not be hand-edited.

### `stackkit plan`

Exact-v0.6 compatibility builds can still inspect historical generated
deployments with packaged OpenTofu. The native v0.8 build never selects that path.

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

Applies only the exact locally generated, verified ResolvedPlan artifact closure.
It does not infer a host, fetch tenant state, or fall back to legacy generation.

Common flags:

- `--auto-approve`
- `--verify`
- `--verify-http`
- `--verify-strict`
- `--json`

The public command applies only local StackSpec, ResolvedPlan, owner-custody,
and generated artifacts. It has no tenant-deployment, Admin endpoint, Admin
token, or kombify.me registration path. Publisher and migration operations
that still consume private control-plane contracts compile only into the
non-public `stackkit-publisher` executable.

Unless `--no-log` is set, rollout evidence is written under
`.stackkit/runs/<runId>/` next to the structured log. On the native v0.8 line,
apply intent is classified before deploy logging or rollout recording starts.
A missing local intent or an admitted local v1 document leaves no `.stackkit`
run artifacts.

Rollout telemetry is local-first by default. Remote traces are disabled unless
`OTEL_EXPORTER_OTLP_ENDPOINT` is supplied, and Sentry is disabled unless
`SENTRY_DSN` is supplied. When enabled, the CLI emits redacted rollout phase
spans and, on failed rollouts, a sanitized Sentry error event plus a local
`.stackkit/runs/<runId>/sentry-event.json` marker with event id/delivery status.
`SENTRY_AUTH_TOKEN` and `SENTRY_API_AUTH_TOKEN` are not accepted on target nodes.
`stack-spec.yaml` never persists DSNs, OTLP endpoints, OTLP header values, or
Sentry API credentials.

`--json` returns `stackkit.apply-result/v2` inside
`stackkit.command-result/v1`. The result includes secret-free
`stackkit.runtime-observation/v2` records bound to the exact Stack, Plan,
signed Apply result, CLI run, Site, node, and execution channel. A configured
Standard process runtime is reported as `source: standard-process`; hybrid
plans retain `local-runtime` on local scopes and `standard-process` only on
the exact configured process channels. This does not transfer provider,
enrollment, or server lifecycle ownership into StackKits.

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

Native-v2 JSON includes the exact runtime mode plus
`stackkit.runtime-observation/v2`. When Verify can prove only the signed Apply
observation, it reports `source: verified-apply-evidence` and `live: false`
instead of inventing current service health. Local Basement probes remain a
separate truthful `runtime.live: true` result when they actually ran.

### `stackkit drift`

`stackkit drift detect [--json]` reuses the read-only Architecture-v2
verification boundary. It verifies the canonical Plan, generated artifacts,
owner-signed Apply evidence, local Owner binding, exact Basement Compose
runtime, services, and probes without `tofu refresh`, output locks, lifecycle
logs, or persisted state. JSON is returned as `stackkit.drift-report/v1`
inside `stackkit.command-result/v1`. Verified runtime Compose, service-set, and
health deviations return `hasDrift: true`; Plan, artifact, Apply-evidence,
Owner-custody, and signature/integrity failures remain hard fail-closed rather
than being downgraded to ordinary drift.

`stackkit drift reconcile --mode standard|advanced` currently denies before
rendering or side effects. Standard reconciliation remains unavailable until
its Owner-approved snapshot/Apply/Verify/rollback transaction is wired to this
command. Advanced reconciliation also remains unavailable: offline capability
verification, Owner-installed trust, deterministic Terramate rendering, and
signed change-set creation exist, but change-set execution is not yet exposed.

### `stackkit advanced trust`

Advanced operations remain optional and account-free. They trust no network
service at execution time. The local Owner explicitly imports an exact public
issuer bundle:

```bash
stackkit advanced trust import \
  --bundle techstack-advanced-trust.json \
  --expect-sha256 sha256:<64-lowercase-hex> \
  --owner-approve \
  --json
```

The CLI verifies the digest and canonical
`stackkit.advanced-trust-bundle/v1` before entering the lifecycle mutation,
then stores an owner-signed private record under
`.stackkit/advanced/trust/bundle.json`. The bundle contains only issuer IDs
and Ed25519 public verification keys; credentials, account tokens, endpoints,
and private keys are invalid inputs.

`stackkit advanced trust inspect [--json]` is read-only and verifies the local
Owner signature and file permissions before returning only the bundle digest,
Owner reference, issuer IDs, and key IDs. Techstack may issue short-lived
capabilities against one of these keys, but cannot install trust or replace
local Owner custody.

`stackkit advanced change-set create --capability <file> --candidate-spec
<file> [--json]` accepts only a candidate whose generation target is
`terramate` and whose stack ID and output root match the verified current
baseline. It resolves both plans in memory and verifies local Owner custody,
the owner-signed trust record, and the short-lived capability before entering
the lifecycle lock or creating a temporary directory. After revalidation it
uses the existing pure OpenTofu/Terramate renderer, writes no generated
deployment output, and atomically stores an owner-signed, content-addressed
change set under `.stackkit/advanced/change-sets/`.

Capability denial is emitted as `stackkit.operation-denial/v1` with a stable
public reason code. Creating a change set does not invoke Terramate, OpenTofu,
Docker, Techstack, or a network service.

### `stackkit remove`

Canonical Architecture v2 removes one exact applied workload:

```bash
stackkit remove --workload photos
stackkit remove --workload photos --auto-approve --json
```

The command verifies current Plan, generation, Apply, and Owner authority,
recovers the sealed applied runtime request, and dispatches a fresh
Owner-signed five-minute removal request through the digest-pinned Standard
execution channel. Success requires selected-runtime-owner `absent` readback
for the exact requirement, instance, and applied artifact digest. Request and
result evidence is persisted below `.stackkit/evidence/removal/`.

OpenTofu/Docker whole-deployment cleanup plus `--purge` and `--force` are
exact-v0.6 compatibility behavior only. Native v2 rejects those flags and
never falls back to label-based Docker deletion.

### `stackkit status`

Reads the verified local Plan, lifecycle state, signed Apply result, service
access manifest, and provider-neutral runtime projection. Native-v2 JSON is
`stackkit.status/v2` and includes `applyState`, the exact local or Standard
process runtime mode, and `stackkit.runtime-observation/v2` records. Status is
read-only: historical signed Apply evidence is marked `live: false`. If Apply
evidence is absent or invalid, the command still returns the Plan and a
`stackkit.actionable-error/v1` recovery contract instead of leaving the
operator at a dead end.

### `stackkit backup`

The native v0.8 path operates the pinned `kopia-agent` rendered and applied by
the Basement core. Before every repository, snapshot, staged-restore, live
activation, or rollback side effect, the command revalidates the current
StackSpec and ResolvedPlan, generated manifest and receipt, exact CUE
backup-policy artifact, local Owner custody, current Apply result, and its
owner-signed receipt. Mutating backup operations share the exclusive local
lifecycle-mutation lock; `status` remains read-only. Authority is checked again
while the lock is held.

Repository, source, exclusions, service identity, and passphrase custody are
not CLI inputs. The passphrase remains in owner-only local custody and is sent
to Kopia only over a redacted stdin boundary. Successful JSON uses
`stackkit.command-result/v1`.

`restore` accepts only the content-addressed ID of an owner-signed native-v2
snapshot anchor. It requires explicit local Owner approval, writes an
owner-signed recovery anchor before invoking Kopia, verifies every snapshot
file before and after extraction, and restores into a deterministic directory
inside the CUE-owned `kopia-restore-staging` volume. The caller cannot choose a
raw Kopia snapshot ID or target path. The final owner-signed result also proves
that the unchanged live Stack authority, PocketID Owner binding, services, and
probes still verify. Staging is excluded from future snapshots, and successful
extraction is journaled before post-verification so a retry does not restore
the same bytes again.

This command is a **verified isolated staging restore**. Live replacement is a
separate, explicit Owner-approved step:

```bash
stackkit backup restore activate sha256:<restore-result-id> \
  --owner-approve \
  --operation-id restore-activation-20260727 \
  --json
```

Activation accepts only the signed restore-result ID, derives the exact managed
volume set from the verified Plan and artifact manifest, and creates a
mandatory Kopia safety snapshot before stopping services or copying data. It
keeps per-volume rollback copies, starts the Basement runtime, and commits only
after the local Owner binding and all selected services and probes verify.

An interrupted activation blocks ordinary lifecycle mutations. Recovery is
explicit and fail-closed:

```bash
stackkit backup restore recover restore-activation-20260727 \
  --owner-approve \
  --rollback \
  --json
```

Recovery reopens the exact owner-signed mutation journal. If a crash occurred
after an activation copy started but before completion was recorded, the
in-flight volume is conservatively treated as modified and rolled back. The
runtime is restarted and Owner/service verification must pass before the
operation is marked recovered.

`list`, `verify`, and `migrate-from-restic` still use the exact-v0.6
compatibility implementation and are rejected by native-v2 builds until their
corresponding lifecycle slices land. The Kopia-independent `emergency-export`
command remains available without claiming archive bytes or a completed data
restore.

The internal owner-signed `stackkit.executor-state-snapshot/v1` store is now
available for the command-level upgrade implementation. It already provides an
immutable verified installed-release proof, exact archive-to-executable byte
matching, current PocketID Owner-binding verification, persisted Kopia-anchor
verification, private atomic CAS objects, and a final operation commit marker.
Its capture entry point is intentionally sealed and cannot yet be called by the
CLI: the next upgrade slice must first add the verifier that binds every
StackSpec/plan/artifact/runtime byte to the current Plan/Generation/Apply
manifest and receipts. Therefore this release does not yet claim a working
transactional upgrade or rollback command. Because Compose is the real Basement
executor, no fictional `terraform.tfstate` is created. An actual OpenTofu target
remains `unsupported_state_snapshot` until a Product Apply executor owns state
pull/restore.

Common commands:

- `stackkit backup init` prints the first-run checklist.
- `stackkit backup configure [--json]` creates or reconnects the exact CUE-governed local filesystem repository.
- `stackkit backup status [--json]` checks the exact local repository.
- `stackkit backup run [--operation-id ID] [--json]` creates a crash-consistent snapshot of the governed read-only Docker-volume source. Reusing an operation ID returns its existing exact snapshot instead of duplicating it.
- `stackkit backup list [--json]` lists snapshots.
- `stackkit backup restore sha256:<snapshot-anchor-id> --owner-approve [--operation-id ID] [--json]` verifies and extracts one signed snapshot into isolated CUE-owned staging. Reusing the operation ID returns the existing exact result.
- `stackkit backup restore activate sha256:<restore-result-id> --owner-approve [--operation-id ID] [--json]` creates the mandatory safety snapshot, activates the exact Plan-owned volume set, restarts the runtime, and verifies Owner binding and services.
- `stackkit backup restore recover <activation-operation-id> --owner-approve --rollback [--json]` explicitly rolls back an interrupted activation from its signed journal and verifies the recovered runtime.
- `stackkit backup verify` runs `kopia repository validate-provider`.
- `stackkit backup emergency-export --target /backup/emergency-export` writes a portable export manifest and restore runbook without requiring a healthy Kopia repository. Use `--large-media-mode manifest-only|include|exclude` to control media handling.
- `stackkit backup migrate-from-restic [--dry-run]` runs the one-shot legacy importer.

Fleet enrollment and controller operations are outside the public CLI contract.
There are no native `--repo`, `--container`, source, exclusion, password,
raw-snapshot, or restore-target overrides. Techstack may dispatch this exact
published CLI and consume its versioned result; it does not replace local
authority. kombify Cloud identity sync is an optional, user-approved
convenience for the local PocketID/TinyAuth identity plane and is never required
to authorize or execute a restore.

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

Immutable v0.6 release artifacts remain available for rollback and migration
inspection. Native v0.8 rejects raw v1 operational commands. The CLI generator
has additionally been removed from current source across every build identity,
so even an exact-v0.6 version string cannot restore v1 generation. Retry the
native workflow with `--spec <stack-spec.v2.json>` after migration completion.

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

The write-capable MCP tools execute the native, identity-bound CLI operations
individually. There is no combined MCP rollout macro or MCP update action.
Techstack may sequence the same published commands through versioned JSON and
JSONL contracts, but it does not gain a second lifecycle implementation.

### `stackkit kit`

Subcommands:

- `kit list`
- `kit upgrade` (deprecated alias for top-level `stackkit upgrade`)
- `kit verify` (deprecated alias for top-level `stackkit verify`)

The public commands resolve and verify signed GitHub release indexes without an
account. Import, export, history, roundtrip, unlock, and Admin resolver
operations are Publisher-only.

`stackkit upgrade` is the canonical public command. Upgrading a single
tool/module (not the whole Kit) stays outside the public v0.8 lifecycle.

### `stackkit-publisher module` (Publisher-only)

Subcommands:

- `module release`
- `module verify-db`
- `module verify-version-bumps`

These commands exist only in `stackkit-publisher`. Use them for module contract
hash release, DB parity checks, and the offline
merge-base guard that requires a strictly higher SemVer whenever a canonical
module contract changes. `verify-version-bumps` accepts exactly one of
`--baseline-ref` or `--baseline-tree`; new modules are allowed, but every
declared module version must be valid SemVer.
Publisher Admin API auth follows the publisher kit commands:
`SERVICE_AUTH_SECRET` mints the
preferred `X-Kombify-Service-Auth` token, with `STACKKIT_ADMIN_TOKEN` or
`KOMBIFY_ADMIN_API_KEY` only as legacy Bearer fallbacks.

### `stackkit registry`

Subcommands:

- `registry info`

The public command reads the embedded CUE-derived snapshot. `snapshot` and
`bake-from-cue` exist only in `stackkit-publisher`.

### `stackkit logs`

Subcommands:

- `logs list [--json]`
- `logs get <run-id> [--json] [--cursor CURSOR] [--max-events N] [--max-bytes N]`
- `logs latest [--json] [--cursor CURSOR] [--max-events N] [--max-bytes N]`
- `logs [run-id] [--jsonl]` (compatibility display/raw JSONL form)

Structured deploy logs live under `.stackkit/logs` unless configured otherwise.
`--json` returns `stackkit.log-list/v1` or `stackkit.log-run/v1` inside the
command-result envelope, including content-addressed local evidence links.
Run IDs are exact basenames returned by `logs list`; traversal and unknown IDs
fail with `stackkit.actionable-error/v1` guidance. Current runs use a UTC
nanosecond timestamp plus a cryptographic nonce and exclusive file creation;
legacy timestamp-only IDs remain readable. JSON reads stream bounded pages
(default 200 events/256 KiB, hard maximum 2000 events/1 MiB). Every page
returns `cursor`, optional `next_cursor`, and `truncated`; a cursor is bound to
the exact run ID and complete file digest, so appended or replaced evidence
must be restarted without the stale cursor. Secret-shaped keys and inline
credentials are redacted before write and again during structured reads.

`--json` on Apply, Status, and Verify reserves stdout for exactly one versioned
JSON value. Early failures and authorization denials retain a non-zero exit and
emit `stackkit.command-result/v1` with `stackkit.actionable-error/v1`; human
banner/status text is suppressed. `--progress-jsonl -` cannot share stdout with
these single-document modes.

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
