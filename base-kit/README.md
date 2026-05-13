# Base Kit

> Single-environment homelab blueprint for Docker-based local or VPS deployments. CUE is the source of truth; OpenTofu output is generated.

## Current Release Default

As of 2026-05-09 the release default is the slice exercised by the fresh Ubuntu VM gate inside Docker Desktop:

| Area | Service | Status |
|------|---------|--------|
| Docker API isolation | Docker socket via target daemon | generated |
| Reverse proxy | `traefik` | enabled default |
| Local DNS | `kombify-point` | enabled default for `*.stack.home` |
| PaaS | `dokploy` | enabled default for local/kombify.me routing |
| Passkey identity | `pocketid` | mandatory default |
| Login gateway | `tinyauth` | generated with PocketID OIDC provider config |
| Node Hub | `dashboard` | StackKits node-local Getting Started, important links, service matrix, and public how-to links at `base.<domain>` |
| Homelab start dashboard | `homepage` | Secondary IaC-generated Homepage/gethomepage config at `home.<domain>` |
| Status monitoring | `uptime-kuma` | enabled default |
| Routing smoke | `whoami` | TinyAuth-protected routing test |
| Password vault | `vaultwarden` | enabled default |
| Photos | `immich` | server, ML, Postgres, and Redis-compatible cache enabled |

PocketID is no longer optional in the Base Kit default: until another passkey-capable identity provider exists, TinyAuth is generated with a PocketID OIDC provider and PocketID is provisioned as the local IdP. `admin-bootstrap`, Smart Home, Files, and AI remain planned or opt-in until their modules can create a working first user and pass the same smoke path.

The production-readiness path builds a fresh Ubuntu target inside Docker Desktop, installs prerequisites with `stackkit prepare`, generates OpenTofu, and applies it inside that Ubuntu target. OpenTofu is not required on the Windows host for this release gate.

- expected healthy platform containers are `traefik`, `kombify-point`, `dokploy`, `dokploy-postgres`, `dokploy-redis`, `pocketid`, `tinyauth`, `dashboard`, `homepage`, and `homepage-socket-proxy`
- expected default app containers are `kuma`, `whoami`, `vaultwarden`, `immich`, `immich-ml`, `immich-postgres`, and `immich-redis`
- disabled services such as `coolify`, `dockge`, and `jellyfin` must not appear as enabled dashboard actions, how-to rows, or active outputs
- TinyAuth is inspected for `PROVIDERS_POCKETID_*` and `OAUTH_AUTO_REDIRECT=pocketid`
- Traefik probes use `*.stack.home` over HTTPS through Kombify Point and StackKit-managed Step-CA. Public/custom domains still use real HTTPS certificates.

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

For the default local spec, use Kombify Point LAN names with StackKit-managed Step-CA HTTPS:

```text
https://base.stack.home
https://home.stack.home
https://id.stack.home
https://auth.stack.home
https://kuma.stack.home
https://whoami.stack.home
https://vault.stack.home
https://photos.stack.home
```

Open Node Hub first. It starts with the first-run checklist, then links to Homepage, status, app platforms, and the public service how-to guides for the enabled services on that node.

For named LAN zones, initialize with `stackkit init base-kit --local-dns --local-name family` for `*.family.home`. Device-local legacy mode remains available with `domain: home.localhost`, but it is not the release default.

TinyAuth receives a generated local break-glass password from the composition engine and is also preconfigured for PocketID OIDC. There is no static `admin/admin123` credential. During local generation the generated values are written to `terraform.tfvars.json`; treat that file as sensitive build output and do not commit it.

## Current Gaps

These are deliberate scope boundaries, not hidden defaults:

- PocketID/OIDC is mandatory for passkey-capable login. The TinyAuth OIDC client is provisioned automatically; full owner/passkey enrollment remains part of the first-run identity flow.
- Jellyfin/media and Coolify/Dockge are opt-in until their first-run UX matches the default path. Uptime Kuma and Whoami are enabled in the default path for diagnostics and TinyAuth SSO routing.
- Vaultwarden has generated admin material, but end-user account provisioning is not yet a one-click flow.
- The documented V6 target still requires `security-baseline`, `admin-bootstrap`, and `login-gateway` to become mandatory defaults.

## Architecture

```text
LAN / browser
      |
      v
Traefik :80/:443
      |
      +--> PocketID      id.stack.home
      +--> TinyAuth      auth.stack.home
      +--> Node Hub      base.stack.home
      +--> Homepage      home.stack.home
      +--> Whoami        whoami.stack.home
      +--> Vaultwarden   vault.stack.home
      +--> Immich        photos.stack.home
      |
      +--> socket-proxy  internal Docker API, never public
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
make test-cue-binding
```
