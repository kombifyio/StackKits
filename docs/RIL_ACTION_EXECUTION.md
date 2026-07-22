---
title: RIL Action Execution Contract
last_verified: 2026-07-22
status: target-contract
---

# RIL Action Execution Contract

StackKits owns the StackKit-side execution primitives used by the `ril-ops`
harness after Gateway policy and user approval succeed.

## Boundary

- `kombify-Agents` plans RIL action cards and resumes approved execution through
  `ril-executor`.
- `kombify-Gateway` enforces AuthContext, entitlements, delegated connector
  grants, tool subset policy, audit, and redaction.
- `kombify-TechStack` owns the action-card lifecycle, execution leases, server
  read model, and verification record.
- `kombify-StackKits` owns the CUE-governed primitive catalog, exact
  ResolvedPlan/runtime-owner admission, and the provider-free execution and
  evidence envelopes consumed by its governed runtime owners. StackKits does
  not expose raw OpenTofu, SSH, Docker, provider lifecycle, or caller-selected
  commands through RIL.

The general RIL catalog is independent from Modern Homelab's
`outbound-control-agent` remote-action contracts. Those contracts govern a
specific Home-to-Cloud federation boundary and are not a general Agent or RIL
execution substrate.

## Current v0.7.1 Checkpoint

The embedded Architecture-v2 product authority contains seven primitives:

| Primitive | Class | Mutation | Recovery | Current support |
|---|---|---:|---|---|
| `plan-drift-repair` | plan/read-only | no | none | `contract-only` |
| `apply-stackkit-change` | apply/high | yes | `rollback-stackkit-change` | `contract-only` |
| `verify-stackkit-state` | verify/read-only | no | none | `executor-bound` to governed-plan readback |
| `rollback-stackkit-change` | rollback/critical | yes | manual | `contract-only` |
| `restart-service` | service/high | yes | manual | `contract-only` |
| `rotate-certificate` | certificate/high | yes | manual | `contract-only` |
| `check-backup` | backup/read-only | no | none | `contract-only` |

`contract-only` means discovery and validation only. It does not authorize an
API call, node handoff, local process, runtime adapter, or provider mutation.
Each primitive receives a deterministic hash derived from its exact CUE
projection and declares its required operation class. Later execution must bind
that hash and class to an authenticated runtime owner; changing only an ID or
title is insufficient.

The sole current owner, `stackkits-governed-state-verifier-v1`, performs an
in-process readback of the exact current canonical ResolvedPlan. It rechecks
the plan hash, embedded CUE contract, product authority, and current generation
binding. Its evidence explicitly records `runtime_state_observed=false`: it is
not a host, container, service-health, provider, or Apply check.

The current validation boundary consumes the exact pinned
`kombify-go-common/rilaction` envelope. It samples one trusted UTC instant and
checks request, approval, and grant freshness; authenticated tenant, current
StackInstance and ResolvedPlan identity; primitive hash and operation class;
approval ceremony and exact grant scopes; typed input closure; and exact
Site/node/module/runtime placement. Validation remains non-authorizing, but
reports `executable=true` only when the exact CUE executor is implemented by
the current binary. `AdmitRILActionAt` rejects all other primitives before
filesystem, network, host, or provider access. `ExecuteRILActionAt` applies a
bounded process-local replay guard and returns stable redacted evidence for the
read-only verifier. Durable cross-process replay and evidence custody remain
separate follow-up work.

Execution persistence is abstracted by the shared `rilaction.ExecutionLedger`.
An atomic reserve returns exactly `acquired`, `replay`, `in-progress`, or
`conflict`; only `acquired` receives an opaque reservation token, and the same
token must fence the final evidence commit. TechStack owns the durable
Postgres/RLS implementation and the outer at-most-once dispatch custody.
StackKits retains a bounded in-memory secondary guard inside its server and CLI
process; it makes no cross-process durability claim.

TechStack PR #200 provides the concrete consumer dispatcher. It refuses HTTP
redirects, authenticates as the TechStack service, forwards the trusted tenant
context, loads the immutable StackSpec plus Inventory snapshot through an
injected Postgres authority, verifies the resolved plan header and body against
the approved request, and strictly validates the returned shared evidence.
Product startup registration remains fail closed until the Postgres
approval/grant/resolution-snapshot source is available; the legacy PocketBase
action route is not an execution fallback.

The concrete service delivery is deliberately two-step:

1. `POST /api/v2/internal/ril-actions/resolve` accepts only StackSpec and
   Inventory, authenticated with the TechStack service token and the trusted
   `X-Kombify-Tenant-ID` transport context. StackKits resolves and retains an
   opaque `CurrentResolution` under the exact tenant and Stack ID.
2. `POST /api/v2/internal/ril-actions/execute` accepts only the shared
   `rilaction.Request`. It samples one UTC instant, requires the same trusted
   tenant, loads the matching current resolution, executes the CUE-selected
   owner, and returns the exact shared evidence document.

No StackSpec, Inventory, URL, provider, lease, generation, transport,
credential, or callback data is added to the action handoff. A StackKits server
restart intentionally discards the opaque resolution and requires TechStack to
resolve again; it never reconstructs execution authority from a plan hash.

The returned `stackkit.ril-action-evidence/v1` shape comes from the exact
pinned `kombify-go-common/rilaction` package. StackKits validates its own result
against the original request before returning it. Evidence contains only
closed, sorted verification/recovery/summary codes and an optional opaque
`diagnostic:` reference; free-form logs and runtime output are not part of the
wire.

## Execution Rules

- No StackKit action executes without an approved TechStack action card, a
  Gateway policy decision, the required connector grant, and the exact current
  ResolvedPlan hash.
- Harnesses do not get raw SSH, Docker socket, or OpenTofu apply authority.
- A handoff selects only the catalog primitive and its governed runtime owner;
  it cannot select a command, binary, path, endpoint, provider, transport, or
  credential.
- TechStack lease/generation/CAS and provider lifecycle stay outside the
  StackKits envelope; opaque approval and grant bindings are authority
  references, not provider-control handles.
- Every future execution returns stable evidence: action-card ID, execution
  ID, primitive ID and contract hash, exact plan hash, target reference,
  redacted logs, verification result, rollback/compensation status, and trace
  ID.
- Failures return public-safe error codes and redacted summaries; raw target
  output remains protected.
- Module/package-specific actions are admitted only after the owning CUE module
  adds a closed primitive contract. Caller-defined extension actions are not an
  escape hatch.

## Public-Beta Proof

The first public-beta proof should show:

1. TechStack creates an action card for one connected RIL server.
2. Gateway denies execution until a delegated connector grant binding and user
   approval exist.
3. TechStack durably reserves the approved request, refreshes the tenant-bound
   StackKits resolution when needed, and calls the exact StackKits execute path.
4. StackKits returns verification evidence.
5. Workbench displays completion or failure without secret leakage.

Detailed execution and node-handoff planning:
[RIL_NODE_HANDOFF_EXECUTION_PLAN.md](RIL_NODE_HANDOFF_EXECUTION_PLAN.md).

Tracking:

- `kombify-StackKits-6nrh`: StackKit RIL action execution and Cloudflare Agent
  handoff.
- `kombify-StackKits-6nrh.1`: approved action primitive catalog.
- `kombify-StackKits-6nrh.2`: Cloudflare Agent node handoff executor contract.
- `kombify-StackKits-6nrh.3`: verification rollback evidence model.
- `kombify-StackKits-6nrh.4`: unapproved/raw execution denial paths.
