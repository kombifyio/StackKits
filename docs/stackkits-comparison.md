# StackKits Comparison

Side-by-side comparison of all three StackKits: Base Kit, Modern Homelab, and High Availability Kit.

---

## Quick Comparison

| | Base Kit | Modern Homelab | High Availability Kit |
|---|---|---|---|
| **One-liner** | Professional single-environment deployment | Bridge home and cloud | No single point of failure |
| **Nodes** | 1 | 2+ | 3+ (odd number) |
| **Container Runtime** | Docker Compose | Docker Compose per node | Docker Swarm |
| **Orchestration** | Single-node Compose | PaaS-coordinated | Swarm scheduler + Raft |
| **Networking** | Docker bridge + Traefik | Overlay tunnel (CF/Pangolin) + Traefik | Swarm overlay (encrypted) + routing mesh |
| **Load Balancing** | Traefik only | Traefik per node | Traefik (L7) + HAProxy (L4) + Keepalived (VIP) |
| **Service Discovery** | Docker DNS | Docker DNS per node | Swarm built-in DNS + CoreDNS + etcd |
| **Database HA** | Single instance | Single instance | Patroni + etcd (PostgreSQL failover) |
| **Cache HA** | Single instance | Single instance | Valkey Sentinel (automatic failover) |
| **Secrets** | .env files / SOPS+age | SOPS+age | Docker Swarm secrets (encrypted at rest) |
| **Identity** | LLDAP + Step-CA, TinyAuth/PocketID | LLDAP + Step-CA, TinyAuth/PocketID | LLDAP cluster + Step-CA HA, TinyAuth/PocketID |
| **PaaS** | Dokploy | Coolify (domain) / Dokploy (no domain) | Dokploy (Swarm mode) / Coolify (cluster-aware) |
| **Minimum CPU** | 2 cores | 2 cores/node | 4 cores/node |
| **Minimum RAM** | 4 GB | 4 GB/node | 8 GB/node |
| **Minimum Disk** | 50 GB | 50 GB/node | 100 GB/node (SSD) |
| **Deployment Mode** | Simple (OpenTofu) | Simple or Advanced (Terramate) | Advanced (Terramate) recommended |
| **Fault Tolerance** | None | Partial (multiple nodes, but no auto-failover) | Full (Swarm reschedules, DB fails over, VIP floats) |
| **Best For** | Single-server setups, standalone deployments, streamlined operations | Hybrid setups, public services, growing homelabs | Production, critical services, uptime SLAs |

---

## Architecture Diagrams

### Base Kit

```
    ┌─────────────────────────────────────────────┐
    │              Single Server                   │
    │                                              │
    │   ┌──────────┐   ┌──────────┐   ┌────────┐  │
    │   │ Traefik  │   │ Dokploy  │   │ Dozzle │  │
    │   │ (proxy)  │   │ (PaaS)   │   │ (logs) │  │
    │   └────┬─────┘   └──────────┘   └────────┘  │
    │        │                                     │
    │   ┌────▼─────────────────────────────────┐   │
    │   │         Docker Compose                │   │
    │   │                                       │   │
    │   │  ┌─────────┐  ┌──────┐  ┌─────────┐  │   │
    │   │  │  App 1  │  │App 2 │  │ App 3   │  │   │
    │   │  └─────────┘  └──────┘  └─────────┘  │   │
    │   └───────────────────────────────────────┘   │
    │                                              │
    │   ┌──────────┐   ┌──────────┐                │
    │   │  LLDAP   │   │ Step-CA  │  (Identity)    │
    │   └──────────┘   └──────────┘                │
    └─────────────────────────────────────────────┘
```

- One server, one Docker Compose stack
- Traefik handles all routing
- Dokploy for git-based deployments
- No redundancy -- if the server goes down, everything goes down

### Modern Homelab

```
    Internet
       │
    ┌──▼───────────────────┐     Tunnel (CF/Pangolin)     ┌───────────────────────┐
    │   Cloud VPS (entry)  │ ◄───────────────────────────► │  Local Server (home)  │
    │                      │                               │                       │
    │  ┌────────────────┐  │                               │  ┌─────────────────┐  │
    │  │    Traefik      │  │                               │  │    Traefik       │  │
    │  │  (public TLS)  │  │                               │  │  (internal)     │  │
    │  └───────┬────────┘  │                               │  └───────┬─────────┘  │
    │          │           │                               │          │            │
    │  ┌───────▼────────┐  │                               │  ┌───────▼─────────┐  │
    │  │   TinyAuth      │  │                               │  │ Docker Compose  │  │
    │  │ (ForwardAuth)  │  │                               │  │                 │  │
    │  └────────────────┘  │                               │  │  ┌──────────┐   │  │
    │                      │                               │  │  │  App 2   │   │  │
    │  ┌────────────────┐  │                               │  │  │  App 3   │   │  │
    │  │ Docker Compose │  │                               │  │  │  App 4   │   │  │
    │  │                │  │                               │  │  │ Storage  │   │  │
    │  │  ┌──────────┐  │  │                               │  │  └──────────┘   │  │
    │  │  │  PaaS    │  │  │                               │  └─────────────────┘  │
    │  │  │  App 1   │  │  │                               │                       │
    │  │  └──────────┘  │  │                               │  ┌─────────────────┐  │
    │  └────────────────┘  │                               │  │ LLDAP + Step-CA │  │
    │                      │                               │  └─────────────────┘  │
    └──────────────────────┘                               └───────────────────────┘
```

- Cloud VPS = public entry point (small, cheap)
- Local server = compute + storage (powerful, your hardware)
- Tunnel bridges CGNAT/DS-Lite (no port forwarding needed)
- Identity-aware proxy (TinyAuth) replaces VPN for access control
- Docker Compose on each node, PaaS coordinates deployments
- No auto-failover -- if a node goes down, its services are offline until manually addressed

### High Availability Kit

```
                        ┌──────────────────────┐
                        │    Keepalived VIP     │
                        │  (Floating IP: .100)  │
                        └──────────┬───────────┘
                                   │
            ┌──────────────────────┼──────────────────────┐
            │                      │                      │
   ┌────────▼────────┐   ┌────────▼────────┐   ┌────────▼────────┐
   │  Node 1 (mgr)   │   │  Node 2 (mgr)   │   │  Node 3 (mgr)   │
   │                  │   │                  │   │                  │
   │ ┌──────────────┐ │   │ ┌──────────────┐ │   │ ┌──────────────┐ │
   │ │Traefik(global)│ │   │ │Traefik(global)│ │   │ │Traefik(global)│ │
   │ │HAProxy+KA    │ │   │ │HAProxy+KA    │ │   │ │HAProxy+KA    │ │
   │ └──────┬───────┘ │   │ └──────┬───────┘ │   │ └──────┬───────┘ │
   │        │         │   │        │         │   │        │         │
   │ ┌──────▼──────────────────────▼──────────────────────▼───────┐ │
   │ │              Docker Swarm Overlay (encrypted)               │ │
   │ │                                                             │ │
   │ │  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌───────────┐  │ │
   │ │  │ Patroni  │  │ Patroni  │  │ Patroni  │  │   etcd    │  │ │
   │ │  │(primary) │  │(replica) │  │(replica) │  │ (3 nodes) │  │ │
   │ │  └──────────┘  └──────────┘  └──────────┘  └───────────┘  │ │
   │ │                                                             │ │
   │ │  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌───────────┐  │ │
   │ │  │ Valkey   │  │ Valkey   │  │ Valkey   │  │ Valkey    │  │ │
   │ │  │(primary) │  │(replica) │  │(replica) │  │ Sentinel  │  │ │
   │ │  └──────────┘  └──────────┘  └──────────┘  │ (3 nodes) │  │ │
   │ │                                             └───────────┘  │ │
   │ │  ┌──────────┐  ┌──────────┐  ┌──────────────────────────┐  │ │
   │ │  │ CoreDNS  │  │ CoreDNS  │  │ Application Services     │  │ │
   │ │  │ + etcd   │  │ + etcd   │  │ (replicated across nodes)│  │ │
   │ │  └──────────┘  └──────────┘  └──────────────────────────┘  │ │
   │ │                                                             │ │
   │ └─────────────────────────────────────────────────────────────┘ │
   └─────────────────────────────────────────────────────────────────┘
```

- 3+ Swarm managers maintain Raft quorum
- Encrypted overlay network for all inter-node traffic
- Keepalived provides a floating VIP -- clients always connect to the same IP
- Traefik runs as a global service (one per node, host mode)
- HAProxy provides L4 load balancing with health checks
- Patroni + etcd: automatic PostgreSQL primary election and failover
- Valkey Sentinel: automatic Redis-compatible cache failover
- CoreDNS + etcd: distributed service discovery
- If a node dies, Swarm reschedules its tasks, Patroni promotes a replica, VIP floats to a healthy node

---

## Feature Matrix

### Container Runtime

| Feature | Base | Modern | HA |
|---------|------|--------|-----|
| Docker Compose | Yes | Yes (per node) | No |
| Docker Swarm | No | No | Yes |
| Overlay networking | No | No | Yes (encrypted) |
| Ingress routing mesh | No | No | Yes |
| Built-in service discovery | Docker DNS | Docker DNS | Swarm DNS + VIP |
| Declarative desired state | Manual | Manual | Automatic reconciliation |
| Task rescheduling on failure | No | No | Yes (automatic) |

### Networking

| Feature | Base | Modern | HA |
|---------|------|--------|-----|
| Reverse proxy | Traefik | Traefik per node | Traefik (global) |
| L4 load balancer | -- | -- | HAProxy |
| VIP failover | -- | -- | Keepalived (VRRP) |
| Tunnel (CGNAT bypass) | -- | CF Tunnel / Pangolin | Optional |
| Identity-aware proxy | -- | TinyAuth (ForwardAuth) | TinyAuth (ForwardAuth) |
| Network encryption | TLS only | TLS + tunnel | TLS + Swarm mTLS + overlay encryption |

### Data Resilience

| Feature | Base | Modern | HA |
|---------|------|--------|-----|
| Database | Single PostgreSQL | Single PostgreSQL | Patroni cluster (auto-failover) |
| Cache | Single Redis/Valkey | Single Redis/Valkey | Valkey Sentinel (auto-failover) |
| DNS | Docker DNS | Docker DNS | CoreDNS + etcd (distributed) |
| Key-value store | -- | -- | etcd (Raft consensus) |
| Shared storage | Local volumes | Local volumes per node | GlusterFS (add-on) |
| Backup | Add-on (Restic) | Add-on (Restic) | Add-on (Restic + Swarm backup) |

### Security

| Feature | Base | Modern | HA |
|---------|------|--------|-----|
| TLS termination | Traefik (ACME) | Traefik (ACME) | Traefik (ACME) |
| Node-to-node encryption | N/A | Tunnel only | Swarm mTLS (automatic) |
| Secrets management | .env / SOPS+age | SOPS+age | Docker Swarm secrets (encrypted Raft) |
| Config management | Files | Files | Docker Swarm configs |
| Certificate rotation | Manual | Step-CA | Step-CA + Swarm auto-rotate |
| Identity (L1) | LLDAP + Step-CA | LLDAP + Step-CA | LLDAP cluster + Step-CA HA |
| Identity (L2) | TinyAuth / PocketID | TinyAuth / PocketID | TinyAuth / PocketID |

### Operations

| Feature | Base | Modern | HA |
|---------|------|--------|-----|
| Deployment method | `docker compose up` | PaaS git deploy | `docker stack deploy` |
| Rolling updates | Manual restart | PaaS-managed | Swarm native (zero-downtime) |
| Automatic rollback | No | PaaS-dependent | Swarm native |
| Health checks | Docker-level | Docker-level | Swarm-level (reschedule on failure) |
| Scaling | Manual | Manual per node | `docker service scale` |
| Drift detection | Terramate (optional) | Terramate (optional) | Terramate (recommended) |
| Log aggregation | Dozzle | Dozzle per node | `docker service logs` + Loki |
| Monitoring | Uptime Kuma / Beszel | Uptime Kuma / Beszel | Prometheus + Grafana + cAdvisor |

---

## Choosing the Right StackKit

### Choose Base Kit if:

- You have **one server** (physical or VPS)
- You want a **professional, hardened single-environment setup** out of the box
- You prefer **streamlined operations** without multi-node coordination
- Scheduled maintenance windows are acceptable for updates
- You want **maximum simplicity** with production-grade defaults

### Choose Modern Homelab if:

- You have **hardware at home AND a cloud VPS** (or plan to get one)
- You want **public-facing services** without port forwarding (CGNAT/DS-Lite)
- You want **identity-aware access control** without running a VPN
- You want to keep data at home but serve it through the cloud
- You are a **growing homelab** ready for multi-node

### Choose High Availability Kit if:

- You run **services that cannot go down** (family depends on them, small business, SLA)
- You have **3+ machines** (physical or cloud)
- You need **automatic failover** (database, cache, routing)
- You want **zero-downtime deployments** (rolling updates, rollback)
- You need **encrypted cluster communication** (mTLS between all nodes)
- You are comfortable with slightly more complexity for significantly more reliability

---

## Upgrade Path

```
Base Kit ──► Modern Homelab ──► High Availability Kit
   (1 node)        (2+ nodes)       (3+ nodes)
```

Each StackKit builds on the concepts of the previous one:

1. **Base to Modern**: Add a second node (cloud VPS), enable tunnel, switch to identity-aware proxy model. Your existing services keep running -- you add a cloud entry point.

2. **Modern to HA**: Initialize Docker Swarm across your nodes, migrate from Docker Compose to `docker stack deploy`, add HA services (Patroni, Valkey Sentinel, HAProxy, Keepalived). This is a bigger migration since it changes the container runtime.

kombify-TechStack automates both transitions through the wizard -- you don't need to manually reconfigure everything.

---

## Service Map

Shows which services run in each StackKit and their deployment mode.

| Service | Base | Modern | HA (deploy mode) |
|---------|------|--------|-------------------|
| Traefik | Single | Per-node | Global (host mode) |
| Dokploy / Coolify | Single | Per-node | Replicated (1) |
| LLDAP | Single | Single | Replicated (1-2) |
| Step-CA | Single | Single | Replicated (1) |
| TinyAuth | Optional | Per-node | Replicated (2) |
| PocketID | Optional | Optional | Replicated (2) |
| HAProxy | -- | -- | Global |
| Keepalived | -- | -- | Global |
| Patroni (PostgreSQL) | -- | -- | Replicated (3) |
| etcd | -- | -- | Replicated (3) |
| Valkey | -- | -- | Replicated (3) |
| Valkey Sentinel | -- | -- | Replicated (3) |
| CoreDNS | -- | -- | Replicated (2-3) |
| Uptime Kuma | Single | Per-node | Replicated (1) |
| Dozzle | Single | Per-node | -- |
| Prometheus | Add-on | Add-on | Add-on (Replicated 1-2) |
| Grafana | Add-on | Add-on | Add-on (Replicated 1) |
| cAdvisor | Add-on | Add-on | Add-on (Global) |
| Node Exporter | Add-on | Add-on | Add-on (Global) |
| Loki + Promtail | Add-on | Add-on | Add-on (Replicated + Global) |

---

## Cost Comparison (Typical Setups)

| Aspect | Base | Modern | HA |
|--------|------|--------|-----|
| Hardware | 1 mini PC (~$200) | 1 mini PC + 1 VPS (~$5/mo) | 3 mini PCs (~$600) or 3 VPS (~$15/mo) |
| Domain | Optional | Recommended | Recommended |
| Cloud tunnel | -- | Free (Cloudflare) | Optional |
| Total monthly | $0 | ~$5 | ~$0-15 (hardware) or ~$15-45 (cloud) |
| Electricity | ~$5/mo | ~$5/mo + VPS | ~$15/mo (3 nodes) |

All StackKits are **free and open-source**. Costs are hardware/hosting only.
