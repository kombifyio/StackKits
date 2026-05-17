# Prompt: Enable Monitoring Add-on

You are enabling monitoring for an existing BaseKit rollout.

Before mutation, inspect:

```bash
stackkit status --json
stackkit verify --http --json
stackkit addon list
```

When the operator approves the add-on change, use StackKits commands rather than hand-editing generated files:

```bash
stackkit addon add monitoring
stackkit validate
stackkit generate --force
stackkit plan
stackkit apply
stackkit verify --http --json
```

Record evidence, changed StackSpec fields, latest run ID, service URLs, and verification output.

