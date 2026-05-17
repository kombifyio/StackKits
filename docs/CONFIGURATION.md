# StackKits Configuration

> Last verified: 2026-05-15

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
domain: home.localhost
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
| `stackkit init` | `--compute-tier`, `--domain`, `--local-dns`, `--local-name`, `--mode`, `--output`, `--force`, `--non-interactive`, `--admin-email`, `--service-profile` | Create an initial spec and deployment directory. |
| `stackkit init` owner bootstrap | `--cluster-mode`, `--owner-bootstrap-mode`, `--owner-source`, `--owner-email`, `--owner-username`, `--owner-display-name`, `--recovery-passphrase-hash`, `--recovery-material-ref` | Configure owner and recovery bootstrap. `auto` is for TechStack/SaaS handoff, `custom` is explicit self-hosted Owner, and `none` skips Owner bootstrap. |
| `stackkit init` cloud owner handoff | `--cloud-oidc-issuer`, `--cloud-oidc-client-id`, `--cloud-oidc-client-secret-ref`, `--cloud-oidc-foreign-subject` | Optional metadata for orchestrator-managed auto owner bootstrap. |
| `stackkit app add` | `<name>`, `--image`, `--kind`, `--port`, `--host`, `--auth`, `--health-path`, repeated `--env`, repeated `--secret` | Add or update optional PaaS handoff metadata in `stack-spec.yaml`. StackKit generates manifest/compose handoff files; the selected PaaS owns user app deployment and lifecycle. |
| `stackkit generate` | `--output`, `--force`, `--fragments`, `KOMBIFY_API_KEY`, `STACKKIT_DNS_TOKEN`, `STACKKIT_DNS_EMAIL` | Generate rollout artifacts from CUE and spec. `--fragments` is the experimental per-module OpenTofu path. |
| `stackkit apply` | `--auto-approve`, `--tenant-deployment`, `--admin-endpoint`, `--admin-token`, `--verify`, `--verify-http`, `--verify-strict` | Apply generated infrastructure and optionally report or verify results. |
| `stackkit apply` env | `STACKKIT_ADMIN_ENDPOINT`, `STACKKIT_ADMIN_URL`, `STACKKIT_ADMIN_TOKEN`, `STACKKIT_BOOTSTRAP_TOKEN`, `KOMBIFY_API_KEY` | Admin reporting, tenant bootstrap fetch, and kombify.me registration. |
| `stackkit verify` | `--json`, `--http`, `--strict`, `--host`, `--user`, `--key`, `--port`, `--remote-dir` | Verify local or remote deployment state. |

`stackkit app add --host` accepts a DNS hostname only. Do not include `http://`, `https://`, paths, or ports; TLS and routing are derived from the StackKit domain/platform contract.

BaseKit currently supports `--service-profile admin-only` for managed first rollouts. It keeps L1/L2 services and admin access enabled while disabling L3 application modules such as Vaultwarden, Jellyfin, and Immich. The one-line installer exposes the same switch through `STACKKIT_SERVICE_PROFILE=admin-only`, which lets Admin-managed deployments roll out a stable platform baseline first and leave owner-specific Layer-3 setup for the SaaS surface.

The BaseKit installer also configures the node-local StackKits API image. If `STACKKIT_SERVER_IMAGE` is set, that image is used directly. Otherwise current release archives install the `stackkit-server` binary beside the CLI and `base-install.sh` builds a local `stackkit-server:local` image after Docker preparation. This keeps managed rollouts independent of a registry-hosted system image; operators can still point `STACKKIT_SERVER_IMAGE` at a pinned internal image when they want centralized image promotion.

## Platform App Deployment Env

Generated platform app manifests use `stackkit.platform-apps/v2`. The manifest separates StackKit-owned `systemApps` such as the Node Hub and `stackkit-server` from user-facing `apps`. User-facing `apps` are handoff metadata for the selected PaaS; StackKit records them in status but does not deploy or manage their lifecycle.

Each platform app may also carry first-run setup metadata:

- `setupPolicy: "manual"` leaves setup in the app UI and records no setup run by default.
- `setupPolicy: "on_demand"` is reserved for explicit user-triggered setup drops.
- `setupPolicy: "automatic"` runs during rollout or is already represented by a platform compose provisioner in the generated handoff.

Any other setup policy is rejected during spec validation.

Layer rules are part of the public service contract:

- The Base Node Hub is the bootstrap entrypoint. Local `.localhost` and managed LAN-DNS Base routes are open by default so first setup is reachable before a PocketID user exists. They must show `Diese Seite ist aktuell ungeschützt.` while `protect_base_hub=false`; set `protect_base_hub=true` and re-apply after owner setup to put local Base behind TinyAuth. Public/non-local Base routes remain protected when TinyAuth is enabled.
- Other L1/L2 platform services must be complete after rollout. The user must not land in a required upstream setup wizard for the identity layer, reverse proxy, selected PaaS, Uptime Kuma, or routing diagnostics.
- Uptime Kuma and Whoami are L2 platform services, not L3 apps. Uptime Kuma is bootstrapped automatically and registers monitors for enabled L1/L2/L3 services.
- L3 application tools may keep app-local first-run setup. When StackKits has a supported setup drop, the Node Hub can expose `Do the setup for me`; otherwise it exposes the public How-to guide.

BaseKit records `immich-owner-bootstrap` as an `on_demand` setup drop for Immich. The Node Hub exposes this as a user-triggered setup action instead of pretending that Immich is fully configured at deploy time.

`stackkit-server` reads the generated platform-app manifest from `<base-dir>/.platform-apps-manifest.json` or `<base-dir>/platform-apps/manifest.json`. In BaseKit deployments it is mounted at `/workspace` so the Dashboard action and the generated manifest share one rollout source of truth.

| Variable | Purpose |
| --- | --- |
| `STACKKITS_SETUP_ACTION_MODE` | `dry-run` validates the setup drop; `apply` executes implemented node-local drops. |
| `STACKKIT_ADMIN_EMAIL` | Admin email used by supported owner-bootstrap drops. |
| `STACKKIT_ADMIN_PASSWORD` | Admin password used by supported owner-bootstrap drops. |
| `STACKKIT_SETUP_IMMICH_URL` | Internal Immich URL for `immich-owner-bootstrap`; defaults to `http://immich:2283`. |

The StackKits service catalog and registry snapshot also mirror tool UI metadata (`layer`, `logo_url`, `setup_policy`, and `setup_action_label`). The kombify DB must keep the same fields on the canonical tool/service rows so generated Node Hub cards and product UI use one repeatable metadata source.

## Owner Bootstrap Contract

`owner.bootstrapMode` is the lane selector for first-user setup:

- `auto` is the TechStack SaaS path. The StackSpec carries `source: cloud` plus a recovery material reference or hash. `owner.email` and `owner.username` are not required in the public spec because TechStack resolves the real Owner from the authenticated Cloud profile.
- `custom` is the self-hosted explicit Owner path. It requires `source: local`, `owner.email`, `owner.username`, and an argon2id `recoveryPassphraseHash`.
- `none` is the OSS/BYOS or manual setup path. It must not carry owner identity or recovery fields.

Plaintext recovery passphrases are never valid StackSpec fields.

`stackkit apply` resolves platform adapter configuration from environment first, then from `.stackkit/platform.json` in the deployment working directory. Use the generic `STACKKIT_PLATFORM_*` names where possible. Provider-specific names remain supported for compatibility. `base-install.sh` persists this file automatically when endpoint/token variables are present, and the managed Admin bootstrap path writes the same file from deployment-scoped spec environment before redacting platform token values from `stack-spec.yaml`.

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
  "platform": "coolify",
  "endpoint": "http://127.0.0.1:3000",
  "token": "<platform-api-token>",
  "environmentId": "<optional>",
  "serverId": "<optional>"
}
```

Coolify is the default platform. StackKits bootstraps its root user during installation from generated `admin_email` and `admin_password_plaintext` values by passing Coolify's official `ROOT_USERNAME`, `ROOT_USER_EMAIL`, and `ROOT_USER_PASSWORD` installer variables. Dokploy remains an explicit alternative. Platform API tokens are required only when StackKit-owned `systemApps` need to be registered through the PaaS adapter. User `apps` remain handoff metadata and do not make `stackkit apply` responsible for app deployment.

## Dev PaaS App Handoff Env

`base-install.sh` can add one dev-only PaaS handoff app before `generate` by calling `stackkit app add`. This is for local validation of handoff manifests and must not be treated as StackKit-managed app deployment. Set `STACKKIT_ENABLE_DEV_APP_HANDOFF=true` and `STACKKIT_DEV_APP_IMAGE` to enable this path.

| Variable | Default | Purpose |
| --- | --- | --- |
| `STACKKIT_ENABLE_DEV_APP_HANDOFF` | `false` | Enables the dev-only handoff helper. |
| `STACKKIT_DEV_APP_IMAGE` | empty | Immutable container image for the handoff app. |
| `STACKKIT_APP_NAME` | `web` | App key under `apps:` and route service name. |
| `STACKKIT_APP_KIND` | `sveltekit` | App kind. Only `sveltekit` is currently accepted. |
| `STACKKIT_APP_PORT` | `3000` | Internal app port. |
| `STACKKIT_APP_AUTH` | `login-gateway` | Route auth mode: `login-gateway` or `public`. |
| `STACKKIT_APP_HOST` | generated | Optional explicit host. kombify.me defaults to `<prefix>-<app>.kombify.me`; custom domains default to `<app>.<domain>`. |
| `STACKKIT_APP_HEALTH_PATH` | `/health` | Health endpoint path. |
| `STACKKIT_APP_ENV` | empty | Comma-separated `KEY=value` app environment entries. |
| `STACKKIT_APP_SECRETS` | empty | Comma-separated `KEY=env:NAME|doppler:NAME|vault:NAME|file:PATH` secret references. |

If this dev helper is enabled, `stackkit apply` writes the handoff into `.stackkit/state.yaml` and generated manifest files. The external PaaS remains responsible for registering, deploying, and operating the user app.

## Registry and Admin API

| Command | Required configuration |
| --- | --- |
| `stackkit registry snapshot` | `--endpoint`; `--token` or `STACKKIT_ADMIN_TOKEN`; optional `--output`. |
| `stackkit registry bake-from-cue` | `--modules-dir`; optional `--output`. |
| `stackkit registry info` | Optional `--json`. |
| `stackkit module release` / `stackkit module verify-db` | `--endpoint` or `STACKKIT_ADMIN_ENDPOINT`; preferred `SERVICE_AUTH_SECRET`; legacy `--token`, `STACKKIT_ADMIN_TOKEN`, or `KOMBIFY_ADMIN_API_KEY`. |
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
| `STACKKIT_LOCAL_DOMAIN` | Overrides the default local domain when no explicit `--domain` or `domain:` is set. Leave unset for the release default `home.localhost`. Generated default links must remain browser-native, portless, and free of hosts-file or DNS setup requirements. |

## Production Test Env Vars

| Area | Variables |
| --- | --- |
| Fresh Ubuntu VM | `STACKKIT_FRESH_VM_IMAGE`, `STACKKIT_FRESH_VM_KEEP`, `STACKKIT_FRESH_VM_SSH_PORT`, `STACKKIT_FRESH_VM_HTTP_PORT`, `STACKKIT_FRESH_VM_HTTPS_PORT`, `STACKKIT_FRESH_VM_TRAEFIK_PORT`, `STACKKIT_FRESH_VM_OUTPUT` |
| Scenario artifacts | `STACKKIT_SCENARIO_OUTPUT` |
| Docker auth injection | `STACKKIT_FRESH_VM_DOCKER_CONFIG`, `STACKKIT_FRESH_VM_DOCKER_CONFIG_JSON` |
| Local bundle/installer | `STACKKIT_CURRENT_BUNDLE_PATH`, `STACKKIT_BASE_INSTALL_PATH`, `STACKKIT_BASE_INSTALL_URL`, `STACKKIT_INSTALL_URL`, `STACKKIT_SERVER_IMAGE`, `STACKKIT_SERVER_LOCAL_IMAGE` |
| kombify API | `KOMBIFY_API_URL`, `KOMBIFY_API_KEY`, `KOMBIFY_JWT_TOKEN` |
| kombify Simulate | `KOMBIFY_SIM_BASE_URL`, `KOMBIFY_SIM_CLIENT_ID`, `KOMBISIM_AUTH_CLOUD_CLIENT_ID`, `KOMBIFY_SIM_REDIRECT_URL`, `KOMBISIM_AUTH_CLOUD_REDIRECT_URL` |
| Cloudflare DNS tests | `STACKKIT_DNS_TOKEN`, `STACKKIT_DNS_ZONE_ID`, `STACKKIT_DNS_ZONE`, `CLOUDFLARE_API_TOKEN`, `CLOUDFLARE_ZONE_ID`, `CLOUDFLARE_EMAIL` |
| Cloud node defaults | `STACKKIT_E2E_CLOUD_NODE_ENGINE`, `STACKKIT_E2E_CLOUD_NODE_IMAGE`, `STACKKIT_E2E_CLOUD_NODE_REGION` |
| SSH/proxy jump | `KOMBIFY_PROXY_JUMP`, `KOMBIFY_PROXY_JUMP_KEY`, `KOMBIFY_PROXY_JUMP_KEY_PEM`, `KOMBIFY_PROXY_JUMP_PASSWORD`, `KOMBIFY_SSH_KEY_PATH`, `KOMBIFY_SSH_PASSWORD` |

Fresh-VM release smoke requires Docker authentication when anonymous Docker Hub pulls are rate-limited. Use either `STACKKIT_FRESH_VM_DOCKER_CONFIG` to point to a Docker config file or `STACKKIT_FRESH_VM_DOCKER_CONFIG_JSON` to pass the JSON content directly.

Cloud production tests default to the `centron-managed` Sim provider because it is the standard subscriber runtime. The same StackKit readiness contract can be run against another managed provider by setting `STACKKIT_E2E_CLOUD_NODE_ENGINE` and, when needed, `STACKKIT_E2E_CLOUD_NODE_REGION`; for example `digitalocean-managed` uses `fra1`. Provider profiles must stay below the Node contract: StackKit assertions should verify OS, SSH, Docker, public origin, ports, generated service URLs, and registry state rather than provider-specific implementation details.
