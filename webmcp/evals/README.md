# StackKits WebMCP v1 agent evals

These five short evaluations are qualitative contest evidence, not a
probabilistic CI gate. Run them in a WebMCP-capable browser against the exact
deployed SHA and record the structured outcome plus visible Planner state.

| Eval | Agent request | Expected tool path and evidence |
| --- | --- | --- |
| Small photo server | “I want Photos on a 2-core, 2 GiB RAM, 10 GiB free Basement server. Use the smallest declared tier and prepare the handoff.” | `list → profile(basement-kit, low) → assess → handoff`; Photos selects `immich-lite`, status `pass`, handoff includes `--use-case photos`. |
| Excluded Media | “Prepare Media on Basement low.” | Profile keeps Media visible with its CUE reason; handoff is `blocked` with `USE_CASE_NOT_AVAILABLE_FOR_TIER`; no steps execute. |
| Too-small host | “Assess Basement standard with 1 CPU, 4 GiB RAM, and 20 GiB free, then prepare it.” | Assessment overall `fail`; handoff is `blocked` with `DECLARED_CAPACITY_BELOW_MINIMUM`. |
| Missing capacity | “Prepare Basement standard; I only know it has 4 CPU cores.” | Assessment overall `unverified`; handoff is `blocked` with `DECLARED_CAPACITY_INCOMPLETE`. |
| Valid standard handoff | “Prepare Basement standard for 4 CPU, 4 GiB RAM, and 20 GiB free.” | Status `pass`; exact `init → validate → resolve → generate → plan` metadata; `stackkit.apply` is separate and `executable:false`. |

For every eval also verify:

- UI selection and status equal the tool result;
- `effects.executed`, `effects.target_mutation`, and
  `effects.provider_action` are all `false`;
- provenance matches the deployed source SHA and catalog digests;
- aborting an invocation leaves the previous visible state unchanged.
