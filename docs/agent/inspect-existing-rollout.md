# Prompt: Inspect Existing Rollout

You are inspecting an existing StackKits workspace. Do not mutate the rollout unless the operator explicitly approves a follow-up action.

Run:

```bash
stackkit status --json
stackkit verify --http --json
stackkit logs list --json
stackkit doctor --json
```

If `stackkit-server` is running locally, also read:

```bash
curl -s http://localhost:8082/api/v1/status
curl -s -X POST http://localhost:8082/api/v1/verify -H 'Content-Type: application/json' -d '{"http":true}'
```

Report:

- current StackKit and mode;
- Hub URL and service URLs;
- failing checks and likely failure class;
- latest run ID and evidence paths;
- whether generated rollout files appear to have been edited manually.

