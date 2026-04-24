# kombify.me Integration Guide

> For coding agents and developers integrating StackKits or kombify-TechStack services with kombify.me.

## What is kombify.me?

kombify.me is a wildcard subdomain tunnel service — the kombify equivalent of traefik.me or ngrok. It gives any homelab service a permanent, publicly reachable HTTPS URL at `*.kombify.me` without requiring the user to manage domains, certificates, or port forwarding.

**Architecture:**
- **Gateway** (`kombify-me-gateway`, Docker container on VPS): receives all `*.kombify.me` traffic via Cloudflare Tunnel, manages tunnel registry
- **Agent** (binary on homelab/VPS): opens a WebSocket control channel to the gateway, forwards HTTP requests to local services

---

## Subdomain Naming Convention

### Auto-registered (via API)

| Kind | Pattern | Example |
|------|---------|---------|
| SaaS base | `{homelab}-{userid}.kombify.me` | `mylab-u8f3k2.kombify.me` |
| SaaS service | `{homelab}-{userid}-{service}.kombify.me` | `mylab-u8f3k2-grafana.kombify.me` |
| Self-hosted base | `sh-{homelab}-{fingerprint}.kombify.me` | `sh-mylab-a1b2c3.kombify.me` |
| Self-hosted service | `sh-{homelab}-{fingerprint}-{service}.kombify.me` | `sh-mylab-a1b2c3-grafana.kombify.me` |

Rules:
- Max 63 characters total
- Lowercase alphanumeric + hyphens only
- Reserved names blocked: `api`, `www`, `admin`, `mail`, `status`, `docs`, `app`, `dashboard`, etc.

---

## API Reference

**Base URL:** `https://kombify.me/_kombify/api`

**Via Cloudflare Edge (kombify-TechStack internal):** `https://api.kombify.io/v1/subdomains/*`

### Register a user account

```bash
curl -X POST {BASE_URL}/register \
  -H "Content-Type: application/json" \
  -d '{"email":"user@example.com","password":"secret","name":"My Lab"}'
```

Response includes `api_key` — **returned only once, store it immediately**.

### Login

```bash
curl -X POST {BASE_URL}/login \
  -H "Content-Type: application/json" \
  -d '{"email":"user@example.com","password":"secret"}'
```

Response includes `token` (JWT) and `user_id`.

### Auto-register a subdomain

```bash
curl -X POST {BASE_URL}/auto-register \
  -H "X-Kombify-API-Key: kbi_<your_api_key>" \
  -H "Content-Type: application/json" \
  -d '{
    "homelab_name": "mylab",
    "kind": "saas",
    "user_id_suffix": "u8f3k2"
  }'
```

`kind` values: `saas` | `selfhosted`

Response:
```json
{
  "subdomain": "mylab-u8f3k2",
  "full_domain": "mylab-u8f3k2.kombify.me",
  "target_type": "tunnel",
  "status": "active"
}
```

### Register a service subdomain

```bash
curl -X POST {BASE_URL}/auto-register-service \
  -H "X-Kombify-API-Key: kbi_<your_api_key>" \
  -H "Content-Type: application/json" \
  -d '{
    "base_subdomain": "mylab-u8f3k2",
    "service_name": "grafana",
    "expose": true
  }'
```

Response includes `full_domain: "mylab-u8f3k2-grafana.kombify.me"`.

### List subdomains

```bash
curl {BASE_URL}/subdomains \
  -H "X-Kombify-API-Key: kbi_<your_api_key>"
```

### Get subdomain info

```bash
curl {BASE_URL}/subdomains/mylab-u8f3k2 \
  -H "X-Kombify-API-Key: kbi_<your_api_key>"
```

### Update subdomain (expose/hide a service)

```bash
curl -X PUT {BASE_URL}/subdomains/mylab-u8f3k2-grafana \
  -H "X-Kombify-API-Key: kbi_<your_api_key>" \
  -H "Content-Type: application/json" \
  -d '{"exposed": true, "status": "active"}'
```

`status` values: `active` | `inactive`

---

## Agent Setup

### Download / build

```bash
# Use a release artifact from the kombify.me deployment you operate, or build the agent from source.
# Building from source requires Go 1.25+
go build -o agent ./cmd/agent
```

### Config file (`agent.yaml`)

```yaml
# Gateway WebSocket endpoint — do NOT include /_kombify/tunnel (agent appends it)
gateway_url: wss://kombify.me

# API key from registration (kbi_ prefix)
api_key: kbi_<your_api_key>

tunnels:
  - subdomain: mylab-u8f3k2         # must already exist in the registry
    local_addr: http://127.0.0.1:80  # where the local service listens
    protocol: http

  - subdomain: mylab-u8f3k2-grafana
    local_addr: http://127.0.0.1:3000
    protocol: http
```

**Critical**: `gateway_url` must NOT include `/_kombify/tunnel` — the agent appends it automatically. Using the wrong URL causes a doubled path and connection failure.

### Run

```bash
./agent -config agent.yaml
```

Expected startup logs:
```
INF connecting to gateway url=wss://.../_kombify/tunnel
INF connected to gateway tunnels=2
```

### Systemd service (Linux)

```ini
# ~/.config/systemd/user/kombify-agent.service
[Unit]
Description=kombify.me Agent
After=network.target

[Service]
ExecStart=/home/user/agent -config /home/user/agent.yaml
Restart=always
RestartSec=10

[Install]
WantedBy=default.target
```

```bash
systemctl --user daemon-reload
systemctl --user enable --now kombify-agent
loginctl enable-linger $USER   # persist after logout
```

---

## How Traffic Flows

```
Browser → https://mylab-u8f3k2.kombify.me/path
  → Cloudflare CDN (wildcard *.kombify.me)
  → Cloudflare Tunnel (cloudflared on VPS)
  → kombify-me-gateway container (port 8080, dokploy-network)
    → looks up subdomain "mylab-u8f3k2" in kombify-DB
    → sends HTTP request over WebSocket tunnel
      → Agent on homelab receives request
      → forwards to http://127.0.0.1:80/path
      → returns response up through tunnel
  → Browser receives response
```

The `Host` header the agent receives is always `{subdomain}.kombify.me` (canonical form). The agent matches requests using `strings.HasPrefix(req.Host, subdomain+".")`.

---

## Security Model

- **API keys**: prefixed `kbi_`, hashed (SHA-256) at rest. Returned only once at registration — not recoverable.
- **Service subdomains**: `exposed: false` by default — traffic returns 403 until explicitly exposed.
- **Body size limit**: 10 MB per tunnelled request.
- **Tunnel timeout**: 30 seconds per request.

---

## StackKit Integration Patterns

### Pattern 1: Tunnel (Agent-Based)

When a StackKit is deployed behind NAT and needs a public URL via tunnel:

1. **On first deploy**: call `/auto-register` with `homelab_name` + `user_id_suffix` derived from the stack's metadata → get base subdomain
2. **For each service**: call `/auto-register-service` with `service_name` matching the StackKit service definition → set `expose: true`
3. **Write `agent.yaml`**: generated from the subdomain list, injected into the homelab as a StackKit config file
4. **Start agent**: run as a systemd user service alongside the stack

The base subdomain (`mylab-u8f3k2.kombify.me`) can serve as the stack's management dashboard URL. Individual services get their own subdomain (`mylab-u8f3k2-grafana.kombify.me`, etc.).

### Pattern 2: Direct Connect (Registry-Based)

When a stackkit-server instance is publicly reachable (e.g., VPS with public IP), the Cloudflare Edge path can proxy directly to it without the tunnel agent.

**How it works:**

1. **On deploy** (`stackkit apply`): if `domain: kombify.me`, the CLI registers the instance with kombify's registry API (`POST /registry/instances`). The instance ID is derived from subdomain prefix + StackKit name + device fingerprint.
2. **Heartbeat**: `stackkit-server` sends a heartbeat every 60 seconds (`PUT /registry/instances/{id}/heartbeat`) so Kong knows the instance is alive.
3. **On shutdown**: `stackkit-server` deregisters itself (`DELETE /registry/instances/{id}`).
4. **Kong routing**: Kong reads the registry and routes `*.kombify.me` traffic directly to the instance's public endpoint.

**stack-spec.yaml:**

```yaml
domain: kombify.me
subdomainPrefix: mylab
```

**Environment variables:**

- `KOMBIFY_API_KEY` — API key for kombify registry (required)
- `STACKKITS_INSTANCE_ID` — Override auto-generated instance ID (optional)

**stackkit-server flags:**

```bash
stackkit-server --instance-id "mylab-base-kit-a1b2c3"
# or via env: STACKKITS_INSTANCE_ID=mylab-base-kit-a1b2c3
```

**Registry API endpoints** (on kombify.me):

| Method | Path | Purpose |
|--------|------|---------|
| `POST` | `/registry/instances` | Register instance |
| `PUT` | `/registry/instances/{id}/heartbeat` | Send heartbeat |
| `DELETE` | `/registry/instances/{id}` | Deregister instance |

**Registration payload:**

```json
{
  "instance_id": "mylab-base-kit-a1b2c3",
  "endpoint_url": "https://api.mylab.kombify.me",
  "stackkit": "base-kit",
  "services": [
    {"name": "traefik", "status": "running"},
    {"name": "dashboard", "url": "https://base.mylab.kombify.me", "status": "running"}
  ],
  "status": "running",
  "api_port": 8082
}
```

**When to use which pattern:**

| Scenario | Pattern | Why |
|----------|---------|-----|
| Homelab behind NAT, no public IP | Tunnel (Agent) | Can't receive inbound connections |
| VPS with public IP | Direct Connect | Lower latency, no tunnel overhead |
| Cloud VM (Hetzner, DO, etc.) | Direct Connect | Public IP available |
| Mixed (local + cloud nodes) | Both | Tunnel for local, Direct Connect for cloud |

---

## Health Check

```bash
curl https://kombify.me/_kombify/health
# {"status":"ok","active_tunnels":N}
```

---

## Hosting

The gateway runs as a Docker container on kombify-managed infrastructure. Internal hostnames and IP addresses are intentionally omitted from this public guide.

| Component | Location | Details |
|-----------|----------|---------|
| **Gateway** | kombify-managed infrastructure | Containerized gateway for HTTPS entrypoint and WebSocket tunnel termination |
| **Database** | Managed PostgreSQL | Stores tunnel registry and account data |
| **DNS/TLS** | Cloudflare | Domain `kombify.me`, wildcard TLS, Cloudflare Tunnel |
| **Tunnel ingress** | cloudflared | Routes `*.kombify.me` traffic to the active gateway |
