---
name: homelab-mcp
description: Set up the Home Assistant owner and connect agents to its native MCP at /api/mcp. Use the routed smart-home address. Keep credentials private and tokens out of generate artifacts.
---

# Homelab Home Assistant MCP

## Installation and configuration ownership

Use the recorded installation method, instance origin and management scope.
Home Assistant's name alone does not establish Container, HAOS, Supervisor or
permission to change the installation. Existing instances start observed.
Connecting or disconnecting them must not create an owner, apply defaults,
replace configuration, or remove the original installation.

New HAOS instances use the versioned baseline through their admitted runtime
owner. Apply it only to a fresh instance. Imported backups and existing
instances retain accounts, templates, automations, integrations and personal
settings. Repeated baseline reconciliation preserves subsequent user changes.
HAOS and Core versions are separate facts; do not upgrade an existing system
merely because a catalog pin differs. Container instructions below remain
specific to the native Container installation.

The Companion helps explore use cases, brainstorm possibilities and perform
authorized actions through the actual connected capabilities. Inspect the
available tools and APIs before promising an action. A loaded MCP integration
does not establish authenticated MCP access, automation editing, Supervisor
access, backup capability or entity exposure. Report missing capabilities
explicitly. Do not create an additional guided use-case onboarding flow.

Home Assistant owns native authentication and product setup. Enable MCP only
after authorization and preserve entity exposure choices. MQTT, Zigbee2MQTT
and radio forwarding are optional and require an explicit need; they are not
part of the automatic baseline.

## Recovery

Keep an encrypted backup outside the instance and its recovery key in separate
secure custody. Verify restoration before treating recovery as available.
Before updates, create and verify a backup and check the intended HAOS/Core
version independently. Native backups may omit external databases, MQTT
services or radio state; account for those dependencies explicitly.

Migration restores into an isolated target and preserves the source and its
initial backup. Stop the source before transferring devices and activating the
target; two instances must never control the same installation concurrently.
Before returning to the source, preserve the new target state, stop the target
and reactivate the original. Do not merge configurations or delete migration
backups automatically. Provider operations belong to the authorized runtime
owner, never to an inferred Home Assistant MCP tool.

For the native Container installation, StackKits generate writes `.stackkit/agent/home-assistant.mcp.json` with `https://smart-home.<domain>/api/mcp`. The UI is `https://smart-home.<domain>`. External instances use the authenticated runtime owner's bound address; do not reuse the Container route for a separate HAOS VM. Auth is Home Assistant OAuth/IndieAuth or a long-lived token the Homelab owner creates. Generate never writes the token.

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
