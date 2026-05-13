# StackKits API

> Last verified: 2026-05-09

This document summarizes the StackKits HTTP API for operators, TechStack integrations, and AI agents. The contract source is [api/openapi/stackkits-v1.yaml](../api/openapi/stackkits-v1.yaml); the server implementation lives in [cmd/stackkit-server](../cmd/stackkit-server) and [internal/api](../internal/api).

Implementation note: `internal/api/server.go` currently registers health, capabilities, catalog, validation, generation, log, and internal runtime-action routes. The OpenAPI contract and capability map also list Direct Connect registry routes; their route wiring is tracked in Beads (`kombify-StackKits-ati`).

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

Internal runtime-action endpoints under `/api/v1/internal/runtime-actions/*` are not browser/API-key endpoints. They require `X-Kombify-Service-Auth` with caller `techstack` and audience `stackkits`; configure `SERVICE_AUTH_SECRET` and optional `SERVICE_AUTH_SECRET_NEXT` on the server. The endpoints default to `STACKKITS_RUNTIME_ACTION_MODE=dry-run`; `apply` mode runs local OpenTofu commands against the supplied `tofu_dir` and can run a configured restore verifier via `STACKKITS_RESTORE_DRILL_COMMAND`.

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
| `GET` | `/api/v1/logs` | List deploy log runs with pagination. | Yes |
| `GET` | `/api/v1/logs/latest` | Read the newest deploy log. | Yes |
| `GET` | `/api/v1/logs/{runID}` | Read a deploy log by run ID. | Yes |
| `GET` | `/api/v1/logs/{runID}/stream` | Stream deploy log events via SSE. | Yes |
| `POST` | `/api/v1/internal/runtime-actions/stackkit-rollout` | Run/dry-run StackKits rollout for TechStack. | Servicecall |
| `POST` | `/api/v1/internal/runtime-actions/stackkit-verify` | Verify StackKits rollout state for TechStack. | Servicecall |
| `POST` | `/api/v1/internal/runtime-actions/restore-drill` | Run/dry-run restore-drill handoff for TechStack. | Servicecall |
| `POST` | `/api/v1/registry/instances` | Register a `stackkit-server` instance for Direct Connect. | Yes |
| `DELETE` | `/api/v1/registry/instances/{instanceId}` | Deregister an instance. | Yes |
| `PUT` | `/api/v1/registry/instances/{instanceId}/heartbeat` | Send an instance heartbeat. | Yes |

## Logs

Deploy logs are read from `STACKKITS_LOG_DIR` or from `<base-dir>/.stackkit/logs` when no explicit log directory is set. The log API can list runs, read a specific run, filter by event level or prefix, and stream a run as server-sent events.

## Runtime Actions

TechStack calls the internal runtime-action endpoints during managed wizard rollout. Each endpoint accepts the same JSON payload:

```json
{
  "action": "stackkit_rollout",
  "stack_id": "stack-123",
  "stack_name": "Managed Base",
  "stackkit": "base-kit",
  "tofu_dir": "/shared/stacks/stack-123/tofu",
  "unified_path": "/shared/stacks/stack-123/unified-spec.yaml"
}
```

Supported `action` values are `stackkit_rollout`, `verify_rollout`, and `restore_drill`. Dry-run mode validates and echoes the handoff contract; apply mode runs OpenTofu `init`/`apply` for rollout, `state list` for verification, and `STACKKITS_RESTORE_DRILL_COMMAND` for restore proof when configured. Without that command, restore-drill remains an explicit `skipped` result.

## Registry

Registry endpoints are for Direct Connect lifecycle state. They are present in the OpenAPI contract and capability map; local server route wiring is tracked separately. Instance heartbeat also requires `KOMBIFY_API_KEY` when running the server heartbeat loop with `STACKKITS_INSTANCE_ID` or `--instance-id`.

## Rate Limits

The server defaults to `60` requests per IP per minute. Configure with `--rate-limit` or `STACKKITS_RATE_LIMIT`; set `0` to disable. When behind trusted proxies, configure `--trusted-proxies` or `STACKKITS_TRUSTED_PROXIES` so rate limiting can safely use `X-Forwarded-For`.

## Local Smoke

```bash
stackkit-server --api-key dev-secret --base-dir .

curl -s http://localhost:8082/api/v1/health
curl -s -H "X-API-Key: dev-secret" http://localhost:8082/api/v1/capabilities
curl -s -H "X-API-Key: dev-secret" http://localhost:8082/api/v1/stackkits
```
