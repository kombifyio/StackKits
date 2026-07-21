# StackKits API

> Last verified: 2026-07-20

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

Internal runtime-action endpoints are not browser/API-key endpoints. They require `X-Kombify-Service-Auth` with caller `techstack` and audience `stackkits`; configure `SERVICE_AUTH_SECRET` and optional `SERVICE_AUTH_SECRET_NEXT` on the server. The versioned wire contract is `github.com/kombify/kombify-go-common/runtimeaction`; local OpenAPI schemas document the service surface but are not a separate source of truth. Governed rollout and verification use the physically separate `/api/v2/internal/runtime-actions/stackkit-rollout` and `/api/v2/internal/runtime-actions/stackkit-verify` routes. Those routes admit only explicit `stackkit.runtime-action/v2alpha1`: a closed provider-free envelope containing StackSpec, Inventory, expected plan hash, stack/tenant/owner identity, and endpoint-bound rollout/verify operation. Raw host transport, SSH material, provider lease identity, caller-selected IaC paths, backup, wipe, restore, upgrade, and simulation have no representation. Valid V2 admission currently returns typed `501 architecture_v2_runtime_action_not_implemented` before dry-run, OpenTofu, SSH, enrollment, or legacy verification.

The three deployment routes below `/api/v1/internal/runtime-actions/` are exact-v0.6 compatibility only. Native v0.7 returns `410 legacy_runtime_action_retired` before decoding or execution. This StackSpec/deployment retirement does not silently re-version the independently shared backup-operation protocol (`backup-run`, `backup-status`, `backup-restore`, `backup-wipe`); those actions are a separate go-common contract and are not evidence that StackSpec v1 remains operational.

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
| `GET` | `/api/v1/stackkits/{name}/defaults` | Read versioned initial StackSpec authoring data. | Yes |
| `POST` | `/api/v1/validate` | Validate v2 against CUE or v1 as read-only migration input. | Yes |
| `POST` | `/api/v1/validate/partial` | Validate versioned initial StackSpec authoring input. | Yes |
| `POST` | `/api/v1/generate/tfvars` | Exact-v0.6 compatibility generator; not advertised on native v0.7. | Yes |
| `POST` | `/api/v1/generate/preview` | Exact-v0.6 compatibility preview; not advertised on native v0.7. | Yes |
| `GET` | `/api/v1/status` | Read node-local StackKit rollout status. | Yes |
| `POST` | `/api/v1/verify` | Run node-local read-only verification. | Yes |
| `POST` | `/api/v1/doctor` | Run read-only node-local diagnostics. | Yes |
| `POST` | `/api/v1/plan` | Preview local management readiness without mutation. | Yes |
| `GET` | `/api/v1/runs/{runID}/evidence` | Read rollout evidence by run ID. | Yes |
| `GET` | `/api/v1/logs` | List deploy log runs with pagination. | Yes |
| `GET` | `/api/v1/logs/latest` | Read the newest deploy log. | Yes |
| `GET` | `/api/v1/logs/{runID}` | Read a deploy log by run ID. | Yes |
| `GET` | `/api/v1/logs/{runID}/stream` | Stream deploy log events via SSE. | Yes |
| `GET` | `/api/v1/setup/base-hub/protection` | Exact-v0.6 Base Hub protection state; native v0.7 returns 501. | Yes |
| `POST` | `/api/v1/setup/base-hub/protection` | Exact-v0.6 TinyAuth artifact mutation; native v0.7 returns 501 before writes. | Yes |
| `GET` | `/api/v1/setup/initial-access` | Exact-v0.6 technical bootstrap state; native v0.7 returns 501. | Yes |
| `POST` | `/api/v1/setup/initial-access/reveal` | Exact-v0.6 credential reveal; native v0.7 returns 501 before state access. | Yes |
| `POST` | `/api/v1/setup/services/{service}/run` | Exact-v0.6 setup-drop executor; native v0.7 returns 501 before external calls or writes. | Yes |
| `POST` | `/api/v1/internal/runtime-actions/stackkit-rollout` | Exact-v0.6 legacy rollout; native v0.7 returns 410. | Servicecall |
| `POST` | `/api/v1/internal/runtime-actions/stackkit-verify` | Exact-v0.6 legacy verification; native v0.7 returns 410. | Servicecall |
| `POST` | `/api/v1/internal/runtime-actions/restore-drill` | Exact-v0.6 legacy restore drill; native v0.7 returns 410. | Servicecall |
| `POST` | `/api/v1/registry/instances` | Exact-v0.6 in-memory compatibility registry; native v0.7 returns 501. | Yes |
| `DELETE` | `/api/v1/registry/instances/{instanceId}` | Exact-v0.6 in-memory deregistration; native v0.7 returns 501. | Yes |
| `PUT` | `/api/v1/registry/instances/{instanceId}/heartbeat` | Exact-v0.6 in-memory heartbeat; native v0.7 returns 501. | Yes |

### Initial StackSpec authoring

These two `/api/v1` paths have a build-versioned compatibility contract. An
exact v0.6 server retains the legacy default StackSpec and arbitrary partial
wizard-field responses. A native v0.7 server does not expose that v1 authoring
loop:

- `GET /api/v1/stackkits/{name}/defaults` returns the embedded CUE
  Definition's authoring contract. It includes a canonical `stack_spec`,
  `spec_hash`, and `validation_scope: spec-only` only when the Definition does
  not require a user override. Otherwise it returns `required_overrides` and
  deliberately omits the placeholder spec.
- `POST /api/v1/validate/partial` accepts
  `apiVersion: stackkit/v2alpha1`, `kind: InitialStackSpecAuthoring`, a
  canonical `kitProfile`, and only the governed `metadata.name` and
  `network.domain.base` override paths. It materializes the Definition-owned
  initial StackSpec and validates the complete result against CUE.

Neither v0.7 response asserts Inventory, ResolvedPlan, generation, or execution
readiness. Clients must use `POST /api/v2/resolve` with explicit Inventory for
those later stages. The machine-readable `oneOf` contracts live in the OpenAPI
document.

Full validation follows the same boundary. Canonical v2 input is checked
directly against the embedded CUE authority and returns only `spec_hash`
evidence. v1 remains accepted on v0.7 solely as `legacy-read-only`: the response
sets `operational: false`, includes the complete `migration_report`, and never
passes through generation admission. Unknown v1 fields make the read-only
validation result invalid rather than being discarded.

The old `/api/v1/generate/tfvars` and `/api/v1/generate/preview` routes remain
callable only to provide exact-v0.6 compatibility or a typed native-line
migration error. Native v0.7 capability discovery omits them. Architecture v2
generation is `StackSpec + Inventory -> ResolvedPlan -> generation
authorization -> executor`; spec-only HTTP input cannot produce tfvars or a
readiness preview.

## Management

The management endpoints are for node-local agents, dashboards, and the single user-facing `stackkit` MCP connection through the local `stackkit-mcp` adapter or `stackkit-server /mcp`.

On native v0.7, `GET /api/v1/status` validates the local StackSpec v2 and reports
`intent_valid`, `specHash`, `validationScope: spec-only`, and
`readiness: resolve-required`. It deliberately does not project the old
deployment state into an operational claim. The legacy Verify, Doctor, and Plan
handlers return typed `501 operational_surface_unavailable` until they consume
an exact verified ResolvedPlan plus execution evidence; native MCP discovery
does not advertise them. Exact v0.6 retains the following compatibility
behavior:

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

## Exact-v0.6 Legacy Runtime Actions

The following payload documents only the retired exact-v0.6 deployment surface. Native v0.7 must not author or execute it; TechStack uses the governed v2 contract instead.

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

Registry endpoints retain exact-v0.6 in-process compatibility state only. They
do not publish to Kombify, Cloudflare, TechStack, or another central registry
and therefore cannot claim Direct Connect registration. Native v0.7 returns
typed 501 before decode or map mutation and omits these operations from
capability discovery. A future registry must use a versioned external contract
with build/Stack/plan identity and observed evidence. Exact-v0.6 instance
heartbeat still requires `KOMBIFY_API_KEY` when the local server loop uses
`STACKKITS_INSTANCE_ID` or `--instance-id`.

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
