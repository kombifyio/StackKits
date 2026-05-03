# StackKits Network Security Architecture

> Defense-in-depth networking and container security for StackKits, from single-environment (1..N co-located servers) to HA cluster.

**Last Updated**: 2026-02-13
**Scope**: This document covers the **network and infrastructure security layer** within StackKits — the controls that protect the transport and runtime environment beneath the identity layer. For identity architecture (OIDC, passkeys, mTLS, RBAC), see [IDENTITY-STACKKITS.md](IDENTITY-STACKKITS.md). For platform architecture, see [ARCHITECTURE_V5.md](ARCHITECTURE_V5.md).

---

## 1. Relationship to Identity & Architecture

### What IDENTITY-STACKKITS.md covers (and this document does not)

Authentication (who you are): passkeys, PocketID, TinyAuth, OIDC flows. Authorization (what you may do): LLDAP groups, RBAC roles. Device trust (are you allowed to connect): mTLS via Step-CA. These are well-defined and architecturally sound.

### What this document covers (the gap)

Network controls (how traffic flows): firewall policy, VLAN segmentation, Docker network isolation, DNS security. Runtime hardening (how containers run): capability drops, read-only filesystems, resource limits, socket protection. Threat detection (what's happening right now): intrusion detection, log aggregation, WAF, vulnerability scanning. Operational security (how secrets and images are managed): encrypted secrets, image scanning, supply chain integrity.

### Design principle: Layer 1 + Layer 2, not Add-Ons

Per Architecture v4, **Add-Ons MUST NOT modify Layer 1 foundation settings**. Network security fundamentals (firewall, SSH hardening, Docker daemon config, container defaults) are Layer 1 concerns and belong in `base/security.cue`. Platform security (Traefik middlewares, Docker network templates, CrowdSec integration) is Layer 2 and belongs in `base/network.cue`.

Only advanced, optional security extensions (SIEM, vulnerability scanning, eBPF runtime monitoring) qualify as Add-Ons because they extend capabilities without modifying the foundation.

```
┌──────────────────────────────────────────────────────────────┐
│  base/security.cue (Layer 1 — always deployed)               │
│  • SSH hardening, firewall policy, Docker daemon config      │
│  • Container security defaults, host OS hardening            │
│  • DNS resolver configuration                                │
├──────────────────────────────────────────────────────────────┤
│  base/network.cue (Layer 2 — always deployed)                │
│  • Traefik security middlewares (headers, rate limits)        │
│  • Docker Compose network templates (frontend/backend/internal)│
│  • Docker socket proxy, CrowdSec bouncer                     │
│  • VLAN-aware network configuration                          │
├──────────────────────────────────────────────────────────────┤
│  addons/ (Layer 2/3 — opt-in extensions)                     │
│  • addons/siem/ — Wazuh SIEM                                 │
│  • addons/vulnerability-scanning/ — Trivy + Nuclei           │
│  • addons/runtime-security/ — Tetragon eBPF                  │
└──────────────────────────────────────────────────────────────┘
```

---

## 2. Threat Model for Self-Hosted Infrastructure

Before defining controls, the threats they address:

| Threat | Attack Vector | Impact | StackKit Relevance |
|--------|--------------|--------|-------------------|
| **Automated scanning** | Shodan, Masscan, botnets probe open ports | Service compromise, crypto mining | All StackKits with public exposure |
| **Docker-UFW bypass** | Docker NAT rules execute before UFW INPUT chain | Containers exposed to internet despite firewall rules | All StackKits using Docker port mapping |
| **Docker socket escalation** | Container mounts `/var/run/docker.sock` | Root-equivalent host access | All StackKits (Traefik, PaaS tools require socket) |
| **Lateral movement** | Compromised container reaches database, admin UI | Data exfiltration, full compromise | Flat Docker networks without isolation |
| **Supply chain attack** | Malicious base image, dependency confusion | Backdoor in running services | All StackKits deploying third-party images |
| **DNS rebinding** | Malicious DNS response points internal IP | Bypasses mTLS, accesses internal services | All StackKits without DNS filtering |
| **OWASP Top 10** | SQLi, XSS, SSRF against web applications | Data breach, service takeover | StackKits with public-facing services |
| **Credential stuffing** | Automated login attempts against exposed UIs | Account takeover | StackKits with password-based auth |

---

## 3. Defense-in-Depth Model

Seven layers, each independently effective, collectively comprehensive:

```
┌─ Layer 7: Monitoring & Response ──────────────────────────┐
│  CrowdSec, Loki, Prometheus, Grafana, Wazuh (HA)         │
├─ Layer 6: Application Security ───────────────────────────┤
│  TinyAuth/PocketID (→ IDENTITY doc), WAF, security headers│
├─ Layer 5: Container Security ─────────────────────────────┤
│  cap_drop ALL, read_only, no-new-privileges, non-root     │
├─ Layer 4: Docker Engine Security ─────────────────────────┤
│  Socket proxy, DOCKER-USER chain, internal networks, icc  │
├─ Layer 3: Host OS Security ───────────────────────────────┤
│  SSH keys only, auto-updates, AppArmor, Lynis             │
├─ Layer 2: Network Segmentation ───────────────────────────┤
│  VLANs, inter-VLAN firewall, Docker network isolation     │
├─ Layer 1: Perimeter ─────────────────────────────────────-┤
│  Firewall, Cloudflare Tunnel, DNS filtering               │
└───────────────────────────────────────────────────────────┘
```

---

## 4. Critical Vulnerabilities (Must-Fix for All StackKits)

These three issues affect every StackKit regardless of pattern or context and must be resolved in `base/security.cue`.

### 4.1 Docker-UFW Bypass

**Problem:** Docker publishes container ports via NAT rules in the PREROUTING and FORWARD chains. These execute *before* UFW's INPUT chain. A `ufw deny 8080` rule does nothing to prevent external access to a container published with `-p 8080:80`.

**Solutions (context-dependent):**

| Context | Recommended Fix | Implementation |
|---------|----------------|----------------|
| `local` | Bind all ports to `127.0.0.1` | `ports: ["127.0.0.1:8080:80"]` in Compose |
| `cloud` | `DOCKER-USER` iptables chain | Allow only Traefik → backend, deny all else |
| `pi` | Bind all ports to `127.0.0.1` | Same as local, lower resource overhead |

**DOCKER-USER chain example (cloud context):**
```bash
# Default deny all external access to Docker containers
iptables -I DOCKER-USER -i eth0 -j DROP
# Allow established connections
iptables -I DOCKER-USER -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
# Allow Traefik ports only
iptables -I DOCKER-USER -i eth0 -p tcp --dport 80 -j ACCEPT
iptables -I DOCKER-USER -i eth0 -p tcp --dport 443 -j ACCEPT
```

### 4.2 Docker Socket Exposure

**Problem:** Traefik (and PaaS tools like Dokploy/Coolify) require access to the Docker socket for service discovery. Mounting `/var/run/docker.sock` directly grants root-equivalent access to the host. A vulnerability in Traefik or the PaaS could compromise the entire node.

**Solution:** Deploy a filtering socket proxy that exposes only read-only API endpoints.

```yaml
# docker-compose template for all StackKits
services:
  socket-proxy:
    image: tecnativa/docker-socket-proxy:latest
    restart: unless-stopped
    environment:
      CONTAINERS: 1        # Traefik needs container listing
      NETWORKS: 1          # Traefik needs network info
      SERVICES: 1          # Swarm service discovery (High Availability Kit)
      TASKS: 1             # Swarm task info (High Availability Kit)
      POST: 0              # Block all write operations
      BUILD: 0
      COMMIT: 0
      CONFIGS: 0
      DISTRIBUTION: 0
      EXEC: 0              # Critical: block exec into containers
      GRPC: 0
      IMAGES: 0
      INFO: 1
      NODES: 0
      PLUGINS: 0
      SECRETS: 0
      SESSION: 0
      SWARM: 0
      SYSTEM: 0
      VOLUMES: 0
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
    networks:
      - socket-proxy
    security_opt:
      - "no-new-privileges:true"
    cap_drop:
      - ALL
    read_only: true
    tmpfs:
      - /run

  traefik:
    # ...
    environment:
      DOCKER_HOST: tcp://socket-proxy:2375
    depends_on:
      - socket-proxy
    networks:
      - socket-proxy
      - frontend
    # NO docker.sock volume mount
```

### 4.3 Missing Container Hardening Defaults

**Problem:** Without explicit security constraints, containers run with full Linux capabilities, writable filesystems, and unrestricted resources — a single container escape grants broad host access.

**Solution:** Define hardened defaults in `base/security.cue` that apply to all StackKit-managed containers:

```yaml
# Container security defaults (base/security.cue)
x-security-defaults: &security-defaults
  security_opt:
    - "no-new-privileges:true"
  cap_drop:
    - ALL
  # cap_add only what's specifically needed per service
  user: "1000:1000"         # Non-root, override per service if needed
  read_only: true
  tmpfs:
    - "/tmp:rw,noexec,nosuid,size=64m"
  deploy:
    resources:
      limits:
        memory: 512M
        cpus: '1.0'
        pids: 100

# Per-service overrides where necessary
services:
  traefik:
    <<: *security-defaults
    cap_add:
      - NET_BIND_SERVICE    # Needed for ports 80/443
    user: "1000:1000"
    read_only: true
    tmpfs:
      - "/tmp:rw,noexec,nosuid,size=64m"

  lldap:
    <<: *security-defaults
    # No additional capabilities needed

  step-ca:
    <<: *security-defaults
    # No additional capabilities needed
```

---

## 5. Docker Daemon Hardening

Applied via `/etc/docker/daemon.json` during Layer 1 provisioning (OpenTofu):

```json
{
  "icc": false,
  "no-new-privileges": true,
  "userland-proxy": false,
  "log-driver": "json-file",
  "log-opts": {
    "max-size": "10m",
    "max-file": "3"
  },
  "live-restore": true,
  "default-address-pools": [
    {
      "base": "172.20.0.0/14",
      "size": 24
    }
  ]
}
```

| Setting | Purpose |
|---------|---------|
| `icc: false` | Disables inter-container communication on the default bridge. Containers can only communicate through explicit Docker network links. |
| `no-new-privileges: true` | Prevents privilege escalation inside containers via setuid/setgid binaries. |
| `userland-proxy: false` | Uses iptables for port forwarding instead of a userland process, improving performance and reducing attack surface. |
| `live-restore: true` | Containers keep running during Docker daemon restarts (important for updates). |
| `default-address-pools` | Uses a non-default IP range to avoid conflicts with homelab VLANs and VPN subnets. |

**High Availability Kit addition:** For Docker Swarm clusters, add:
```json
{
  "iptables": true,
  "ip-forward": true,
  "swarm-default-advertise-addr": "eth0"
}
```

---

## 6. Docker Compose Network Templates

All StackKits use a standard network layout that isolates traffic by function:

```yaml
networks:
  # Public-facing: Traefik ↔ Application frontends
  frontend:
    driver: bridge

  # Internal-only: Application ↔ Database/Cache (no internet access)
  backend:
    driver: bridge
    internal: true          # Critical: blocks all egress

  # Internal-only: Socket proxy ↔ Traefik (isolated from everything else)
  socket-proxy:
    driver: bridge
    internal: true

  # Internal-only: Monitoring stack (Prometheus ↔ exporters)
  monitoring:
    driver: bridge
    internal: true
```

**Network assignment pattern:**
```yaml
services:
  traefik:
    networks: [frontend, socket-proxy]

  web-app:
    networks: [frontend, backend]

  database:
    networks: [backend]             # Only reachable from backend

  redis:
    networks: [backend]             # Only reachable from backend

  socket-proxy:
    networks: [socket-proxy]        # Only reachable by Traefik
```

**High Availability Kit extension:** Swarm overlay networks with encryption:
```yaml
networks:
  frontend:
    driver: overlay
    driver_opts:
      encrypted: "true"             # IPsec encryption between nodes
  backend:
    driver: overlay
    driver_opts:
      encrypted: "true"
    internal: true
```

---

## 7. Traefik Security Middlewares

Since Traefik is the integration point for all StackKits (see IDENTITY-STACKKITS.md §3), security middlewares are defined centrally and applied to all routes.

### 7.1 Security Headers

```yaml
# Traefik dynamic config or labels
http:
  middlewares:
    security-headers:
      headers:
        stsSeconds: 31536000
        stsIncludeSubdomains: true
        stsPreload: true
        contentTypeNosniff: true
        frameDeny: true
        browserXssFilter: false          # Deprecated, do not enable
        referrerPolicy: "strict-origin-when-cross-origin"
        permissionsPolicy: "camera=(), microphone=(), geolocation=(), payment=()"
        customResponseHeaders:
          Cross-Origin-Opener-Policy: "same-origin"
          Cross-Origin-Resource-Policy: "same-origin"
          X-Permitted-Cross-Domain-Policies: "none"
```

**Note:** `X-XSS-Protection`, `Expect-CT`, and `Public-Key-Pins` are deprecated and should not be used.

### 7.2 Rate Limiting

```yaml
    rate-limit:
      rateLimit:
        average: 100                    # Requests per second
        burst: 200
        period: 1s
```

### 7.3 CrowdSec Bouncer (IDS + WAF)

CrowdSec serves dual purposes: behavioral IDS (log analysis + crowd-sourced threat intel) and, since v1.6+, application-level WAF via the AppSec component. This replaces the need for a separate WAF tool.

```yaml
# Traefik static config
experimental:
  plugins:
    crowdsec-bouncer:
      modulename: github.com/maxlerebourg/crowdsec-bouncer-traefik-plugin
      version: v1.4.5

# Traefik dynamic config
http:
  middlewares:
    crowdsec:
      plugin:
        crowdsec-bouncer:
          enabled: true
          crowdsecLapiHost: crowdsec:8080
          crowdsecLapiKey: "${CROWDSEC_BOUNCER_KEY}"
          crowdsecMode: stream           # Recommended: cached decisions
          crowdsecAppsecEnabled: true     # WAF via AppSec component
          crowdsecAppsecHost: crowdsec:7422
          crowdsecAppsecFailureBlock: true
          crowdsecAppsecUnreachableBlock: true
```

**CrowdSec container configuration:**
```yaml
services:
  crowdsec:
    image: crowdsecurity/crowdsec:latest
    restart: unless-stopped
    environment:
      COLLECTIONS: >-
        crowdsecurity/traefik
        crowdsecurity/linux
        crowdsecurity/sshd
        crowdsecurity/http-cve
        crowdsecurity/appsec-virtual-patching
        crowdsecurity/appsec-generic-rules
      CUSTOM_HOSTNAME: "${HOSTNAME}"
    volumes:
      - crowdsec-config:/etc/crowdsec
      - crowdsec-data:/var/lib/crowdsec/data
      - traefik-logs:/var/log/traefik:ro
    networks:
      - socket-proxy                    # To receive Traefik logs
    security_opt:
      - "no-new-privileges:true"
    cap_drop:
      - ALL
    cap_add:
      - NET_BIND_SERVICE
```

### 7.4 Middleware Chain (Recommended Order)

```
Request → Traefik
    → [1] crowdsec (IP reputation + AppSec WAF)
    → [2] rate-limit
    → [3] security-headers
    → [4] tinyauth (ForwardAuth → PocketID → LLDAP)
    → Backend service
```

For public anonymous services (e.g., landing pages), the TinyAuth middleware is omitted. For admin UIs, mTLS is added as an additional layer before the chain.

---

## 8. VLAN Segmentation

### 8.1 Reference VLAN Layout

Applicable when Node-Context is `local` and the operator has a managed switch and a VLAN-capable router/firewall (OPNsense recommended).

| VLAN | ID | Subnet | Purpose | Firewall Policy |
|------|----|--------|---------|----------------|
| Management | 10 | 10.10.10.0/24 | Switch, router, IPMI, OPNsense | Inbound: admin devices only |
| Trusted | 20 | 10.10.20.0/24 | Workstations, admin laptops | Full internal access |
| Services | 30 | 10.10.30.0/24 | Docker hosts, NAS | Inbound: Traefik ports, SSH from mgmt |
| IoT | 40 | 10.10.40.0/24 | Smart home, cameras | Outbound: DNS + WAN only, no RFC1918 |
| DMZ | 50 | 10.10.50.0/24 | Public-facing reverse proxy | Inbound: 80/443 from WAN, outbound: services VLAN |
| Guest | 60 | 10.10.60.0/24 | Guest WiFi | Internet only, client isolation |
| Backup | 70 | 10.10.70.0/24 | Backup server/NAS target | Inbound: backup agents only |

**Default policy:** Deny all inter-VLAN traffic. Explicit allow rules only for defined flows.

### 8.2 Context-Dependent Network Security

Not every context supports VLANs. The network security posture adapts:

| Context | Network Isolation Strategy |
|---------|--------------------------|
| `local` | Full VLAN segmentation (if hardware supports it), Docker network isolation, host firewall |
| `cloud` | Provider security groups/firewall, Docker network isolation, DOCKER-USER chain |
| `pi` | Docker network isolation, host firewall (UFW). VLANs possible but rarely practical on Pi hardware. |

---

## 9. DNS Security

DNS is the most frequently overlooked attack vector in homelabs. DNS rebinding attacks can bypass mTLS entirely by resolving a public hostname to an internal IP.

### Recommended Stack

```
Client → AdGuard Home (filtering + local rewrites)
            → Unbound (recursive resolver, DNSSEC validation)
                → Root DNS servers (no third-party dependency)
```

| Component | Purpose | Context |
|-----------|---------|---------|
| **AdGuard Home** | Ad/malware blocking, local DNS rewrites (*.homelab.example.com → internal IPs), DNS rebinding protection | All contexts |
| **Unbound** | Recursive DNS resolver with DNSSEC validation, eliminates dependency on upstream DNS providers | `local`, `cloud` |
| **Cloudflare DoH** | Upstream fallback when recursive resolution is impractical | `pi` (resource-constrained) |

**Critical setting:** Enable DNS rebinding protection in AdGuard Home to block DNS responses containing private IP ranges from external queries.

### kombination.yaml integration

```yaml
# kombination.yaml
services:
  dns:
    provider: adguard-home          # Default for all contexts
    upstream: unbound-recursive     # local/cloud default
    # upstream: cloudflare-doh      # pi context default
    dnssec: true
    rebinding_protection: true
    local_rewrites: true            # *.${domain} → internal IPs
```

---

## 10. Host OS Hardening

Applied during Layer 1 provisioning via OpenTofu, managed by `base/security.cue`.

### SSH Hardening

```yaml
# /etc/ssh/sshd_config (applied via OpenTofu)
ssh:
  permit_root_login: false
  password_authentication: false     # Key-only
  pubkey_authentication: true
  max_auth_tries: 3
  x11_forwarding: false
  allow_agent_forwarding: false
  client_alive_interval: 300
  client_alive_count_max: 2
```

### Automatic Updates

```yaml
# Unattended-upgrades (Debian/Ubuntu)
auto_updates:
  enabled: true
  security_only: true               # Only security patches
  auto_reboot: false                 # Operator controls reboot timing
  notification: email                # or ntfy
```

### Firewall (Context-Dependent)

| Context | Tool | Configuration |
|---------|------|--------------|
| `local` | UFW + ufw-docker | Default deny incoming, allow SSH from mgmt VLAN, allow 80/443 on DMZ interface |
| `cloud` | UFW + DOCKER-USER chain | Default deny, allow SSH from known IPs, allow 80/443 |
| `pi` | UFW + bind-to-localhost | Default deny, allow SSH from LAN, all container ports bound to 127.0.0.1 |

### Fail2ban (Immediate SSH Protection)

Deployed alongside CrowdSec as a lightweight, immediate SSH defense:

```yaml
# /etc/fail2ban/jail.local
fail2ban:
  sshd:
    enabled: true
    maxretry: 3
    bantime: 3600
    findtime: 600
```

CrowdSec replaces Fail2ban for HTTP-layer protection but Fail2ban remains for direct SSH brute-force defense until CrowdSec is fully operational.

---

## 11. Secrets Management

### Recommended: SOPS + age

SOPS (Secrets OPerationS) with age encryption is the best fit for StackKits because it's Git-native (encrypted values live in the repository), requires no server component, and aligns with the declarative CUE/OpenTofu workflow.

```bash
# Encrypt secrets in kombination.yaml or .env files
sops --encrypt --age age1ql3z7hjy54pw3hyww5ayyfg7zqgvc7w3j2elw8zmrj2kg5sfn9aqmcac8p \
  secrets.yaml > secrets.enc.yaml

# Decrypt for provisioning
sops --decrypt secrets.enc.yaml > secrets.yaml
```

**Key hierarchy:**

| Secret Type | Managed By | Encryption |
|-------------|-----------|------------|
| Infrastructure secrets (DB passwords, API keys) | SOPS + age | Encrypted in Git |
| TLS certificates (inter-service mTLS) | Step-CA | Auto-issued, short-lived |
| OIDC signing keys | PocketID | Generated at deploy |
| CrowdSec bouncer keys | CrowdSec CLI | Generated at deploy, stored encrypted |
| Backup encryption keys | age | Offline storage (not in Git) |

### Pre-commit protection

```bash
# .pre-commit-config.yaml
repos:
  - repo: https://github.com/gitleaks/gitleaks
    hooks:
      - id: gitleaks
```

Gitleaks scans every commit for accidentally committed secrets (API keys, passwords, tokens) before they enter the repository.

---

## 12. StackKit × Context Security Matrix

The combination of StackKit pattern and Node-Context produces specific security configurations. Each cell inherits all controls from lighter configurations.

### Base Kit

| Control | `local` | `cloud` | `pi` |
|---------|---------|---------|------|
| **Firewall** | UFW + bind-to-localhost | UFW + DOCKER-USER chain | UFW + bind-to-localhost |
| **Docker socket** | Socket proxy (Tecnativa) | Socket proxy (Tecnativa) | Socket proxy (Tecnativa) |
| **Docker daemon** | icc:false, no-new-privileges | icc:false, no-new-privileges | icc:false, no-new-privileges |
| **Container defaults** | Full hardening template | Full hardening template | Reduced memory limits (256M) |
| **Networks** | frontend + backend (internal) | frontend + backend (internal) | frontend + backend (internal) |
| **SSH** | Key-only, Fail2ban | Key-only, Fail2ban | Key-only, Fail2ban |
| **Security headers** | Full set via Traefik | Full set via Traefik | Full set via Traefik |
| **IDS** | CrowdSec + Traefik bouncer | CrowdSec + Traefik bouncer | CrowdSec (no AppSec, RAM) |
| **DNS** | AdGuard Home + Unbound | AdGuard Home + Unbound | AdGuard Home + Cloudflare DoH |
| **Auto-updates** | unattended-upgrades | unattended-upgrades | unattended-upgrades |
| **Secrets** | SOPS + age | SOPS + age | SOPS + age |
| **Monitoring** | Uptime Kuma | Uptime Kuma | Uptime Kuma |

### Modern Homelab (inherits all Base Kit controls, adds:)

| Control | `local` | `cloud` |
|---------|---------|---------|
| **VLANs** | Full layout (§8.1) if hardware supports | Provider security groups |
| **WAF** | CrowdSec AppSec (virtual patching + CRS) | CrowdSec AppSec |
| **CrowdSec collections** | + http-cve, http-dos, suricata (if OPNsense) | + http-cve, http-dos |
| **CrowdSec bouncers** | Traefik plugin + firewall bouncer | Traefik plugin + firewall bouncer |
| **DNS** | AdGuard Home → Unbound (recursive, DNSSEC) | AdGuard Home → Unbound |
| **Monitoring** | Prometheus + Grafana + Loki + Promtail | Prometheus + Grafana + Loki |
| **Image scanning** | Trivy (scan on pull, fail on CRITICAL) | Trivy |
| **Secrets** | SOPS + age + Gitleaks pre-commit | SOPS + age + Gitleaks |
| **Tunnel security** | Cloudflare Tunnel → Traefik (no open ports) | Direct Traefik (minimal ports) |
| **VPN** | Headscale/Tailscale for admin access | Headscale/Tailscale |
| **Alerting** | ntfy (self-hosted push) | ntfy |

### High Availability Kit (inherits all Modern Homelab controls, adds:)

| Control | `local` | `cloud` |
|---------|---------|---------|
| **Docker runtime** | Rootless Docker or Sysbox | Rootless Docker |
| **Swarm security** | Encrypted overlay networks, auto-lock | Encrypted overlay, auto-lock |
| **WAF mode** | CrowdSec AppSec enforcing (paranoia level 2) | AppSec enforcing |
| **Network IDS** | Suricata on OPNsense → CrowdSec feed | Provider IDS + CrowdSec |
| **SIEM** | Wazuh (FIM, rootkit detection, MITRE ATT&CK) | Wazuh |
| **Runtime security** | Tetragon eBPF (optional Add-On) | Tetragon eBPF |
| **Vulnerability scanning** | Trivy (daily) + Nuclei (weekly) | Trivy + Nuclei |
| **Image signing** | Cosign (key-based) | Cosign |
| **Supply chain** | SBOM generation (Syft), approved base images only | SBOM, approved images |
| **Policy enforcement** | OPA + Conftest (no root, no privileged, limits required) | OPA + Conftest |
| **Backup security** | Encrypted, immutable, VLAN-isolated, monthly restore test | Encrypted, S3 versioning |
| **Incident response** | Automated IP blocking, container isolation | Automated response |

---

## 13. Tool Reference

### Always deployed (Layer 1 + 2)

| Tool | Purpose | Resource Impact | License |
|------|---------|----------------|---------|
| **Tecnativa docker-socket-proxy** | Filters Docker API access for Traefik/PaaS | ~5MB RAM | Apache-2.0 |
| **CrowdSec** | IDS/IPS with crowd-sourced threat intel (40K+ agents) | ~100MB RAM | MIT |
| **CrowdSec Traefik Plugin** | Traefik middleware for IP blocking + AppSec WAF | Negligible (in-process) | MIT |
| **AdGuard Home** | DNS filtering, rebinding protection, local rewrites | ~50MB RAM | GPL-3.0 |
| **Fail2ban** | SSH brute-force protection | ~30MB RAM | GPL-2.0 |
| **Uptime Kuma** | Service availability monitoring | ~100MB RAM | MIT |
| **SOPS + age** | Secrets encryption (CLI tools, no daemon) | 0 (CLI only) | MPL-2.0 / BSD-3 |

### Modern Homelab additions

| Tool | Purpose | Resource Impact | License |
|------|---------|----------------|---------|
| **Unbound** | Recursive DNS resolver with DNSSEC | ~30MB RAM | BSD-3 |
| **Prometheus** | Metrics collection | ~200MB RAM | Apache-2.0 |
| **Grafana** | Metrics visualization + dashboards | ~200MB RAM | AGPL-3.0 |
| **Loki + Promtail** | Log aggregation + shipping | ~200MB RAM | AGPL-3.0 |
| **Trivy** | Container image vulnerability scanning | 0 (CI/scan-time) | Apache-2.0 |
| **ntfy** | Self-hosted push notifications | ~20MB RAM | Apache-2.0 / GPL-2.0 |
| **OPNsense** | Router/firewall with Suricata IDS | Dedicated hardware | BSD-2 |

### High Availability Kit additions

| Tool | Purpose | Resource Impact | License |
|------|---------|----------------|---------|
| **Wazuh** | SIEM: FIM, rootkit detection, MITRE mapping | 6–8GB RAM minimum | GPL-2.0 |
| **Tetragon** | eBPF runtime security (kernel-level monitoring) | ~100MB RAM | Apache-2.0 |
| **Nuclei** | Vulnerability scanning (6K+ templates) | 0 (scan-time) | MIT |
| **Cosign** | Container image signing (Sigstore) | 0 (CI-time) | Apache-2.0 |
| **Syft** | SBOM generation | 0 (CI-time) | Apache-2.0 |
| **OPA + Conftest** | Policy-as-Code for Dockerfiles/Compose | 0 (CI-time) | Apache-2.0 |

All tools are SaaS-safe (open-source licenses compatible with commercial distribution).

---

## 14. kombination.yaml Security Block

The security section of `kombination.yaml` provides the user-facing configuration:

```yaml
# kombination.yaml — security section
version: "2.0"
stackkit: modern                       # base | modern | ha
context: local                         # local | cloud | pi

security:
  # Layer 1: Foundation
  firewall:
    enabled: true                      # Default: true
    docker_fix: auto                   # auto | bind_localhost | docker_user_chain
  ssh:
    password_auth: false               # Default: false
    root_login: false                  # Default: false
  auto_updates:
    enabled: true                      # Default: true
    security_only: true                # Default: true
  docker_daemon:
    icc: false                         # Default: false
    no_new_privileges: true            # Default: true
  container_defaults:
    read_only: true                    # Default: true
    cap_drop_all: true                 # Default: true
    non_root: true                     # Default: true
    resource_limits: true              # Default: true

  # Layer 2: Platform
  socket_proxy:
    enabled: true                      # Default: true
  crowdsec:
    enabled: true                      # Default: true
    appsec: true                       # Default: true (Modern + HA)
  security_headers:
    enabled: true                      # Default: true
    hsts: true                         # Default: true
  dns:
    provider: adguard-home             # Default: adguard-home
    upstream: auto                     # auto | unbound | cloudflare-doh
    rebinding_protection: true         # Default: true

  # Layer 2: Monitoring (Modern + HA)
  monitoring:
    stack: auto                        # auto | minimal | full
    alerting: ntfy                     # ntfy | email | none

  # Advanced (HA only)
  image_scanning:
    enabled: false                     # Default: false (true for HA)
    fail_on: [CRITICAL]
  secrets:
    tool: sops-age                     # Default: sops-age
    gitleaks: false                    # Default: false (true for Modern+)
```

**CUE resolution:** The `auto` values are resolved by the CUE engine based on StackKit × Context. For example, `docker_fix: auto` resolves to `bind_localhost` for `local`/`pi` contexts and `docker_user_chain` for `cloud`.

---

## 15. CUE Schema Locations

These are the files within the StackKits repository that implement this security architecture:

```
github.com/kombifyio/stackkits/
├── base/
│   ├── security.cue                   # L1: SSH, firewall, Docker daemon, container defaults
│   ├── network.cue                    # L2: Traefik middlewares, Docker networks, CrowdSec
│   └── observability.cue              # L2: Monitoring stack selection
│
├── contexts/
│   ├── local.cue                      # local: UFW + bind-to-localhost, VLAN support
│   ├── cloud.cue                      # cloud: DOCKER-USER chain, security groups
│   └── pi.cue                         # pi: reduced limits, Cloudflare DoH
│
├── addons/
│   ├── siem/                          # Wazuh SIEM (High Availability Kit, optional)
│   │   └── addon.cue
│   ├── vulnerability-scanning/        # Trivy + Nuclei
│   │   └── addon.cue
│   └── runtime-security/             # Tetragon eBPF
│       └── addon.cue
│
├── base-kit/
│   └── defaults.cue                   # Base Kit security presets
├── modern-homelab/
│   └── defaults.cue                   # Modern Homelab security presets (inherits Base)
└── ha-kit/
    └── defaults.cue                   # High Availability Kit security presets (inherits Modern)
```

---

## 16. Implementation Roadmap

### Phase 1: Foundation (Week 1)

These seven items block >90% of automated attacks and should be implemented first, regardless of StackKit pattern.

| # | Task | Time | Impact |
|---|------|------|--------|
| 1 | SSH key-only auth, disable root login | 30 min | Eliminates brute-force SSH access |
| 2 | UFW + Docker-UFW fix (bind-to-localhost or DOCKER-USER) | 1–2 hrs | Closes the most common Docker exposure |
| 3 | unattended-upgrades (security patches) | 15 min | Auto-patches known vulnerabilities |
| 4 | Docker daemon hardening (daemon.json) | 30 min | icc:false, no-new-privileges system-wide |
| 5 | Docker socket proxy for Traefik | 1 hr | Eliminates root-equivalent socket exposure |
| 6 | Container hardening defaults (all Compose files) | 1–2 hrs | cap_drop ALL, read_only, non-root |
| 7 | CrowdSec + Traefik bouncer plugin | 1–2 hrs | Behavioral IDS + crowd-sourced IP blocking |

### Phase 2: Platform Security (Weeks 2–3)

| # | Task | StackKit |
|---|------|---------|
| 8 | Security headers middleware (Traefik) | All |
| 9 | Docker Compose network isolation (frontend/backend/internal) | All |
| 10 | AdGuard Home + DNS rebinding protection | All |
| 11 | Fail2ban for SSH | All |
| 12 | Uptime Kuma monitoring | All |
| 13 | CrowdSec AppSec (WAF) | Modern + HA |
| 14 | Prometheus + Grafana + Loki | Modern + HA |
| 15 | SOPS + age secrets integration | Modern + HA |

### Phase 3: Hardening (Month 2)

| # | Task | StackKit |
|---|------|---------|
| 16 | VLAN segmentation (if local context + managed switch) | Modern + HA |
| 17 | Unbound recursive DNS with DNSSEC | Modern + HA |
| 18 | Trivy image scanning in CI/deploy pipeline | Modern + HA |
| 19 | Gitleaks pre-commit hooks | Modern + HA |
| 20 | OPNsense + Suricata (if local context) | Modern + HA |
| 21 | ntfy alerting integration | Modern + HA |
| 22 | Headscale/Tailscale for admin access | Modern + HA |

### Phase 4: Zero Trust (Month 3+)

| # | Task | StackKit |
|---|------|---------|
| 23 | Rootless Docker or Sysbox runtime | HA |
| 24 | Swarm encrypted overlay networks + auto-lock | HA |
| 25 | OPA + Conftest policy enforcement | HA |
| 26 | Wazuh SIEM deployment | HA |
| 27 | Tetragon eBPF runtime monitoring | HA |
| 28 | Cosign image signing + Syft SBOM | HA |
| 29 | Nuclei weekly vulnerability scans | HA |
| 30 | 3-2-1 backup strategy with encryption + immutability | HA |
| 31 | Automated incident response (IP blocking, container isolation) | HA |

---

## 17. Integration with Identity Architecture

This document and IDENTITY-STACKKITS.md together form the complete security posture:

```
NETWORK-SECURITY-STACKKITS.md          IDENTITY-STACKKITS.md
(this document)                        (identity layer)
─────────────────────────              ─────────────────────
Perimeter defense                      WHO authenticates
Network segmentation                   HOW they authenticate
Container isolation                    WHAT they're authorized to do
Runtime hardening                      Device trust (mTLS)
Threat detection                       Session management
DNS security                           RBAC groups
Secrets management                     Disaster recovery (identity)
Image supply chain                     Auth mode configuration
```

**Shared components:**

| Component | Network Security Role | Identity Role |
|-----------|----------------------|---------------|
| **Traefik** | Security headers, rate limiting, CrowdSec bouncer | ForwardAuth middleware (TinyAuth) |
| **Step-CA** | mTLS between services (internal PKI) | Device trust certificates |
| **CrowdSec** | Behavioral IDS, IP blocking, WAF | Brute-force protection for login endpoints |
| **Docker networks** | Traffic isolation | Identity services on isolated backend network |

---

## Appendix A: Security Add-On Schemas

### addons/siem/addon.cue

```cue
#AddOn: {
    metadata: {
        name:        "siem"
        version:     "1.0.0"
        description: "Wazuh SIEM: file integrity monitoring, rootkit detection, MITRE ATT&CK mapping"
        author:      "kombify"
    }
    compatibility: {
        stackkits: ["ha"]
        contexts:  ["local", "cloud"]
        requires:  ["monitoring"]
        conflicts: []
    }
    resources: {
        minMemoryMB: 6144
        minCPUCores: 4
        requiresGPU: false
    }
}
```

### addons/vulnerability-scanning/addon.cue

```cue
#AddOn: {
    metadata: {
        name:        "vulnerability-scanning"
        version:     "1.0.0"
        description: "Trivy image scanning + Nuclei vulnerability scanning"
        author:      "kombify"
    }
    compatibility: {
        stackkits: ["base", "modern", "ha"]
        contexts:  ["local", "cloud"]
        requires:  []
        conflicts: []
    }
    resources: {
        minMemoryMB: 512
        minCPUCores: 1
        requiresGPU: false
    }
}
```

### addons/runtime-security/addon.cue

```cue
#AddOn: {
    metadata: {
        name:        "runtime-security"
        version:     "1.0.0"
        description: "Tetragon eBPF runtime security monitoring"
        author:      "kombify"
    }
    compatibility: {
        stackkits: ["modern", "ha"]
        contexts:  ["local", "cloud"]
        requires:  ["monitoring"]
        conflicts: []
    }
    resources: {
        minMemoryMB: 256
        minCPUCores: 1
        requiresGPU: false
    }
}
```

---

## Appendix B: Implementation Status

| Component | In base/security.cue | In base/network.cue | Deployed | Priority |
|-----------|---------------------|---------------------|----------|----------|
| Docker daemon hardening | ❌ | — | ❌ | P0 |
| Container security defaults | ❌ | — | ❌ | P0 |
| Docker socket proxy | — | ❌ | ❌ | P0 |
| Docker-UFW bypass fix | ❌ | — | ❌ | P0 |
| Security headers middleware | — | ❌ | ❌ | P0 |
| CrowdSec + Traefik bouncer | — | ❌ | ❌ | P0 |
| Docker network templates | — | ❌ | ❌ | P1 |
| AdGuard Home + Unbound | ❌ | — | ❌ | P1 |
| SOPS + age integration | ❌ | — | ❌ | P1 |
| Trivy scanning | — | — | ❌ (Add-On) | P2 |
| Wazuh SIEM | — | — | ❌ (Add-On) | P3 |
| Tetragon eBPF | — | — | ❌ (Add-On) | P3 |

### Priority gaps (aligned with IDENTITY-STACKKITS.md Appendix)

1. **Docker socket proxy** — Immediate dependency for Traefik security across all StackKits.
2. **Docker-UFW bypass fix** — Without this, firewall rules are ineffective for containerized services.
3. **Container hardening defaults** — Template for all StackKit-managed services.
4. **CrowdSec integration** — Behavioral IDS + WAF in one tool, native Traefik plugin.
5. **Security headers** — Trivial to implement, closes entire class of browser-based attacks.
6. **Docker network isolation** — Prevents lateral movement between containers.

---

## Related Documents

- [IDENTITY-STACKKITS.md](IDENTITY-STACKKITS.md) — Identity architecture (OIDC, passkeys, mTLS, RBAC)
- [ARCHITECTURE_V5.md](ARCHITECTURE_V5.md) — StackKits architecture (current), 3-layer model, Use Case vs Add-On
- [IDENTITY-PLATFORM.md](IDENTITY-PLATFORM.md) — kombifySphere/SaaS identity model
