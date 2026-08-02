---
title: RIL Ownership Boundary
last_verified: 2026-08-02
status: superseded-checkpoint
---

# RIL Ownership Boundary

The v0.7 StackKits-hosted RIL execution checkpoint is retired. StackKits no
longer exposes RIL admission, execution, replay-ledger, or RuntimeAction HTTP
surfaces and does not project their shared Go packages into the public mirror.
Techstack owns those hosted concerns, including action-card lifecycle,
idempotency, transport, runtime inventory, provider/server lifecycle, and
multi-server orchestration.

StackKits still owns two things that the cutover must not erase:

- the CUE catalog facts for approved action primitives and executor identity,
  including approval, recovery, target, evidence, and prohibition metadata;
- provider-neutral local execution through the generated StackAction contract
  and the published `stackkit` CLI.

`contract-only` catalog entries remain discovery and validation metadata. They
do not create a StackKits HTTP executor. Techstack may dispatch only the closed
StackAction/CLI vocabulary to an already bound host; StackKits revalidates the
local contract and produces local lifecycle evidence. Standard Mode remains
account-free and does not depend on Techstack or RIL.

The former RIL and RuntimeAction paths are intentionally absent. Their local
rollout, verify, restore-drill, and backup behavior is preserved one-for-one
below `/api/v1/internal/stack-actions/*`; RIL admission itself has no StackKits
replacement because it is not StackKits-owned behavior.

Historical implementation detail remains available in Git history and the
completed v0.7.1 roadmap record. It must not be used as a current API contract.
