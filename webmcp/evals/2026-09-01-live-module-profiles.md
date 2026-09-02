# Native module-profile live evaluation

Evidence class: **public production website**. Evaluated on 2026-09-01 at
`https://stackkit.cc/planner` through the Codex in-app browser's native WebMCP
capability, without an injected `modelContext`. This is not a claim of a
ChatGPT-hosted browser run, a published CLI release, or a Devpost submission.

- Source: `88c11f16eace96c3c3f6fdcd209783e94ad8eb35` (PR #827 merge).
- Authority: `6a0f1e4fac50ab4825431909a0ee39c9f3cc7e97ffb4d6cff7140d2ba6ac902e`.
- Catalog: `cce8e48773a311968be1d6de8c912fa83232bfaae5aa28a3b2a104294e87a4c3`.
- Website adapter: GitHub Actions run `33558858044`, **success**, including
  exact-current-main Render deployment, live projection and installer smoke.

Discovery exposed the four read-only v2alpha1 tools. Their results and the
visible planner showed the same source and catalog identities.

| Scenario | Observed result |
| --- | --- |
| Core Lite `low`, 2 CPU / 2 GiB RAM / 10 GiB storage | Full `list → profiles → assess → handoff` passed. UI showed the selected alternative/profile, three passing axes, and a ready handoff. |
| Core Lite `low`, 1 CPU / 1 GiB RAM / 5 GiB storage | Handoff blocked with `DECLARED_CAPACITY_BELOW_MINIMUM` and no executable steps. |
| Core Lite with only 4 CPU declared | Handoff blocked with `DECLARED_CAPACITY_INCOMPLETE`; RAM and storage stayed undeclared. |
| Core Lite `low` plus Immich Lite `low`, 2 / 2 / 10 | Overall `unverified`; photo CPU/storage facts are not declared. RAM warned against the 2.8125 GiB reference. No resource figures were fabricated. |
| Photos selected without required Basement core alternative | `invalid_input`, `REQUIRED_USE_CASE_SELECTION_MISSING`, no executable steps. |
| Core Lite `low` plus Immich `standard`, 4 / 8 / 100 | Mixed independent profiles accepted. RAM passed against 3.3125 GiB; missing photo CPU/storage facts kept the aggregate `unverified`. |

The RAM slider's ArrowRight interaction changed the matching numeric input from
8 to 9 GiB. Prior capacity/handoff results were invalidated on edit, as covered
by the deterministic local browser boundary check.

Every inspected tool result declared `executed:false`, `target_mutation:false`
and `provider_action:false`. The ready handoff included all five operations and
their mutation/idempotency/approval metadata. `stackkit.apply` was a separate
`executable:false` follow-up; `stackkit compat` remained mandatory. No CLI,
provider or filesystem action was executed by the browser.

The no-WebMCP fallback was verified locally against exact PR source
`834523a5693d3d72586368a285d7b818b6b3a864` by the existing browser smoke (7599 ms).
This document does not relabel that local fallback check as a production run.

The overall Delivery run `33558694720` failed because the catalog adapter sent
an obsolete `modules` workflow input. The detached OSS publisher `33558982391`
also stopped before publication because its derived `v0.22.18` lacked a release
changelog entry. These failures do not negate the website evidence, and the
website evidence does not imply that the complete release succeeded.
