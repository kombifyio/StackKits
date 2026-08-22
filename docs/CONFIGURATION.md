# StackKits Configuration

> Last verified: 2026-07-27

This document collects the runtime configuration surfaces for StackKits. CUE remains the technical contract source of truth; `stack-spec.yaml`, CLI flags, environment variables, registry snapshots, and server settings are inputs or mirrors, not replacements for CUE contracts.

## Configuration Surfaces

| Surface | Owner | Purpose |
| --- | --- | --- |
| CUE files under `base/`, `basement-kit/`, `cloud-kit/`, and `modules/` | Developers | Schemas, defaults, constraints, and deployment shape. |
| `stack-spec.yaml` | Operators or TechStack | User intent and selected defaults for one deployment. |
| CLI flags | Operators or CI | One-run overrides for init, generate, apply, verify, and registry operations. |
| `stackkit-server` flags/env | Operators or platform | Local API auth, CORS, rate limits, and log directory. |
| Registry snapshot | Release pipeline | Read-only, CUE-derived catalog mirror baked into CLI/runtime; it has no Admin or DB availability dependency. |
| Test env vars | CI/operators | Fresh-VM, kombify.me, Simulate, and proxy test credentials. |

## Stack Spec

The default spec path is `stack-spec.yaml`. `kombination.yaml` is accepted when
the default file is missing. Do not hand-author a minimal default:
`stackkit init --owner-source=local` writes the complete CUE-owned seed. Its
native-v2 Basement shape includes:

```yaml
apiVersion: stackkit/v2alpha1
kind: StackSpec
metadata:
  name: my-homelab
source:
  kind: native-v2
kit:
  slug: basement-kit
install:
  mode: bootstrapped
  runtime: docker
generation:
  strategy: kit-template
  target: compose
storage:
  dataRoot: /absolute/local/path/data
  backupRoot: /absolute/local/path/backups
  stacksRoot: /absolute/local/path/stacks
network:
  mode: private
  domain:
    base: home.localhost
sites:
  - id: home
    kind: home
    failureDomain: home-primary
nodes:
  - id: main
    siteRef: home
    roles: [controller, worker]
    failureDomain: node-main
controlPlane:
  mode: single
  authoritySiteRef: home
  members: [main]
```

`stackkit init --owner-source=local` derives platform-appropriate absolute
storage paths and persists Owner custody separately under `.stackkit/custody/`.
The StackSpec does not contain private keys, Cloud sessions, or an Admin-owned
identity envelope. The canonical schema and examples are documented in
[stack-spec-reference.md](stack-spec-reference.md). Generated OpenTofu,
Compose, tfvars, scripts, and snapshots are outputs and must not be hand-edited.

## Global CLI Flags

| Flag | Default | Notes |
| --- | --- | --- |
| `--verbose`, `-v` | `false` | Enable verbose output. |
| `--quiet`, `-q` | `false` | Suppress non-essential output. |
| `--chdir`, `-C` | `.` | Change working directory before running. |
| `--spec`, `-s` | `stack-spec.yaml` | Spec path; `kombination.yaml` fallback is supported. |
| `--context` | auto | Override node context for compatibility/runtime commands. Native v2 `init` rejects it because the selected kit owns placement. |
| `--no-log` | `false` | Disable structured deploy logging. |

## Init, Generate, Apply, Verify

| Command | Important flags/env | Purpose |
| --- | --- | --- |
| `stackkit init` (native v2: v0.8+) | `--name`, `--domain`, `--owner-source=local`, `--expected-spec-hash`, `--non-interactive` | Materialize the selected product's embedded CUE authoring seed as canonical StackSpec v2. Basement includes concrete absolute local storage roots; `--owner-source=local` establishes standalone owner custody. Create is atomic no-replace; an existing CUE-valid v2 spec changes only when its exact normalized hash is supplied. `--force` is rejected. Cloud Kit and Modern Home Lab require `--domain`. No empty deployment directory is created and no readiness claim is made. |
| Exact-v0.6 `stackkit init` compatibility | `--compute-tier`, `--domain`, `--local-dns`, `--local-name`, `--mode`, `--output`, `--force`, `--non-interactive`, `--admin-email`, `--service-profile` | Historical authoring compatibility only. It cannot restore the current-source v1 generator, which is retired across build identities. |
| `stackkit init` owner bootstrap | `--owner-source=local`, optional exact `--local-site`, `--local-node`, `--local-execution-channel`; legacy v1 flags remain migration-only | Native v2 creates local ownerRef/Ed25519/step-ca custody and desired PocketID projection outside StackSpec. |
| `stackkit secrets materialize` | global `--spec` / `--chdir` | Explicitly create or reuse owner-bound local custody for every `secret://` reference after a canonical workload change. The command validates intent first, emits no locator/material, and leaves deterministic generation plus Apply free of secret-minting side effects. |
| `stackkit init` cloud owner handoff (v0.6 compatibility) | `--cloud-oidc-issuer`, `--cloud-oidc-client-id`, `--cloud-oidc-client-secret-ref`, `--cloud-oidc-foreign-subject` | Optional legacy metadata for orchestrator-managed auto owner bootstrap. |
| `stackkit addon list` | none | On v0.7, list the embedded CUE add-on catalog and, when a canonical v2 spec exists, its validated kit-filtered selection state. This is discovery only. `addon add/remove` remains v0.6 compatibility-only. |
| `stackkit app add` (v0.6 compatibility) | `<name>`, `--image`, `--kind`, `--port`, `--host`, `--auth`, `--health-path`, repeated `--env`, repeated `--secret` | Add legacy PaaS handoff metadata. Native v2 fails closed: catalog apps remain StackKit-owned, and no TechStack customer-workload desired-state contract exists yet. |
| `stackkit generate` | `--output`, `--inventory`, `--resolved-plan`; deprecated rejection-only `--force`, `--fragments` | Resolve StackSpec v2 and atomically persist the current canonical ResolvedPlan, then render only its governed output. StackSpec v1 returns `migration_required` before output or lifecycle state is written. |
| `stackkit apply` | `--auto-approve`, `--verify`, `--verify-http`, `--verify-strict` | Apply only local generated infrastructure and optionally verify results. The public command has no Admin or tenant-deployment path. |
| `stackkit apply` env | local execution, renderer, and optional observability variables only | Public Apply does not consume Admin endpoints/tokens, Kombify credentials, or a managed bootstrap envelope. |
| `stackkit verify` | `--json`, `--http`, `--strict`, `--host`, `--user`, `--key`, `--port`, `--remote-dir`, `--inventory`, `--resolved-plan`, `--artifact-manifest`, `--generation-receipt` | Verify deployment state. Architecture v2 verifies the governed artifact closure before its typed verifier boundary; raw SSH remains an explicit v1-only compatibility transport. |

On the v0.6 compatibility line, `stackkit app add --host` accepts a DNS hostname only. Do not include `http://`, `https://`, paths, or ports; TLS and routing are derived from the legacy StackKit domain/platform contract.

The v0.6 compatibility Basement Kit supports `--service-profile admin-only` for managed first rollouts. Native v2 init rejects this flag because application selection is not part of its initial authoring seed. On v0.6 the profile keeps L1/L2 services and admin access enabled while disabling L3 application modules such as Vaultwarden, Jellyfin, and Immich. The one-line installer exposes the same switch through `STACKKIT_SERVICE_PROFILE=admin-only`.

The public Basement installer installs the released artifacts, initializes local
Owner custody, and validates StackSpec v2. It does not prepare a host, build a
server image, or silently apply workloads. Generation and Apply remain explicit
standalone lifecycle commands.

## Platform App Deployment Env

Generated v0.6 platform app manifests use `stackkit.platform-apps/v2`. The manifest separates StackKit-owned `systemApps` such as the Node Hub and `stackkit-server` from L3 `apps`. Product-bundled L3 apps carry `ownership: "stackkit"` and are PaaS-intended through the selected adapter. Customer-owned apps created with the v0.6-only `stackkit app add` carry `ownership: "customer"` or omit ownership; StackKit records those handoffs but does not manage their lifecycle. Native Architecture v2 does not import arbitrary customer apps into StackSpec.

Each platform app may also carry first-run setup metadata:

- `setupPolicy: "manual"` leaves setup in the app UI and records no setup run by default.
- `setupPolicy: "on_demand"` generates a setup drop and exposes it as a Base Hub one-click action.
- `setupPolicy: "automatic"` runs during rollout when the drop is a compose provisioner, or through the node-local setup endpoint with persistent, idempotent `SetupRun` state.

Any other setup policy is rejected during spec validation.

Layer rules are part of the public service contract:

- The Base Node Hub is the bootstrap entrypoint. Local `.localhost` and managed LAN-DNS Base routes are open by default so first setup is reachable before a PocketID user exists. They must show `This page is currently unprotected.` while bootstrap-open; after owner setup, use the `Protect Base Hub` button in the Hub to persist the protection setting and move local Base behind TinyAuth. Public/non-local Base routes remain protected when TinyAuth is enabled. The onboarding panel is hidden on later page loads once the one-time technical bootstrap credentials have been revealed.
- Other L1/L2 platform services must be complete after rollout. The user must not land in a required upstream setup wizard for the identity layer, reverse proxy, selected PaaS, Uptime Kuma, or routing diagnostics.
- Uptime Kuma and Whoami are L2 platform services, not L3 apps. Uptime Kuma is bootstrapped automatically and registers monitors for enabled L1/L2/L3 services. Kuma v2 bootstraps use SQLite explicitly, create the local `admin` app account only for setup, disable app auth behind TinyAuth/PocketID, and upsert monitors by name instead of duplicating them. In the Coolify router path, Kuma checks the router-internal endpoint (`coolify-proxy`) with the public service `Host` header instead of relying on container DNS for `*.home.localhost`.
- On the exact-v0.6 compatibility line, StackKit-owned L3 application tools are
  PaaS-intended and may expose legacy `on_demand` setup actions. Native v0.8
  does not select, render, or expose Photos, Vault, or Files by default; these
  application slices are v0.9 opt-ins. User-installed applications outside a
  governed StackKit manifest remain unmanaged state.

On the exact-v0.6 compatibility line, `application.files.enabled` controls the
legacy Files use case and `application.files.tool` selects `cloudreve` or
`nextcloud`. That compatibility behavior does not make Files part of the v0.8
Basement core or readiness claim.

The exact-v0.6 compatibility line may record Immich, Vaultwarden, and Files
provider setup drops. Those legacy setup-drop and demo-data behaviors are not
part of the native v0.8 core lifecycle.

`stackkit-server` reads the generated platform-app manifest from `<base-dir>/.platform-apps-manifest.json` or `<base-dir>/platform-apps/manifest.json`. In Basement Kit deployments it is mounted at `/workspace` so the Dashboard action and the generated manifest share one rollout source of truth.

| Variable | Purpose |
| --- | --- |
| `STACKKITS_SETUP_ACTION_MODE` | `dry-run` validates the setup drop; `apply` executes implemented node-local drops. |
| `STACKKIT_ADMIN_EMAIL` | Technical bootstrap admin email used by supported setup drops. This is not the PocketID Owner login. |
| `STACKKIT_ADMIN_PASSWORD` | Technical bootstrap admin password used by supported setup drops. This is not a PocketID password. |
| `STACKKIT_SETUP_IMMICH_URL` | Internal Immich URL for `immich-owner-bootstrap`; defaults to `http://immich:2283`. |

The CUE service and module contracts define tool roles, selection, delivery,
and setup metadata. The release pipeline derives the embedded registry snapshot
and Node Hub projection from those contracts. A private kombify DB may mirror a
published projection for product UI or fleet views, but DB parity is not a
public generation, lifecycle, or release prerequisite.

The bounded catalog freshness check verifies that the embedded, CUE-derived
read model matches the canonical module contracts. It is deliberately DB-free;
no Admin endpoint or private credential participates in public validation.

## Install Modes and Bootstrap

`mode` selects the installation automation level:

- `bare` deploys infrastructure and selected StackKit tools without Base Hub, `stackkit-server`, SetupRuns, or demo data. Setup policy is forced to `manual`.
- `bootstrapped` is the default. Base Hub, owner/identity, monitoring baseline,
  and L1/L2 platform setup are automatic. Native v0.8 leaves L3 application
  slices unselected; exact-v0.6 compatibility may retain `on_demand` defaults.
- `advanced` is the capability-gated Terramate lifecycle mode: bootstrapped
  baseline, StackKit-packaged Terramate orchestration,
  drift/change/rollback/restore-drill surfaces, and Runtime Intelligence Layer
  handoff. It does not select Photos, Vault, Files, or another L3 application.
  Techstack may unify a user-approved v0.9 opt-in, but the CUE contract remains
  final authority. Legacy `terramate` / `advanced-terramate` inputs normalize to
  this same Advanced contract.

`bootstrap` configures setup policy defaults; it is not a second install mode.
`bootstrap.platformPolicy` defaults to `automatic` outside `bare`. On
compatibility inputs, `bootstrap.applicationDefaultPolicy` may default to
`on_demand`; this policy affects only an already-selected application and never
selects one implicitly. More specific policies override in this order:
`services.<tool>.setup.policy`, then `application.<useCase>.setup.policy`, then
the bootstrap default, then the mode default. Valid policy values are `manual`,
`on_demand`, and `automatic`.

`demoData.enabled` defaults to `false`. Setup packs seed first-login sample content only when this is explicitly enabled.

## Owner Bootstrap Contract

Native v2 uses local Owner custody. `stackkit init --owner-source=local`
creates the stable `ownerRef`, Ed25519 evidence key, step-ca certificate, and
desired PocketID projection outside StackSpec. The first Apply realizes the
PocketID Owner plus TinyAuth OIDC client and signs their binding. Public Apply
accepts only this local custody; Techstack or kombify Cloud may propose a
user-approved profile projection, and kombify Cloud may synchronize approved
user fields as a convenience, but neither can replace local authority or sync
passwords, passkeys, private keys, or Cloud sessions.

The fields below describe only the bounded v1 compatibility/migration contract:

`owner.bootstrapMode` is the lane selector for first-user setup:

- `auto` is the TechStack SaaS path. The public/default StackSpec carries `source: cloud` or `source: first-run` plus policy only. `owner.email` and `owner.username` are not required or invented in the public spec because Admin resolves the real Owner from the tenant deployment and sends it as a private identity-bootstrap envelope.
- `custom` is the self-hosted explicit Owner path. It requires `source: local`, `owner.email`, `owner.username`, and an argon2id `recoveryPassphraseHash`.
- `none` is the OSS/BYOS or manual setup path. It must not carry owner identity or recovery fields.

The Owner is the normal daily admin for PocketID, Coolify, StackKit Server, Kuma, and later tool setup. `adminEmail` is a compatibility alias only: when `owner.email` is available, the generated `admin_email` for Coolify/Kuma/bootstrap credentials resolves to the Owner email.

Legacy managed identity-bootstrap envelopes are Publisher-only and are never
part of public StackSpec exports. Plaintext recovery passphrases are never
valid public StackSpec fields. Cloud passwords, passkeys, sessions, and private
keys are never user-sync fields.

`breakGlass` is the separate emergency path. It is enabled by default with `scope: full-emergency-admin` and covers a PocketID admin, TinyAuth static fallback, and server recovery material in the encrypted recovery bundle. Synthetic local defaults use reserved/local domains such as `admin@example.com` and `.invalid`; tests must not invent real `@kombify.io` accounts.

## Historical v0.6 Platform Adapter Contract

The following platform environment, `.stackkit/platform.json`, generated
technical-admin, and standalone-fallback behavior describes immutable v0.6
release artifacts only. Native v2 uses local owner-signed runtime custody and
typed renderer contracts; it does not accept these variables as public Apply
authority or let a Kit own external platform credentials/lifecycle.

Historical `stackkit apply` resolved platform adapter configuration from
environment first, then from `.stackkit/platform.json`.

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

Historical Coolify generation bootstrapped a technical root user and API token;
Komodo was the alternative and Dokploy remained draft. Those generated
`admin_password_plaintext`, placement-ID, and fallback semantics are not native
v2 configuration. Current owner identity is realized through PocketID, TinyAuth,
step-ca, and the signed local Owner binding.

## Dev PaaS App Handoff Env

The variables below describe archived v0.6 development installers only.
Current `base-install.sh` does not call `stackkit app add`; native applications
come from the CUE-owned module catalog.

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

## Public release and Publisher configuration

| Command | Required configuration |
| --- | --- |
| `stackkit kit list` | Optional `--channel stable\|beta\|edge` and `--json`; reads the public GitHub release index. |
| `stackkit verify` | Optional `--offline` and `--json`; verifies cached release trust material plus local lifecycle state. |
| `stackkit upgrade` | Optional `--to latest\|vX.Y.Z\|channel:<name>` and `--dry-run`; reads the public GitHub release index. |
| `stackkit registry info` | Optional `--json`; reads only the embedded CUE-derived registry snapshot. |
| `stackkit-publisher ...` | Private Publisher/Admin configuration. These commands and their credential variables do not compile into the public `stackkit` executable. |

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
| n/a | `SERVICE_AUTH_SECRET` | empty | Shared HS256 secret required for TechStack internal StackAction calls. |
| n/a | `SERVICE_AUTH_SECRET_NEXT` | empty | Optional rotated service-auth secret accepted alongside the current secret. |
| n/a | `STACKKITS_STACK_ACTION_MODE` | `dry-run` | `dry-run` validates the handoff; `apply` executes local OpenTofu rollout/verification commands. |
| n/a | `STACKKITS_RESTORE_DRILL_COMMAND` | empty | Optional restore verifier command for `restore_drill` in `apply` mode; receives `STACKKIT_STACK_ACTION`, `STACKKIT_STACK_ID`, `STACKKIT_STACK_NAME`, `STACKKIT_STACKKIT`, `STACKKIT_TOFU_DIR`, and `STACKKIT_UNIFIED_PATH`. |

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
| Cloud node defaults | `STACKKIT_E2E_SERVER_PROVIDER`, `STACKKIT_E2E_CLOUD_NODE_ENGINE`, `STACKKIT_TECHSTACK_LEASE_PROVIDER`, `STACKKIT_E2E_CLOUD_NODE_IMAGE`, `STACKKIT_E2E_CLOUD_NODE_REGION` |
| SSH/proxy jump | `KOMBIFY_PROXY_JUMP`, `KOMBIFY_PROXY_JUMP_KEY`, `KOMBIFY_PROXY_JUMP_KEY_PEM`, `KOMBIFY_PROXY_JUMP_PASSWORD`, `KOMBIFY_SSH_KEY_PATH`, `KOMBIFY_SSH_PASSWORD` |

Fresh-VM release smoke requires Docker authentication when anonymous Docker Hub pulls are rate-limited. Use either `STACKKIT_FRESH_VM_DOCKER_CONFIG` to point to a Docker config file or `STACKKIT_FRESH_VM_DOCKER_CONFIG_JSON` to pass the JSON content directly.

When `STACKKIT_FRESH_VM_HTTP_PORT`, `STACKKIT_FRESH_VM_HTTPS_PORT`, `STACKKIT_FRESH_VM_SSH_PORT`, and `STACKKIT_FRESH_VM_TRAEFIK_PORT` are unset, the local Fresh-VM harness lets Docker allocate isolated host ports and then discovers the mapped ports for SSH and HTTP probes. Set these variables only when a fixed local port is intentionally needed for manual inspection or a dedicated runner.

Cloud production tests default to the `centron-managed` Sim/Lease provider. Provider selection uses `STACKKIT_E2E_SERVER_PROVIDER`, then `STACKKIT_E2E_CLOUD_NODE_ENGINE`, then `STACKKIT_TECHSTACK_LEASE_PROVIDER`, then `centron-managed`; set `STACKKIT_E2E_CLOUD_NODE_REGION` only when a provider requires an explicit region. Provider profiles must stay below the Node contract: StackKit assertions should verify OS, SSH, Docker, public origin, ports, generated service URLs, cleanup, and registry state rather than provider-specific implementation details.
