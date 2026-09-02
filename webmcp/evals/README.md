# StackKits WebMCP v2alpha1 agent evals

These five short evaluations are qualitative contest evidence, not a
probabilistic CI gate. Run them in a WebMCP-capable browser against the exact
deployed SHA and record the structured outcome plus visible Planner state.

The [2026-09-01 local development record](2026-09-01-local-module-profiles.md)
is separate from the required exact-production evidence.

| Eval | Agent request | Expected tool path and evidence |
| --- | --- | --- |
| Small photo server | “I want Photos on a 2-core, 2 GiB RAM, 10 GiB free Basement server. Select the standalone core and smallest declared Photos module profile, then prepare the handoff.” | `list → get_module_profiles(basement-kit) → assess → handoff`; Photos selects its declared lite alternative/profile and the result exposes status/facts. |
| Required alternative missing | “Prepare Basement without selecting the core alternative.” | Assessment and handoff remain blocked with `REQUIRED_USE_CASE_SELECTION_MISSING`; no profile or alternative is inferred. |
| Mixed profiles | “Use the standalone core and the standard Photos module profile.” | Assessment uses independent module-local selections; no global compute tier is emitted or inferred. |
| Missing capacity | “Prepare Basement; I only know it has 4 CPU cores.” | Assessment overall `unverified`; handoff is `blocked` with `DECLARED_CAPACITY_INCOMPLETE` or module-profile undeclared reasons where authority is partial. |
| Handoff boundary | “Prepare a valid Basement handoff.” | Exact `init → validate → resolve → generate → plan` metadata; init argv includes `--api-version stackkit/v2alpha2`, explicit module/use-case flags, and `stackkit.apply` is separate and `executable:false`. |

For every eval also verify:

- UI selection and status equal the tool result;
- `effects.executed`, `effects.target_mutation`, and
  `effects.provider_action` are all `false`;
- provenance matches the deployed source SHA and catalog digests;
- aborting an invocation leaves the previous visible state unchanged.
