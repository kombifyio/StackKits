# StackKits Architecture v2 Agent Monitoring Notes

StackKits does not expose a separate permanent MCP monitoring server. The durable day-2 surface is the single `stackkit` MCP connection backed by `stackkit-server /mcp`.

On native v0.7, `GET /api/v1/status` is intentionally spec-only: it proves canonical desired intent and reports `resolve-required`, not deployment readiness. Legacy node-local HTTP Verify, Doctor, and Plan handlers are unavailable until they are rebuilt on exact ResolvedPlan and execution-evidence inputs.

Operational monitoring must be derived from the exact persisted Architecture v2 chain:

- canonical StackSpec hash;
- observed Inventory hash;
- ResolvedPlan hash;
- generation manifest and authorization;
- executor receipt and runtime evidence;
- `stackkit verify --json` output bound to that chain.

BaseKit may deploy Uptime Kuma and generated service URLs, but those observations do not replace the governed artifact and receipt chain. Extend the protected day-2 MCP surface only with read-only tools that consume those authorities; do not reconstruct state from legacy rollout files.
