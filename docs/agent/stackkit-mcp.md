# StackKits MCP Connector

`stackkit-mcp` is a local agent bridge installed beside `stackkit` and `stackkit-server`.

Default stance:

- stdio transport by default;
- HTTP transport binds to `127.0.0.1` by default;
- docs and read-only local/server tools are enabled before action tools;
- write tools require `STACKKIT_MCP_ALLOW_WRITE=true`;
- management tools exposed beyond loopback require `STACKKIT_MCP_TOKEN`.

Modes:

- `docs`: public docs, prompts, schema, and API lookup tools.
- `local`: local validation and preview planning helpers.
- `server`: calls a node-local `stackkit-server`.
- `actions`: optional write-capable mode, disabled unless explicitly allowed.

Read-only tools:

- `stackkit_docs_search`
- `stackkit_api_overview`
- `stackkit_api_endpoint`
- `stackkit_get_openapi_spec`
- `stackkit_install_plan`
- `stackkit_self_check_plan`
- `stackkit_validate_spec`
- `stackkit_generate_preview`
- `stackkit_status`
- `stackkit_verify`
- `stackkit_logs_list`
- `stackkit_log_get`
- `stackkit_doctor`
- `stackkit_compat_check`

Write tools stay disabled unless `STACKKIT_MCP_ALLOW_WRITE=true`:

- `stackkit_init`
- `stackkit_generate`
- `stackkit_apply`
- `stackkit_addon_add`
- `stackkit_remove`

Example Codex config:

```json
{
  "mcpServers": {
    "stackkit": {
      "command": "stackkit-mcp",
      "args": ["--mode", "docs,local,server"]
    }
  }
}
```

