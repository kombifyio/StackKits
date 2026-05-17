# StackKits Agent Guide

StackKits is agent-ready through static public docs, deterministic CLI commands, a node-local API surface, and a local MCP connector installed beside the CLI.

BaseKit is the verified beta one-click path. Modern Homelab and HA Kit are alpha/scaffolding definitions until their release matrices graduate. Current BaseKit evidence proves the local fallback path plus auth/setup guards; Cubi/Coolify-managed L3 rollout remains a documented blocker.

Use `stackkit agent prompt <scenario>` for copy-ready prompts and `stackkit agent mcp-config` for local connector configuration.

Core autonomous flow:

```bash
stackkit init base-kit --non-interactive --admin-email admin@example.com
stackkit prepare --dry-run
stackkit validate
stackkit generate --force
stackkit plan
stackkit apply
stackkit verify --http --json
```

Do not hand-edit generated rollout artifacts. Change StackSpec, CUE, or Go source, then regenerate.

Published prompts:

- [Autonomous BaseKit rollout](/getting-started/agents/basekit-autonomous-rollout.md)
- [Inspect existing rollout](/getting-started/agents/inspect-existing-rollout.md)
- [Diagnose failed rollout](/getting-started/agents/diagnose-failed-rollout.md)
- [Enable monitoring add-on](/getting-started/agents/enable-monitoring-addon.md)
- [Generate and apply through SSH](/getting-started/agents/ssh-rollout.md)
