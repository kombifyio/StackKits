# ADR-0040: Scope application readiness and client onboarding

- Status: Accepted for the Homelab quality-review implementation
- Date: 2026-09-02
- Scope: Local access, application status, State Console, CLI/MCP and onboarding
- Related: [ADR-0031](ADR-0031-stackkits-standalone-lifecycle-boundary.md), [ADR-0039](ADR-0039-module-local-compute-profiles.md)

## Context

The old default-link rule split local access between `home.test` intent and
`*.home.localhost` compatibility links. The latter resolves to the device
opening the URL rather than the Homelab, while the former did not establish a
client resolver path. Internal service health does not prove browser access,
first-user setup, or application recovery.

## Decision

The existing application lifecycle and runtime evidence remain the authorities.
Status derives five independent axes: installed, reachable, setup, usable and
recoverable. Every positive claim must retain its Plan/workload binding, time,
source and applicable observation scope. Missing evidence remains unverified.
A probe from the CLI host proves access from that host; it cannot certify all
household, LAN, mobile, VPN or public clients.

The accepted local default is one canonical service URL set at
`https://<service>.home`. The StackKit-owned Unbound runtime resolves that zone
to the Home node and the existing Owner CA issues its TLS certificates. Each
device enrolls once while on the LAN: a device client installs a scoped DNS
profile and the public Owner-CA root after explicit OS approval, then the user
authenticates and registers or confirms a device-bound passkey. CA trust alone
does not authorize access. Router, DHCP, hosts-file, `.local`, `.localhost`,
and `.arpa` configuration are not part of the user path.

Remote access is an optional choice in the same device enrollment. It binds the
existing `private-remote-access` capability as a private split tunnel, retains
the same `*.home` URLs, and does not publish services. A direct WireGuard peer
behind arbitrary NAT is not sufficient evidence: zero-router remote
reachability requires an authenticated reachable coordination or relay path.

Custom domains do not consume the private `home` enrollment profile. Their
certificate stays with the selected ingress owner: Traefik directly, or the
declared Coolify/Komodo adapter when that platform owns ingress. Cloudflare
Origin CA ([provider contract](https://developers.cloudflare.com/ssl/origin-configuration/origin-ca/))
is an allowed service option only for authenticated
Cloudflare-to-origin TLS. Its certificates are not direct-browser trust and
must never replace the Owner CA for private `*.home` access or be offered as a
LAN trust workaround.

Onboarding must make resolver, certificate trust, device enrollment, optional
remote access and first-user setup state visible. The client adapter performs
network/trust configuration; the owner only grants the explicit OS approval.
A missing adapter or missing client evidence produces a concrete next step,
never a ready badge. TLS verification must not be disabled to conceal an
unfinished trust step. Generated artifacts remain outputs and must not be
manually patched to make a printed link work.

An encrypted archive, a snapshot or a verified staged copy establishes only
the corresponding data evidence. Recoverable application status requires
verified application activation and a relevant functional check through the
existing restore lifecycle. An internal health probe alone establishes neither
client reachability nor application usability.

## Consequences and implementation status

This supersedes the prior `.localhost`/`home.test` split in Golden Rules
§1.10–11. Stable links and device-managed setup are mandatory; observable scope
and honest readiness remain mandatory behavior.

The derived status/State Console projection is implemented. `stackkit verify
--http --json` and the opt-in `stackkit status --http --json` now produce the
same Plan- and Apply-bound route observation. A positive result proves only the
`verifier-host` vantage; each failed probe retains a bounded failure class.
Plan- and Apply-bound setup receipts now cover the selected native standalone
Photos, Cloudreve Files, Jellyfin Media and Home Assistant adapters, including authenticated owner readback and
temporary session cleanup. Manual personal onboarding remains visible when an
application requires it. Vaultwarden adds signed invitation preparation through
existing administrator-token custody; personal registration, keys and client
decryption remain with the official client and cannot become a setup-complete
claim from the admin readback. Restore verification now also checks the selected
standalone application deployments through their existing product observation
contracts, including actual HTTP status codes. This is runtime health evidence;
database/content checks, client vantages and the final live rollout remain
pending. This decision does
not add a v0.x publication gate.

The StackKits server-side `home` resolver, Owner-CA custody and
`.stackkit/access.json` enrollment handoff are implemented. Applying that
handoff in OS-specific clients and the required second-device LAN/passkey proof
remain pending; the manifest itself is not live device evidence.
