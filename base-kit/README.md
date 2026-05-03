# Base Kit

> Single-environment homelab blueprint for Docker-based local or VPS deployments. CUE is the source of truth; OpenTofu output is generated.

## Current Verified Default

As of 2026-04-23 the Level 0 CLI default is intentionally reduced to the services that pass local Docker/OpenTofu smoke tests:

| Area | Service | Status |
|------|---------|--------|
| Docker API isolation | `socket-proxy` | verified |
| Reverse proxy | `traefik` `v3.6.13` | verified |
| Passkey identity | `pocketid` | mandatory default |
| Login gateway | `tinyauth` | verified with PocketID OIDC provider config |
| Password vault | `vaultwarden` | container and route verified |
| Media | `jellyfin` | container and health route verified |

PocketID is no longer optional in the Base Kit default: until another passkey-capable identity provider exists, TinyAuth is generated with a PocketID OIDC provider and PocketID is provisioned as the local IdP. `admin-bootstrap`, Smart Home, Files, and AI remain planned or opt-in until their modules can create a working first user and pass the same smoke path.

The production-readiness path generates OpenTofu resources and applies them on a fresh Ubuntu target:

- healthy platform containers include `step-ca`, `traefik`, `pocketid`, `tinyauth`, `dokploy`, `dashboard`, `kuma`, and `whoami`
- standard app containers include `vaultwarden`, `jellyfin`, and the generated photos stack when enabled
- TinyAuth is inspected for `PROVIDERS_POCKETID_*` and `OAUTH_AUTO_REDIRECT=pocketid`
- Traefik probes use `*.home.localhost` over HTTPS with Step-CA; Jellyfin is reachable through Traefik but the root UI still needs first-run app setup, so use `/health` for infrastructure smoke.

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
tofu -chdir=build/basekit-local init -backend=false
tofu -chdir=build/basekit-local plan
```

## Access

For the default local spec on the same device, use the browser-reserved `.localhost` suffix:

```text
https://base.home.localhost
https://id.home.localhost
https://auth.home.localhost
https://vault.home.localhost
https://media.home.localhost
```

For LAN-wide clear names, initialize with `stackkit init base-kit --local-dns` for `*.home`, or add `--local-name family` for `*.family.home`. That deploys Kombify Point DNS and requires router/client DNS to point at the StackKits server IP.

TinyAuth receives a generated local break-glass password from the composition engine and is also preconfigured for PocketID OIDC. There is no static `admin/admin123` credential. During local generation the generated values are written to `terraform.tfvars.json`; treat that file as sensitive build output and do not commit it.

## Current Gaps

These are deliberate scope boundaries, not hidden defaults:

- PocketID/OIDC is mandatory for passkey-capable login. The TinyAuth OIDC client is provisioned automatically; full owner/passkey enrollment remains part of the first-run identity flow.
- Immich/photos is opt-in until its Postgres, Redis, and ML dependencies are rendered and smoked as a unit.
- Jellyfin currently passes infrastructure health and Traefik discovery, but app-level first-run user setup is not automated.
- Vaultwarden has generated admin material, but end-user account provisioning is not yet a one-click flow.
- The documented V6 target still requires `security-baseline`, `admin-bootstrap`, and `login-gateway` to become mandatory defaults.

## Architecture

```text
LAN / browser
      |
      v
Traefik :80/:443
      |
      +--> PocketID      id.home.localhost
      +--> TinyAuth      auth.home.localhost
      +--> Vaultwarden   vault.home.localhost
      +--> Jellyfin      media.home.localhost
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
