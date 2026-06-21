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

## MCP App

The connector embeds the onboarding resource `ui://stackkits/onboarding.html`. It is generated from the mcp-use-compatible app layer and exposed through the Go MCP runtime so hosts can render StackKits guidance inside the MCP client. The resource is a single stateful MCP App widget; it uses widget-local state for progress and can call the native connector tools from the widget when the host supports the Apps SDK bridge.

The authoring package lives in `mcp-use/stackkits-app/`. The production runtime stays in Go; mcp-use owns the app metadata, Inspector workflow, and generated OpenMCP/widget artifacts. It is not a second production MCP connector.

The app flow is:

1. Contact and workspace: collect owner/admin email, stack name, workspace, and spec path.
2. StackKit and profile: choose the published BaseKit beta profile plus install context, compute tier, and service profile.
3. Domain and core settings: choose local `home.localhost`, managed `kombify.me`, a custom domain, or LAN DNS.
4. Review and plan: render a StackSpec preview, optionally save YAML with `stackkit_config_set`, create a spec with `stackkit_init`, validate, preview generation, and run a bounded plan flow.
5. Rollout and evidence: run gated rollout/apply, verify, doctor, and logs while the widget shows step progress.

Use [../INSTALLATION_PROCESSES.md](../INSTALLATION_PROCESSES.md) to decide whether native MCP is the right execution or day-2 path. The comparison is based on configuration/individualization degree, access options, and automation degree.

## Read-Only Tools

- `stackkit_docs_search`
- `stackkit_api_overview`
- `stackkit_api_endpoint`
- `stackkit_get_openapi_spec`
- `stackkit_install_plan`
- `stackkit_self_check_plan`
- `stackkit_onboarding_app`
- `stackkit_validate_spec`
- `stackkit_generate_preview`
- `stackkit_config_get`
- `stackkit_status`
- `stackkit_verify`
- `stackkit_logs_list`
- `stackkit_log_get`
- `stackkit_doctor`
- `stackkit_compat_check`

## Write Tools

Write tools execute CLI-equivalent operations on the local target workspace only:

- `stackkit_init`
- `stackkit_prepare`
- `stackkit_generate`
- `stackkit_plan`
- `stackkit_apply`
- `stackkit_update`
- `stackkit_config_set`
- `stackkit_rollout`

`stackkit_apply` and `stackkit_rollout` set `--skip-platform-apps` by default, so this surface manages StackKits infrastructure rollout and verification, not L3 app lifecycle orchestration.

Out of scope:

- `stackkit app add`
- customer app rollout
- managed-serverless provisioning
- SaaS placement orchestration
- internal Kombify operator MCPs

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
