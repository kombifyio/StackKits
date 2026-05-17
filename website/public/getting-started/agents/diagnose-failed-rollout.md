# Prompt: Diagnose Failed Rollout

Diagnose a failed StackKits rollout with read-only evidence first.

Run:

```bash
stackkit logs list --json
stackkit logs latest --json
stackkit doctor --json
stackkit verify --http --json
stackkit status --json
```

Classify the failure as host-prerequisite, docker-daemon, image-pull, network-or-dns, generated-config, opentofu-plan, opentofu-apply, service-health, or unknown.

Do not edit generated OpenTofu or Compose files.

