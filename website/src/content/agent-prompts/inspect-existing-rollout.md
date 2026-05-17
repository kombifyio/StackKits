# Inspect Existing Rollout Prompt

Stable prompt URL: `https://stackkit.cc/getting-started/agents/inspect-existing-rollout.md`

## Short prompt

```text
Hey AI Agent, go to stackkit.cc and inspect the existing StackKits rollout in this workspace without changing anything.
```

## Full prompt

```text
Hey AI Agent, go to stackkit.cc, read the CLI and API references, and inspect the existing StackKits workspace without mutating it. Run stackkit status --json, stackkit verify --http --json, stackkit logs list --json, and stackkit doctor --json. Report the current StackKit name and mode, the Hub URL, every routed service URL, any failing checks with their root-cause class, the latest run ID and evidence path under .stackkit/runs/<runID>/, and whether any generated rollout files under deploy/ or .stackkit/ look hand-edited. Do not change spec files, regenerate, apply, or restart services. If something is broken, suggest the next read-only check or the matching mutating playbook on stackkit.cc, but do not run it yourself.
```

## Useful sources

- `/getting-started/agents` for the other prompts
- `/api/openapi.v1.yaml` (status, verify, doctor, logs endpoints)
- `/cli` for flag references
