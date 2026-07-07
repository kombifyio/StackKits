# StackKits API

> Last verified: 2026-06-13

This document summarizes the StackKits HTTP API for operators, TechStack integrations, and AI agents. The contract source is [api/openapi/stackkits-v1.yaml](../api/openapi/stackkits-v1.yaml); the server implementation lives in [cmd/stackkit-server](../cmd/stackkit-server) and [internal/api](../internal/api).

Implementation note: `internal/api/server.go` registers health, capabilities, catalog, validation, generation, node-local management, log, node-local setup, internal runtime-action, and Direct Connect registry routes.

## Surfaces

| Surface | Base URL | Purpose |
| --- | --- | --- |
| Local development | `http://localhost:8082` | Local `stackkit-server` process. |
| Production edge | `https://api.kombify.io/stackkits` | Cloudflare edge route used by kombify consumers. |

## Authentication

`stackkit-server` requires `X-API-Key` for all non-public endpoints when `STACKKITS_API_KEY` or `--api-key` is configured. Local development can opt out with `--allow-unauthenticated` or `STACKKITS_ALLOW_UNAUTHENTICATED=true`.

Public endpoints:

- `GET /health`
- `GET /api/v1/health`
- `GET /api/v1/openapi.yaml`
- `OPTIONS` preflight requests

Protected endpoints return structured JSON errors for missing or invalid keys. CORS can be enabled with explicit origins; wildcard CORS is local-development-only.

Set `STACKKITS_RUNTIME_PROFILE=production`, `public`, `managed`, or
`enterprise` for non-local deployments. In those profiles the server refuses
to start with unauthenticated API access or wildcard CORS, even if the local
development flags are present.

Internal runtime-action endpoints under `/api/v1/internal/runtime-actions/*` are not browser/API-key endpoints. They require `X-Kombify-Service-Auth` with caller `techstack` and audience `stackkits`; configure `SERVICE_AUTH_SECRET` and optional `SERVICE_AUTH_SECRET_NEXT` on the server. The wire contract is `github.com/kombify/kombify-go-common/runtimeaction`; local OpenAPI schemas document the service surface but are not a separate source of truth. The endpoints default to `STACKKITS_RUNTIME_ACTION_MODE=dry-run`; `apply` mode runs local OpenTofu commands against the supplied `tofu_dir` and can run a configured restore verifier via `STACKKITS_RESTORE_DRILL_COMMAND`.

For `ril-ops` public beta, these internal runtime actions are the only StackKit
execution lane the harness may reach, and only through TechStack plus Gateway
approval. Agents and Workbench never receive raw SSH, Docker socket, or
OpenTofu apply authority. See
[RIL_ACTION_EXECUTION.md](RIL_ACTION_EXECUTION.md).

## Response Model

JSON endpoints use the shared API envelope:

- `success`: boolean result marker.
- `data`: response payload for successful requests.
- `error`: structured error details for failed requests.
- `meta`: request metadata, including `request_id` when available.

Clients may pass `X-Request-ID`; otherwise the server generates one and returns it on the response.

## Endpoints

| Method | Path | Purpose | Auth |
| --- | --- | --- | --- |
| `GET` | `/health` | Root health check. | No |
| `GET` | `/api/v1/health` | Versioned health check. | No |
| `GET` | `/api/v1/openapi.yaml` | OpenAPI 3.1 YAML contract. | No |
| `GET` | `/api/v1/capabilities` | Machine-readable API capability discovery. | Yes |
| `GET` | `/api/v1/stackkits` | List available StackKits. | Yes |
| `GET` | `/api/v1/stackkits/{name}` | Read one StackKit definition. | Yes |
| `GET` | `/api/v1/stackkits/{name}/schema` | Read the raw CUE schema for a StackKit. | Yes |
| `GET` | `/api/v1/stackkits/{name}/defaults` | Read default `stack-spec` values. | Yes |
| `POST` | `/api/v1/validate` | Validate a full StackSpec against CUE. | Yes |
| `POST` | `/api/v1/validate/partial` | Validate partial wizard fields. | Yes |
| `POST` | `/api/v1/generate/tfvars` | Generate `terraform.tfvars` JSON from a validated spec. | Yes |
| `POST` | `/api/v1/generate/preview` | Preview generated infrastructure without writing files. | Yes |
| `GET` | `/api/v1/status` | Read node-local StackKit rollout status. | Yes |
| `POST` | `/api/v1/verify` | Run node-local read-only verification. | Yes |
| `POST` | `/api/v1/doctor` | Run read-only node-local diagnostics. | Yes |
| `POST` | `/api/v1/plan` | Preview local management readiness without mutation. | Yes |
| `GET` | `/api/v1/runs/{runID}/evidence` | Read rollout evidence by run ID. | Yes |
| `GET` | `/api/v1/logs` | List deploy log runs with pagination. | Yes |
| `GET` | `/api/v1/logs/latest` | Read the newest deploy log. | Yes |
| `GET` | `/api/v1/logs/{runID}` | Read a deploy log by run ID. | Yes |
| `GET` | `/api/v1/logs/{runID}/stream` | Stream deploy log events via SSE. | Yes |
| `GET` | `/api/v1/setup/base-hub/protection` | Read Base Hub protection state. | Yes |
| `POST` | `/api/v1/setup/base-hub/protection` | Protect Base Hub and the node-local API with TinyAuth. | Yes |
| `GET` | `/api/v1/setup/initial-access` | Read whether one-time technical bootstrap credential reveal is available. | Yes |
| `POST` | `/api/v1/setup/initial-access/reveal` | Reveal technical bootstrap credentials once after Base Hub is protected. | Yes |
| `POST` | `/api/v1/setup/services/{service}/run` | Validate or execute a node-local first-run setup drop for a service. | Yes |
| `POST` | `/api/v1/internal/runtime-actions/stackkit-rollout` | Run/dry-run StackKits rollout for TechStack. | Servicecall |
| `POST` | `/api/v1/internal/runtime-actions/stackkit-verify` | Verify StackKits rollout state for TechStack. | Servicecall |
| `POST` | `/api/v1/internal/runtime-actions/restore-drill` | Run/dry-run restore-drill handoff for TechStack. | Servicecall |
| `POST` | `/api/v1/registry/instances` | Register a `stackkit-server` instance for Direct Connect. | Yes |
| `DELETE` | `/api/v1/registry/instances/{instanceId}` | Deregister an instance. | Yes |
| `PUT` | `/api/v1/registry/instances/{instanceId}/heartbeat` | Send an instance heartbeat. | Yes |

## Management

The management endpoints are for node-local agents, dashboards, and the single user-facing `stackkit` MCP connection through the local `stackkit-mcp` adapter or `stackkit-server /mcp`. They are read-only in v1:

- `GET /api/v1/status` loads local `stack-spec.yaml`, `.stackkit/state.yaml`, log metadata, and mutation policy.
- `POST /api/v1/verify` runs the same verifier shape as `stackkit verify`; pass `{"http":true,"strict":true}` to include URL probes and promote warnings to failures.
- `POST /api/v1/doctor` returns diagnostic checks for spec, state, generated files, logs, and kit release stance.
- `POST /api/v1/plan` reports dry-run readiness and next CLI commands; it does not run OpenTofu and does not write files.
- `GET /api/v1/runs/{runID}/evidence` reads `.stackkit/runs/<runID>/metadata.json`, `events.jsonl`, and `summary.json`.

Mutating management endpoints such as `apply` or destructive operations are intentionally absent by default. Use CLI commands with explicit operator approval for those workflows.

## Logs

Deploy logs are read from `STACKKITS_LOG_DIR` or from `<base-dir>/.stackkit/logs` when no explicit log directory is set. The log API can list runs, read a specific run, filter by event level or prefix, and stream a run as server-sent events.

## Setup Actions

The Node Hub exposes `Protect Base Hub` at `POST /api/v1/setup/base-hub/protection`.
This is the supported first-run path after owner setup: it persists the Base Hub
protection flag and updates the local Traefik dynamic middleware so Base Hub and
the node-local API move behind TinyAuth without asking the user to edit
variables or run OpenTofu manually.

After Base Hub is protected, the Node Hub can call
`POST /api/v1/setup/initial-access/reveal` to show the generated technical
bootstrap credentials once. These credentials are for TinyAuth/PaaS/service
setup only; the Owner login remains the PocketID passkey account. The status and
reveal payload expose `credentialRole`, `ownerLogin`, and `credentialBoundary`
so Admin, Hub, and recovery surfaces do not mix technical admin material with
the Owner. The endpoint refuses to reveal while Base is bootstrap-open, writes
`.stackkit/initial-access.revealed.json` on first reveal, and stores only the
role/boundary metadata plus selected PaaS in that marker, never the plaintext
password.

The Node Hub posts setup/retry actions to `POST /api/v1/setup/services/{service}/run`. The server resolves the service through the StackKits service catalog, loads the generated `.platform-apps-manifest.json`, and executes matching L3 drops whose manifest policy is `automatic` or `on_demand`.

`STACKKITS_SETUP_ACTION_MODE=dry-run` validates the manifest and returns the planned drop. `STACKKITS_SETUP_ACTION_MODE=apply` runs implemented node-local drops, persists each `SetupRun` in `.stackkit/state.yaml`, and treats completed drops as idempotent on re-run. Each persisted run records a stable `runId`, current phase, attempts, timestamps, phase logs, machine-readable `evidence`, a stable `failureClass` for failed runs, and manifest-provided `rollbackNotes` so the Node Hub can show retry-safe diagnostics. Basement Kit currently implements `cloudreve-owner-bootstrap`, `immich-owner-bootstrap`, and `vaultwarden-admin-handoff`; rollout-owned drops such as Kuma bootstrap are also persisted as setup-run evidence during apply. Immich uses `STACKKIT_ADMIN_EMAIL`, `STACKKIT_ADMIN_PASSWORD`, and `STACKKIT_SETUP_IMMICH_URL` to create the technical bootstrap account, configure PocketID OAuth, and prepare the app-local Owner account/session handoff. Cloudreve resolves the activated PocketID Owner, creates or logs into the matching app-local Files account, prepares the StackKit session bridge, and seeds demo content only when enabled. Vaultwarden verifies the generated admin endpoint/token, proves `ADMIN_TOKEN_B64`/PHC runtime storage, keeps app-local signups disabled, and uses the Vaultwarden admin invite endpoint to pre-provision the activated PocketID Owner email; the encrypted Vaultwarden account setup remains user-completed and the admin token stays break-glass material.

## Runtime Actions

TechStack calls the internal runtime-action endpoints during managed wizard rollout. Each endpoint accepts the same JSON payload:

```json
{
  "action": "stackkit_rollout",
  "stack_id": "stack-123",
  "stack_name": "Managed Base",
  "stackkit": "basement-kit",
  "tofu_dir": "/shared/stacks/stack-123/tofu",
  "unified_path": "/shared/stacks/stack-123/unified-spec.yaml",
  "runtime_target": {
    "host": "main.stack.example",
    "user": "root",
    "port": 22
  },
  "platform_nodes": [
    {
      "name": "worker-1",
      "role": "worker",
      "services": ["vaultwarden"],
      "platform": {
        "server_id": "real-platform-server-id"
      }
    }
  ],
  "owner_spec_bootstrap": {
    "endpoint": "/api/v1/stacks/stack-123/owner-spec",
    "token": "short-lived-token",
    "expires_at": "2026-05-14T10:15:00Z",
    "scopes": ["read:owner-spec"]
  }
}
```

Supported `action` values are `stackkit_rollout`, `verify_rollout`, and `restore_drill`. `runtime_target` is the primary/foundation node for rollout execution. `platform_nodes[]` carries supplemental worker/storage nodes for capacity or service placement; Coolify/Dokploy require real platform placement identifiers, while Komodo requires either a real `server_id` or a Periphery onboarding bootstrap with Core address, onboarding key, and SSH target. Dry-run mode validates and echoes the handoff contract; apply mode runs OpenTofu `init`/`apply` for rollout, prepares supplemental platform nodes before app deployment, `state list` for verification, and `STACKKITS_RESTORE_DRILL_COMMAND` for restore proof when configured. Without that command, restore-drill remains an explicit `skipped` result.

Apply-mode rollout responses include platform app evidence when the generated handoff manifest is present:

- `platform_refs`: raw selected-PaaS deployment refs from the adapter.
- `platform_system_apps`: artifact-ready state for StackKit-owned system apps such as `stackkit-hub` and `stackkit-server`.
- `platform_apps`: artifact-ready state for StackKit-owned L3 apps such as `vaultwarden`, `immich`, and `cloudreve`.

The `platform_system_apps` and `platform_apps` arrays use the same state shape as `stackkit status --json`: `name`, `platform`, `management`, `externalId`, `deploymentId`, `observedStatus`, `observedAt`, `setupPolicy`, `composePath`, and setup-drop metadata. For `on_demand` L3 apps, `observedStatus: "deploy:accepted"` is valid platform evidence until browser/setup evidence proves user-facing readiness.

## Registry

Registry endpoints are for Direct Connect lifecycle state. They are present in the OpenAPI contract, capability map, and `internal/api/server.go` route registration. Instance heartbeat also requires `KOMBIFY_API_KEY` when running the server heartbeat loop with `STACKKITS_INSTANCE_ID` or `--instance-id`.

## Rate Limits

The server defaults to `60` requests per IP per minute. Configure with `--rate-limit` or `STACKKITS_RATE_LIMIT`; set `0` to disable. When behind trusted proxies, configure `--trusted-proxies` or `STACKKITS_TRUSTED_PROXIES` so rate limiting can safely use `X-Forwarded-For`.

## Local Smoke

```bash
stackkit-server --api-key dev-secret --base-dir .

curl -s http://localhost:8082/api/v1/health
curl -s -H "X-API-Key: dev-secret" http://localhost:8082/api/v1/capabilities
curl -s -H "X-API-Key: dev-secret" http://localhost:8082/api/v1/status
curl -s -H "X-API-Key: dev-secret" -X POST http://localhost:8082/api/v1/verify -d '{"http":true}'
curl -s -H "X-API-Key: dev-secret" http://localhost:8082/api/v1/stackkits
```
