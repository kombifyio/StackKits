# ADR-0031 — StackKits Standalone Lifecycle Boundary

**Status:** Accepted (2026-07-26)
**Owner:** StackKits
**Related:** ADR-0016, ADR-0018, ADR-0029
**Supersedes:** ADR-0018 server-side compatibility resolver, mandatory Admin
node mirror, and Admin-owned public upgrade resolution

## Context

The public StackKits CLI must install and operate a Basement homelab without a
Kombify account. The current implementation violates that product boundary in
three places:

1. public kit discovery and upgrade resolution read an Admin API or database;
2. native-v2 Apply has no complete local owner-custody bootstrap;
3. generation can report readiness without a concrete deployable workload.

Techstack must be able to orchestrate the same lifecycle as an optional
operator UI, but it must not become the source of StackKit content, local owner
identity, node state, or evidence. Provider and server lifecycle also remain
outside StackKits.

## Decision

### 1. Local authority

CUE contracts, the canonical StackSpec, the immutable ResolvedPlan, and
node-local lifecycle state are the operational sources of truth. A remote
database may mirror published catalog or fleet state, but absence or
unavailability of that mirror cannot prevent the public standalone lifecycle.

Local owner custody is established explicitly during
`stackkit init --owner-source=local`. The CUE contract and resolved Inventory
own the trust profile and exact Site/node/execution-channel binding; Go
composition may realize and consume those decisions but must not invent them.
The local Owner is realized as a PocketID human identity and group membership,
authenticated to protected services through TinyAuth. StackKits binds that
identity projection to the stable `ownerRef`, the local Ed25519 evidence key,
and its step-ca certificate. PocketID and TinyAuth are therefore the human
identity/login plane; the local custody key and certificate are the lifecycle
authority and evidence root.
Apply and every mutating Day-2 operation use construction-owned evidence rooted
in that custody. Caller-supplied evidence, provider credentials, and remote
signing are not accepted as local authority. CLI binding overrides must match
the persisted, plan-bound local authority exactly.

### 2. Public distribution

GitHub Releases in the public StackKits repository are the public distribution
authority. Every release publishes a versioned release index containing kit,
channel, platform, asset digest, SPDX SBOM identity, and attestation identity.

The public CLI resolves `stable`, `beta`, `edge`, and explicit SemVer versions
from that index. It verifies and caches the selected index and integrity
material before atomically installing or upgrading local release content.
No account, Admin endpoint, Kombify-controlled host, or private credential is
required.

### 3. Public/private executable boundary

The public `stackkit` executable contains only end-user authoring, generation,
apply, verification, backup, restore, upgrade, and drift operations.
Publishing, DB ingest/export, Admin registry maintenance, and managed reporting
belong to a separate private publisher executable and may not be reachable from
the public command tree or dependency graph.

### 4. Lifecycle modes

Standard Mode owns account-free executor-native apply, verify, upgrade,
snapshot, rollback, backup, restore, drift detection, and approved standard
reconciliation. The v0.8 Basement default executes Compose directly; its
recovery state therefore binds the exact Compose generation/runtime closure.
An OpenTofu state snapshot is mandatory only when OpenTofu is the actual
selected Product Apply executor. A target without a real state owner fails
before lifecycle side effects instead of manufacturing an empty state file.

Advanced Mode adds StackKits-owned Terramate rendering and coordinated change
sets, rollback, restore drills, and runtime-intelligence integration. Advanced
operations require a short-lived Techstack-issued capability that StackKits
validates offline before rendering or side effects. Standard operations remain
available without that capability.

### 5. Techstack and kombify Cloud integration

Techstack is the optional Orchestrator UI over the standalone product. It
consumes an exactly pinned published StackKits release plus versioned CLI
JSON/JSONL contracts. It may unify compatible user-approved configuration
inputs, present Standard lifecycle operations, and dispatch capability-gated
Advanced Day-2 operations such as Terramate change sets, coordinated rollback,
restore drills, and Runtime Intelligence Layer (RIL) workflows. StackKits CUE
validation remains the final authority for the resulting StackSpec and
ResolvedPlan. Techstack does not import StackKits source packages, copy kit
catalogs or renderers, mint local owner evidence, or reinterpret a ResolvedPlan.

kombify Cloud may provide an optional convenience identity and user-sync lane.
A Cloud profile can become a desired local PocketID user/Owner projection only
through an explicit, authenticated, user-approved sync request. The local
StackKits workflow validates and realizes the projection; the local `ownerRef`,
custody key, step-ca certificate, PocketID directory, and TinyAuth binding
remain authoritative. Passwords, passkeys, private keys, and Cloud session
credentials are never synchronized. Cloud absence or sync failure cannot block
the standalone lifecycle or silently replace the local Owner.

### 6. Release gates

The v0.8 standalone-ready claim requires a clean public archive to complete:

```text
init --owner-source=local -> validate -> generate -> apply -> verify
```

The path must deploy the Basement core platform and must not contact a
Kombify-controlled host. The v0.x publication path remains source-integrity
based: absent Candidate/device evidence is recorded as `pending/unverified`
and never fabricated. A stable-standalone support claim additionally requires
local upgrade/snapshot/rollback, backup/restore, drift, Advanced capability
denial, public export, and exact-source release evidence.

## Consequences

- ADR-0018's snapshot, state-anchor, confirmation, and rollback safety
  decisions remain valid, interpreted through the selected executor: Compose
  gets an executor-native recovery closure; OpenTofu state is required only
  when OpenTofu actually executes the Product Apply.
- ADR-0018's Admin compatibility resolver, mandatory node deployment mirror,
  and DB channel authority are superseded for the public lifecycle.
- Admin and fleet systems may consume local receipts asynchronously but cannot
  become mutation prerequisites.
- Release metadata and public/private dependency boundaries become explicit
  versioned contracts with tamper and export tests.
- v0.8 is the standalone lifecycle milestone. Broader Modern Homelab and HA
  expansion moves to v0.9.

## Migration

1. Establish local owner custody, then bind it to CUE-resolved trust and
   single-node Inventory authority.
2. Introduce the public release index and structurally separate publisher code.
3. Make Basement resolution and rendering truthfully deployable.
4. Move upgrade, backup, restore, and drift execution to the local v2 contract.
5. Add offline-gated Terramate Advanced Mode.
6. Remove the public Admin-dependent compatibility paths after their local
   replacements and deprecated aliases are proven.
