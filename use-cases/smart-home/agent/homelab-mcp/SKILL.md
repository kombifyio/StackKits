---
name: homelab-mcp
description: Set up the Home Assistant owner and connect agents to its native MCP at /api/mcp. Use the routed smart-home address. Keep credentials private and tokens out of generate artifacts.
---

# Homelab Home Assistant MCP

StackKits generate writes `.stackkit/agent/home-assistant.mcp.json` with `https://smart-home.<domain>/api/mcp`. The UI is `https://smart-home.<domain>`. Auth is Home Assistant OAuth/IndieAuth or a long-lived token the Homelab owner creates. Generate never writes the token.

The Home Assistant MCP Server integration is product-owned config-flow; enable it after the owner exists so `/api/mcp` answers.

The generated `.stackkit/agent/home-assistant-owner.json` describes the supported
native setup adapter and private credential-file path. It does not create or
select an account. Your username and display name come from that private file.

## Owner setup

For an applied native Architecture v2 `standalone-compose` workload, create a
private file at `.stackkit/setup/home-assistant-owner.json` with your chosen
credentials:

```json
{
  "username": "homelab",
  "password": "<your unique password>",
  "displayName": "Homelab Owner",
  "language": "en"
}
```

Restrict this file to the workspace owner. Pass only its path through MCP; never
send the password as a tool argument or place credentials in generated files.
`language` is optional and defaults to `en`.

```sh
stackkit setup smart-home --credentials-file .stackkit/setup/home-assistant-owner.json --owner-approve --json
```

The CLI verifies the current Plan, signed Apply, exact container custody and
application version before setup. It creates an owner only while the user
onboarding step is open, then authenticates with the supplied credentials and
checks that the resulting user is both owner and administrator. Temporary login
tokens stay in memory and are revoked before return. Existing installations use
the same login verification; an unavailable onboarding endpoint is not proof of
completion.

Complete the remaining personal settings, location and integrations in Home
Assistant, then run setup again to refresh its signed evidence. Smart Home does
not support `--complete-onboarding`: those choices belong to the owner. A
successful owner login can therefore report `onboardingComplete: false`, and the
State Console will continue to show that setup needs attention. If an operation
was interrupted, retry using its reported `--operation-id` after addressing the
diagnostic. MFA and additional login challenges require manual attention and
cannot be bypassed by this setup action.

The legacy deployment runner retains its `homelab` username convention and uses
the same owner verifier. The native command uses the explicit username from the
private credential file.

## Do

- Point the MCP client at that HTTPS URL. Transport is Streamable HTTP.
- After the Homelab owner exists, complete OAuth in the client or create a long-lived token.
- Reverse-proxy access uses the StackKits route `smart-home.<domain>`. HA trusts `X-Forwarded-*` from the generated `configuration.yaml` and sets `external_url` when the delivery host is known.

## Do not

- Do not invent a second Home Assistant MCP.
- Do not put `HOMEASSISTANT_TOKEN` into `.stackkit/` files.
- Do not treat Coolify as the MCP server. Coolify only deploys the digest-pinned container.
