---
title: RIL Action Execution Contract
last_verified: 2026-07-01
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
- `kombify-Techstack` owns the action-card lifecycle, execution leases, server
  read model, and verification record.
- `kombify-StackKits` owns StackKit apply/verify/rollback primitives, packaged
  OpenTofu execution, generated manifest interpretation, and node-local handoff
  evidence.

## Execution Rules

- No StackKit action executes without an approved TechStack action card and a
  Gateway policy decision.
- Harnesses do not get raw SSH, Docker socket, or OpenTofu apply authority.
- Apply/verify/rollback must run through StackKit runtime-action APIs or a
  Cloudflare Agent node handoff that preserves the same evidence contract.
- Every execution returns stable evidence: action ID, run ID, target node,
  command class, redacted logs, verification result, rollback/compensation
  status, and trace ID.
- Failures return public-safe error codes and redacted summaries; raw target
  output remains protected.

## Public-Beta Proof

The first public-beta proof should show:

1. TechStack creates an action card for one connected RIL server.
2. Gateway denies execution until a delegated connector grant binding and user
   approval exist.
3. `ril-executor` calls the approved TechStack/StackKit execution path.
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
