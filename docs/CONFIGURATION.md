# StackKits Configuration

> Last verified: 2026-05-13

This document collects the runtime configuration surfaces for StackKits. CUE remains the technical contract source of truth; `stack-spec.yaml`, CLI flags, environment variables, registry snapshots, and server settings are inputs or mirrors, not replacements for CUE contracts.

## Configuration Surfaces

| Surface | Owner | Purpose |
| --- | --- | --- |
| CUE files under `base/`, `base-kit/`, `modules/`, `addons/` | Developers | Schemas, defaults, constraints, and deployment shape. |
| `stack-spec.yaml` | Operators or TechStack | User intent and selected defaults for one deployment. |
| CLI flags | Operators or CI | One-run overrides for init, generate, apply, verify, and registry operations. |
| `stackkit-server` flags/env | Operators or platform | API auth, CORS, rate limits, log directory, and registry heartbeat. |
| Registry snapshot | Release pipeline | Read-only catalog mirror baked into CLI/runtime when Admin API is unavailable. |
| Test env vars | CI/operators | Fresh-VM, kombify.me, Simulate, and proxy test credentials. |

## Stack Spec

The default spec path is `stack-spec.yaml`. `kombination.yaml` is accepted when the default file is missing.

```yaml
stackkit: base-kit
mode: simple
domain: stack.home
adminEmail: admin@example.com
compute:
  tier: standard
context: local
addons: []
nodes:
  - name: main
    role: main
```

The canonical schema and examples are documented in [stack-spec-reference.md](stack-spec-reference.md). Generated OpenTofu, Compose, tfvars, scripts, and snapshots are outputs and must not be hand-edited.

## Global CLI Flags

| Flag | Default | Notes |
| --- | --- | --- |
| `--verbose`, `-v` | `false` | Enable verbose output. |
| `--quiet`, `-q` | `false` | Suppress non-essential output. |
| `--chdir`, `-C` | `.` | Change working directory before running. |
| `--spec`, `-s` | `stack-spec.yaml` | Spec path; `kombination.yaml` fallback is supported. |
| `--context` | auto | Override node context: `local`, `cloud`, or `pi`. |
| `--no-log` | `false` | Disable structured deploy logging. |

## Init, Generate, Apply, Verify

| Command | Important flags/env | Purpose |
| --- | --- | --- |
| `stackkit init` | `--compute-tier`, `--domain`, `--local-dns`, `--local-name`, `--mode`, `--output`, `--force`, `--non-interactive`, `--admin-email` | Create an initial spec and deployment directory. |
| `stackkit init` owner bootstrap | `--cluster-mode`, `--owner-source`, `--owner-email`, `--owner-username`, `--owner-display-name`, `--recovery-passphrase-hash` | Configure local owner and recovery bootstrap. |
| `stackkit init` cloud owner placeholders | `--cloud-oidc-issuer`, `--cloud-oidc-client-id`, `--cloud-oidc-client-secret-ref`, `--cloud-oidc-foreign-subject` | Reserved for cloud-owner phase. |
| `stackkit app add` | `<name>`, `--image`, `--kind`, `--port`, `--host`, `--auth`, `--health-path`, repeated `--env`, repeated `--secret` | Add or update a platform-deployed SvelteKit app in `stack-spec.yaml`. If the spec explicitly uses `paas: none`, the command switches it to Dokploy because apps require a standard PaaS adapter. |
| `stackkit generate` | `--output`, `--force`, `--fragments`, `KOMBIFY_API_KEY`, `STACKKIT_DNS_TOKEN`, `STACKKIT_DNS_EMAIL` | Generate rollout artifacts from CUE and spec. `--fragments` is the experimental per-module OpenTofu path. |
| `stackkit apply` | `--auto-approve`, `--tenant-deployment`, `--admin-endpoint`, `--admin-token`, `--verify`, `--verify-http`, `--verify-strict` | Apply generated infrastructure and optionally report or verify results. |
| `stackkit apply` env | `STACKKIT_ADMIN_ENDPOINT`, `STACKKIT_ADMIN_URL`, `STACKKIT_ADMIN_TOKEN`, `STACKKIT_BOOTSTRAP_TOKEN`, `KOMBIFY_API_KEY` | Admin reporting, tenant bootstrap fetch, and kombify.me registration. |
| `stackkit verify` | `--json`, `--http`, `--strict`, `--host`, `--user`, `--key`, `--port`, `--remote-dir` | Verify local or remote deployment state. |

`stackkit app add --host` accepts a DNS hostname only. Do not include `http://`, `https://`, paths, or ports; TLS and routing are derived from the StackKit domain/platform contract.

## Platform App Deployment Env

Generated platform app manifests use `stackkit.platform-apps/v2`. The manifest separates StackKit-owned `systemApps` such as the Node Hub and `stackkit-server` from user-facing `apps` such as user SvelteKit apps.

Each platform app may also carry first-run setup metadata:

- `setupPolicy: "manual"` leaves setup in the app UI and records no setup run by default.
- `setupPolicy: "on_demand"` is reserved for explicit user-triggered setup drops.
- `setupPolicy: "automatic"` is reserved for rollout-time setup drops once a runner is implemented.

Any other setup policy is rejected during spec validation.

BaseKit records `immich-owner-bootstrap` as a manual setup drop for Immich, but does not add an `init-immich` compose sidecar in the default Dokploy/Coolify path.

`stackkit apply` resolves platform adapter configuration from environment first, then from `.stackkit/platform.json` in the deployment working directory. Use the generic `STACKKIT_PLATFORM_*` names where possible. Provider-specific names remain supported for compatibility.

| Variable | Provider alias | Purpose |
| --- | --- | --- |
| `STACKKIT_PLATFORM_ENDPOINT` | `DOKPLOY_API_URL`, `COOLIFY_API_URL` | Platform API base URL. |
| `STACKKIT_PLATFORM_TOKEN` | `DOKPLOY_API_KEY`, `COOLIFY_API_TOKEN` | Platform API token. |
| `STACKKIT_PLATFORM_ENVIRONMENT_ID` | `DOKPLOY_ENVIRONMENT_ID` | Dokploy environment. |
| `STACKKIT_PLATFORM_SERVER_ID` | `DOKPLOY_SERVER_ID`, `COOLIFY_SERVER_UUID` | Target server. |
| `STACKKIT_PLATFORM_PROJECT_UUID` | `COOLIFY_PROJECT_UUID` | Coolify project. |
| `STACKKIT_PLATFORM_ENVIRONMENT_NAME` | `COOLIFY_ENVIRONMENT_NAME` | Coolify environment name. |
| `STACKKIT_PLATFORM_ENVIRONMENT_UUID` | `COOLIFY_ENVIRONMENT_UUID` | Coolify environment UUID. |
| `STACKKIT_PLATFORM_DESTINATION_UUID` | `COOLIFY_DESTINATION_UUID` | Coolify destination. |

Persisted platform config shape:

```json
{
  "platform": "dokploy",
  "endpoint": "http://127.0.0.1:3000",
  "token": "<platform-api-token>",
  "environmentId": "<optional>",
  "serverId": "<optional>"
}
```

Dokploy and Coolify platform APIs require pre-existing API tokens for compose registration. The persisted file removes the need to pass env vars on every apply, but token bootstrap remains a separate platform setup responsibility. When user `apps` are present, missing endpoint/token configuration is a hard `stackkit apply` error rather than a warning-only skip.

## Base Installer SvelteKit App Env

`base-install.sh` can add one SvelteKit app before `generate` by calling `stackkit app add`. Set `STACKKIT_APP_IMAGE` to enable this path.

| Variable | Default | Purpose |
| --- | --- | --- |
| `STACKKIT_APP_IMAGE` | empty | Immutable container image for the app. Required to enable app deployment. |
| `STACKKIT_APP_NAME` | `web` | App key under `apps:` and route service name. |
| `STACKKIT_APP_KIND` | `sveltekit` | App kind. Only `sveltekit` is currently accepted. |
| `STACKKIT_APP_PORT` | `3000` | Internal app port. |
| `STACKKIT_APP_AUTH` | `login-gateway` | Route auth mode: `login-gateway` or `public`. |
| `STACKKIT_APP_HOST` | generated | Optional explicit host. kombify.me defaults to `<prefix>-<app>.kombify.me`; custom domains default to `<app>.<domain>`. |
| `STACKKIT_APP_HEALTH_PATH` | `/health` | Health endpoint path. |
| `STACKKIT_APP_ENV` | empty | Comma-separated `KEY=value` app environment entries. |
| `STACKKIT_APP_SECRETS` | empty | Comma-separated `KEY=env:NAME|doppler:NAME|vault:NAME|file:PATH` secret references. |

## Registry and Admin API

| Command | Required configuration |
| --- | --- |
| `stackkit registry snapshot` | `--endpoint`; `--token` or `STACKKIT_ADMIN_TOKEN`; optional `--output`. |
| `stackkit registry bake-from-cue` | `--modules-dir`; optional `--output`. |
| `stackkit registry info` | Optional `--json`. |
| `stackkit kit list` | `--endpoint` or `STACKKIT_ADMIN_ENDPOINT` or `ADMIN_PUBLIC_API_URL`; `--token` or `STACKKIT_ADMIN_TOKEN` or `KOMBIFY_ADMIN_API_KEY`. |
| `stackkit kit export` | `--slug`, `--from-api` or `STACKKIT_ADMIN_ENDPOINT`, `--token` or `STACKKIT_ADMIN_TOKEN`, `--output`, `--format`; `--from-yaml` for offline tests. |
| `stackkit kit verify` | `--kit-dir`, optional Admin API endpoint/token, optional `--strict`. |
| `stackkit wizard report` | `--endpoint` or `STACKKIT_ADMIN_ENDPOINT`; `--token` or `STACKKIT_ADMIN_TOKEN`; `--answers` or repeated `--intent`. |

## Server Configuration

| Flag | Environment | Default | Purpose |
| --- | --- | --- | --- |
| `--port` | n/a | `8082` | HTTP listen port. |
| `--base-dir` | `STACKKITS_BASE_DIR` | current directory | StackKit catalog root. |
| `--api-key` | `STACKKITS_API_KEY` | required | `X-API-Key` value for protected endpoints. |
| `--allow-unauthenticated` | `STACKKITS_ALLOW_UNAUTHENTICATED` | `false` | Local-only auth bypass. |
| `--cors-origins` | `STACKKITS_CORS_ORIGINS` | empty | Comma-separated browser origins. |
| `--allow-wildcard-cors` | `STACKKITS_ALLOW_WILDCARD_CORS` | `false` | Local-only wildcard CORS. |
| `--rate-limit` | `STACKKITS_RATE_LIMIT` | `60` | Requests per IP per minute; `0` disables. |
| `--trusted-proxies` | `STACKKITS_TRUSTED_PROXIES` | empty | Trusted proxy IPs/CIDRs for `X-Forwarded-For`. |
| `--log-dir` | `STACKKITS_LOG_DIR` | `<base-dir>/.stackkit/logs` | Deploy log directory. |
| `--log-level` | n/a | `info` | `debug`, `info`, `warn`, or `error`. |
| `--instance-id` | `STACKKITS_INSTANCE_ID` | empty | Registry heartbeat instance ID. |
| n/a | `SERVICE_AUTH_SECRET` | empty | Shared HS256 secret required for TechStack internal runtime-action calls. |
| n/a | `SERVICE_AUTH_SECRET_NEXT` | empty | Optional rotated service-auth secret accepted alongside the current secret. |
| n/a | `STACKKITS_RUNTIME_ACTION_MODE` | `dry-run` | `dry-run` validates the handoff; `apply` executes local OpenTofu rollout/verification commands. |
| n/a | `STACKKITS_RESTORE_DRILL_COMMAND` | empty | Optional restore verifier command for `restore_drill` in `apply` mode; receives `STACKKIT_RUNTIME_ACTION`, `STACKKIT_STACK_ID`, `STACKKIT_STACK_NAME`, `STACKKIT_STACKKIT`, `STACKKIT_TOFU_DIR`, and `STACKKIT_UNIFIED_PATH`. |

Registry heartbeat additionally requires `KOMBIFY_API_KEY`.

## kombify.me and Direct Connect

| Variable | Purpose |
| --- | --- |
| `KOMBIFY_API_KEY` | kombify.me registration and server heartbeat. |
| `KOMBIFY_DEVICE_FINGERPRINT` | Optional explicit device fingerprint override. |
| `STACKKIT_KOMBIFY_ME_API_KEY` | Test fallback for kombify.me registry verification. |
| `KOMBIFY_ME_API_KEY` | Production tests for kombify.me domain behavior. |

## Local Domain Defaults

| Variable | Purpose |
| --- | --- |
| `STACKKIT_LOCAL_DOMAIN` | Overrides the default local domain when no explicit `--domain` or `domain:` is set. Use `home.localhost` for browser-native, no-hosts-file local testing; leave unset for the release default `stack.home`. |

## Production Test Env Vars

| Area | Variables |
| --- | --- |
| Fresh Ubuntu VM | `STACKKIT_FRESH_VM_IMAGE`, `STACKKIT_FRESH_VM_KEEP`, `STACKKIT_FRESH_VM_SSH_PORT`, `STACKKIT_FRESH_VM_HTTP_PORT`, `STACKKIT_FRESH_VM_HTTPS_PORT`, `STACKKIT_FRESH_VM_TRAEFIK_PORT`, `STACKKIT_FRESH_VM_OUTPUT` |
| Scenario artifacts | `STACKKIT_SCENARIO_OUTPUT` |
| Docker auth injection | `STACKKIT_FRESH_VM_DOCKER_CONFIG`, `STACKKIT_FRESH_VM_DOCKER_CONFIG_JSON` |
| Local bundle/installer | `STACKKIT_CURRENT_BUNDLE_PATH`, `STACKKIT_BASE_INSTALL_PATH`, `STACKKIT_BASE_INSTALL_URL`, `STACKKIT_INSTALL_URL` |
| kombify API | `KOMBIFY_API_URL`, `KOMBIFY_API_KEY`, `KOMBIFY_JWT_TOKEN` |
| kombify Simulate | `KOMBIFY_SIM_BASE_URL`, `KOMBIFY_SIM_CLIENT_ID`, `KOMBISIM_AUTH_CLOUD_CLIENT_ID`, `KOMBIFY_SIM_REDIRECT_URL`, `KOMBISIM_AUTH_CLOUD_REDIRECT_URL` |
| Cloudflare DNS tests | `STACKKIT_DNS_TOKEN`, `STACKKIT_DNS_ZONE_ID`, `STACKKIT_DNS_ZONE`, `CLOUDFLARE_API_TOKEN`, `CLOUDFLARE_ZONE_ID`, `CLOUDFLARE_EMAIL` |
| Cloud node defaults | `STACKKIT_E2E_CLOUD_NODE_ENGINE`, `STACKKIT_E2E_CLOUD_NODE_IMAGE`, `STACKKIT_E2E_CLOUD_NODE_REGION` |
| SSH/proxy jump | `KOMBIFY_PROXY_JUMP`, `KOMBIFY_PROXY_JUMP_KEY`, `KOMBIFY_PROXY_JUMP_KEY_PEM`, `KOMBIFY_PROXY_JUMP_PASSWORD`, `KOMBIFY_SSH_KEY_PATH`, `KOMBIFY_SSH_PASSWORD` |

Fresh-VM release smoke requires Docker authentication when anonymous Docker Hub pulls are rate-limited. Use either `STACKKIT_FRESH_VM_DOCKER_CONFIG` to point to a Docker config file or `STACKKIT_FRESH_VM_DOCKER_CONFIG_JSON` to pass the JSON content directly.
