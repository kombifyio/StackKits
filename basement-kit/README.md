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
| Local access | scoped LAN DNS + Owner-CA HTTPS | canonical `*.lab.home` URLs on enrolled devices; no router/DHCP change |
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
| Host security baseline | unattended security updates and kernel parameters on every node; default-drop inbound nftables (LAN, overlay and container bridges admitted), key-only sshd and fail2ban on the home site | applied by `stackkit apply`, recorded in `.stackkit/security-baseline.json`, observed continuously by `stackkit host security verify` |

PocketID is no longer optional in the Basement Kit default: until another passkey-capable identity provider exists, TinyAuth is generated with a PocketID OIDC provider and PocketID is provisioned as the local IdP. `admin-bootstrap`, Smart Home, and AI remain planned or opt-in until their modules can create a working first user and pass the same smoke path.

The production-readiness path builds a fresh Ubuntu target inside Docker Desktop, installs prerequisites with `stackkit prepare`, generates OpenTofu, and applies it inside that Ubuntu target. OpenTofu is not required on the Windows host for this release gate. Product-bundled L3 applications are PaaS-intended by default in the StackKit contract. A passing SK-S1 Enterprise gate must show Vaultwarden, Immich, and any enabled StackKit-owned/default L3 module as manageable apps in the selected PaaS with external app IDs/status evidence. User-installed apps outside that manifest path are state-unmanaged by StackKit.

- expected healthy platform containers include Coolify/Coolify proxy plus `pocketid`, `tinyauth`, `homepage`, and `homepage-socket-proxy`; StackKit-owned apps must surface through Coolify with external IDs in strict default tests
- expected default L3 apps are recorded in `.platform-apps-manifest.json` with `ownership: "stackkit"` and delivered through the selected PaaS, not started by direct Docker Compose fallback
- disabled services such as `komodo`, `dokploy`, `dockge`, and `jellyfin` must not appear as enabled dashboard actions, how-to rows, or active outputs
- TinyAuth is inspected for the v5 `TINYAUTH_OAUTH_PROVIDERS_POCKETID_*` contract and `TINYAUTH_OAUTH_AUTOREDIRECT=pocketid`
- Local probes use the canonical `*.lab.home` routes over Owner-CA HTTPS. The StackKits LAN resolver and Step-CA are part of the local default; device enrollment installs only this zone and public root with explicit OS approval. Public/custom domains continue through the selected router and their declared certificate provider.

If Docker Hub rate-limits anonymous image pulls, the VM smoke is externally inconclusive. Seed the Ubuntu target with Docker auth via `STACKKIT_FRESH_VM_DOCKER_CONFIG` or `STACKKIT_FRESH_VM_DOCKER_CONFIG_JSON` and rerun.

## Requirements

| | Low profile floor | Standard floor | Recommended |
|--|-------------------|----------------|-------------|
| CPU | 2 cores | 2 cores | 4 cores |
| RAM | 2 GB | 4 GB | 4 GB |
| Disk | 10 GB | 20 GB | 20 GB |
| OS | Ubuntu 22.04+ | Ubuntu 22.04+ | Ubuntu 24.04 LTS |
| Runtime | Docker 24+ | Docker 24+ | Docker 29 tested locally |

The standard floor is the kit floor in `stackfile.cue` (`hostRequirements`).
The low profile is the smaller `low` compute graph: the standalone core
without PaaS management, with Photos as Immich Lite (no machine learning). The
`requirements.minimum` block in `stackkit.yaml` describes that low profile. The
figures cover the platform only; photo, file and vault data need their own
disk.

### Hardware

The kit floor above comes from the CUE contract; each selected application adds
its own module-local profile (`low`, `standard` or `high`) with a host floor and
a memory reservation, for example 6 GB of RAM for full Photos (Immich). Host
floors combine by maximum and reservations add up; the total is computed at
admission against the attested host inventory (see the
[profile table](../docs/ARCHITECTURE.md#catalog-owned-module-placement-and-hardware-eligibility)).
These values are declared policy and upstream minima, not measurements.

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
https://base.lab.home
https://home.lab.home
https://id.lab.home
https://auth.lab.home
https://kuma.lab.home
https://whoami.lab.home
https://vault.lab.home
https://photos.lab.home
https://files.lab.home
```

Open Node Hub first. `https://base.lab.home` is the canonical local first-setup entrypoint. Device enrollment is Home-authority scoped: connect on the LAN, authenticate as a Homelab user, approve the scoped DNS and Owner-CA profile, and register or confirm the device passkey. Root trust does not authorize a service session. The Hub is bootstrap-open only until the PocketID Owner exists, then `Protect Base Hub` moves Base and the node-local API behind TinyAuth. Optional remote enrollment adds a private split tunnel with the same URLs; it does not publish services or install a second namespace.

The default domain is `lab.home`. It has two labels on purpose: TinyAuth scopes its login session cookie to the parent of `auth.<domain>`, and a single label such as `home` is a public suffix (the Public Suffix List's default rule), so no browser shares that cookie with `base.home` or the application hosts and every protected link loops back to login. `stackkit init` and `stackkit validate` therefore refuse a domain that is its own public suffix (a single label, or a listed suffix such as `home.arpa`) with guidance to use a domain such as `lab.home`.

### Upgrading an install on the `home` domain

The v0.47.4 → next-release bridge below is awaiting real-host qualification. It preserves the prior release's binaries and catalog during the domain rollout, then uses `stackkit upgrade` for the release change. Do not use the target release's `generate` or `apply` before that upgrade: doing so would deploy its catalog before the sealed release-upgrade checkpoint exists.

Run these commands from the existing installation workspace as its usual operator, with PocketID running and the original `home` StackSpec and signed custody intact. First preserve the installed v0.47.4 CLI, packaged tools, and adjacent provider mirror. Keep this directory until the transition and recovery checks finish; copying these executables is not a backup of the installation or its data.

```sh
set -eu
# Set this to the exact published release that contains migrate-domain.
: "${STACKKIT_MIGRATION_RELEASE:?Set STACKKIT_MIGRATION_RELEASE to the exact target release}"
bridge_root=$(mktemp -d "$HOME/.stackkit-domain-bridge.XXXXXX")
prior_bin=$(dirname "$(readlink -f "$(command -v stackkit)")")
mkdir -p "$bridge_root/prior/bin" "$bridge_root/prior/lib/stackkit" "$bridge_root/target/bin"
for tool in stackkit tofu terramate stackkit-server stackkit-mcp; do
  if [ -f "$prior_bin/$tool" ]; then
    cp -pL "$prior_bin/$tool" "$bridge_root/prior/bin/$tool"
  fi
done
test -x "$bridge_root/prior/bin/stackkit"
test -x "$bridge_root/prior/bin/tofu"
cp -a "$prior_bin/../lib/stackkit/providers" "$bridge_root/prior/lib/stackkit/"
echo "Preserved bridge tools: $bridge_root"
prior="$bridge_root/prior/bin/stackkit"
target="$bridge_root/target/bin/stackkit"
"$prior" version
PATH="$bridge_root/prior/bin:$PATH" "$prior" verify
```

Check that the preserved CLI reports v0.47.4 before continuing. A missing provider mirror or failed verification stops this recipe; restore the existing installation's verified tool bundle instead of substituting newer tools. For another prior release, qualify its domain compatibility separately.

The old CLI does not contain `migrate-domain`. Install the exact target into its separate prefix, leaving the existing executables and mirror intact:

```sh
curl -fsSL https://install.stackkit.cc -o "$bridge_root/install.sh"
PATH="$bridge_root/target/bin:$PATH" \
  STACKKIT_INSTALL_DIR="$bridge_root/target/bin" \
  STACKKIT_RELEASE_VERSION="$STACKKIT_MIGRATION_RELEASE" \
  STACKKIT_SKIP_SK_SYMLINK=1 STACKKIT_CLI_ONLY=1 sh "$bridge_root/install.sh"
"$target" version
```

CLI-only installation does not roll out the runtime. It also refreshes shared kit definitions in `~/.stackkits`; this native StackSpec bridge uses each CLI's embedded catalog. The shell installer verifies archive checksums. That bootstrap check is separate from the signed release-index and attestation verification performed by `upgrade`.

Use explicit executable paths throughout the transition:

```sh
"$target" user owner migrate-domain --owner-approve
PATH="$bridge_root/prior/bin:$PATH" "$prior" generate
PATH="$bridge_root/prior/bin:$PATH" "$prior" apply
PATH="$bridge_root/prior/bin:$PATH" "$prior" verify
# Only after the prior release verifies on lab.home:
PATH="$bridge_root/target/bin:$PATH" "$target" upgrade --to "$STACKKIT_MIGRATION_RELEASE"
"$target" user owner activate --owner-approve
"$target" user activate alex --owner-approve
```

Stop on any error. If preparation is interrupted, repeat the same target `migrate-domain` command before generating, applying, or upgrading. Signed recovery state resumes the frozen transition and rejects unrelated changes; keep the same workspace, target binary and StackSpec path. Use `--spec <workspace-relative-path>` consistently for a non-default StackSpec. Do not delete recovery state or replace the domain manually to bypass an error. Preparation can already update TinyAuth's live OIDC callback, so an interruption may leave login unavailable until preparation and the prior-release rollout complete.

Preparation validates and updates the StackSpec, retains the original owner signing key, CA, service secrets and user subjects, and updates TinyAuth's callback without rotating its client secret. The preserved prior CLI then reloads the domain configuration using its original catalog. Enroll client DNS for `lab.home` using the existing Owner CA before opening the new URLs. A previously installed `home` resolver scope also covers `lab.home`; re-enrollment narrows it.

The domain transition itself does not create a sealed rollback checkpoint. The subsequent `upgrade` checkpoints the **migrated prior release on `lab.home`** and governs the release rollout and recovery from there. It does not provide rollback to the original `home` configuration. If original-domain rollback is required, this bridge does not satisfy that requirement; checkpointed domain migration is still pending. Runtime preservation, interruption recovery, and the full prior-release bridge remain pending real-host evidence.

Passkeys for `https://id.home` cannot sign in at `https://id.lab.home`. Activation waits until the running PocketID advertises the new issuer, then returns a one-time URL for the same owner or household subject. Existing household members use `user activate`, without deleting or recreating their accounts. Activation status becomes active only after a new credential appears following new-domain enrollment; old credentials remain preserved. Share each household activation URL only with that person.

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
- Vaultwarden 1.37.2 receives a native PocketID SSO client restricted to `owners` and `admins`, matching the catalog's `vault` privilege. The client uses PKCE, the exact `/identity/connect/oidc-signin` callback, and the existing Home CA. Public signups stay closed; the Owner invitation, app-local email/master-password login and verified break-glass admin token remain available. PocketID authenticates the person; each user chooses and unlocks a personal master password. StackKit never generates a user vault password or shared decryption key. Browser SSO and existing-account association still require runtime qualification. The outer TinyAuth gate remains, so mobile/extension clients that cannot carry its session are not yet a supported path. Existing admin-saved `config.json` settings can override environment SSO settings; an older saved override requires explicit reconciliation before SSO can be claimed active. These settings follow the pinned upstream [SSO configuration](https://github.com/dani-garcia/vaultwarden/blob/1.37.2/src/config.rs) and [identity flow](https://github.com/dani-garcia/vaultwarden/blob/1.37.2/src/api/identity.rs).
- Jellyfin/media and Dockge are opt-in/manual until their first-run UX matches the default path.
- The Coolify-managed L3 application layer now has a strict generated bootstrap contract. Direct Docker Compose starts for StackKit-owned/default L3 apps are invalid managed release evidence; product-bundled L3 apps must be manageable selected-PaaS apps with platform external IDs in state. User-installed apps outside StackKit manifests are state-unmanaged.
- The host `security-baseline` is mandatory for Basement Kit beta release evidence. `admin-bootstrap` and broader login-gateway follow-ups remain tracked in the roadmap.

## Architecture

```text
enrolled LAN device
      |
      v
scoped DNS (*.lab.home -> Home node) + Owner-CA HTTPS
      |
      v
Coolify Traefik/proxy :443       (PaaS router = StackKit router; Golden Rules §3/§5.6)
      |
      +--> Coolify        coolify.lab.home   (platform management)
      +--> PocketID       id.lab.home
      +--> TinyAuth       auth.lab.home
      +--> Node Hub       base.lab.home
      +--> Homepage       home.lab.home
      +--> Whoami         whoami.lab.home
      +--> Uptime Kuma    kuma.lab.home
      +--> Vaultwarden    vault.lab.home
      +--> Immich         photos.lab.home
      +--> Files          files.lab.home     (Cloudreve default)
      |
      +--> socket-proxy   internal Docker API, never public

With explicit `paas: komodo`, exactly one StackKit-owned Traefik replaces the
Coolify proxy as the router (accepted adapter exception).
```

Security defaults currently covered by generated resources:

- `stackkit apply` configures the host baseline: unattended security updates and kernel hardening (universal), and on the home site an nftables table that drops inbound traffic except loopback, established traffic, the LAN, overlay interfaces and container bridges, key-only sshd (root login limited to keys) and a fail2ban sshd jail. It never closes the ssh session in use or a configured management network, and refuses to disable password logins when no account holds an authorized key. `stackkit host security verify` re-observes all of it as expiring evidence.
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
