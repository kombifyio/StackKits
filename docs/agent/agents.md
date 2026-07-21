# StackKits Agent Guide

StackKits v0.7 is agent-ready through canonical CUE Definitions, deterministic CLI stages, a truthful node-local API, and one user-facing MCP connection named `stackkit`.

Basement Kit and Cloud Kit are provider-free single-site profiles; Modern Homelab composes explicit home and cloud sites. StackSpec contains desired intent only. TechStack or another authorized external executor owns provider lifecycle, credentials, host observation, management addresses, and transport.

## Agent Contract

CUE is the technical contract source of truth. StackSpec v2 records desired intent; observed Inventory is separate; ResolvedPlan binds both. Generated OpenTofu, Docker Compose, scripts, tfvars, state, and snapshots are outputs and must never be hand-edited.

Default native-v0.7 flow:

```bash
stackkit version
mkdir my-homelab
cd my-homelab
stackkit init basement-kit --non-interactive
stackkit prepare --dry-run
stackkit validate
stackkit resolve --inventory <observed-inventory.json> --output deploy/.stackkit/resolved-plan.json
stackkit generate
stackkit plan
stackkit apply
stackkit verify --json
```

Do not replace an existing StackSpec blindly. Native CLI replacement requires `--expected-spec-hash`; MCP uses the equivalent `expected_spec_hash`. Both use the same CUE-normalized CAS authority, creation remains no-replace, and retries of an already-applied candidate are idempotent.

Agent-facing helpers:

```bash
stackkit agent install-plan --json
stackkit agent self-check --json
stackkit agent prompt basekit-autonomous-rollout
stackkit agent mcp-config --client codex --mode docs,local,server
```

## Evidence

An autonomous run should preserve:

- exact candidate version and Definition digest;
- canonical StackSpec and Inventory hashes;
- exact ResolvedPlan hash;
- generation manifest and authorization;
- executor receipt and runtime evidence;
- `stackkit verify --json` output;
- confirmation that no generated artifact was hand-edited.

## StackKits MCP

Agents configure one MCP server named `stackkit`. The local adapter and protected `stackkit-server /mcp` expose the same versioned contract. Write-capable process tools are available only when the MCP process can bind the packaged sibling CLI with the identical version, commit, and startup digest. Native v0.7 exposes v2 authoring, resolve, generate, plan, apply, and plan verification; v1 is only read-only validation and migration input.
