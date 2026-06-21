# Base Kit

> Single-environment homelab blueprint for Docker-based local or VPS deployments. CUE is the source of truth; OpenTofu output is generated.

## Current Release Default

As of 2026-06-10 the release default is the slice exercised by the fresh Ubuntu VM gate inside Docker Desktop:

| Area | Service | Status |
|------|---------|--------|
| Docker API isolation | Docker socket via target daemon | generated |
| Reverse proxy | Coolify Traefik/proxy | the selected PaaS router owns the traffic path (default `paas: coolify`); a StackKit-owned Traefik runs only for explicit `paas: komodo` |
| Local access | browser-native `.localhost` names | enabled default for `*.home.localhost` |
| PaaS | `coolify` | enabled default for local/kombify.me/custom-domain routing; `komodo` is the beta-supported alternative; `dokploy` remains draft |
| Passkey identity | `pocketid` | mandatory default |
| Login gateway | `tinyauth` | generated with PocketID OIDC provider config |
| Node Hub | `dashboard` | StackKits node-local onboarding, protected technical bootstrap access reveal, service matrix, and public how-to links at `base.<domain>` |
| Homelab start dashboard | `homepage` | Secondary IaC-generated Homepage/gethomepage config at `home.<domain>` |
| Status monitoring | `uptime-kuma` | enabled default |
| Routing smoke | `whoami` | TinyAuth-protected routing test |
| Password vault | `vaultwarden` | enabled default |
| Photos | `immich` | server, ML, Postgres, and Redis-compatible cache enabled |
| Files | `cloudreve` | enabled default document-management provider; `nextcloud` is the configured alternative |

PocketID is no longer optional in the Base Kit default: until another passkey-capable identity provider exists, TinyAuth is generated with a PocketID OIDC provider and PocketID is provisioned as the local IdP. `admin-bootstrap`, Smart Home, and AI remain planned or opt-in until their modules can create a working first user and pass the same smoke path.

The production-readiness path builds a fresh Ubuntu target inside Docker Desktop, installs prerequisites with `stackkit prepare`, generates OpenTofu, and applies it inside that Ubuntu target. OpenTofu is not required on the Windows host for this release gate. Product-bundled L3 applications are PaaS-intended by default in the StackKit contract. A passing SK-S1 Enterprise gate must show Vaultwarden, Immich, and any enabled StackKit-owned/default L3 module as manageable apps in the selected PaaS with external app IDs/status evidence. User-installed apps outside that manifest path are state-unmanaged by StackKit.

- expected healthy platform containers include Coolify/Coolify proxy plus `pocketid`, `tinyauth`, `homepage`, and `homepage-socket-proxy`; StackKit-owned apps must surface through Coolify with external IDs in strict default tests
- expected default L3 apps are recorded in `.platform-apps-manifest.json` with `ownership: "stackkit"` and delivered through the selected PaaS, not started by direct Docker Compose fallback
- disabled services such as `komodo`, `dokploy`, `dockge`, and `jellyfin` must not appear as enabled dashboard actions, how-to rows, or active outputs
- TinyAuth is inspected for the v5 `TINYAUTH_OAUTH_PROVIDERS_POCKETID_*` contract and `TINYAUTH_OAUTH_AUTOREDIRECT=pocketid`
- Traefik probes use `*.home.localhost` over HTTP for the local default. Public/custom domains still use real HTTPS certificates. Kombify Point and Step-CA are explicit LAN-DNS options, not the default local user path.

If Docker Hub rate-limits anonymous image pulls, the VM smoke is externally inconclusive. Seed the Ubuntu target with Docker auth via `STACKKIT_FRESH_VM_DOCKER_CONFIG` or `STACKKIT_FRESH_VM_DOCKER_CONFIG_JSON` and rerun.

## Requirements

| | Minimum | Recommended |
|--|---------|-------------|
| CPU | 2 cores | 4+ cores |
| RAM | 2 GB | 4+ GB |
| Disk | 10 GB | 20+ GB |
| OS | Ubuntu 22.04+ | Ubuntu 24.04 LTS |
| Runtime | Docker 24+ | Docker 29 tested locally |

OpenTofu is invoked by the `stackkit` CLI. Users should not edit generated `.tf` files.

## Quick Start

```bash
stackkit init base-kit
stackkit prepare
stackkit generate
stackkit plan
stackkit apply
```

For local development smoke tests, generating into `build/` is enough:

```bash
stackkit generate --spec base-kit/default-spec.yaml --output build/basekit-local --force
stackkit --chdir build/basekit-local plan
```

## Access

For the default local spec, use the links exactly as generated. They must not require hosts-file edits, manual DNS setup, trust-store setup, or port suffixes:

```text
http://base.home.localhost
http://home.home.localhost
http://id.home.localhost
http://auth.home.localhost
http://kuma.home.localhost
http://whoami.home.localhost
http://vault.home.localhost
http://photos.home.localhost
http://files.home.localhost
```

Open Node Hub first. `http://base.home.localhost` is the local first-setup entrypoint and is intentionally anonymous during bootstrap, because no PocketID owner may exist yet. The Hub must show `This page is currently unprotected.` while bootstrap-open. The dashboard onboarding starts with PocketID Owner/passkey setup, then offers `Protect Base Hub`, then the protected one-time technical bootstrap credential reveal, and finally app-specific setup actions or How-to links. StackKit persists the protection setting and switches the local router so Base and the node-local API move behind TinyAuth without manual variable edits. Public/non-local Base routes remain protected when TinyAuth is enabled. On subsequent page loads, the onboarding panel is hidden after the one-time technical bootstrap credentials have been revealed. Other protected default routes and L3 application routes must reject anonymous access unless the StackSpec/module access policy explicitly configures public unauthenticated exposure.

For named LAN zones, initialize with `stackkit init base-kit --local-dns --local-name family` only when you explicitly want a managed LAN resolver path. Those names are not the default and must not be presented as ready-to-open links unless StackKit owns or verifies the resolver.

TinyAuth receives a generated local break-glass password from the composition engine and is also preconfigured for PocketID OIDC. There is no static `admin/admin123` credential. During local generation the generated values are written to `terraform.tfvars.json`; treat that file as sensitive build output and do not commit it.

Coolify receives a generated policy-compliant root password through its official `ROOT_USERNAME`, `ROOT_USER_EMAIL`, and `ROOT_USER_PASSWORD` installer variables. The root email is the same technical admin email rendered into the StackSpec; local-only rollouts synthesize the reserved `admin@example.com` address when no admin email is supplied. After Coolify is installed, the generated bootstrap disables public registration, clears Coolify onboarding, enables Coolify's API, creates a root-scoped StackKit platform token inside Coolify, resolves the StackKit project/environment/server/destination placement IDs, starts/reconciles the Coolify proxy, and writes `.stackkit/platform.json` for the app-deployment phase. That file includes `bootstrapEvidence` for API access, team management, proxy routing, secrets, backup volume labels plus restore-drill handoff, health checks, and service handoff. The user must never be expected to discover or create a Coolify root account or API token manually after opening the generated links.

Komodo is the beta-supported alternative through explicit `paas: komodo`; Coolify remains the default. The generated rollout installs Komodo Core, Periphery, and MongoDB, creates the initial local admin from generated technical bootstrap credentials, disables further registration, creates a Komodo API key through the HTTP API, and writes `.stackkit/platform.json` with `apiKey`/`apiSecret` plus the same bootstrap-evidence shape. Initial Komodo routing is StackKit-owned Traefik, not a Komodo-owned router; the Core API host port is loopback-bound in bridge mode for node-local bootstrap.

Dokploy is draft. Its generated adapter code may remain available for explicit development diagnostics, but it is not part of the beta-supported alternative set and is not a canonical E2E scenario until promoted.

## Current Gaps

These are deliberate scope boundaries, not hidden defaults:

- PocketID/OIDC is mandatory for passkey-capable login. The TinyAuth OIDC client is provisioned automatically; owner/passkey enrollment is the first dashboard onboarding step. Service admin passwords are not PocketID passwords; the Node Hub can reveal the generated technical bootstrap credentials once after Base is protected.
- Coolify and Komodo have generated admin/API bootstrap and machine-readable platform bootstrap evidence. Backup scheduling is still marked `prepared`, not production-complete, until the v0.4 PaaS hardening work promotes concrete backup operations.
- Uptime Kuma, Whoami, Files, Vaultwarden, and Immich carry automatic v0.4 beta setup drops. Node Hub setup actions remain visible as idempotent retry/fallback controls for Owner-dependent drops such as `immich-owner-bootstrap`.
- Vaultwarden is enabled by default, receives a generated admin token, verifies that token through the admin endpoint, uses PHC+B64 runtime storage, and records a controlled break-glass posture in `SetupRun` evidence. Native app-local Owner account provisioning remains a beta limitation; the default access boundary is TinyAuth/PocketID in front of the app, and the admin token is not the PocketID Owner login.
- Jellyfin/media and Dockge are opt-in/manual until their first-run UX matches the default path.
- The Coolify-managed L3 application layer now has a strict generated bootstrap contract. Direct Docker Compose starts for StackKit-owned/default L3 apps are invalid managed release evidence; product-bundled L3 apps must be manageable selected-PaaS apps with platform external IDs in state. User-installed apps outside StackKit manifests are state-unmanaged.
- `security-baseline`, `admin-bootstrap`, and `login-gateway` are planned to become mandatory defaults; the roadmap for that lives in the accepted ADRs (the former "V6 target" document was folded into them).

## Architecture

```text
LAN / browser
      |
      v
Coolify Traefik/proxy :80        (PaaS router = StackKit router; Golden Rules §3/§5.6)
      |
      +--> Coolify        coolify.home.localhost   (platform management)
      +--> PocketID       id.home.localhost
      +--> TinyAuth       auth.home.localhost
      +--> Node Hub       base.home.localhost
      +--> Homepage       home.home.localhost
      +--> Whoami         whoami.home.localhost
      +--> Uptime Kuma    kuma.home.localhost
      +--> Vaultwarden    vault.home.localhost
      +--> Immich         photos.home.localhost
      +--> Files          files.home.localhost     (Cloudreve default)
      |
      +--> socket-proxy   internal Docker API, never public

With explicit `paas: komodo`, exactly one StackKit-owned Traefik replaces the
Coolify proxy as the router (accepted adapter exception).
```

Security defaults currently covered by generated resources:

- Docker socket access goes through `tecnativa/docker-socket-proxy`.
- Traefik uses Docker discovery through the socket proxy.
- Service routes are label-driven from CUE module contracts.
- Secrets and user credentials are generated, not hard-coded.

Host hardening (`UFW`, `fail2ban`, SSH hardening, unattended upgrades) is planned under `security-baseline` and is not part of the verified Docker-only default yet.

## Development Gates

For code or CUE changes, run:

```bash
go test ./...
cue vet ./base/...
cue vet ./base-kit/...
cue vet -c=false ./modules/...
cue vet ./modern-homelab/...
mise run test:cue-binding
```
