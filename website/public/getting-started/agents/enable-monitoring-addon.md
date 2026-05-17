# Prompt: Enable Monitoring Add-on

Inspect before mutation:

```bash
stackkit status --json
stackkit verify --http --json
stackkit addon list
```

After operator approval:

```bash
stackkit addon add monitoring
stackkit validate
stackkit generate --force
stackkit plan
stackkit apply
stackkit verify --http --json
```

Record evidence, changed StackSpec fields, latest run ID, service URLs, and verification output.

