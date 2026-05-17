# StackKits Agent Guide

StackKits is agent-ready through static public docs, deterministic CLI commands, a node-local API surface, and a local MCP connector installed beside the CLI.

BaseKit is the verified beta one-click path. Modern Homelab and HA Kit are alpha/scaffolding definitions until their release matrices graduate. Current BaseKit evidence proves the local fallback path plus auth/setup guards; Cubi/Coolify-managed L3 rollout remains a documented blocker.

## Agent Contract

Use CUE and StackSpec files as source of truth. Generated OpenTofu, Docker Compose, scripts, tfvars, state, and snapshots are outputs. Do not hand-edit generated rollout artifacts.

Default autonomous flow:

```bash
curl -sSL https://base.stackkit.cc | sh
mkdir my-homelab
cd my-homelab
stackkit init base-kit --non-interactive --admin-email admin@example.com
stackkit prepare --dry-run
stackkit validate
stackkit generate --force
stackkit plan
stackkit apply
stackkit verify --http --json
```

Agent-facing helpers:

```bash
stackkit agent install-plan --json
stackkit agent self-check --json
stackkit agent prompt basekit-autonomous-rollout
stackkit agent mcp-config --client codex --mode docs,local,server
```

## Evidence

Release-hardening and autonomous-agent runs should produce:

- a run manifest matching `stackkit-agent-run-manifest.schema.json`;
- a functional result matching `stackkit-agent-functional-result.schema.json`;
- `stackkit verify --http --json` output;
- links or paths to logs and rollout evidence;
- confirmation that generated artifacts were not hand-edited.

The canonical local BaseKit Hub URL is `http://base.home.localhost`.

## Local MCP

`stackkit-mcp` is shipped as a separate binary next to `stackkit` and `stackkit-server`. The default transport is stdio. HTTP transport binds to loopback by default. Management and write-capable modes require explicit operator opt-in.
