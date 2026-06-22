# 6. Service URL Matrix — L2 Platform Layer

Date: 2026-03-11
Status: Proposed

## Context

StackKits need to produce correct service URLs for every supported domain mode and production reverse proxy backend. This is a Layer 2 (Platform) concern because it spans:

- **Ingress** (Traefik configuration, TLS termination, routing rules)
- **PAAS** (Coolify manages its own Traefik instance; Komodo currently uses StackKit-owned Traefik)
- **Identity** (TinyAuth ForwardAuth middleware URLs depend on the domain)
- **DNS** (resolution differs: public DNS, kombify.me registry, local Kombify Point)

Currently only selected paths are implemented and verified. The normal matrix covers custom-domain, kombify.me, browser-native local defaults, and explicit local DNS across the supported routing backends.

2026-06-02 status note, updated 2026-06-22: the default BaseKit contract is Coolify-first, with Komodo as the beta-supported alternative. StackKit-owned system and L3 apps must be registered through the selected PaaS adapter, and the standalone StackKit-owned routing fallback is explicit opt-in only. Dokploy remains draft and is not part of the canonical three-scenario E2E matrix. Live release evidence is intentionally capped at SK-S1 `bootstrapped` local Docker Desktop/Fresh Ubuntu Coolify, SK-S2 `bootstrapped` TechStack Lease kombify.me Komodo plus Runtime Action servicecall coverage, and SK-S3 `bootstrapped` provider-leased custom-domain Coolify with managed cleanup.

## Decision

Implement the supported domain mode x reverse proxy backend combinations as part of the L2 Platform Layer. The URL generation, TLS strategy, and DNS resolution are determined at `stackkit generate` time based on the stack-spec.yaml.

### URL Matrix Dimensions

#### Domain Modes (rows)

| Mode | Domain Example | TLS Strategy | DNS Resolution |
|------|---------------|-------------|----------------|
| **Custom domain** | `*.kombify.pro` for provided `kombify.pro` | ACME (TLS-ALPN-01 or DNS-01) | User manages DNS or StackKits automates exact service records |
| **kombify.me** | `*.mylab.kombify.me` | Managed by kombify (Cloudflare wildcard) | kombify.me subdomain registry + tunnel/direct connect |
| **Local default** | `*.home.localhost` | HTTP in local-only mode | Browser/OS `.localhost` handling on the current device |
| **Explicit local DNS** | `*.stack.home` / `*.<name>.home` | HTTP or accepted local CA path | Kombify Point only when StackKit owns or verifies the resolver |

#### Reverse Proxy Backends (columns)

| Backend | When Used | Traefik Owner | Service Discovery |
|---------|-----------|---------------|-------------------|
| **Standalone Traefik** | Legacy/nonstandard modes only | StackKit-managed Traefik container | Docker labels |
| **Coolify + Traefik** | Standard/default PAAS | Coolify-managed Traefik | Coolify routing + Docker labels |
| **Komodo + StackKit Traefik** | Production alternative PaaS (`paas: komodo`) | StackKit-managed Traefik | Komodo Stack resources + StackKit route labels |
| **Dokploy + Traefik** | Draft adapter only | Dokploy-managed Traefik | Dokploy routing + Docker labels |

### URL Generation Pattern

All three backends produce the same URL pattern for a given domain mode:

```
{service}.{domain}
```

Examples:
- Custom: `kuma.kombify.pro`, `base.kombify.pro` for a provided `kombify.pro` domain
- kombify.me: `mylab-kuma.kombify.me`, `mylab-base.kombify.me` (flat naming)
- Local default: `kuma.home.localhost`, `base.home.localhost`
- Explicit local DNS: `kuma.stack.home`, `base.family.home`

The difference is HOW the routing happens internally:

| Backend | Routing Mechanism |
|---------|-------------------|
| Standalone Traefik | Docker labels on each container → Traefik routes by `Host()` |
| Komodo + StackKit Traefik | Komodo owns stack resources; StackKit owns exactly one Traefik for generated service routes |
| Dokploy + Traefik | Draft only until promoted; Dokploy creates Traefik config for its managed apps |
| Coolify + Traefik | Coolify manages its own Traefik; StackKit platform services attach labels to Coolify's Traefik network |

### TLS Strategy Per Scenario

| | Standalone Traefik | Komodo + StackKit Traefik | Coolify + Traefik |
|---|---|---|---|
| **Custom domain** | ACME cert resolver on StackKit Traefik | ACME on StackKit Traefik | ACME on Coolify's Traefik (DNS-01 for the provided service records) |
| **kombify.me** | kombify manages TLS (Cloudflare) | kombify manages TLS | kombify manages TLS |
| **Local** | HTTP local-only or accepted local CA path | HTTP local-only or accepted local CA path | HTTP local-only or accepted local CA path |

### DNS Resolution Per Scenario

| | Standalone Traefik | Komodo + StackKit Traefik | Coolify + Traefik |
|---|---|---|---|
| **Custom domain** | User DNS or exact service A records | User DNS or exact service A records | User DNS or exact service A records |
| **kombify.me** | kombify registry + tunnel/direct connect | kombify registry + tunnel/direct connect | kombify registry + tunnel/direct connect |
| **Local default** | `.localhost` | `.localhost` | `.localhost` |
| **Explicit local DNS** | Kombify Point | Kombify Point | Kombify Point |

## Implementation Plan

### Phase 1: Standalone Traefik (DONE)

- [x] Custom domain with TLS-ALPN-01 (port 443 public)
- [x] Custom domain with DNS-01 (behind NAT, Cloudflare verified)
- [x] Local default (`home.localhost`) with no hosts-file edits, DNS setup, trust-store setup, or port suffixes
- [x] Explicit local DNS (`stack.home` / `<name>.home`) with Kombify Point + HTTP
- [ ] kombify.me with Direct Connect registry

### Phase 2: Komodo + StackKit Traefik

Komodo is the beta-supported alternative. It owns stack resources, while the current adapter contract keeps exactly one StackKit-owned Traefik for generated service routes.

Implementation:
1. Detect when PAAS = Komodo at standard tier
2. Bootstrap Komodo Core, Periphery, and DB without UI
3. Persist endpoint, API key, API secret, and server context in `.stackkit/platform.json`
4. Keep one StackKit-owned Traefik for generated service routes
5. Kombify Point/local DNS is managed by StackKit only for explicit LAN-DNS mode

### Phase 3: Coolify + Traefik

Coolify has its own integrated router and API model:
1. Coolify manages Traefik via its own config
2. StackKit platform services join Coolify's network
3. Service labels follow Coolify's conventions
4. ACME configured through Coolify's settings UI or environment

### Cross-Cutting Concerns

**ForwardAuth (TinyAuth):** The `tinyauth` middleware must reference the correct TinyAuth URL regardless of which Traefik manages the routing. The `TINYAUTH_APPURL` and ForwardAuth address URL change based on domain mode.

**PocketID:** The `PUBLIC_APP_URL` must match the actual accessible URL for the domain mode.

**Dashboard:** Service cards link to `{scheme}://{service}.{domain}` without host-port suffixes. Local default cards use `http://*.home.localhost`; public/custom cards use HTTPS.

**kombify.me flat naming:** Service URLs use `{prefix}-{service}.kombify.me` (single DNS level), not `{service}.{prefix}.kombify.me` (nested). This applies regardless of reverse proxy backend.

## Consequences

### Positive
- Users get a consistent experience regardless of PAAS choice
- Domain mode and reverse proxy are orthogonal — any combination works
- Clear separation: domain/TLS is a platform concern (L2), not per-service

### Negative
- The URL matrix is larger than the canonical E2E matrix, so contract tests must cover non-E2E combinations
- Coolify Traefik integration requires understanding its internal networking
- kombify.me + selected PaaS requires coordinating TLS between kombify and the selected router

## Alternatives Considered

1. **Only support standalone Traefik** — Too limiting. Users who choose Coolify or Komodo as their PAAS shouldn't lose domain flexibility.
2. **Always deploy a separate Traefik alongside PAAS Traefik** — Port conflicts (both want 80/443). Wasteful.
3. **Only support custom domains with PAAS** — Breaks the principle that domain mode and PAAS are independent choices.
