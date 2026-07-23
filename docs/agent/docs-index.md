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

Basement Kit and Cloud Kit are the stable one-click paths; `v0.7.5` is the
current unpinned public latest on the native Architecture-v2 line. Modern
Homelab is included in the public release as Preview and graduates only the
Runtime owners whose exact boundaries are bound; remaining publication,
Cloud-verifier, backend-Health, and live-evidence surfaces stay fail-closed.
Product-bundled L3 applications are PaaS-intended by default; release evidence
focuses on owner/passkey activation, selected-PaaS setup, protected app health,
retryable L3 setup actions, and seeded content only when demo data is
explicitly enabled. User-installed apps outside that path are state-unmanaged.
