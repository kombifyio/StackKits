# Modern Homelab StackKit

> **Status: Alpha/Scaffolding** - Architecture designed, implementation in progress

Hybrid homelab bridging local servers with cloud VPS via identity-aware proxies.
Docker Compose per node, coordinated by Coolify or Dokploy.

---

## Architecture

```
                          PUBLIC INTERNET
                               |
                          HTTPS (443)
                               v
┌──────────────────────────────────────────────────────────┐
│                    CLOUD NODE (VPS)                       │
│                                                          │
│  ┌────────────────────────────────────────────────────┐  │
│  │              Traefik (Reverse Proxy + TLS)          │  │
│  └─────────────────────┬──────────────────────────────┘  │
│          ┌─────────────┼─────────────┐                   │
│          v             v             v                   │
│  ┌──────────┐  ┌──────────┐  ┌──────────────────────┐   │
│  │ TinyAuth │  │ Coolify  │  │ OTel Gateway / VM / │   │
│  │ (Auth)   │  │ or       │  │ Grafana / Loki      │   │
│  │          │  │ Dokploy  │  │ (Monitoring Add-On) │   │
│  └──────────┘  └──────────┘  └──────────────────────┘   │
│                      │                                   │
│              ┌───────┴────────┐                          │
│              │ CF Tunnel      │                          │
│              │ or Pangolin    │                          │
│              └───────┬────────┘                          │
└──────────────────────┼───────────────────────────────────┘
                       │
         ┌─────────────┼─────────────┐
         v             v             v
┌──────────────┐ ┌──────────────┐ ┌──────────────┐
│ LOCAL NODE 1 │ │ LOCAL NODE 2 │ │ LOCAL NODE N │
│              │ │              │ │              │
│ Immich       │ │ Jellyfin     │ │ Ollama       │
│ Home Asst.   │ │ *arr Stack   │ │ Open WebUI   │
│ Cloudreve    │ │ Game Server  │ │ Gitea        │
│              │ │              │ │              │
│ Alloy OTLP   │ │ Alloy OTLP   │ │ Alloy OTLP   │
│ Agent        │ │ Agent        │ │ Agent        │
└──────────────┘ └──────────────┘ └──────────────┘
```

## Key Design Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| **Container Runtime** | Docker Compose per node | No Swarm complexity, PaaS coordinates multi-node |
| **Network Model** | Identity-Aware Proxy | LLDAP + Step-CA + TinyAuth make VPN optional |
| **PaaS Selection** | Context-driven | Domain + wildcard = Coolify, else = Dokploy |
| **CGNAT Bypass** | Tunnel (not VPN) | Cloudflare Tunnel (free) or Pangolin (self-hosted) |
| **Monitoring** | OTLP gateway + VictoriaMetrics | Central fan-in on the cloud node with lower-overhead storage |
| **Log Agent** | Grafana Alloy | Per-node OTLP agent for logs and metrics |
| **Secrets** | SOPS + age | Git-native, no external dependencies |

## Monitoring Topology

- Every node runs `grafana-alloy` as the local OTLP agent.
- The cloud node hosts the central `otel-gateway`, which receives OTLP from all nodes and forwards metrics to `victoriametrics`.
- `grafana` reads from VictoriaMetrics for dashboards, while `loki` remains the optional log backend.
- `uptime-kuma` and `beszel` remain availability and operator UX tools, not the canonical metrics pipeline.

## PaaS Decision Logic

```
User has own domain AND can wildcard?
  YES → Coolify (multi-node, git deploys, full PaaS)
  NO  → Dokploy (traefik-me + MagicDNS, simpler setup)
```

## Identity Stack (not VPN)

The identity-aware proxy model eliminates the need for VPN:

| Layer | Service | Purpose |
|-------|---------|---------|
| L1 | **LLDAP** | Lightweight LDAP user directory |
| L1 | **Step-CA** | Internal PKI, auto-renew certs, mTLS |
| L2 | **TinyAuth** | ForwardAuth proxy for all Traefik routes |
| L2 | **PocketID** | Optional OIDC provider with passkeys |

VPN (Headscale/Tailscale) is available as the `vpn-overlay` add-on for users who want mesh networking.

## Node Requirements

| Node Type | Role | Minimum | Recommended |
|-----------|------|---------|-------------|
| **Cloud** (VPS) | Ingress, management | 2 CPU, 4 GB RAM, 20 GB | 4 CPU, 8 GB RAM, 50 GB |
| **Local** (On-prem) | Compute, storage | 2 CPU, 4 GB RAM, 50 GB | 4 CPU, 16 GB RAM, 200 GB |

Minimum topology: 1 cloud + 1 local node.

## Add-On Ecosystem

### Infrastructure Add-Ons

| Add-On | Services | Placement | License |
|--------|----------|-----------|---------|
| `tunnel` | Cloudflare Tunnel / Pangolin | Cloud | Free / AGPL-3 |
| `monitoring` | OTel Gateway, VictoriaMetrics, Grafana, Loki, Alloy | Cloud + Daemonset | Apache-2 / AGPL-3 |
| `backup` | Restic + scheduler | Daemonset | BSD-2 |
| `vpn-overlay` | Headscale / Tailscale | Cloud + Daemonset | BSD-3 |
| `authelia` | Authelia (replaces TinyAuth) | Cloud | Apache-2 |

### Use Case Add-Ons (10 Homelab Scenarios)

| Add-On | Services | Placement | License |
|--------|----------|-----------|---------|
| `vault` | Vaultwarden | Cloud | AGPL-3 |
| `photos` | Immich | Local | AGPL-3 |
| `media` | Jellyfin + Sonarr + Radarr + Prowlarr | Local | GPL-2 / GPL-3 |
| `file-sharing` | Cloudreve / OpenCloud / Nextcloud | Local | GPL-3 / Apache-2 |
| `smart-home` | Home Assistant + Mosquitto + Zigbee2MQTT | Local | Apache-2 |
| `ai-workloads` | Ollama + Open WebUI | Local (GPU) | MIT / BSD-3 |
| `calendar` | Radicale + Bloben | Local | GPL-3 / AGPL-3 |
| `mail` | Stalwart (IMAP/JMAP/SMTP + CalDAV) | Cloud | AGPL-3 |
| `dev-platform` | Gitea + Woodpecker CI | Local | MIT / Apache-2 |
| `gameserver` | Generic game server framework | Local | - |
| `remote-desktop` | Apache Guacamole | Local | Apache-2 |

## File Structure

```
modern-homelab/
├── stackkit.yaml          # StackKit metadata and addon ecosystem
├── stackfile.cue          # Main CUE schema (#ModernHomelabStack)
├── services.cue           # Core platform service definitions
├── defaults.cue           # Context-driven default values
├── README.md              # This file
├── contexts/
│   ├── cloud.cue          # Cloud node context defaults
│   └── local.cue          # Local node context defaults
└── templates/
    └── simple/            # OpenTofu templates (TODO)
```

## Deployment Modes

### Simple (Default)
- OpenTofu-only Day-1 provisioning
- Direct state management
- Simple rollback via state

### Advanced
- Terramate-orchestrated with drift detection
- Stack ordering: foundation → platform → tunnel → services
- Day-2 operations: drift detection, rolling updates, change sets

## Comparison with base-kit

| Feature | base-kit | modern-homelab |
|---------|-------------|----------------|
| Nodes | Single server | Multi-server (cloud + local) |
| PaaS | Dokploy (default) | Coolify or Dokploy (context-driven) |
| Network | Local / LAN | Identity-aware proxy + tunnel |
| Domain | Optional | Required for Coolify, optional for Dokploy |
| Monitoring | OTel collector baseline + uptime UX | OTel node agents + cloud gateway + VictoriaMetrics/Grafana add-on |
| Identity | TinyAuth (optional) | TinyAuth (always on) + PocketID (optional) |
| Use Cases | Basic services | Full 10-scenario add-on ecosystem |

## See Also

- [`base-kit`](../base-kit/) - Single-environment homelab (1..N servers) with Dokploy
- [`ha-kit`](../ha-kit/) - High-availability homelab (planned)
- [`addons/`](../addons/) - Composable add-on ecosystem

## License

Apache-2.0
