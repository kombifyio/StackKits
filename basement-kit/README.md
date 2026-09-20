# Basement Kit

> Local single-environment homelab (1..N nodes, exactly one `main`) for Docker-based home/LAN deployments. Installer: `https://base.stackkit.cc` (`base` = home base). CUE is the source of truth; OpenTofu output is generated.

> **Taxonomy (ADR-0026):** Basement Kit is the
> **local** product profile derived from the shared `base.#StackBase`
> library. **Cloud Kit** (`cloud-kit`, installer `cloud.stackkit.cc`) is the sibling cloud
> profile over the same core. The retired `base-kit` slug is no longer installable;
> `stackkit init base-kit` is aliased to `basement-kit` with a deprecation warning. A hybrid
> local+cloud deployment is **Modern Homelab**; redundant control planes are selected through
> the Basement-specific `addons/ha` realization. Legacy `context: local|pi` is v1 migration
> input only and does not select Basement Kit identity or canonical Architecture v2 behavior.

## Current Release Default

As of 2026-06-10 the release default is the slice exercised by the fresh Ubuntu VM gate inside Docker Desktop:

| Area | Service | Status |
|------|---------|--------|
| Docker API isolation | Docker socket via target daemon | generated |
| Reverse proxy | Coolify Traefik/proxy | the selected PaaS router owns the traffic path (default `paas: coolify`); a StackKit-owned Traefik runs only for explicit `paas: komodo` |
| Local access | scoped LAN DNS + Owner-CA HTTPS | canonical `*.home` URLs on enrolled devices; no router/DHCP change |
| PaaS | `coolify` | enabled default for local/kombify.me/custom-domain routing; `komodo` is the beta-supported alternative; `dokploy` remains draft |
| Passkey identity | `pocketid` | mandatory default |
| Login gateway | `tinyauth` | generated with PocketID OIDC provider config |
| Node Hub | `dashboard` | StackKits node-local onboarding, protected technical bootstrap access reveal, service matrix, and public how-to links at `base.<domain>` |
| Homelab start dashboard | `homepage` | Secondary IaC-generated Homepage/gethomepage config at `home.<domain>` |
| Status monitoring | `uptime-kuma` | enabled default |
| Routing smoke | `whoami` | TinyAuth-protected routing test |
| Password vault | `vaultwarden` | enabled default |
| Photos | `immich` | server, ML, Postgres, and Redis-compatible cache enabled |
| Files | `cloudreve` | enabled native file-storage and sharing workload; no native alternative is admitted |
| Host security baseline | UFW, fail2ban, unattended-upgrades, SSH/sysctl hardening | applied by `stackkit apply` on Ubuntu and recorded in `.stackkit/security-baseline.json` |

PocketID is no longer optional in the Basement Kit default: until another passkey-capable identity provider exists, TinyAuth is generated with a PocketID OIDC provider and PocketID is provisioned as the local IdP. `admin-bootstrap`, Smart Home, and AI remain planned or opt-in until their modules can create a working first user and pass the same smoke path.

The production-readiness path builds a fresh Ubuntu target inside Docker Desktop, installs prerequisites with `stackkit prepare`, generates OpenTofu, and applies it inside that Ubuntu target. OpenTofu is not required on the Windows host for this release gate. Product-bundled L3 applications are PaaS-intended by default in the StackKit contract. A passing SK-S1 Enterprise gate must show Vaultwarden, Immich, and any enabled StackKit-owned/default L3 module as manageable apps in the selected PaaS with external app IDs/status evidence. User-installed apps outside that manifest path are state-unmanaged by StackKit.

- expected healthy platform containers include Coolify/Coolify proxy plus `pocketid`, `tinyauth`, `homepage`, and `homepage-socket-proxy`; StackKit-owned apps must surface through Coolify with external IDs in strict default tests
- expected default L3 apps are recorded in `.platform-apps-manifest.json` with `ownership: "stackkit"` and delivered through the selected PaaS, not started by direct Docker Compose fallback
- disabled services such as `komodo`, `dokploy`, `dockge`, and `jellyfin` must not appear as enabled dashboard actions, how-to rows, or active outputs
- TinyAuth is inspected for the v5 `TINYAUTH_OAUTH_PROVIDERS_POCKETID_*` contract and `TINYAUTH_OAUTH_AUTOREDIRECT=pocketid`
- Local probes use the canonical `*.home` routes over Owner-CA HTTPS. The StackKits LAN resolver and Step-CA are part of the local default; device enrollment installs only this zone and public root with explicit OS approval. Public/custom domains continue through the selected router and their declared certificate provider.

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
stackkit init basement-kit
stackkit validate
stackkit generate
stackkit plan
stackkit apply
stackkit verify --json
```

For local development smoke tests, use a clean workspace and the native owner
contract:

```bash
mkdir build/basement-local
stackkit --chdir build/basement-local init basement-kit --owner-source=local
stackkit --chdir build/basement-local validate
stackkit --chdir build/basement-local generate
```

## Access

For the default local spec, enroll the device once and then use the links exactly as generated. Enrollment installs scoped DNS and the public Owner-CA root with explicit OS approval; it does not require router, DHCP, hosts-file, or per-link changes:

```text
https://base.home
https://home.home
https://id.home
https://auth.home
https://kuma.home
https://whoami.home
https://vault.home
https://photos.home
https://files.home
```

Open Node Hub first. `https://base.home` is the canonical local first-setup entrypoint. Device enrollment is Home-authority scoped: connect on the LAN, authenticate as a Homelab user, approve the scoped DNS and Owner-CA profile, and register or confirm the device passkey. Root trust does not authorize a service session. The Hub is bootstrap-open only until the PocketID Owner exists, then `Protect Base Hub` moves Base and the node-local API behind TinyAuth. Optional remote enrollment adds a private split tunnel with the same URLs; it does not publish services or install a second namespace.

Custom domains remain explicit configuration. They do not create a second local alias: the selected domain becomes the one canonical service URL set and is routed by the selected Coolify/Traefik or Komodo/Traefik path.

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
- The host `security-baseline` is mandatory for Basement Kit beta release evidence. `admin-bootstrap` and broader login-gateway follow-ups remain tracked in the roadmap.

## Architecture

```text
enrolled LAN device
      |
      v
scoped DNS (*.home -> Home node) + Owner-CA HTTPS
      |
      v
Coolify Traefik/proxy :443       (PaaS router = StackKit router; Golden Rules §3/§5.6)
      |
      +--> Coolify        coolify.home   (platform management)
      +--> PocketID       id.home
      +--> TinyAuth       auth.home
      +--> Node Hub       base.home
      +--> Homepage       home.home
      +--> Whoami         whoami.home
      +--> Uptime Kuma    kuma.home
      +--> Vaultwarden    vault.home
      +--> Immich         photos.home
      +--> Files          files.home     (Cloudreve default)
      |
      +--> socket-proxy   internal Docker API, never public

With explicit `paas: komodo`, exactly one StackKit-owned Traefik replaces the
Coolify proxy as the router (accepted adapter exception).
```

Security defaults currently covered by generated resources:

- `stackkit apply` configures the Ubuntu host baseline: UFW denies incoming traffic except SSH/80/443, fail2ban protects SSH, unattended-upgrades applies security updates, SSH password authentication is disabled, root transport remains key-only for provider leases, and sysctl network/kernel hardening is applied.
- Docker socket access goes through `tecnativa/docker-socket-proxy`.
- Traefik uses Docker discovery through the socket proxy.
- Service routes are label-driven from CUE module contracts.
- Secrets and user credentials are generated, not hard-coded.

## Development Gates

For code or CUE changes, run:

```bash
go test ./...
cue vet -c=false ./foundation/...
cue vet ./basement-kit/...
cue vet -c=false ./modules/...
mise run test:cue-binding
```
