# Prompt: Inspect Existing Architecture v2 Rollout

Inspect an existing StackKits workspace without mutating desired intent or rollout state.

Run:

```bash
stackkit status --json
stackkit validate
stackkit verify --json
stackkit logs list --json
```

On native v0.7, node-local HTTP status is spec-only. If `stackkit-server` is running locally, `GET /api/v1/status` may prove `intent_valid` and `resolve-required`; it must not be reported as deployment readiness. Legacy HTTP Verify, Doctor, and Plan surfaces return a typed unavailable response until they consume an exact ResolvedPlan and execution evidence.

Report:

- StackKit profile and StackSpec hash;
- exact ResolvedPlan, generation manifest, authorization, and receipt hashes when present;
- whether each artifact is bound to the same desired intent and observed Inventory;
- failing checks and likely failure class;
- whether generated rollout files appear to have been edited manually.
