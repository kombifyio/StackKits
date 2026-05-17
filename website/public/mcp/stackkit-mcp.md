# StackKits MCP Connector

`stackkit-mcp` is a local agent bridge installed beside `stackkit` and `stackkit-server`.

Default stance:

- stdio transport by default;
- HTTP transport binds to `127.0.0.1` by default;
- docs and read-only local/server tools are enabled before action tools;
- write tools require `STACKKIT_MCP_ALLOW_WRITE=true`;
- management tools exposed beyond loopback require `STACKKIT_MCP_TOKEN`.

Modes: `docs`, `local`, `server`, and optional `actions`.

Read-only tools include docs search, API lookup, OpenAPI retrieval, install plans, self-check plans, validation, generate preview, status, verify, logs, doctor, and compatibility checks.

Write tools such as init, generate, apply, add-on add, and remove stay disabled unless explicitly allowed.

