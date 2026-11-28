# StackKit Open Spec (v0, draft)

The StackKit Open Spec is the thin, published contract behind "the open
homelab standard": what a kit is, which lifecycle verbs every kit supports,
how placement is described, and how verification evidence is shaped. It
indexes artifacts that already ship with every release; it introduces no new
schema. The spec version follows the release tag it ships with, and changes
within v0 are additive.

Everything below works without a kombify account. Standard Mode needs no kombify account, no
hosted kombify endpoint, and no Homelab.

## 1. Architecture snapshot

The facts-only architecture snapshot is the machine-readable description of a
release: kits, topology, capabilities, placement model, modules, add-ons, and
support maturity.

| Artifact | Role |
| --- | --- |
| [`architecture-snapshot.json`](../architecture-snapshot.json) | The snapshot for this release |
| [`architecture-snapshot.schema.json`](../architecture-snapshot.schema.json) | Closed downstream contract (schema v3): `placement_model`, `stackkits`, `mode_matrix`, `modules`, `addons` |
| [`architecture/v2/authority-manifest.json`](../architecture/v2/authority-manifest.json) | Authority manifest for the Architecture v2 kit definitions |

CUE is the technical source of truth (`foundation/`, the kit directories,
`use-cases/`, `modules/`); the snapshot is a projection of it. Curated
application modules declare their containers with the typed
[`#AppModuleV3`](../foundation/app_module_v3.cue) contract
([ADR-0055](ADR/ADR-0055-declarative-app-module-v3-and-three-zone-lifecycle.md)). A consumer reads the
snapshot, never the CUE, and must tolerate additive fields.

## 2. Lifecycle verbs

Every kit is driven by the same verbs through the `stackkit` CLI and the
`stackkit-mcp` connector. The verbs, their inputs and their outputs are
documented in [`docs/CLI.md`](CLI.md).

| Verb | Meaning |
| --- | --- |
| `init` | Create a StackSpec for a kit with local owner custody (`--owner-source=local`) |
| `validate` | Validate the StackSpec against the CUE contract |
| `generate` | Render deterministic rollout artifacts: Docker Compose workloads and the OpenTofu Stage 1 per-stack roots that execute them |
| `apply` | Execute the generated plan on the target host (`--setup-style bootstrapped` by default, `--setup-style clean` or `--clean-rollout` for a Clean Rollout) |
| `verify` | Check routes, identity and service health after apply |
| `backup` (`backup run`, `backup restore …`) | Snapshot declared volumes and restore them with verified readback and explicit activation |
| `upgrade` | Move an installed kit to a newer release with compatibility checks |
| `remove` | Remove an application or the kit while keeping data explicit |
| `retry-appsetup` | Run a failed Tier-2 app personalization step again |
| `diagnose` | Write a redacted, secret-free host health snapshot ([ADR-0051](ADR/ADR-0051-server-mcp-host-execution-authority.md)) |
| `repair <action>` | Run one bounded, effect-classified host repair under the repair policy and circuit breaker ([ADR-0051](ADR/ADR-0051-server-mcp-host-execution-authority.md)) |

A verb either completes with evidence or fails with a terminal reason. It
never reports success for work it did not do.

### Execution engine

OpenTofu Stage 1 over Docker Compose is the single core execution engine for
curated workloads
([ADR-0054](ADR/ADR-0054-single-core-engine-and-coolify-extensibility-boundary.md)).
Standard mode executes the per-stack OpenTofu roots sequentially without
Terramate; Homelab-managed advanced mode orchestrates the same roots with
Terramate. `status` and `diagnose` pair the OpenTofu plan with live container,
image-digest, health and route observation, so a clean plan alone never reports
a healthy host. Layer-2 PaaS engines are not part of the core execution path.

Coolify `4.7.5+` is an optional Layer-3 extension for its template marketplace
and custom or Git-built apps. It never manages curated apps and holds no
custody. Kit-Traefik and the access gate own host ports 80/443, certificates
and sign-in policy in every address zone; Coolify's proxy runs HTTP-only on a
private network behind them, and admission fails closed if it binds 80/443 or
enables ACME.

### Rollout tiers

- **Tier 1** runs in every rollout: core services and the required safety setup
  (digest-pinned images, private networks, generated secrets, no default
  passwords, closed open registration, Pocket ID with the access gate,
  private-by-default routing). It fails closed.
- **Tier 2** runs only with the default `bootstrapped` setup style: per-app
  personalization such as owner accounts and single sign-on wiring. A Tier-2
  failure never fails the apply; it records `bootstrap_degraded`, keeps the app
  private behind the access gate and names `retry-appsetup`. The `clean` setup
  style (Clean Rollout) runs Tier 1 only.

### Address zones

Three address zones are equal first-class modes, selected with
`init --address-zone` or inferred from the domain and kit
([ADR-0055](ADR/ADR-0055-declarative-app-module-v3-and-three-zone-lifecycle.md)):

| Zone | Example | Contract |
| --- | --- | --- |
| `custom-domain` | `*.example.com` | ACME HTTP-01 or DNS-01 directly on Kit-Traefik; no `kombify.me` dependency |
| `kombify-me` | `{base}.kombify.me` | No own domain needed; per-host `__Host-` sessions unless public-suffix isolation is proven |
| `local-only` | `*.lab.home` | Local owner CA and resolver; no internet, cloud or account dependency |

## 3. Placement taxonomy

Placement describes where a kit or module may run. The vocabulary is
published in [`foundation/placement.cue`](../foundation/placement.cue):

- `#PlacementMode`: `local-only`, `standard`, `managed-serverless`.
- `#PlacementSupport`: per-module eligibility metadata (which modes a module
  can run in).

The open-source lifecycle realizes `local-only` and `standard`.
`managed-serverless` is eligibility metadata only; its realization is outside
StackKits and outside this spec. Kit-level placement appears in the snapshot's
`placement_model`.

`managed_app_fit` is a separate, default-false catalog fact, not a placement
mode or a launch approval. A shared profile declares its total memory cap
(128–512 MB), no Docker socket, and either an isolated volume or object storage.
Vaultwarden fits with an isolated volume. Cloudreve does not qualify: the current
profile uses a local volume and has a 768 MB memory cap; a capped object-storage
profile is not published yet. The current automation home profile includes
PostgreSQL and has a combined 768 MB cap; it also does not qualify. A declared
admission prerequisite blocks eligibility. Provider selection, backups, ingress
and plan enforcement stay in the Homelab control plane.

## 4. Verification evidence

Maturity words are evidence-backed, never declared. The evidence shapes are
public JSON Schemas; the release ships the instances.

| Artifact | Shape |
| --- | --- |
| `release-evidence.json` (release asset) | [`schemas/release-evidence.schema.json`](../schemas/release-evidence.schema.json): per-check `pass` / `pending` / `not_applicable` with a reason |
| Standalone OSS end-to-end receipt | [`schemas/standalone-oss-e2e-receipt.schema.json`](../schemas/standalone-oss-e2e-receipt.schema.json) |
| Compatibility projection | [`schemas/stackkits-compatibility-v1.schema.json`](../schemas/stackkits-compatibility-v1.schema.json), published per release |
| OS, hypervisor and application compatibility evidence | [`schemas/os-compat-matrix.schema.json`](../schemas/os-compat-matrix.schema.json), [`docs/data/os-compat/latest.json`](data/os-compat/latest.json), [`docs/OS_COMPATIBILITY.md`](OS_COMPATIBILITY.md): fresh-guest install, setup, verify, backup and restore results per operating system, hypervisor and use case |

Vocabulary:

- Kits: `supported` (a cited verification path exists for the committed
  cell), `preview` (installs, but verification or recovery evidence is
  pending), `alpha` (definition only).
- Compatibility rows (operating systems, hypervisors, kits by environment,
  applications): `supported` (every lifecycle phase passed in the newest run
  on the evidence release), `preview` (install through verify passed; a later
  phase failed) or `unverified` (no completed run on that release yet, or the
  newest run failed before verify). Absence of evidence is published as
  `unverified`, never as support.
- A status widens only when a cited run exists for the exact release.

## 5. Conformance

A tool or service is StackKit-compatible when it

1. consumes the architecture snapshot of the exact release it targets,
2. drives kits only through the lifecycle verbs above (directly or through
   the MCP connector) and preserves their evidence,
3. describes placement with the published taxonomy, and
4. publishes verification results in the evidence shapes above with the
   release tag they belong to.

## Status

This is a v0 draft published with the public beta. A separate specification
repository with a versioning policy follows after the beta; until then this
document and the linked artifacts are the spec.
