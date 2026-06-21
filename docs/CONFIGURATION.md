# StackKits Configuration

> Last verified: 2026-06-02

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
mode: bootstrapped
domain: home.localhost
adminEmail: admin@example.com
bootstrap:
  platformPolicy: automatic
  applicationDefaultPolicy: on_demand
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

The BaseKit installer also configures the node-local StackKits API image. If `STACKKIT_SERVER_IMAGE` is set, that image is used directly. Otherwise current release archives install the static `stackkit-server` binary beside the CLI and `base-install.sh` builds a local `stackkit-server:local` scratch image after Docker preparation, copying the host CA bundle into the image. This keeps managed rollouts independent of a registry-hosted system image and avoids package-manager network access during the installer; operators can still point `STACKKIT_SERVER_IMAGE` at a pinned internal image when they want centralized image promotion.

## Platform App Deployment Env

Generated platform app manifests use `stackkit.platform-apps/v2`. The manifest separates StackKit-owned `systemApps` such as the Node Hub and `stackkit-server` from L3 `apps`. Product-bundled L3 apps carry `ownership: "stackkit"` and are PaaS-intended through the selected adapter. Customer-owned apps created with `stackkit app add` carry `ownership: "customer"` or omit ownership; StackKit records those handoffs in status but does not deploy or manage their lifecycle. Apps installed outside these manifests are state-unmanaged by StackKit.

Each platform app may also carry first-run setup metadata:

- `setupPolicy: "manual"` leaves setup in the app UI and records no setup run by default.
- `setupPolicy: "on_demand"` generates a setup drop and exposes it as a Base Hub one-click action.
- `setupPolicy: "automatic"` runs during rollout when the drop is a compose provisioner, or through the node-local setup endpoint with persistent, idempotent `SetupRun` state.

Any other setup policy is rejected during spec validation.

Layer rules are part of the public service contract:

- The Base Node Hub is the bootstrap entrypoint. Local `.localhost` and managed LAN-DNS Base routes are open by default so first setup is reachable before a PocketID user exists. They must show `This page is currently unprotected.` while bootstrap-open; after owner setup, use the `Protect Base Hub` button in the Hub to persist the protection setting and move local Base behind TinyAuth. Public/non-local Base routes remain protected when TinyAuth is enabled. The onboarding panel is hidden on later page loads once the one-time technical bootstrap credentials have been revealed.
- Other L1/L2 platform services must be complete after rollout. The user must not land in a required upstream setup wizard for the identity layer, reverse proxy, selected PaaS, Uptime Kuma, or routing diagnostics.
- Uptime Kuma and Whoami are L2 platform services, not L3 apps. Uptime Kuma is bootstrapped automatically and registers monitors for enabled L1/L2/L3 services. Kuma v2 bootstraps use SQLite explicitly, create the local `admin` app account only for setup, disable app auth behind TinyAuth/PocketID, and upsert monitors by name instead of duplicating them. In the Coolify router path, Kuma checks the router-internal endpoint (`coolify-proxy`) with the public service `Host` header instead of relying on container DNS for `*.home.localhost`.
- StackKit-owned/default L3 application tools are PaaS-intended. In `bootstrapped` mode the default path is `on_demand` for Photos, Files, and Vault: the Node Hub exposes setup actions, but `stackkit apply` does not preconfigure them unless the spec sets the use case or tool policy to `automatic`. User-installed L3 apps outside this manifest path remain unmanaged state.

Files is part of the BaseKit default. `application.files.enabled` controls the use case, and `application.files.tool` selects `cloudreve` or `nextcloud`. Compatible `services.files.*` aliases are accepted only when they do not conflict with the public `application.files.*` values. Generated rollout values set `enable_files`, `files_provider`, and exactly one provider flag (`enable_cloudreve` or `enable_nextcloud`). Cloudreve is the default provider in all BaseKit tiers; Nextcloud is allowed only for `standard` and `high` tiers.

BaseKit records `immich-owner-bootstrap`, `vaultwarden-admin-handoff`, and the Files provider owner bootstrap as setup drops when their effective policy is `on_demand` or `automatic`. `stackkit apply` preserves existing setup-run evidence before rewriting `.stackkit/state.yaml`, records completed automatic `compose-provisioner` drops such as Kuma during platform app rollout, then triggers only `automatic` node-local `stackkit-script` drops through the Base Hub setup API and reloads persisted state. `POST /api/v1/setup/services/{service}/run` remains the one-click/retry path and skips the runner on completed re-runs. Persisted runs include stable `runId`, attempt count, phase logs, machine-readable evidence, stable failure classes, retry timestamps, and rollback notes from the generated manifest. Immich seeds a beta demo image when `demoData.enabled` is true and the library is empty. Cloudreve seeds `StackKit Demo/README.txt` through the native Cloudreve v4 API when `demoData.enabled` is true and updates that file idempotently on retry. Vaultwarden verifies the admin endpoint with generated material, requires PHC+B64 runtime token transport, rejects plaintext `ADMIN_TOKEN` environment persistence, keeps app-local signups disabled, and records the resulting break-glass posture without treating the admin token as the PocketID Owner login.

`stackkit-server` reads the generated platform-app manifest from `<base-dir>/.platform-apps-manifest.json` or `<base-dir>/platform-apps/manifest.json`. In BaseKit deployments it is mounted at `/workspace` so the Dashboard action and the generated manifest share one rollout source of truth.

| Variable | Purpose |
| --- | --- |
| `STACKKITS_SETUP_ACTION_MODE` | `dry-run` validates the setup drop; `apply` executes implemented node-local drops. |
| `STACKKIT_ADMIN_EMAIL` | Technical bootstrap admin email used by supported setup drops. This is not the PocketID Owner login. |
| `STACKKIT_ADMIN_PASSWORD` | Technical bootstrap admin password used by supported setup drops. This is not a PocketID password. |
| `STACKKIT_SETUP_IMMICH_URL` | Internal Immich URL for `immich-owner-bootstrap`; defaults to `http://immich:2283`. |

The StackKits service catalog and registry snapshot also mirror tool UI metadata (`layer`, `logo_url`, `setup_policy`, `setup_action_label`) plus v0.4 bootstrap metadata (`role`, `default_tool`, `alternatives`, `delivery.managedBy`, `bootstrap_provider`). The kombify DB must keep the same fields on the canonical tool/service rows so generated Node Hub cards and product UI use one repeatable metadata source.

The fast Admin-generated-CUE freshness gate lives in `cmd/stackkit/commands/generated_catalog_freshness_test.go` and is covered by `go test ./cmd/stackkit/commands`. It compares sentinel tool rows from the embedded Admin registry snapshot against `base/generated/tool_catalog.cue`, and compares sentinel BaseKit services from the current module contracts against the generated `#ServiceCatalog`. It is intentionally DB-free and must fail when Cloudreve, Nextcloud, Files, Photos, Vault, Uptime Kuma, Whoami, layer labels, logo URLs, setup policies, or setup action metadata drift out of the generated CUE artifact.

## Install Modes and Bootstrap

`mode` selects the installation automation level:

- `bare` deploys infrastructure and selected StackKit tools without Base Hub, `stackkit-server`, SetupRuns, or demo data. Setup policy is forced to `manual`.
- `bootstrapped` is the default. Base Hub, owner/identity, monitoring baseline, and L1/L2 platform setup are automatic. L3 applications default to `on_demand`.
- `advanced` includes the bootstrapped surface plus Terramate/day-2 lifecycle and runtime-intelligence metadata. L3 remains `on_demand` unless the TechStack/kombify-Desk path or the spec sets `automatic`.

`bootstrap` configures setup policy defaults; it is not a second install mode. `bootstrap.platformPolicy` defaults to `automatic` outside `bare`, and `bootstrap.applicationDefaultPolicy` defaults to `on_demand`. More specific policies override in this order: `services.<tool>.setup.policy`, then `application.<useCase>.setup.policy`, then the bootstrap default, then the mode default. Valid policy values are `manual`, `on_demand`, and `automatic`.

`demoData.enabled` defaults to `false`. Setup packs seed first-login sample content only when this is explicitly enabled.

## Owner Bootstrap Contract

`owner.bootstrapMode` is the lane selector for first-user setup:

- `auto` is the TechStack SaaS path. The public/default StackSpec carries `source: cloud` or `source: first-run` plus policy only. `owner.email` and `owner.username` are not required or invented in the public spec because Admin resolves the real Owner from the tenant deployment and sends it as a private identity-bootstrap envelope.
- `custom` is the self-hosted explicit Owner path. It requires `source: local`, `owner.email`, `owner.username`, and an argon2id `recoveryPassphraseHash`.
- `none` is the OSS/BYOS or manual setup path. It must not carry owner identity or recovery fields.

The Owner is the normal daily admin for PocketID, Coolify, StackKit Server, Kuma, and later tool setup. `adminEmail` is a compatibility alias only: when `owner.email` is available, the generated `admin_email` for Coolify/Kuma/bootstrap credentials resolves to the Owner email.

Managed `stackkit apply --tenant-deployment` must receive `.stackkit/identity-bootstrap.json` from Admin when `owner.bootstrapMode=auto`. Without that runtime handoff the CLI fails before deployment instead of silently skipping Owner/PocketID bootstrap. The handoff may contain one-time material for the VM; plaintext recovery passphrases are never valid public StackSpec fields.

`breakGlass` is the separate emergency path. It is enabled by default with `scope: full-emergency-admin` and covers a PocketID admin, TinyAuth static fallback, and server recovery material in the encrypted recovery bundle. Synthetic local defaults use reserved/local domains such as `admin@example.com` and `.invalid`; tests must not invent real `@kombify.io` accounts.

`stackkit apply` resolves platform adapter configuration from environment first, then from `.stackkit/platform.json` in the deployment working directory. Use the generic `STACKKIT_PLATFORM_*` names where possible. Provider-specific names remain supported for compatibility. In the default self-managed BaseKit path, the generated Coolify bootstrap creates a root-scoped Coolify API token inside the installed Coolify instance, enables the Coolify API, resolves the StackKit project/environment/server/destination placement IDs, and writes `.stackkit/platform.json` before StackKit-owned app deployment begins. In the explicit Komodo path, the generated bootstrap logs in with the generated initial admin, creates a Komodo API key/secret through the HTTP API, and writes the same file with `apiKey`/`apiSecret`. `base-install.sh` still persists this file automatically when endpoint/token or endpoint/api-key/api-secret variables are present for external platform targets, and the managed Admin bootstrap path writes the same file from deployment-scoped spec environment before redacting platform credential values from `stack-spec.yaml`.

| Variable | Provider alias | Purpose |
| --- | --- | --- |
| `STACKKIT_PLATFORM_ENDPOINT` | `DOKPLOY_API_URL`, `COOLIFY_API_URL`, `KOMODO_API_URL` | Platform API base URL. |
| `STACKKIT_PLATFORM_TOKEN` | `DOKPLOY_API_KEY`, `COOLIFY_API_TOKEN` | Platform API token. |
| `STACKKIT_PLATFORM_API_KEY` | `KOMODO_API_KEY` | Komodo API key. |
| `STACKKIT_PLATFORM_API_SECRET` | `KOMODO_API_SECRET` | Komodo API secret. |
| `STACKKIT_PLATFORM_ENVIRONMENT_ID` | `DOKPLOY_ENVIRONMENT_ID` | Dokploy environment. |
| `STACKKIT_PLATFORM_SERVER_ID` | `DOKPLOY_SERVER_ID`, `COOLIFY_SERVER_UUID`, `KOMODO_SERVER_ID` | Target server. |
| `STACKKIT_PLATFORM_PROJECT_UUID` | `COOLIFY_PROJECT_UUID` | Coolify project. |
| `STACKKIT_PLATFORM_ENVIRONMENT_NAME` | `COOLIFY_ENVIRONMENT_NAME` | Coolify environment name. |
| `STACKKIT_PLATFORM_ENVIRONMENT_UUID` | `COOLIFY_ENVIRONMENT_UUID` | Coolify environment UUID. |
| `STACKKIT_PLATFORM_DESTINATION_UUID` | `COOLIFY_DESTINATION_UUID` | Coolify destination. |

Persisted platform config shape:

```json
{
  "platform": "coolify",
  "endpoint": "http://127.0.0.1:8000",
  "token": "<platform-api-token>",
  "projectUuid": "<coolify-project-uuid>",
  "environmentId": "production",
  "environmentUuid": "<coolify-environment-uuid>",
  "serverId": "<coolify-server-uuid>",
  "destinationUuid": "<coolify-destination-uuid>"
}
```

Coolify is the default platform. StackKits bootstraps its root user during installation from generated `admin_password_plaintext` and the same technical admin email rendered into `adminEmail` by passing Coolify's official `ROOT_USERNAME`, `ROOT_USER_EMAIL`, and `ROOT_USER_PASSWORD` installer variables, then creates the API token required for StackKit-owned `systemApps` and product-bundled L3 `apps`. Local-only rollouts synthesize `admin@example.com` when no admin email is supplied; local tests must not use Kombify-owned domains for synthetic users. The bootstrap disables Coolify public registration and clears Coolify onboarding before the rollout can pass. Komodo is the beta-supported alternative. Dokploy remains a draft adapter and is not part of the canonical E2E matrix. If strict PaaS mode reaches `stackkit apply` without a complete platform config, the apply fails; it must not fall through to standalone Compose unless `platformFallback.mode: "standalone-compose"` is explicitly enabled. Customer-owned user `apps` remain handoff metadata and do not make `stackkit apply` responsible for app deployment.

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
| n/a | `STACKKITS_RUNTIME_PROFILE` | `local` | Set to `production`, `public`, `managed`, or `enterprise` to reject unauthenticated mode and wildcard CORS at startup. |
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

When `STACKKIT_FRESH_VM_HTTP_PORT`, `STACKKIT_FRESH_VM_HTTPS_PORT`, `STACKKIT_FRESH_VM_SSH_PORT`, and `STACKKIT_FRESH_VM_TRAEFIK_PORT` are unset, the local Fresh-VM harness lets Docker allocate isolated host ports and then discovers the mapped ports for SSH and HTTP probes. Set these variables only when a fixed local port is intentionally needed for manual inspection or a dedicated runner.

Cloud production tests default to the `digitalocean-managed` Sim provider with region `fra1` because that is the currently available live-node runner. The same StackKit readiness contract can be run against another managed provider by setting `STACKKIT_E2E_CLOUD_NODE_ENGINE` and, when needed, `STACKKIT_E2E_CLOUD_NODE_REGION`. Provider profiles must stay below the Node contract: StackKit assertions should verify OS, SSH, Docker, public origin, ports, generated service URLs, and registry state rather than provider-specific implementation details.
