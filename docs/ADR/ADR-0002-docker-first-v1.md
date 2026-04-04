# ADR-001: Docker-First Strategy for v1.0

> **Status:** Accepted  
> **Date:** January 15, 2026  
> **Decision Makers:** kombify-TechStack Team  
> **Scope:** StackKits + kombify-TechStack Core

---

## Context

The StackKits repository originally planned a 3-layer architecture where:
- Layer 1 (CORE): Shared OS-level configuration
- Layer 2 (PLATFORM): Container orchestration (Docker OR Kubernetes)
- Layer 3 (STACKKIT): Use-case configurations (Base Kit, Modern Homelab, High Availability Kit)

The `ha-kit` StackKit was designed for Kubernetes (k3s) while `base-kit` and `modern-homelab` used Docker. This created:

1. **Implementation complexity** - Two completely different tech stacks
2. **Maintenance burden** - Kubernetes templates require different expertise
3. **Testing overhead** - Need k3s clusters for integration testing
4. **User confusion** - Different deployment models within one product

## Decision

**For v1.0, ALL StackKits will use the Docker platform with Dokploy as the PaaS.**

| StackKit | Before (Planned) | After (v1.0) |
|----------|-----------------|--------------|
| `base-kit` | Docker + Dokploy | Docker + Dokploy ✓ |
| `modern-homelab` | Docker + Coolify | Docker + Dokploy |
| `ha-kit` | Kubernetes (k3s) | Docker Swarm + Dokploy |

### Key Enabler: Dokploy Native Swarm Support

Dokploy v0.18+ natively supports Docker Swarm mode, enabling:
- Multi-node deployments
- Service replication
- Rolling updates
- Failover between nodes

This eliminates the need for Kubernetes to achieve HA in homelab scenarios.

## Architecture: 3 Layers Retained

We **keep the 3-layer architecture** but with Docker as the only implemented platform for v1.0:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                        3-LAYER ARCHITECTURE (v1.0)                           │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  LAYER 3: STACKKITS                                                         │
│  ─────────────────────────────                                              │
│  ┌─────────────┐   ┌─────────────┐   ┌─────────────┐                        │
│  │ base-       │   │ modern-     │   │ ha-         │                        │
│  │ homelab     │   │ homelab     │   │ homelab     │                        │
│  │             │   │             │   │             │                        │
│  │ • 1 Node    │   │ • 2-5 Nodes │   │ • 3+ Nodes  │                        │
│  │ • Dokploy   │   │ • Dokploy   │   │ • Dokploy   │                        │
│  │ • Local     │   │ • Swarm     │   │ • Swarm HA  │                        │
│  └──────┬──────┘   └──────┬──────┘   └──────┬──────┘                        │
│         └─────────────────┼─────────────────┘                               │
│                           │                                                  │
│  LAYER 2: PLATFORMS       │                                                  │
│  ─────────────────────    │                                                  │
│  ┌────────────────────────┴────────────────────┐   ┌─────────────────────┐  │
│  │ DOCKER PLATFORM (v1.0)                      │   │ KUBERNETES (v1.1+)  │  │
│  │ ✅ Implemented                              │   │ 🔲 Planned          │  │
│  │ • Docker Engine                             │   │ • k3s               │  │
│  │ • Docker Swarm                              │   │ • Ingress           │  │
│  │ • Dokploy (PaaS)                            │   │ • MetalLB           │  │
│  │ • Traefik                                   │   │ (Future StackKits)  │  │
│  └─────────────────────────────────────────────┘   └─────────────────────┘  │
│                           │                                                  │
│  LAYER 1: CORE            │                                                  │
│  ─────────────────────    │                                                  │
│  ┌────────────────────────┴─────────────────────────────────────────────┐   │
│  │ BASE FOUNDATION (All StackKits)                                       │   │
│  │ • Bootstrap (packages, users, directories)                           │   │
│  │ • Security (UFW, SSH hardening, Fail2ban)                            │   │
│  │ • Network (DNS, TLS modes, firewall rules)                           │   │
│  │ • Observability (health checks, logging)                             │   │
│  └──────────────────────────────────────────────────────────────────────┘   │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

## v1.0 Unified Stack

### Default Configuration (All StackKits)

| Component | Choice | Reason |
|-----------|--------|--------|
| **OS** | Ubuntu 24.04 LTS | Best Docker support, widest compatibility |
| **Container Runtime** | Docker 27.x | Industry standard, Dokploy requirement |
| **Multi-node** | Docker Swarm | Native in Docker, Dokploy integrated |
| **PaaS** | Dokploy | Best UX for homelabs, Swarm support |
| **Reverse Proxy** | Traefik v3 | Auto-discovery, Docker native |
| **Monitoring** | Uptime Kuma | Simple, effective, low resources |

### Flexibility Points (Config-Level, No Code Changes)

| Aspect | Default | Alternatives |
|--------|---------|--------------|
| OS | Ubuntu 24.04 | Debian 12, Ubuntu 22.04 |
| PaaS | Dokploy | Coolify (via variant) |
| Monitoring | Uptime Kuma | Beszel, Prometheus (via variant) |
| Proxy | Traefik | Caddy (via variant) |

## StackKit Differentiation (v1.0)

| Feature | base-kit | modern-homelab | ha-kit |
|---------|--------------|----------------|------------|
| **Nodes** | 1 | 2-5 | 3+ |
| **Swarm** | No | Yes | Yes (HA) |
| **Dokploy Mode** | Standalone | Swarm | Swarm + replicas |
| **Network** | Local | Hybrid | Hybrid + failover |
| **VPN** | None | Headscale | Headscale |
| **Complexity** | Single-Server | Intermediate | Advanced |
| **Use Case** | Single server | Home + VPS | Production-like |

## Future: Kubernetes Support (v1.1+)

Kubernetes will be added as **separate StackKits**, not modifications to existing ones:

| Future StackKit | Platform | Use Case |
|-----------------|----------|----------|
| `k3s-homelab` | Kubernetes | Single k3s node learning |
| `k3s-cluster` | Kubernetes | Multi-node k3s HA |
| `k8s-production` | Kubernetes | Enterprise k8s patterns |

This approach:
- **Preserves simplicity** for Docker users
- **Clear separation** between Docker and k8s worlds
- **No breaking changes** to existing StackKits
- **Dedicated testing** for Kubernetes stacks

## Consequences

### Positive

1. **Faster v1.0 delivery** - Focus on one platform
2. **Lower complexity** - Docker skills sufficient
3. **Better testing** - Unified test infrastructure
4. **Clearer documentation** - One path per StackKit
5. **Dokploy expertise** - Deep integration vs shallow support

### Negative

1. **No k8s for v1.0** - Users wanting k3s must wait or self-configure
2. **Swarm limitations** - Not as feature-rich as k8s for edge cases
3. **Refactoring ha-kit** - Need to rewrite from k3s to Swarm

### Mitigations

1. Document Kubernetes as "Planned for v1.1" in all relevant docs
2. Keep `platforms/kubernetes/` scaffolding for future use
3. Add "Kubernetes-alternative" section explaining Swarm capabilities

## Implementation Tasks

See: StackKits ROADMAP.md Sprint S3-2026 (Architecture Alignment)

## Related Documents

- [ARCHITECTURE_3LAYER.md](./ARCHITECTURE_3LAYER.md) - Updated with v1.0 scope
- [StackKits ROADMAP.md](./ROADMAP.md) - Sprint S3-2026 tasks
- [kombify-TechStack ROADMAP.md](../../kombify-TechStack/docs/ROADMAP.md) - Sprint 4 alignment tasks
