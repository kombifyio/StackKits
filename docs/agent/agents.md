# StackKits Agent Guide

StackKits is agent-ready through static public docs, deterministic CLI commands, a node-local API surface, and one user-facing MCP connection named `stackkit`.

Basement Kit and Cloud Kit are the stable one-click paths; the unpinned public latest is `v0.6.0`, the stable architecture baseline. Architecture v2 runtime surfaces and Modern Homelab that have not graduated remain explicit fail-closed Preview contracts. Product-bundled L3 applications are PaaS-intended by default; Basement Kit evidence must prove owner/passkey activation, selected-PaaS setup, protected app health, retryable L3 setup actions, and seeded content only when demo data is explicitly enabled. User-installed apps outside that path are state-unmanaged.

For choosing the right install path, use [../INSTALLATION_PROCESSES.md](../INSTALLATION_PROCESSES.md). It classifies website prompting, full installer, direct CLI, on-server agent, external SSH agent, native MCP, and stdio MCP fallback by three pillars: configuration/individualization, access options/authority boundary, and automation degree (`A0-A4`). Treat website discovery as the read-only start, SSH as the external bootstrap authority, `stackkit-mcp` as the local adapter for the single `stackkit` connection, and protected remote `/mcp` as a target StackKit-owned day-2 capability after install.

## Agent Contract

Use CUE and StackSpec files as source of truth. Generated OpenTofu, Docker Compose, scripts, tfvars, state, and snapshots are outputs. Do not hand-edit generated rollout artifacts.

Default autonomous flow:

```bash
curl -sSL https://base.stackkit.cc | sh
mkdir my-homelab
cd my-homelab
stackkit init basement-kit --non-interactive --admin-email admin@example.com
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

The canonical local Basement Kit Hub URL is `http://base.home.localhost`.

## StackKits MCP

Agents should configure one MCP server named `stackkit`. On local machines this starts `stackkit-mcp` as an adapter. On installed targets the same connector can later be reached through protected `stackkit-server /mcp`. Management and write-capable modes require explicit operator opt-in.
