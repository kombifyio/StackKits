# Prompt: Diagnose Failed Rollout

You are diagnosing a failed StackKits rollout. Prefer read-only evidence first.

Run:

```bash
stackkit logs list --json
stackkit logs latest --json
stackkit doctor --json
stackkit verify --http --json
stackkit status --json
```

Classify the failure as one of:

- host-prerequisite
- docker-daemon
- image-pull
- network-or-dns
- generated-config
- opentofu-plan
- opentofu-apply
- service-health
- unknown

Do not edit generated OpenTofu or Compose files. If a fix is needed, change the StackSpec, kit CUE, or Go source, then regenerate and rerun the narrow relevant gate.

