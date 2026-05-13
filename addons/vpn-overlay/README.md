# VPN Overlay Add-On (Optional)

> **Note:** This addon is **optional**. The identity stack (LLDAP + Step-CA + PocketID + TinyAuth) 
> is the **recommended approach** for secure multi-node communication and zero-trust access.

Provides secure VPN mesh networking for multi-node homelab deployments.

## When to Use VPN Overlay

Consider this addon if you:
- Need direct node-to-node communication without identity proxying
- Have strict network isolation requirements  
- Want to connect legacy systems that don't support modern auth
- Prefer WireGuard-based mesh topology

## Overview

The VPN Overlay add-on creates a private network that connects all your homelab nodes, regardless of their physical location. This enables:

- **Secure communication** between nodes across different networks
- **Service discovery** via MagicDNS (node1.tailnet, node2.tailnet, etc.)
- **Subnet routing** to access local networks on remote nodes
- **Exit node** capability for internet access via specific nodes

## Supported Providers

| Provider | Self-Hosted | Cloud | Best For |
|----------|-------------|-------|----------|
| **Headscale** | ✅ Yes | ❌ No | Full control, privacy-focused |
| **Tailscale** | ❌ No | ✅ Yes | Easy setup, excellent UX |
| **NetBird** | ✅ Yes | ✅ Yes | Modern alternative |
| **ZeroTier** | ✅ Yes | ✅ Yes | Large networks |

## Quick Start

### Option 1: Self-Hosted Headscale

1. **Deploy Headscale server** on your primary node:

```bash
# Add vpn-overlay addon to your stack
kombify stack add-addon vpn-overlay --provider headscale

# Generate the configuration
kombify stack generate

# Deploy Headscale
cd stack/addons/vpn-overlay
docker compose up -d headscale
```

2. **Create a namespace and auth key**:

```bash
docker exec headscale headscale namespaces create default
docker exec headscale headscale preauthkeys create --namespace default --reusable
# Copy the auth key output
```

3. **Configure other nodes**:

```bash
# On each additional node
export TS_AUTHKEY="your-auth-key"
export TS_LOGIN_SERVER="https://hs.yourdomain.com"

docker compose up -d tailscale
```

### Option 2: Tailscale Cloud

1. **Get an auth key** from [Tailscale Admin Console](https://login.tailscale.com/admin/settings/keys)

2. **Deploy on each node**:

```bash
export TS_AUTHKEY="tskey-auth-xxxxx"
docker compose -f tailscale-client-compose.yaml up -d
```

## Configuration

### CUE Configuration

```cue
import "kombify.dev/addons/vpn-overlay"

addons: {
    "vpn-overlay": vpnoverlay.#Config & {
        provider: "headscale"
        
        headscale: {
            serverUrl: "https://hs.example.com"
            authKey:   "secret://vpn/headscale-auth"
            namespace: "homelab"
        }
        
        network: {
            advertiseRoutes: ["192.168.1.0/24"]
            acceptRoutes:    true
            exitNode:        false
        }
    }
}
```

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `TS_AUTHKEY` | Pre-auth key for unattended setup | Required |
| `TS_LOGIN_SERVER` | Headscale URL (empty for Tailscale.com) | Empty |
| `TS_HOSTNAME` | Hostname for this node | System hostname |
| `TS_ROUTES` | Comma-separated subnets to advertise | Empty |
| `TS_ACCEPT_ROUTES` | Accept routes from other nodes | `true` |
| `TS_EXIT_NODE` | Enable exit node mode | `false` |
| `TS_TAGS` | ACL tags (Tailscale only) | Empty |

## Architecture

```
┌──────────────────────────────────────────────────────────────┐
│                    VPN Mesh Overlay                          │
│  ┌─────────────┐    ┌─────────────┐    ┌─────────────┐      │
│  │  Node 1     │────│  Headscale  │────│  Node 3     │      │
│  │  (Home)     │    │  (Primary)  │    │  (Cloud)    │      │
│  │  10.0.1.x   │    │  100.64.x.x │    │  10.0.3.x   │      │
│  └─────────────┘    └─────────────┘    └─────────────┘      │
│        │                   │                   │             │
│        │    100.64.0.0/10  │     WireGuard     │             │
│        └───────────────────┴───────────────────┘             │
│                                                              │
│  ┌─────────────┐                                             │
│  │  Node 2     │    Routes: 192.168.1.0/24 via Node 1       │
│  │  (Office)   │            172.16.0.0/24 via Node 2        │
│  │  172.16.x.x │            10.0.3.0/24 via Node 3          │
│  └─────────────┘                                             │
└──────────────────────────────────────────────────────────────┘
```

## StackKit Integration

### Modern Homelab

The VPN overlay is **required** for Modern Homelab deployments:

```yaml
# stack-spec.yaml
stackkit: modern-homelab
addons:
  vpn-overlay:
    enabled: true
    provider: headscale
```

### High Availability Kit

Optional but recommended for HA deployments:

```yaml
# stack-spec.yaml
stackkit: ha-kit
addons:
  vpn-overlay:
    enabled: true
    network:
      advertiseRoutes:
        - "10.10.0.0/16"  # Docker overlay network
```

## Service Discovery

With MagicDNS enabled, services are accessible via:

- `node1.your-tailnet.ts.net` - Node's VPN address
- `grafana.node1.your-tailnet.ts.net` - Service on specific node

For Headscale, configure your DNS:

```yaml
# /etc/headscale/config.yaml
dns_config:
  magic_dns: true
  base_domain: homelab.local
  nameservers:
    - 1.1.1.1
```

## Security Considerations

1. **Protect auth keys** - Store in secrets manager, not in code
2. **Use ACL tags** (Tailscale) - Restrict what nodes can access
3. **Enable MFA** on your coordination server admin access
4. **Regular key rotation** - Create time-limited auth keys
5. **Audit connections** - Monitor Headscale logs for unauthorized access

## Troubleshooting

### Node won't connect

```bash
# Check tailscale status
docker exec tailscale tailscale status

# Check logs
docker logs tailscale

# Verify auth key is valid
docker exec headscale headscale preauthkeys list --namespace default
```

### DNS not resolving

```bash
# Test MagicDNS
docker exec tailscale tailscale ip -4
docker exec tailscale tailscale dns status

# Check Headscale DNS config
docker exec headscale headscale debug dns
```

### Routes not working

```bash
# List advertised routes
docker exec tailscale tailscale status --json | jq '.Peer[].AllowedIPs'

# Enable route on Headscale
docker exec headscale headscale routes enable --route <route-id>
```

## Related Addons

- **monitoring** - Prometheus metrics for VPN status
- **backup** - Backup Headscale database
- **ci-cd** - Deploy VPN changes via GitOps

## References

- [Headscale Documentation](https://headscale.net/)
- [Tailscale Documentation](https://tailscale.com/kb)
- [WireGuard Protocol](https://www.wireguard.com/)
