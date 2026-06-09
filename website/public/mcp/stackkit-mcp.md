# StackKits MCP Connector

StackKits has one user-facing MCP connection named `stackkit`.

Implementation has two entrypoints for that same connector:

- local adapter: `stackkit-mcp` over stdio or loopback HTTP;
- durable endpoint: `stackkit-server` on the target host at `POST /mcp` and `GET /openmcp.json`.

`https://stackkit.cc/openmcp.json` is read-only website discovery for installers, docs, schemas, OpenAPI, prompts, and local connector configuration. It is not the execution connector.

`stackkit-mcp` uses the same tool/resource/prompt registration as `stackkit-server`, so it is an adapter for the same StackKits MCP connector, not a second connector.

Use `/getting-started/installation-processes.md` to choose the right path by configuration/individualization degree, access options, and automation degree.

Website discovery does not execute target actions. If an agent starts from the website and then installs or manages a server, it switches to shell, SSH, local MCP, or an explicitly protected target MCP endpoint.

Transport stance:

- Standard remote MCP uses Streamable HTTP on `POST /mcp`.
- `stackkit-mcp` stdio is the local subprocess adapter.
- WebSocket would be a custom transport/gateway, not the default interoperable StackKits surface.
- Durable external access to `stackkit-server /mcp` is a target day-2 capability after install, not the current default first-install path.

Default stance:

- read-only tools are available by default;
- write tools require `STACKKIT_MCP_ALLOW_WRITE=true`;
- MCP HTTP token auth uses `STACKKIT_MCP_TOKEN`;
- every tool is annotated as read-only/non-read-only, idempotent, destructive, and closed-world.
- non-loopback access must be protected through tunnel, VPN, private network, mTLS/reverse proxy, or an OAuth-aware gateway.

Read-only tools include docs search, API lookup, OpenAPI retrieval, install plans, self-check plans, onboarding app metadata, validation, generate preview, config read, status, verify, logs, doctor, and compatibility checks.

Write tools execute local CLI-equivalent StackKits operations only:

- `stackkit_init`
- `stackkit_prepare`
- `stackkit_generate`
- `stackkit_plan`
- `stackkit_apply`
- `stackkit_update`
- `stackkit_config_set`
- `stackkit_rollout`

The connector embeds the MCP App resource `ui://stackkits/onboarding.html`. The app is a single stateful onboarding widget for email capture, workspace/spec path, StackKit/profile selection, local/kombify.me/custom/LAN-DNS domain choice, StackSpec preview or YAML save, rollout progress, verify, logs, doctor, and evidence review.

`stackkit_apply` and `stackkit_rollout` skip platform app lifecycle by default. This surface does not run `stackkit app add`, customer app rollout, managed-serverless provisioning, SaaS placement orchestration, or internal Kombify operator MCPs.

Recommended single local connection:

```toml
[mcp_servers.stackkit]
command = "stackkit-mcp"
args = ["--mode", "docs,local,server"]
```

Native local connector:

```text
POST http://localhost:8082/mcp
GET  http://localhost:8082/openmcp.json
```

Protected remote connector target:

```text
POST https://<protected-target>/mcp
GET  https://<protected-target>/openmcp.json
```
