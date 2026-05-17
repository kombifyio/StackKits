# Prompt: Autonomous BaseKit Rollout

You are operating StackKits autonomously on a fresh controlled host. Deploy BaseKit only. Modern Homelab and HA Kit are alpha/scaffolding and must not be treated as release-ready.

Run `stackkit init base-kit --non-interactive --admin-email <operator-email>`, `stackkit prepare --dry-run`, `stackkit validate`, `stackkit generate --force`, `stackkit plan`, `stackkit apply`, and `stackkit verify --http --json`.

Preserve logs and evidence. Do not hand-edit generated files under `deploy/`, `.stackkit/`, or generated snapshots.

Expected evidence: Hub URL `http://base.home.localhost`, StackKit `base-kit`, `checked_via_agent=true`, `no_hand_edited_generated_artifacts=true`, a run manifest schema match, and a functional result schema match.

