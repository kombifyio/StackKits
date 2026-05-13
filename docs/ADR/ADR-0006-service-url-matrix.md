# 6. Service URL Matrix — L2 Platform Layer

Date: 2026-03-11
Status: Proposed

## Context

StackKits need to produce correct service URLs for every combination of domain mode and reverse proxy backend. This is a Layer 2 (Platform) concern because it spans:

- **Ingress** (Traefik configuration, TLS termination, routing rules)
- **PAAS** (Dokploy/Coolify manage their own Traefik instances)
- **Identity** (TinyAuth ForwardAuth middleware URLs depend on the domain)
- **DNS** (resolution differs: public DNS, kombify.me registry, local Kombify Point)

Currently only selected paths are implemented and verified. The normal matrix covers custom-domain, kombify.me, and local DNS across the supported routing backends; legacy device-local names remain compatibility shortcuts, not a separate strategic default.

## Decision

Implement the supported domain mode x reverse proxy backend combinations as part of the L2 Platform Layer. The URL generation, TLS strategy, and DNS resolution are determined at `stackkit generate` time based on the stack-spec.yaml.

### The 9 Scenarios

#### Domain Modes (rows)

| Mode | Domain Example | TLS Strategy | DNS Resolution |
|------|---------------|-------------|----------------|
| **Custom wildcard** | `*.sk-<run>.kombify.pro` | ACME (TLS-ALPN-01 or DNS-01) | User manages DNS or StackKits automates the DNS provider |
| **kombify.me** | `*.mylab.kombify.me` | Managed by kombify (Cloudflare wildcard) | kombify.me subdomain registry + tunnel/direct connect |
| **Local default DNS** | `*.stack.home` / `*.<name>.home` | HTTP in local-only mode | Kombify Point (`*.stack.home` / `*.<name>.home` -> LAN IP) |
| **Legacy device-local** | `*.home.localhost` | HTTP in local-only mode | Browser/OS `.localhost` handling on the current device |

#### Reverse Proxy Backends (columns)

| Backend | When Used | Traefik Owner | Service Discovery |
|---------|-----------|---------------|-------------------|
| **Standalone Traefik** | Legacy/nonstandard modes only | StackKit-managed Traefik container | Docker labels |
| **Dokploy + Traefik** | Standard tier, default PAAS | Dokploy-managed Traefik | Dokploy routing + Docker labels |
| **Coolify + Traefik** | Alternative PAAS (`--paas coolify`) | Coolify-managed Traefik | Coolify routing + Docker labels |

### URL Generation Pattern

All three backends produce the same URL pattern for a given domain mode:

```
{service}.{domain}
```

Examples:
- Custom: `kuma.sk-<run>.kombify.pro`, `base.sk-<run>.kombify.pro`
- kombify.me: `mylab-kuma.kombify.me`, `mylab-base.kombify.me` (flat naming)
- Local default DNS: `kuma.stack.home`, `base.family.home`
- Legacy device-local: `kuma.home.localhost`, `base.home.localhost`

The difference is HOW the routing happens internally:

| Backend | Routing Mechanism |
|---------|-------------------|
| Standalone Traefik | Docker labels on each container → Traefik routes by `Host()` |
| Dokploy + Traefik | Dokploy creates Traefik config for its managed apps; StackKit services use Docker labels on Dokploy's Traefik |
| Coolify + Traefik | Coolify manages its own Traefik; StackKit platform services attach labels to Coolify's Traefik network |

### TLS Strategy Per Scenario

| | Standalone Traefik | Dokploy + Traefik | Coolify + Traefik |
|---|---|---|---|
| **Custom domain** | ACME cert resolver on StackKit Traefik | ACME on Dokploy's Traefik (wildcard via DNS-01) | ACME on Coolify's Traefik (wildcard via DNS-01) |
| **kombify.me** | kombify manages TLS (Cloudflare) | kombify manages TLS | kombify manages TLS |
| **Local** | HTTP local-only or accepted local CA path | HTTP local-only or accepted local CA path | HTTP local-only or accepted local CA path |

### DNS Resolution Per Scenario

| | Standalone Traefik | Dokploy + Traefik | Coolify + Traefik |
|---|---|---|---|
| **Custom domain** | User DNS (wildcard A record) | User DNS (wildcard A record) | User DNS (wildcard A record) |
| **kombify.me** | kombify registry + tunnel/direct connect | kombify registry + tunnel/direct connect | kombify registry + tunnel/direct connect |
| **Local** | Kombify Point | Kombify Point | Kombify Point |

## Implementation Plan

### Phase 1: Standalone Traefik (DONE)

- [x] Custom domain with TLS-ALPN-01 (port 443 public)
- [x] Custom domain with DNS-01 (behind NAT, Cloudflare verified)
- [x] Local DNS (`stack.home` / `<name>.home`) with Kombify Point + HTTP
- [x] Legacy device-local (`home.localhost`) without claiming trusted browser certificates
- [ ] kombify.me with Direct Connect registry

### Phase 2: Dokploy + Traefik

The key challenge: when Dokploy is the PAAS, it manages its OWN Traefik instance. StackKit platform services (TinyAuth, PocketID, Dashboard, Kuma, Whoami) need to route through Dokploy's Traefik, not a separate one.

Implementation:
1. Detect when PAAS = Dokploy at standard tier
2. Skip deploying a separate Traefik container
3. Attach platform service Docker labels to Dokploy's Traefik network
4. Configure ACME/DNS-01 on Dokploy's Traefik (not StackKit's)
5. Kombify Point/local DNS still managed by StackKit for local mode

### Phase 3: Coolify + Traefik

Same principle as Dokploy, but Coolify has different internals:
1. Coolify manages Traefik via its own config
2. StackKit platform services join Coolify's network
3. Service labels follow Coolify's conventions
4. ACME configured through Coolify's settings UI or environment

### Cross-Cutting Concerns

**ForwardAuth (TinyAuth):** The `tinyauth` middleware must reference the correct TinyAuth URL regardless of which Traefik manages the routing. The `APP_URL` and ForwardAuth address URL change based on domain mode.

**PocketID:** The `PUBLIC_APP_URL` must match the actual accessible URL for the domain mode.

**Dashboard:** Service cards link to `https://{service}.{domain}` — URL generation must be correct for all 9 scenarios.

**kombify.me flat naming:** Service URLs use `{prefix}-{service}.kombify.me` (single DNS level), not `{service}.{prefix}.kombify.me` (nested). This applies regardless of reverse proxy backend.

## Consequences

### Positive
- Users get a consistent experience regardless of PAAS choice
- Domain mode and reverse proxy are orthogonal — any combination works
- Clear separation: domain/TLS is a platform concern (L2), not per-service

### Negative
- 9 scenarios to test and maintain
- Dokploy and Coolify Traefik integration requires understanding their internal networking
- kombify.me + Coolify/Dokploy requires coordinating TLS between kombify and the PAAS-managed Traefik

## Alternatives Considered

1. **Only support standalone Traefik** — Too limiting. Users who choose Coolify or Dokploy as their PAAS shouldn't lose domain flexibility.
2. **Always deploy a separate Traefik alongside PAAS Traefik** — Port conflicts (both want 80/443). Wasteful.
3. **Only support custom domains with PAAS** — Breaks the principle that domain mode and PAAS are independent choices.
