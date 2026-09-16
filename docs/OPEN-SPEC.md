# StackKit Open Spec (v0, draft)

The StackKit Open Spec is the thin, published contract behind "the open
homelab standard": what a kit is, which lifecycle verbs every kit supports,
how placement is described, and how verification evidence is shaped. It
indexes artifacts that already ship with every release; it introduces no new
schema. The spec version follows the release tag it ships with, and changes
within v0 are additive.

Everything below is account-free. Standard Mode needs no kombify account, no
hosted kombify endpoint, and no Techstack.

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
`use-cases/`); the snapshot is a projection of it. A consumer reads the
snapshot, never the CUE, and must tolerate additive fields.

## 2. Lifecycle verbs

Every kit is driven by the same verbs through the `stackkit` CLI and the
`stackkit-mcp` connector. The verbs, their inputs and their outputs are
documented in [`docs/CLI.md`](CLI.md).

| Verb | Meaning |
| --- | --- |
| `init` | Create a StackSpec for a kit with local owner custody (`--owner-source=local`) |
| `validate` | Validate the StackSpec against the CUE contract |
| `generate` | Render deterministic rollout artifacts (standalone Docker Compose by default) |
| `apply` | Execute the generated plan on the target host |
| `verify` | Check routes, identity and service health after apply |
| `backup` (`backup run`, `backup restore …`) | Snapshot declared volumes and restore them with verified readback and explicit activation |
| `upgrade` | Move an installed kit to a newer release with compatibility checks |
| `remove` | Remove an application or the kit while keeping data explicit |

A verb either completes with evidence or fails with a terminal reason. It
never reports success for work it did not do.

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

## 4. Verification evidence

Maturity words are evidence-backed, never declared. The evidence shapes are
public JSON Schemas; the release ships the instances.

| Artifact | Shape |
| --- | --- |
| `release-evidence.json` (release asset) | [`schemas/release-evidence.schema.json`](../schemas/release-evidence.schema.json): per-check `pass` / `pending` / `not_applicable` with a reason |
| Standalone OSS end-to-end receipt | [`schemas/standalone-oss-e2e-receipt.schema.json`](../schemas/standalone-oss-e2e-receipt.schema.json) |
| Compatibility projection | [`schemas/stackkits-compatibility-v1.schema.json`](../schemas/stackkits-compatibility-v1.schema.json), published per release |
| OS, hypervisor and application compatibility evidence | [`schemas/os-compat-matrix.schema.json`](../schemas/os-compat-matrix.schema.json), [`docs/data/os-compat/latest.json`](data/os-compat/latest.json), [`docs/OS_COMPATIBILITY.md`](OS_COMPATIBILITY.md) |
| Use-case runtime evidence | [`docs/data/use-case-runtime-evidence/`](data/use-case-runtime-evidence/): fresh-guest install, setup, verify, backup and restore receipts per use case and release |

Vocabulary:

- Kits: `supported` (a cited verification path exists for the committed
  cell), `preview` (installs, but verification or recovery evidence is
  pending), `alpha` (definition only).
- Operating systems: `unverified` (no valid receipt for this release) or
  `unsupported` (policy). Absence of evidence is published as `unverified`,
  never as support.
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
