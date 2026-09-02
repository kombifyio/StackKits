---
adr: ADR-0039
title: Module-local compute profiles and explicit workload alternatives
status: Accepted
date: 2026-09-01
last_verified: 2026-09-01
---

# ADR-0039: Module-local compute profiles and explicit workload alternatives

**Status:** Accepted · **Owner:** StackKits

**Supersedes for native authoring:** ADR-0036 and ADR-0037. Their global graph
semantics remain only in the explicit `stackkit/v2alpha1` compatibility adapter.
**Related:** ADR-0029, ADR-0031, ADR-0035, ADR-0038

## Context

A kit-wide `install.computeTier` cannot describe a small core with a full photo
module, or independently size multiple applications. A global `low` choice also
coupled a use-case exclusion to unrelated modules. Treating those graphs as a
recommendation vocabulary would preserve that architectural error in the UI,
CLI, compiler, and downstream consumers.

## Decision

Native intent uses `stackkit/v2alpha2`. It forbids `install.computeTier` and
selects `modules.<module_id>.computeProfile` independently for each selected
resource-bearing module. `low`, `standard`, and `high` are profile IDs, not
universal capacity or quality promises. `standard` is not an implicit fallback.

| Axis | Authority and native intent |
|---|---|
| Product | Explicit StackKit definition and workload policy |
| Workload implementation | `workloads.<use_case_id>.alternative` |
| Compute | CUE `computeProfiles`; `modules.<module_id>.computeProfile` |
| Storage | Independently declared `storageProfiles` and `storageProfile` |
| Accelerator | Independently declared `acceleratorProfiles` and `acceleratorProfile` |
| Device | `nodes[].hardware.profile`, independent from the profiles above |
| Engine | `install.mode` and `install.runtime`, unchanged |

Every active module that declares a profile dimension requires an explicit
selection for that dimension. Pure policy, adapter, or plan-only contracts do
not acquire a synthetic compute profile. A profile selector refines a selected
module; it does not enable an unselected workload alternative. Required and
default use cases also require an explicit native alternative.

Existing Core Lite and Immich Lite implementations remain named workload
alternatives. A low Core Lite profile and standard Immich profile are a valid
mixed selection; choosing a low core does not exclude standard Media globally.
A catalog profile without an executable, `apply-ready` realization cannot gain
execution authority from its parent module. Undeclared profiles fail closed.
Storage and accelerator selections have the same realization requirement.
They are orthogonal refinements of a selected compute realization and cannot
create an axis-only module. A compute profile's optional `components` list is
the exact fixed module component closure, not a second graph selector;
orthogonal profiles may reference only components already materialized by that
module. A different component graph remains a separately governed workload
alternative or module contract.

The selected core profile may declare `platformManagement` for the platform it
actually ships. Authoring projects that fact and the compiler verifies it;
inventory does not select a platform, workload, profile, or device class.

## Resource and integrity semantics

Only CUE-declared resource facts may be projected. Initial application RAM
reservations are exact sums of existing pinned component reservations in GiB
(for example 64 MiB is 0.0625 GiB), not newly asserted host minima. Missing CPU,
storage, recommendation, or workload-size facts remain unknown.

For modules placed on one node, independently per CPU/RAM/storage axis:

1. The host floor is the maximum selected module host floor.
2. Reservations are additive across compute, storage, and accelerator profiles.
3. The minimum is the maximum of the host floor and aggregate reservation.
4. Recommendation is the maximum of that minimum and the sum of each module's
   maximum compute recommendation/reservation, selected orthogonal reservations,
   and explicitly declared headroom.

Missing facts never become zero. A known minimum failure takes precedence over
an incomplete declaration: `fail`, then `unverified`, then `warning`, then
`pass`. Module profiles retain common architecture, virtualization, and
attestation constraints; profile restrictions participate in the existing
placement path rather than a second solver.

ResolvedPlan binds each selected profile ID, canonical body and SHA-256, its
native/legacy source, and deterministic per-node resource demand. Persisted-plan
validation rechecks those bindings and demand against the same catalog; merely
recomputing `planHash` cannot authorize edited profile data.

The plan also retains the exact normalized inventory document covered by
`inventoryHash`, excluding the existing self-referential host binding and
conformance envelopes. Validation recomputes module admission from that body
with the same compiler routines; an edited `runtimeAdmission` or readiness
status cannot supersede insufficient facts. This is consistency evidence, not
fresh host attestation: current-source comparison and target preflight remain
mandatory. Plans produced by older authority bundles must be resolved again.
The normalized source API must agree with compatibility metadata; relabeling
a native plan cannot enable the legacy global-tier adapter.

Declared browser capacity is not observed host compatibility. OS, architecture,
virtualization, ports, runtime prerequisites and final attested capacity remain
the existing target preflight/Apply boundary. Native aggregate reservations
participate in compiler admission without rewriting intent or inventory.

## Authoring and consumers

The same authoring/compiler path serves CLI, HTTP, standalone MCP, and WebMCP
handoff. A native CLI example is:

```sh
stackkit init basement-kit --api-version stackkit/v2alpha2 \
  --domain example.org --non-interactive --owner-source=local \
  --use-case photos \
  --use-case-alternative basement-core=standalone-lite \
  --use-case-alternative photos=immich \
  --module-compute-profile stackkits-basement-core-lite-runtime=low \
  --module-compute-profile stackkits-immich-runtime=standard
```

This authors intent; it is not installation or proof of host capacity. Native
authoring never silently replaces an unavailable alternative or fills a profile
with `standard`. Existing versioned v2alpha1 files continue through the named
legacy adapter. `--compute-tier` is accepted only with explicit v2alpha1 CLI
authoring; adopting v2alpha2 requires explicit native selections, not relabeling
old bytes. The retained State Console preset editor identifies its v2alpha1
compatibility scope and links to the shared native planner.

The public browser contract is separately versioned `stackkits-webmcp/v2alpha1`.
Its v1 global-tier catalog is compatibility data, not the native authority.
The shared planner and four read-only tools expose the same selected profiles,
use-case alternatives, CPU/RAM/storage sliders, assessment and reviewable
`init -> validate -> resolve -> generate -> plan` argv. `apply` is never an
executable browser handoff step. Unsupported or incomplete selections explain
their blocked/unverified status rather than silently producing a command.

StackKits owns profiles, public facts, validation and execution. Techstack owns
the recommendation policy over those published facts and proposes explicit
module-local choices with provenance. Cloud only projects the proposal; it
does not introduce another solver. Missing AI/offload execution cannot be
invented as a working alternative. Hosted services remain optional for
standalone StackKits use.

## Delivery and evidence

Implementation, focused local validation, public-export reproducibility,
merge, exact-SHA deployment, live browser/CLI evidence, and contest submission
remain separate claims. This ADR establishes the contract; it does not mark
any later delivery stage complete.
