---
title: RIL Node Handoff Execution Plan
last_verified: 2026-07-28
status: implementation plan
---

# RIL Node Handoff Execution Plan

StackKits owns approved execution primitives for RIL action cards. It does not
decide whether a user may execute an action; Gateway and TechStack must already
provide an approved action-card context, policy decision, trace ID, and scoped
connector binding where required.

## Primitive Catalog

The first product-authority catalog now includes:

- `apply-stackkit-change`
- `verify-stackkit-state`
- `rollback-stackkit-change`
- `restart-service`
- `rotate-certificate`
- `check-backup`
- `plan-drift-repair`

Each primitive declares typed inputs, risk, required approval and grants,
target scope, verification steps, recovery behavior, redaction rules,
prohibited raw authorities, and evidence fields. Six remain `contract-only`.
`verify-stackkit-state` is bound to the in-process governed-plan readback
owner; it explicitly does not observe a node or host runtime. No external
node-handoff readiness is claimed.

## v0.9 Target: Techstack Node-Agent Transport

The shared provider-free handoff now carries:

- `actionCardId` and approval-receipt reference
- `executionId`
- `traceId`
- `tenantId`
- `stackId`, exact `resolvedPlanHash`, Site/node and runtime-owner references
- `primitiveId` and exact primitive-contract hash
- opaque connector-grant and execution-channel bindings
- expiry, nonce, and idempotency key
- redaction policy
- opaque evidence-sink reference

No callback URL, host address, raw command, provider/server resource,
caller-selected transport, or credential enters the StackKits envelope.
In the v0.9 target path, Techstack Core/UI sends a closed `StackKitCommand` over the Techstack-owned
outbound/reverse mTLS gRPC worker channel to its already enrolled node Agent.
The Agent revalidates the exact published release pin, index, receipt,
executable, capability when required, target, expiry, and idempotency binding;
scrubs Techstack/Kombify control environment; and invokes the exact local
`stackkit` CLI subprocess. It returns only bounded
`stackkit.command-result/v1` and `stackkit.rollout-event/v1` JSONL.

StackKits exposes no gRPC endpoint and owns no Techstack Agent enrollment,
certificates, worker transport, provider/server lifecycle, or generic shell.
Cloudflare infrastructure may host parts of Techstack's outer orchestration,
but Cloudflare-specific fields are not part of the StackKits contract.

This is not Slice 1 delivery evidence. Slice 1 establishes the local authority
and desired identity-projection model; executable Agent admission is Slice 2.

The current StackKits validator binds this envelope to an authenticated tenant
context, one fresh `CurrentResolution`, the exact CUE primitive, and the
current plan target graph. It rejects all `contract-only` primitives before an
execution path is reached. The built-in verifier is process-local,
replay-guarded, and read-only. The Techstack node dispatch must additionally be
durably idempotent, authenticated, expiry-bound, release-pinned, and
tenant/stack/node scoped. Missing approval, missing grant/binding, wrong
tenant/stack/node, stale plan, substituted primitive hash, or unsupported
primitive fails closed.

## Evidence Model

The governed-plan verifier returns:

- action-card ID, execution ID, primitive ID/hash, plan hash, and trace ID;
- exact stack target and executor reference;
- redacted logs or log references;
- verification result;
- explicit no-recovery result;
- public-safe status without a protected diagnostic payload.

Future mutating or node-local owners must extend this with exact
Site/node/runtime target evidence, durable custody, protected diagnostic
references, and rollback or compensation results.

## Work Packages

- `kombify-StackKits-6nrh.1`: Approved RIL action primitive catalog.
- `kombify-StackKits-6nrh.2`: Techstack node-Agent-to-pinned-CLI executor contract.
- `kombify-StackKits-6nrh.3`: StackKit verification rollback evidence model.
- `kombify-StackKits-6nrh.4`: Reject unapproved and raw provider execution
  paths.

## Beta Acceptance

The orchestrated RIL path is ready only when a TechStack-approved action reaches
the enrolled node Agent through the mTLS worker channel, the Agent verifies and
invokes only the exact pinned CLI without a generic shell, durable
verification/recovery evidence is returned, and unapproved raw SSH, Docker,
OpenTofu, direct provider-input, wrong tenant/stack/node, stale plan, binary or
primitive substitution, expired capability, and missing grant attempts all
fail closed before rendering or side effects.
