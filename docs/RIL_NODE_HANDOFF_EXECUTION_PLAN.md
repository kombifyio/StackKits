---
title: RIL Node Handoff Execution Plan
last_verified: 2026-08-02
status: superseded
---

# RIL Node Handoff Execution Plan

This StackKits-hosted execution plan is superseded by the product boundary in
[RIL_ACTION_EXECUTION.md](RIL_ACTION_EXECUTION.md).

Techstack owns RIL admission, dispatch, transport, runtime inventory, and
durable idempotency. StackKits owns the CUE action-catalog facts, the generated
StackAction contract, the standalone CLI lifecycle, and local Owner evidence.
No RIL or legacy RuntimeAction HTTP endpoint remains in this repository.
