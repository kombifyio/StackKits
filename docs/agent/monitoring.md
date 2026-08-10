# StackKits Architecture v2 Agent Monitoring Notes

StackKits does not expose a separate permanent MCP monitoring server. The durable day-2 surface is the single `stackkit` MCP connection backed by `stackkit-server /mcp`.

On native v0.7, `GET /api/v1/status` is intentionally spec-only: it proves canonical desired intent and reports `resolve-required`, not deployment readiness. Legacy node-local HTTP Verify, Doctor, and Plan handlers are unavailable until they are rebuilt on exact ResolvedPlan and execution-evidence inputs.

Operational monitoring must be derived from the exact persisted Architecture v2 chain:

- canonical StackSpec hash;
- observed Inventory hash;
- ResolvedPlan hash;
- generation manifest and authorization;
- executor receipt and runtime evidence;
- `stackkit status --json` and `stackkit verify --json` runtime observations
  bound to Stack, Plan, Apply result, run, Site, node, and execution channel;
- `stackkit logs list --json`, followed by `stackkit logs get <runId> --json`
  or `stackkit logs latest --json`, for content-addressed rollout evidence.

Log reads are bounded streaming pages. Follow `next_cursor` only while
`truncated` is true; the cursor is bound to the exact run ID and full-file
digest and therefore fails closed if an active log changes between requests.
Callers may supply a validated `--correlation-id` to bind a lifecycle request
explicitly to its collision-resistant, exclusively created local run. Stored
events, structured CLI reads, and MCP results all apply recursive defensive
secret redaction.

`live: false` is intentional when Status or Verify projects the last signed
Apply evidence without making a fresh target call. Consumers must display that
freshness state and must not promote it to current health. Provider inventory,
lease state, enrollment, multi-server aggregation, and AI remediation remain
Techstack responsibilities; StackKits emits only its provider-neutral local
lifecycle evidence.

BaseKit may deploy Uptime Kuma and generated service URLs, but those observations do not replace the governed artifact and receipt chain. Extend the protected day-2 MCP surface only with read-only tools that consume those authorities; do not reconstruct state from legacy rollout files.
