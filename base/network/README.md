# Network Templates

> Teil der **IaC-First Architektur** von kombify-TechStack

## Zweck

Dieses Verzeichnis enthält OpenTofu-Templates für die Netzwerk-Konfiguration der Nodes. Dies umfasst sowohl die Host-Netzwerk-Konfiguration als auch Container-/VM-Netzwerke.

## Network Modes

kombify-TechStack unterstützt drei Netzwerk-Modi, die über `network.mode` in der `kombination.yaml` gewählt werden:

### 1. Local Mode (`_local.tf.tmpl`)

**Use Case:** Homelab nur im LAN erreichbar, keine öffentlichen Dienste.

**Features:**
- Docker Bridge Network mit konfigurierbarem Subnet
- Optional: mDNS/Avahi für `.local` Domain Discovery
- Kein Reverse Proxy - direkte Port-Mappings
- Optional: Lokaler DNS via Kombify Point Container
- IPv6 Support (optional)

**Outputs:**
- `network_config` - Netzwerk-Details
- `dns_config` - DNS-Konfiguration
- `mdns_config` - mDNS/Avahi Status
- `service_endpoints` - Service URLs für LAN-Zugriff

### 2. Public Mode (`_public.tf.tmpl`)

**Use Case:** Services sollen aus dem Internet erreichbar sein.

**Features:**
- Traefik v3 Reverse Proxy
- Automatische Let's Encrypt SSL-Zertifikate (HTTP oder DNS Challenge)
- HTTP→HTTPS Redirect
- Rate Limiting Middleware
- Security Headers (XSS, HSTS, etc.)
- Fail2Ban Integration
- Prometheus Metrics

**Outputs:**
- `network_config` - Netzwerk-Details
- `traefik_info` - Proxy-Informationen
- `public_urls` - Öffentliche Service-URLs
- `ssl_config` - SSL/TLS Details
- `security_config` - Security Features

### 3. Hybrid Mode (`_hybrid.tf.tmpl`) 🆕

**Use Case:** Mischung aus öffentlichen und internen Services.

**Features:**
- **Zwei getrennte Netzwerke:** Public (Internet) + Internal (nur LAN/VPN)
- Traefik für öffentliche Services
- Split-Horizon DNS via CoreDNS
- VPN Integration:
  - **Tailscale:** Zero-config Mesh VPN
  - **WireGuard:** Self-hosted VPN
- IP-Whitelist Middleware für interne Services
- Netzwerk-Routing zwischen Zonen

**Outputs:**
- `network_config` - Alle Netzwerk-Details
- `dns_config` - Split-DNS Konfiguration
- `vpn_config` - VPN-Status und Endpoint
- `public_services` - Internet-erreichbare Services
- `internal_services` - Nur intern erreichbare Services
- `security_zones` - Zone-Definitionen

## Was gehört hierher?

- **Host-Networking**: IP-Konfiguration, Routing, DNS
- **Docker Networks**: Overlay-Netzwerke, Bridge-Konfiguration
- **Firewall-Regeln**: iptables/nftables via OpenTofu
- **Load-Balancing**: Traefik/HAProxy-Konfiguration
- **VPN/Tunnel**: WireGuard, Tailscale-Integration
- **DNS**: Kombify Point/CoreDNS-Konfiguration

## IaC-First Prinzip

Der kombify-TechStack-Agent führt **keine Shell-Commands direkt** aus. Stattdessen:

1. Netzwerk-Konfiguration wird deklarativ in OpenTofu definiert
2. Agent führt `tofu apply` aus
3. Änderungen sind reproducible und können gerollt werden

## Template-Files

```
network/
├── README.md              # Diese Dokumentation
├── _local.tf.tmpl         # LAN-only Modus
├── _public.tf.tmpl        # Public Internet Modus
└── _hybrid.tf.tmpl        # Hybrid (Public + Internal)
```

## Template-Variablen (aus UnifiedSpec)

### Gemeinsame Variablen

| Variable | Typ | Default | Beschreibung |
|----------|-----|---------|--------------|
| `docker_network_name` | string | "kombify-TechStack" | Basis-Name für Docker-Netzwerke |
| `network_subnet` | string | varies | CIDR Subnet |
| `network_gateway` | string | varies | Gateway IP |

### Local Mode

| Variable | Typ | Default | Beschreibung |
|----------|-----|---------|--------------|
| `enable_mdns` | bool | false | mDNS/Avahi aktivieren |
| `enable_local_dns` | bool | false | Kombify Point aktivieren |
| `local_domain` | string | "local" | Lokale Domain |
| `dns_upstream` | list(string) | ["1.1.1.1", "8.8.8.8"] | Upstream DNS |
| `service_ports` | map(object) | {} | Port-Mappings |
| `custom_dns_records` | map(string) | {} | Custom DNS A-Records |
| `ipv6_enabled` | bool | false | IPv6 Support |

### Public Mode

| Variable | Typ | Default | Beschreibung |
|----------|-----|---------|--------------|
| `domain` | string | required | Haupt-Domain |
| `email` | string | required | Let's Encrypt Email |
| `enable_ssl` | bool | true | SSL aktivieren |
| `ssl_staging` | bool | false | LE Staging Server |
| `traefik_version` | string | "v3.0" | Traefik Version |
| `rate_limit_average` | number | 100 | Requests/Sekunde |
| `rate_limit_burst` | number | 200 | Max Burst |
| `cloudflare_enabled` | bool | false | DNS Challenge |
| `services` | map(object) | {} | Service-Definitionen |

### Hybrid Mode

| Variable | Typ | Default | Beschreibung |
|----------|-----|---------|--------------|
| `public_subnet` | string | "172.22.0.0/16" | Public Network |
| `internal_subnet` | string | "172.23.0.0/16" | Internal Network |
| `internal_domain` | string | "internal" | Interne Domain |
| `vpn_enabled` | bool | false | VPN aktivieren |
| `vpn_type` | string | "tailscale" | "tailscale" oder "wireguard" |
| `split_dns_enabled` | bool | true | Split-Horizon DNS |
| `tailscale_authkey` | string | "" | Tailscale Auth Key |
| `wireguard_port` | number | 51820 | WG Port |
| `wireguard_peers` | list(object) | [] | WG Peers |

## Beispiel: Service exponieren

### Local Mode

```yaml
# kombination.yaml
network:
  mode: local
  enable_mdns: true
  service_ports:
    grafana:
      internal: 3000
      external: 3000
```

### Public Mode

```yaml
# kombination.yaml
network:
  mode: public
  domain: example.com
  email: admin@example.com
  services:
    grafana:
      subdomain: grafana
      port: 3000
```

### Hybrid Mode

```yaml
# kombination.yaml
network:
  mode: hybrid
  domain: example.com
  internal_domain: home.lan
  vpn:
    enabled: true
    type: tailscale
  public_services:
    nextcloud:
      subdomain: cloud
      port: 80
  internal_services:
    homeassistant:
      port: 8123
```

## Security Best Practices

1. **Local Mode:** Nur für isolierte Homelabs ohne Internetzugang
2. **Public Mode:** Immer SSL aktiviert lassen, Rate Limiting nutzen
3. **Hybrid Mode:** 
   - VPN für Remote-Zugriff auf interne Services
   - Keine direkten Port-Forwards für interne Services
   - Split-DNS verhindert DNS-Leaks

## Abhängigkeiten

- Erfordert abgeschlossene `bootstrap/`-Phase
- Wird vor `lifecycle/`-Phase ausgeführt
- Docker muss auf dem Node installiert sein
