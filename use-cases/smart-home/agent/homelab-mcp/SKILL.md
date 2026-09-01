---
name: homelab-mcp
description: Connect agents to Home Assistant native MCP at /api/mcp after StackKits generate. Use the routed smart-home address. Owner username is homelab. Do not put tokens in generate artifacts.
---

# Homelab Home Assistant MCP

StackKits generate writes `.stackkit/agent/home-assistant.mcp.json` with `https://smart-home.<domain>/api/mcp`. The UI is `https://smart-home.<domain>`. Auth is Home Assistant OAuth/IndieAuth or a long-lived token the Homelab owner creates. Generate never writes the token.

The Homelab owner is username `homelab`. Setup creates that user through `/api/onboarding/users`. The Home Assistant MCP Server integration is product-owned config-flow; enable it after the owner exists so `/api/mcp` answers.

## Do

- Point the MCP client at that HTTPS URL. Transport is Streamable HTTP.
- After the Homelab owner exists, complete OAuth in the client or create a long-lived token.
- Reverse-proxy access uses the StackKits route `smart-home.<domain>`. HA trusts `X-Forwarded-*` from the generated `configuration.yaml` and sets `external_url` when the delivery host is known.

## Do not

- Do not invent a second Home Assistant MCP.
- Do not put `HOMEASSISTANT_TOKEN` into `.stackkit/` files.
- Do not treat Coolify as the MCP server. Coolify only deploys the digest-pinned container.
