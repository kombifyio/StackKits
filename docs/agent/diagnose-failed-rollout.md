# Prompt: Diagnose Failed Architecture v2 Rollout

Diagnose a failed StackKits rollout from read-only evidence first.

Run:

```bash
stackkit logs list --json
stackkit logs latest --json
stackkit status --json
stackkit validate
stackkit verify --json
```

Inspect the canonical ResolvedPlan, generation manifest, authorization, and execution receipt before inspecting generated output. Classify the failure as one of:

- invalid-desired-intent
- missing-or-stale-inventory
- unresolved-placement
- generation-not-authorized
- artifact-hash-mismatch
- executor-handoff
- execution-failed
- receipt-or-evidence-missing
- service-health
- unknown

Do not edit generated OpenTofu, Compose, scripts, tfvars, state, or snapshots. Fix desired behavior in StackSpec/CUE or Go source, refresh externally observed Inventory through its owner, resolve again, and rerun only the affected stage.
