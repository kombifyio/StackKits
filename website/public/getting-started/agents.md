# StackKits Agent Guide

StackKits is agent-ready through static public docs, deterministic CLI commands, a node-local API surface, and one user-facing MCP connection named `stackkit`.

BaseKit is the only public OSS kit surface for this release line. Product-bundled L3 applications are PaaS-intended by default; Coolify-managed application-layer evidence for ready-to-use use cases remains a documented blocker. User-installed apps outside that path are state-unmanaged.

Use `stackkit agent prompt <scenario>` for copy-ready prompts and `stackkit agent mcp-config` for one ready-to-paste `stackkit` MCP connection.

Use [Installation processes](/getting-started/installation-processes.md) to choose between website discovery, full installer, direct CLI, on-server agent, SSH agent, native MCP, and local MCP fallback by configuration/individualization, access options, and automation degree. Website discovery is read-only; SSH is the external bootstrap authority; `stackkit-mcp` is the local adapter for the single `stackkit` connection; protected remote `/mcp` is a target day-2 capability after install.

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
- [Generate and apply through SSH](/getting-started/agents/ssh-rollout.md)
