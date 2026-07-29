# StackKits MCP Connector

StackKits exposes one user-facing MCP connection named `stackkit`.

Implementation has two entrypoints for that same connector:

- local adapter: `stackkit-mcp` over stdio or loopback HTTP;
- durable endpoint: `stackkit-server POST /mcp` after install.

The user should not have to choose between two MCP products. They give their agent one `stackkit` MCP connection. The runtime chooses the local adapter or the protected server endpoint depending on where the agent runs.

## Website Discovery

`https://stackkit.cc/openmcp.json` is read-only. It advertises installer URLs, `llms.txt`, prompt Markdown, OpenAPI/schema mirrors, and local connector configuration. It never executes target-server actions. If an agent uses website discovery and then installs StackKits, the execution channel changes to shell, SSH, local MCP, or a protected target MCP endpoint.

Core public resources:

- `https://stackkit.cc/llms.txt`
- `https://stackkit.cc/llms-full.txt`
- `https://stackkit.cc/getting-started/installation-processes.md`
- `https://stackkit.cc/mcp/stackkit-mcp.md`
- `https://stackkit.cc/api/openapi.v1.yaml`
- `https://stackkit.cc/schemas/stackkit-agent-run-manifest.schema.json`
- `https://stackkit.cc/schemas/stackkit-agent-functional-result.schema.json`

## StackKits MCP Runtime

`stackkit-server` mounts a native Streamable HTTP endpoint:

- `POST /mcp`
- `GET /openmcp.json`

`stackkit-mcp` is the local adapter binary. It uses the same internal tool/resource/prompt registration as `stackkit-server`, so it is not a second connector.

Transport stance:

- `stdio` is the local adapter path for MCP clients that launch `stackkit-mcp` as a subprocess.
- Streamable HTTP is the standards-based remote-capable transport for `POST /mcp`.
- WebSocket is not the default StackKits MCP surface; it would be a custom transport or gateway layer.
- Durable external access to `stackkit-server /mcp` is a target StackKit-owned day-2 capability after install, not the current default first-install path.

Default stance:

- docs/read-only tools are available by default;
- mutating tools require `STACKKIT_MCP_ALLOW_WRITE=true` or `stackkit-server --mcp-allow-write`;
- MCP HTTP token auth uses `STACKKIT_MCP_TOKEN` or `stackkit-server --mcp-token`;
- when no explicit MCP token is configured, `stackkit-server` uses the API key as local MCP token fallback;
- all tools are annotated with read-only, idempotent, destructive, and closed-world policy hints.

For non-loopback access, the connector must be behind a protected path such as VPN, SSH tunnel, private network, mTLS/reverse proxy, or an OAuth-aware gateway. Remote write access also needs explicit write mode and should log run IDs, actor, target, tool inputs, and evidence locations.

## StackKits State Console

The connector embeds `ui://stackkits/state-console.html`. It is generated from
the mcp-use-compatible app layer and exposed through the Go MCP runtime so hosts
can render local StackKits state inside the MCP client. The State Console is a
single stateful UI adapter over registered MCP operations. It is not a Wizard,
Finder, recommendation engine, or lifecycle implementation.

The production runtime stays in Go. The State Console obtains configuration
metadata from the embedded CUE Definition and renders a provider-free request;
it does not hand-author StackSpec YAML.

The native v0.7 app flow is:

1. Workspace: collect deployment name, workspace, and spec path.
2. Explicit Kit configuration: the user chooses Basement Kit, Cloud Kit, or Modern Homelab; the State Console does not score or recommend a Kit.
3. Resolution inputs: reference externally observed Inventory and the canonical ResolvedPlan output path. Provider lifecycle, credentials, management addresses, host facts, and transports remain outside StackSpec.
4. Review and plan: create initial v2 intent through the CUE authoring contract, validate it, resolve it against Inventory, then generate and plan from the exact persisted plan.
5. Operation approval and evidence: stage an Apply request for the connected
   agent. The State Console does not call `stackkit_apply`; the agent must
   present the registered operation for operation-specific confirmation and
   Owner approval before the connector executes it.

Initial authoring is no-replace. Updating existing v2 intent requires its exact CUE-normalized `expected_spec_hash`; stale writers fail without mutation and an already-applied retry is idempotent.

Use [../INSTALLATION_PROCESSES.md](../INSTALLATION_PROCESSES.md) to decide whether native MCP is the right execution or day-2 path. The comparison is based on configuration/individualization degree, access options, and automation degree.

## Native v0.7 Tools

Read-only and diagnostic tools:

- `stackkit_docs_search`
- `stackkit_api_overview`
- `stackkit_api_endpoint`
- `stackkit_get_openapi_spec`
- `stackkit_install_plan`
- `stackkit_self_check_plan`
- `stackkit_state_console`
- `stackkit_validate_spec`
- `stackkit_generate_preview`
- `stackkit_config_get`
- `stackkit_status`
- `stackkit_logs_list`
- `stackkit_log_get`
- `stackkit_compat_check`

Create-only CUE authoring:

- `stackkit_config_set` validates through the embedded CUE authority, creates a missing canonical v2 spec without invoking the CLI, and replaces existing v2 intent only through `expected_spec_hash` compare-and-swap.

Process-backed write, artifact, and plan-verification tools:

These tools are registered only when write mode is enabled and the MCP process cryptographically binds the packaged sibling CLI with the identical version, commit, and startup digest:

- `stackkit_init`
- `stackkit_resolve`
- `stackkit_generate`
- `stackkit_plan`
- `stackkit_apply`
- `stackkit_verify_plan`

Native init and `stackkit_config_set` share one persistence authority: create is no-replace, replacement requires the exact current CUE-normalized hash (`--expected-spec-hash` in the CLI, `expected_spec_hash` in MCP), and already-applied retries are idempotent. Native v0.7 does not register the legacy combined rollout, update, node-local HTTP verify/doctor, or arbitrary provider/SSH inputs.

## Exact-v0.6 HTTP Compatibility

Current source preserves only the protected node-local HTTP-backed
`stackkit_verify` and `stackkit_doctor` MCP tools when built for exact v0.6.
Process-backed v1 init/generate/apply/update and the combined rollout macro are
not rebuilt. Immutable published v0.6 artifacts remain the historical rollback
boundary.

Out of scope:

- `stackkit app add`
- customer app rollout
- managed-serverless provisioning
- SaaS placement orchestration
- internal Kombify operator MCPs

## Product-Native MCPs

The `stackkit` MCP is the lifecycle and evidence connector. It does not replace
native product MCPs declared by Use Case Packages.

For example, the Smart Home package declares Home Assistant's own MCP server at
`/api/mcp` as `productMcp`. StackKits records, protects, and verifies that
endpoint and can hand it to RIL, while Home Assistant remains the MCP authority
for exposed entities, Assist context, and product-level service calls.

## Client Examples

Recommended single local connection:

```toml
[mcp_servers.stackkit]
command = "stackkit-mcp"
args = ["--mode", "docs,local,server"]
```

Protected durable endpoint after install:

```text
POST http://localhost:8082/mcp
GET  http://localhost:8082/openmcp.json
```

Enable write-capable local agent mode:

```sh
STACKKIT_MCP_ALLOW_WRITE=true STACKKIT_MCP_TOKEN=<local-token> stackkit-server --api-key <api-key>
```
