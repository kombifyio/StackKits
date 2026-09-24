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
- Streamable HTTP is the standards-based remote-capable transport for `POST /mcp`. It is stateless and serves MCP protocol `2026-07-28`: no `initialize` handshake is required, `server/discover` reports the supported protocol versions, and each request carries its protocol version and client capabilities in `_meta` with matching `Mcp-Protocol-Version` and `Mcp-Method` (plus `Mcp-Name` for named calls) headers. The server never issues an `Mcp-Session-Id` and ignores one a client sends. There is no GET stream (`GET /mcp` returns 405). Clients on older protocol versions that still send `initialize` are served per request.
- WebSocket is not the default StackKits MCP surface; it would be a custom transport or gateway layer.
- Durable external access to `stackkit-server /mcp` is a target StackKit-owned day-2 capability after install, not the current default first-install path.

Default stance:

- docs/read-only tools are available by default;
- mutating tools require `STACKKIT_MCP_ALLOW_WRITE=true` or `stackkit-server --mcp-allow-write`;
- every tool declares truthful read-only, idempotent, destructive, and open-world hints; only tools that reach beyond the local host (for example `stackkit_upgrade`, `stackkit_kit_list`, `stackkit_federation_control_send`) are open-world;
- `GET /openmcp.json` lists exactly the tools the running server registers.

MCP HTTP authentication:

- `POST /mcp` accepts only a dedicated MCP token, sent as `Authorization: Bearer <token>` or in the `X-StackKit-MCP-Token` header. Tokens are compared in constant time.
- Token sources, first match wins: `--mcp-token`, `STACKKIT_MCP_TOKEN`, then `STACKKIT_MCP_TOKEN_FILE`. The file variant lets an installer mint the token into a file (for example mode `0600`) instead of passing it on a command line; surrounding whitespace is trimmed, and an unreadable or empty file stops the process at startup.
- The `stackkit-server` API key is never an MCP credential. There is no API-key fallback, `/mcp` does not require the API key, and `stackkit-server` refuses to start when the MCP token equals the API key.
- `stackkit-server` fails closed: without an MCP token, `POST /mcp` stays mounted and answers every request with `401` and a structured `mcp_token_not_configured` error that explains how to configure the token. `--allow-unauthenticated` relaxes only the REST API key, never `/mcp`.
- `stackkit-mcp --transport http` may run without a token only on a loopback listen address such as `127.0.0.1:8091`. Any other listen address requires a token and the process refuses to start without one.

For non-loopback access, the connector must be behind a protected path such as VPN, SSH tunnel, private network, mTLS/reverse proxy, or an OAuth-aware gateway. Remote write access also needs explicit write mode and should log run IDs, actor, target, tool inputs, and evidence locations.

## StackKits State Console

The connector embeds `ui://stackkits/state-console.html`. The Go MCP runtime
owns it: `internal/stackkitmcp/assets/state-console.html` is the source, embedded
into the binary and served so hosts can render local StackKits state inside the
MCP client. The mcp-use-compatible app layer is the derived artifact —
`mcp-use/stackkits-app/scripts/build.mjs` reads that asset and writes the app
bundle, never the reverse. The State Console is a
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

## Native standalone tools

Every server rolled out with StackKits runs this connector, and the `stackkit`
CLI is controllable through it: each public CLI command is either projected as
an MCP tool from the shared operation catalog or recorded as an explicit
exception with reason, scope and removal criterion. The regression test
`TestEveryPublicCLICommandIsAnMCPToolOrAnExplicitException` walks the real
Cobra command tree and fails on any command that is neither.

Built-in read-only and diagnostic tools:

- `stackkit_docs_search`, `stackkit_api_overview`, `stackkit_api_endpoint`,
  `stackkit_get_openapi_spec`
- `stackkit_install_plan`, `stackkit_self_check_plan`, `stackkit_state_console`
- `stackkit_module_profiles`, `stackkit_application_delivery_compatibility`
- `stackkit_validate_spec`, `stackkit_generate_preview`, `stackkit_config_get`,
  `stackkit_compat_check`
- `stackkit_status`, `stackkit_logs_list`, `stackkit_log_get` as HTTP calls to
  `stackkit-server` in `server` mode, only when the process-backed CLI is not
  bound

Create-only CUE authoring:

- `stackkit_config_set` validates through the embedded CUE authority, creates a missing canonical v2 spec without invoking the CLI, and replaces existing v2 intent only through `expected_spec_hash` compare-and-swap.

### Process-backed operation tools

`internal/standaloneoperations` is the one catalog shared by the CLI, this
connector and the State Console. Each contract names a stable `stackkit.*`
operation ID, a `stackkit_*` tool name, the exact CLI command, truthful
mutation/destructive/idempotent/open-world facts, and the typed arguments an
agent may pass. `stackkit operations --json` prints the full catalog, and MCP
`tools/list` shows the tools this process registered.

The connector registers these tools only when the `actions` mode is enabled and
the MCP process cryptographically binds the packaged sibling CLI with the
identical version, commit, and startup digest:

- read-only operations (for example `stackkit_status`, `stackkit_verify`,
  `stackkit_logs_read`, `stackkit_host_preflight`, `stackkit_service_logs`,
  `stackkit_user_list`) register in `actions` mode;
- mutating operations (for example `stackkit_apply`, `stackkit_service_restart`,
  `stackkit_identity_projection_apply`, `stackkit_backup_restore_activate`)
  additionally require `STACKKIT_MCP_ALLOW_WRITE=true`, the exact operation ID
  as `operation_confirmation`, and `owner_approved=true`. Only then does the
  adapter add the CLI's own approval flag such as `--owner-approve`.

Most operation tools are generated from their catalog contract: the input
schema is closed (`additionalProperties: false`), carries `base_dir`,
`spec_path`, `correlation_id` and `timeout_seconds`, and adds each typed
argument. Flag values are passed as `--flag=value` and positional values may not
start with `-`, so an input can never become another CLI flag. The original
lifecycle tools (`stackkit_init`, `stackkit_resolve`, `stackkit_generate`,
`stackkit_plan`, `stackkit_apply`, `stackkit_verify`, `stackkit_status`,
`stackkit_setup`, `stackkit_logs`, `stackkit_backup*`, `stackkit_restore*`,
`stackkit_upgrade`, `stackkit_drift`, `stackkit_remove`) keep their hand-written
typed adapters. `stackkit_remove` requires one exact `workload_ref`; it invokes
native v2 workload-removal authority and never the legacy whole-deployment
cleanup.

### Secret values never cross MCP

Tool inputs carry values, file paths and references by name, never passwords,
API keys, tokens, private keys or passphrases. Commands that would put a secret
value into the MCP transcript stay CLI-only exceptions: `secrets reveal`,
`backup target import` (S3 keys and passphrase on stdin), `cluster join-token`,
`user add` (one-time passkey setup URL) and `user owner activate` (activation
URL). `stackkit_user_owner_status`, `stackkit_backup_target_status` and
`stackkit_secrets_materialize` cover the non-secret parts.

### Explicit exceptions

`internal/standaloneoperations/exceptions.go` records every command that is not
a tool, grouped by scope: CLI user experience (`help`, `completion`,
`version`, `operations`, help-only groups, the interactive `use-cases pick`),
agent bootstrap (`agent *`), aliases of projected operations (`logs get`,
`logs latest`, `kit upgrade`, `kit verify`), exact-v0.6 compatibility verbs,
the secret-bearing commands above, the signed `runtime execute` channel, and
StackKits source-repository tooling (`docs emit-*`, `registry` generators,
`compat emit-os-matrix`).

### Results and evidence

CLI-backed tools publish parsed JSON through MCP `structuredContent`; they do
not require clients to scrape text. Apply, Status, Verify, and Logs preserve
their versioned runtime/log contracts and local evidence links. Adapter or CLI
failures include `stackkit.actionable-error/v1` under `error_details` with a
stable reason code and concrete local recovery commands. These read models
remain usable in Standard Mode without Techstack, Kombify Cloud, an account,
or another hosted endpoint.

CLI-backed inputs accept an optional validated `correlation_id`, forwarded as
the local CLI `--correlation-id`; it is evidence correlation only and grants no
authority. Machine commands reserve stdout for a single JSON document, so MCP
can retain Apply/Status/Verify evidence in `structuredContent` even on a
non-zero actionable result. MCP also recursively redacts returned structured
values and text as a defense for legacy log files and subprocess diagnostics.

Native init and `stackkit_config_set` share one persistence authority: create
is no-replace, replacement requires the exact current CUE-normalized hash
(`--expected-spec-hash` in the CLI, `expected_spec_hash` in MCP), and
already-applied retries are idempotent. Current native builds do not register
the legacy combined rollout, update, node-local HTTP verify/doctor, or
arbitrary provider/SSH inputs.

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
args = ["--mode", "docs,local,server,actions"]
```

Protected durable endpoint after install (send `Authorization: Bearer <mcp-token>`):

```text
POST http://localhost:8082/mcp
GET  http://localhost:8082/openmcp.json
```

Mint a dedicated MCP token and enable write-capable local agent mode:

```sh
umask 077 && openssl rand -hex 32 > /etc/stackkit/mcp-token
STACKKIT_MCP_ALLOW_WRITE=true STACKKIT_MCP_TOKEN_FILE=/etc/stackkit/mcp-token stackkit-server --api-key <api-key>
```

Stateless probe with curl:

```sh
curl -s -X POST http://localhost:8082/mcp \
  -H "Authorization: Bearer $(cat /etc/stackkit/mcp-token)" \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -H 'Mcp-Protocol-Version: 2026-07-28' \
  -H 'Mcp-Method: server/discover' \
  --data '{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}'
```
