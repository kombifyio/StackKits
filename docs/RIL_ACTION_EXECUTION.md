---
title: RIL Ownership Boundary
last_verified: 2026-10-22
status: active
---

# RIL Ownership Boundary

StackKits exposes no RIL admission, execution, replay-ledger, or RuntimeAction
HTTP surfaces and does not project their shared Go packages into the public
mirror.
Techstack owns those hosted concerns, including action-card lifecycle,
idempotency, transport, runtime inventory, provider/server lifecycle, and
multi-server orchestration.

StackKits owns two things:

- the CUE catalog facts for approved action primitives and executor identity,
  including approval, recovery, target, evidence, and prohibition metadata;
- provider-neutral local execution through the generated StackAction contract
  and the published `stackkit` CLI.

`contract-only` catalog entries remain discovery and validation metadata. They
do not create a StackKits HTTP executor. Techstack may dispatch only the closed
StackAction/CLI vocabulary to an already bound host; StackKits revalidates the
local contract and produces local lifecycle evidence. Standard Mode remains
account-free and does not depend on Techstack or RIL.

Local rollout, verify, restore-drill, and backup behavior is served below
`/api/v1/internal/stack-actions/*`; RIL admission is not StackKits-owned
behavior.
