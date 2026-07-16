# StackKits Agent Docs Index

Agent-facing StackKits references:

- `docs/agent/agents.md`: autonomous workflow and evidence contract.
- `docs/agent/stackkit-mcp.md`: single StackKits MCP connection, local adapter, durable endpoint, and security gates.
- `docs/API.md`: node-local API summary.
- `docs/CLI.md`: CLI reference.
- `docs/INSTALLATION_PROCESSES.md`: CLI, MCP, WebMCP, SSH, and on-server agent install method comparison with decision tree.
- `docs/stack-spec-reference.md`: StackSpec field reference.
- `docs/agent/monitoring.md`: status, logs, verification, and evidence surfaces.
- `api/openapi.v1.yaml`: OpenAPI mirror embedded by the StackKits MCP runtime.

Basement Kit and Cloud Kit are the current stable one-click paths; `v0.5.2` is the unpinned public latest patch over the `v0.5.1` Cloud Kit graduation release. Unreleased kit definitions stay outside the public install surface until their release matrices graduate. Product-bundled L3 applications are PaaS-intended by default; release evidence focuses on owner/passkey activation, selected-PaaS setup, protected app health, retryable L3 setup actions, and seeded content only when demo data is explicitly enabled. User-installed apps outside that path are state-unmanaged.
