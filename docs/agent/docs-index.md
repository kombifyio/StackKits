# StackKits Agent Docs Index

Agent-facing StackKits references:

- `docs/agent/agents.md`: autonomous workflow and evidence contract.
- `docs/agent/agents.md` also links the optional Homelab Advanced seam,
  immutable contract consumer lock and existing Wallet host-reader boundary.
- `docs/agent/stackkit-mcp.md`: single StackKits MCP connection, local adapter, durable endpoint, and security gates.
- `docs/API.md`: node-local API summary.
- `docs/CLI.md`: CLI reference.
- `docs/TROUBLESHOOTING.md`: generated stable error IDs, causes and next actions shared with CLI failures and MCP self-check.
- `docs/INSTALLATION_PROCESSES.md`: CLI, MCP, WebMCP, SSH, and on-server agent install method comparison with decision tree.
- `docs/stack-spec-reference.md`: StackSpec field reference.
- `docs/agent/monitoring.md`: status, logs, verification, and evidence surfaces.
- `api/openapi.v1.yaml`: OpenAPI mirror embedded by the StackKits MCP runtime.

Basement Kit targets home hosts and Cloud Kit targets an externally supplied
VPS. The public release README carries the generated, version-bound support table. The current unpinned public latest
is whatever `releases/latest` resolves to in `kombifyio/stackKits`; documentation
does not restate that number. Modern Homelab is a definition archive and graduates
only the Runtime owners whose exact boundaries are bound; remaining publication,
Cloud-verifier, backend-Health, and live-evidence surfaces stay fail-closed.
Product-bundled applications render to Docker Compose and run as OpenTofu
Stage 1 roots.
Komodo, Dokploy and legacy core Coolify `4.1.2` are Layer-2 PaaS engines
retired from 1.0 core execution by ADR-0054; OpenTofu Stage 1 over Compose is the single core
engine, and Coolify `4.7.5+` is only the optional Layer-3 Marketplace and
custom-apps extension. Current-pin release evidence
focuses on owner/passkey activation, exact application setup, protected app health,
retryable L3 setup actions, and seeded content only when demo data is
explicitly enabled. User-installed apps outside that path are state-unmanaged.
