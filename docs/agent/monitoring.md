# StackKits Agent Monitoring Notes

StackKits v1 does not expose a separate permanent MCP monitoring server.

Use the node-local `stackkit-server` read-only API surface for agent-visible rollout state:

- `GET /api/v1/status`
- `POST /api/v1/verify`
- `POST /api/v1/doctor`
- `POST /api/v1/plan`
- `GET /api/v1/logs`
- `GET /api/v1/runs/{runID}/evidence`

BaseKit may deploy Uptime Kuma and generated service URLs, but autonomous release evidence should still include `stackkit verify --http --json` and `.stackkit/runs/<runID>/` evidence.

If remote monitoring later needs agent access, add an optional read-only agent bridge sidecar after the local MCP and management API are proven.
