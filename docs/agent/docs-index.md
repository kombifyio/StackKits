# StackKits Agent Docs Index

Agent-facing StackKits references:

- `docs/agent/agents.md`: autonomous workflow and evidence contract.
- `docs/agent/agents.md` also links the optional Techstack Advanced seam,
  immutable contract consumer lock and existing Wallet host-reader boundary.
- `docs/agent/stackkit-mcp.md`: single StackKits MCP connection, local adapter, durable endpoint, and security gates.
- `docs/API.md`: node-local API summary.
- `docs/CLI.md`: CLI reference.
- `docs/TROUBLESHOOTING.md`: generated stable error IDs, causes and next actions shared with CLI failures and MCP self-check.
- `docs/INSTALLATION_PROCESSES.md`: CLI, MCP, WebMCP, SSH, and on-server agent install method comparison with decision tree.
- `docs/stack-spec-reference.md`: StackSpec field reference.
- `docs/agent/monitoring.md`: status, logs, verification, and evidence surfaces.
- `api/openapi.v1.yaml`: OpenAPI mirror embedded by the StackKits MCP runtime.

Basement Kit is the verified one-click path and Cloud Kit is its preview
counterpart for an externally supplied host. The current unpinned public latest
is whatever `releases/latest` resolves to in `kombifyio/stackKits`; documentation
does not restate that number. Modern Homelab is alpha scaffolding and graduates
only the Runtime owners whose exact boundaries are bound; remaining publication,
Cloud-verifier, backend-Health, and live-evidence surfaces stay fail-closed.
Product-bundled applications use standalone Compose by default. Optional
Marketplace owns Coolify outside default core; Komodo is the planned operations
platform without a store and Dokploy is not pursued. Current-pin release evidence
focuses on owner/passkey activation, exact application setup, protected app health,
retryable L3 setup actions, and seeded content only when demo data is
explicitly enabled. User-installed apps outside that path are state-unmanaged.
