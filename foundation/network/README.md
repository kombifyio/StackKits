# Network Templates

Shared OpenTofu templates for host and container networking, kept in the
StackKits base library so kits do not each carry their own copy.

## Scope

- Host networking: addressing, routing, DNS
- Docker networks: bridge and overlay configuration
- Firewall rules
- Reverse proxy and load balancing
- VPN and tunnel integration
- Local DNS

Network templates assume the `bootstrap/` phase completed and run before the
`lifecycle/` phase. A container engine must be present on the node.

## Network modes

The mode is selected by the resolved plan's network intent.

### Local (`_local.tf.tmpl`)

For a homelab reachable only on the LAN, with no public services.

- Bridge network with a configurable subnet
- Optional mDNS discovery
- Direct port mappings instead of a reverse proxy
- Optional local DNS
- Optional IPv6

Outputs: network details, DNS configuration, mDNS status, and LAN service
endpoints.

### Public (`_public.tf.tmpl`)

For services published on the internet.

- Traefik v3 reverse proxy
- Automatic ACME certificates (HTTP or DNS challenge)
- HTTP to HTTPS redirect
- Rate limiting
- Security headers (HSTS, XSS protection)
- Prometheus metrics

Outputs: network details, proxy information, public service URLs, TLS
details, and the active security features.

### Hybrid (`_hybrid.tf.tmpl`)

For a mix of public and internal services.

- Two separate networks: public and internal
- Traefik for the public zone
- Split-horizon DNS
- VPN integration (Tailscale or WireGuard)
- IP allowlist for internal services
- Routing between zones

Outputs: network details, split-DNS configuration, VPN status and endpoint,
the public and internal service sets, and the zone definitions.

## Files

| File | Purpose |
| --- | --- |
| `_local.tf.tmpl` | LAN-only mode |
| `_public.tf.tmpl` | Public internet mode |
| `_hybrid.tf.tmpl` | Public plus internal zones |

## Template variables

Templates are rendered from the resolved plan. Common inputs are the Docker
network name, subnet, and gateway. Local mode adds the mDNS and local-DNS
toggles, the local domain, upstream resolvers, port mappings, and custom DNS
records. Public mode adds the domain, ACME account email, TLS toggles, the
proxy version, rate-limit thresholds, DNS-challenge settings, and the service
set. Hybrid mode adds the public and internal subnets, the internal domain,
the VPN type and credentials, and the split-DNS toggle.

## Security notes

- Local mode suits isolated homelabs without inbound internet exposure.
- Public mode should keep TLS enabled and rate limiting active.
- Hybrid mode should reach internal services over the VPN rather than through
  port forwards, and split DNS keeps internal names off public resolvers.
