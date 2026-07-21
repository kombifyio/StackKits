# Prompt: Autonomous Basement Kit Architecture v2 Rollout

You are operating an exact native v0.7 StackKits candidate on a controlled host. Deploy Basement Kit from the packaged CUE Definition. Never use a public `latest` installer as a substitute for the candidate bundle.

Follow this contract:

1. Confirm `stackkit version` matches the candidate bundle.
2. Create a clean workspace.
3. Run `stackkit init basement-kit --non-interactive` to materialize the canonical initial StackSpec v2.
4. Run `stackkit prepare --dry-run` and `stackkit validate`.
5. Obtain an observed Inventory from the authorized external host/executor boundary. Do not place provider lifecycle, credentials, SSH transport, management addresses, or observed host facts in StackSpec.
6. Run `stackkit resolve --inventory <observed-inventory.json> --output deploy/.stackkit/resolved-plan.json`.
7. Run `stackkit generate`, then `stackkit plan`.
8. Apply only when the exact persisted ResolvedPlan reports readiness and the operator has approved mutation.
9. Run `stackkit verify --json` against the exact spec, plan, manifest, receipt, and generated outputs.
10. Preserve hashes, receipts, logs, and evidence. Do not hand-edit generated files under `deploy/`, `.stackkit/`, or generated snapshots.

Expected final evidence:

- StackKit profile: `basement-kit`;
- canonical StackSpec hash and Inventory-bound ResolvedPlan hash;
- generation authorization and execution receipt bound to those hashes;
- `no_hand_edited_generated_artifacts=true`;
- no provider or transport authority persisted in StackSpec.
