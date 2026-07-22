---
title: RIL Node Handoff Execution Plan
last_verified: 2026-07-22
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

## Cloudflare Agent Node Handoff

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

No callback URL, host address, raw command, provider/server resource, transport
selection, or credential enters the StackKits envelope. TechStack and its
native provider-control/lease authority resolve external lifecycle and delivery;
StackKits validates only the exact opaque bindings and dispatches to the
primitive's governed runtime owner. A Cloudflare Agent may implement that
external delivery, but Cloudflare-specific fields are not part of the
StackKits contract.

The current StackKits validator binds this envelope to an authenticated tenant
context, one fresh `CurrentResolution`, the exact CUE primitive, and the
current plan target graph. It rejects all `contract-only` primitives before an
execution path is reached. The built-in verifier is process-local,
replay-guarded, and read-only. A later external node dispatch must additionally
be durably idempotent, authenticated, expiry-bound, and
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
- `kombify-StackKits-6nrh.2`: Cloudflare Agent node handoff executor contract.
- `kombify-StackKits-6nrh.3`: StackKit verification rollback evidence model.
- `kombify-StackKits-6nrh.4`: Reject unapproved and raw provider execution
  paths.

## Beta Acceptance

StackKits is ready for RIL public beta only when a TechStack-approved action
can execute the bound verifier through the provider-free envelope, durable
verification/recovery evidence is returned, and unapproved raw SSH, Docker,
OpenTofu, direct provider-input, wrong tenant/stack/node, stale plan, primitive
substitution, and missing grant attempts all fail closed.
