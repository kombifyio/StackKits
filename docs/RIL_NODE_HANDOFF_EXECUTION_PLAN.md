---
title: RIL Node Handoff Execution Plan
last_verified: 2026-07-01
status: implementation plan
---

# RIL Node Handoff Execution Plan

StackKits owns approved execution primitives for RIL action cards. It does not
decide whether a user may execute an action; Gateway and TechStack must already
provide an approved action-card context, policy decision, trace ID, and scoped
connector binding where required.

## Primitive Catalog

The first public-beta primitive catalog should include:

- `apply_stackkit_change`
- `verify_stackkit_state`
- `rollback_stackkit_change`
- `restart_service`
- `rotate_certificate`
- `check_backup`
- `plan_drift_repair`
- package-specific safe actions declared by the active StackKit manifest.

Each primitive declares inputs, risk, required grants, verification steps,
rollback behavior, redaction rules, and evidence fields.

## Cloudflare Agent Node Handoff

When execution must run near the node, the handoff contract carries:

- `actionCardId`
- `executionId`
- `traceId`
- `tenantId`
- `serverId`
- `primitiveId`
- `connectorBindingId`
- redaction policy
- callback URL or event sink

The node handoff is authenticated, idempotent, and tenant/server scoped. Missing
approval, missing binding, wrong tenant, or unsupported primitive fails closed.

## Evidence Model

Every completed or failed execution returns:

- action card ID, execution ID, primitive ID, trace ID;
- target node/server reference;
- command class, not raw command text when sensitive;
- redacted logs or log references;
- verification result;
- rollback or compensation result;
- public-safe status and protected diagnostic reference.

## Work Packages

- `kombify-StackKits-6nrh.1`: Approved RIL action primitive catalog.
- `kombify-StackKits-6nrh.2`: Cloudflare Agent node handoff executor contract.
- `kombify-StackKits-6nrh.3`: StackKit verification rollback evidence model.
- `kombify-StackKits-6nrh.4`: Reject unapproved and raw provider execution
  paths.

## Beta Acceptance

StackKits is ready for RIL public beta when a TechStack-approved action can call
one primitive, return verification evidence, and fail closed for unapproved raw
SSH, raw Docker, raw OpenTofu, direct provider-key, wrong tenant, and missing
grant-binding attempts.
