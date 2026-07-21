# Prompt: External Executor Handoff

Prepare an externally managed host for a governed Architecture v2 rollout. Raw SSH target selection, provider identity, credentials, and transport configuration are not StackSpec intent and must remain owned by the external executor/TechStack boundary.

Collect or confirm outside StackSpec:

- exact target identity and observed Inventory;
- executor identity and authorization scope;
- short-lived transport credentials held only by the executor;
- candidate StackKits version and Definition digest.

Then run the provider-free StackKits stages in the workspace:

```bash
stackkit init basement-kit --non-interactive
stackkit prepare --dry-run
stackkit validate
stackkit resolve --inventory <observed-inventory.json> --output deploy/.stackkit/resolved-plan.json
stackkit generate
stackkit plan
stackkit apply
stackkit verify --json
```

The external executor may use SSH or another transport to execute an authorized bundle, but must not write those transport facts back into StackSpec. Stop if observed Inventory, ResolvedPlan hashes, generation authorization, or executor receipt do not bind exactly.
