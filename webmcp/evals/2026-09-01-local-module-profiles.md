# Native module-profile development evaluation

Evidence class: **local development**, not production or contest-submission proof.
Run date: 2026-09-01. Runtime: the local Vite website and the Codex in-app browser's
native WebMCP capability. The browser discovered and invoked the actual four
registered tools; no `modelContext` test injection was used in this manual run.

The development checkout was based on
`5add761aa9c16fee6933365ac2fa08bbb7528e7b` with the module-profile changes still
uncommitted. That SHA is the local build marker, **not a claim that the changes
already exist at that commit or are deployed**. The inspected authority digest
was `f854f95d33eaea517b7e5b94045460c7a01a355c52f859842367746dae628bad`, and the
catalog digest was `327579c9a4c610c09cca754f97d58812f27a5749eb0f586e2bc22b6e5eb246d3`.

| Request | Observed tool result and visible UI |
| --- | --- |
| Small photo server: Core Lite `low`, Immich Lite `low`, 2 CPU / 2 GiB RAM / 10 GiB storage | Assessment succeeded with overall `unverified`. CPU/storage declarations for the photo module were missing; RAM was `warning` with a 2.8125 GiB reference. The UI showed both selected alternatives and the same axis statuses. |
| Omit the required Basement core alternative | Handoff returned `invalid_input`, `ready:false`, no executable steps, and `REQUIRED_USE_CASE_SELECTION_MISSING`. It did not infer an alternative from the requested module profile. |
| Core Lite `low` with Immich `standard`, 4 CPU / 8 GiB RAM / 100 GiB storage | The mixed selection was accepted independently. RAM passed its 3.3125 GiB reference; missing photo CPU/storage facts kept the aggregate `unverified`. Both different profiles appeared in the UI. |
| Core Lite with only 4 CPU declared | Handoff returned `blocked` and `DECLARED_CAPACITY_INCOMPLETE`. RAM/storage fields stayed blank, axis statuses were `unverified`, and the UI showed a blocked handoff. |
| Core Lite on 1 CPU / 1 GiB RAM / 5 GiB storage | All three axes failed. Handoff returned `blocked`, `DECLARED_CAPACITY_BELOW_MINIMUM`, and no executable steps; the UI showed the same failure. |
| Core Lite `low` on its declared 2 CPU / 2 GiB RAM / 10 GiB storage | `list → profiles → assess → handoff` succeeded. Assessment passed, the UI displayed `ready`, and all five CLI steps carried their mutation/idempotency/approval metadata. `stackkit.apply` remained a separate `executable:false` follow-up. |

Every inspected response reported `effects.executed:false`,
`effects.target_mutation:false`, and `effects.provider_action:false`.
No provider operation, installation, or filesystem command was executed by the
browser. The independently invoked local CLI completed the Core Lite
`init → validate → resolve → generate → plan` sequence in an isolated working
directory; no `apply` ran. That validates authoring/generation, not target-host
compatibility or live workload installation.

The deterministic local browser smoke also exercised the no-WebMCP browser,
slider-first input, a pointer-selected ordinary RAM value, synchronized UI/tool
results, and invalidation of stale results after edits. A reproduced asynchronous
abort regression now preserves a newer human capacity edit when an older agent
invocation is cancelled. These are deterministic boundary checks, not
probabilistic agent-success scores.

Production acceptance must rerun the public flow and verify the actual deployed
SHA and digests. A ChatGPT-hosted browser run, public release, production
activation, and Devpost entry are not asserted by this local record.
