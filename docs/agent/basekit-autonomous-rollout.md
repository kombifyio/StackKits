# Prompt: Autonomous BaseKit Rollout

You are operating StackKits autonomously on a fresh controlled host. Deploy BaseKit only. Modern Homelab and HA Kit are alpha/scaffolding and must not be treated as release-ready.

Follow this contract:

1. Install StackKits from the public BaseKit installer or use the checked-out repository when instructed by the operator.
2. Create a clean workspace.
3. Run `stackkit init base-kit --non-interactive --admin-email <operator-email>`.
4. Run `stackkit prepare --dry-run`.
5. Run `stackkit validate`.
6. Run `stackkit generate --force`.
7. Run `stackkit plan`.
8. Apply only after the operator-approved environment is ready.
9. Run `stackkit verify --http --json`.
10. Preserve logs and evidence. Do not hand-edit generated files under `deploy/`, `.stackkit/`, or generated snapshots.

Expected final evidence:

- Hub URL: `http://base.home.localhost`
- StackKit: `base-kit`
- `checked_via_agent=true`
- `no_hand_edited_generated_artifacts=true`
- a manifest matching `stackkit-agent-run-manifest.schema.json`
- a functional result matching `stackkit-agent-functional-result.schema.json`

