---
adr: ADR-0042
title: Standalone Compose default and optional platform adapters
status: Accepted
date: 2026-09-06
last_verified: 2026-09-06
---

# ADR-0042: Standalone Compose default and optional platform adapters

**Decision owner:** Marcel, explicit product decision on 2026-09-06.
**Implementation and integration:** tracked by `kombify-StackKits-fc91.2.3`;
accepting this decision does not claim implementation or runtime acceptance.

**Supersedes:** ADR-0003's Coolify-first default, mandatory PaaS requirement,
and classification of standalone Compose solely as a fallback for new native
StackKits. Explicit existing selections retain their authority.
**Related:** ADR-0031, ADR-0035, ADR-0039, ADR-0040.

**Superseded in part (2026-09-25):** ADR-0045 supersedes this ADR's
Compose-executor default for the execution layer: every deployment is now
executed and tracked by OpenTofu, with Terramate orchestrating managed
deployments. Docker Compose remains the default application/workload payload
and the native fallback executor; containers run as Compose workloads inside
the OpenTofu-tracked stack. This ADR's decision below on standalone Compose as
the primary product experience, and on Komodo/Coolify as optional platform
adapters, remains authoritative for product boundaries and is unchanged.

## Decision

Standard Mode is the complete, account-free, standalone product. Docker
Compose is its default workload execution adapter on an admitted physical or
virtual host. Bare metal and VM/VPS describe target hosts, not alternative
application managers. Techstack remains optional and owns provider lifecycle
and multi-server orchestration when used.

Standalone Compose is the primary, first-class product experience for the
complete stack, including application workloads, not merely a core bootstrap
or a reduced edition. The CLI is an optional user-facing interface to this
experience, not a separate full edition or a requirement for accessing the
complete feature set. Existing internal CLI execution contracts do not make
manual CLI use mandatory. Other supported interfaces must use the same
contracts and lifecycle rather than introduce a second implementation.

Full/Lite product labels must not imply that enabling Coolify, Komodo or the
CLI unlocks the complete product. Historical module identifiers remain stable
for existing intent; they do not define the new product positioning.

The default core installs neither Coolify nor Komodo, including their private
databases and supporting services. It retains installation, routing and TLS,
owner identity, secrets custody, status, update, backup and restore through
StackKits' existing contracts and lifecycle. A smaller resource profile is
not a substitute for those capabilities. Application-owned databases remain
part of their selected workload and are not removed with a platform manager.

Coolify and Komodo are explicitly enabled integrations on this foundation.
They consume the same workload, identity, routing, data and evidence contracts
and add only their platform-specific bootstrap and execution behavior.
For each workload there is exactly one execution owner and one routing owner.
An installed platform must not also manage a workload assigned to standalone
Compose, nor implicitly replace the router or its access policy.

Implementation proceeds in this order: complete standalone Compose, optional
Komodo integration, then optional Coolify integration. This is a delivery
priority, not evidence that Komodo already has better operational parity.
Dokploy remains draft until separately promoted; this decision does not
promote any kit or compatibility cell.

## Existing intent and transitions

An omitted choice in a new authoring flow resolves to standalone Compose.
Native persisted intent still records explicit workload alternatives and
module-local compute profiles as required by ADR-0039. A UI label, catalog
default or compute profile alone does not change an installed service graph.

Initial authoring may explicitly accept the release's CUE catalog defaults
with `stackkit init <kit> --catalog-defaults`. This materializes declared
alternatives and compute profiles into native v2alpha2 intent; individual
explicit selections take precedence. It is not a compiler fallback, cannot
be combined with a persisted candidate spec, and does not migrate existing
installations. Consumers use this shared authoring boundary rather than
maintaining their own default-selection table.

Existing explicit Coolify/Komodo selections and persisted module identities
must not silently change when the catalog default changes. Existing native
core alternatives that already identify a PaaS-bearing graph require an
explicit transition before removing those services. Legacy compatibility
input is interpreted under its documented contract, not rewritten in place.

Enabling, disabling or switching an adapter requires explicit intent and the
existing reconciliation/migration authority, with data custody, deployment
identity and routing ownership preserved. There is no automatic adoption of
another manager's deployments and no implicit fallback after a deployment
identity is committed. This decision does not add a second lifecycle.

## Integration acceptance

The CUE catalog, native authoring, module contracts, generated plans and actual
Compose output must agree: a new default stack contains the full standalone
core and no platform-manager services. Application defaults select Compose;
explicit supported adapter selections remain explicit and valid.

Techstack must consume the corresponding published catalog and CLI and emit
matching native intent. Producer-only publication does not close integration.
Focused CLI/generate verification proves the rendered boundary; target runtime
evidence must separately establish the affected lifecycle and ownership
behavior. Existing unrelated TLS or recovery failures remain open findings.

Report implemented, locally verified, merged, published and live separately.
Missing runtime or adapter parity evidence remains pending; acceptance of this
ADR is not permission to synthesize a successful compatibility or release row.
