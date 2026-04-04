# StackKits Identity Architecture

> Zero-trust identity for homelabs, deployed as part of the StackKit foundation layer.

**Last Updated**: 2026-02-11
**Scope**: This document covers the identity architecture **within StackKits** — the services deployed into a homelab. For the kombify platform/SaaS identity model (kombifySphere, kombifyAPI, multi-tenancy), see [IDENTITY-PLATFORM.md](IDENTITY-PLATFORM.md).

---

## 1. Strategic Goals & Core Principles

Establish a local-first identity infrastructure that combines strong defaults (passkeys, mTLS, zero-trust) with **intentionally unrestricted capabilities** for advanced users.

**Core principles:**

* **Strong defaults, not hard limitations**
  * StackKits ship with **secure-by-default presets** (passkeys only, mTLS, no password login, no open ports).
  * These are recommendations and starting points, **not hard constraints**.
* **User sovereignty over security posture**
  * Advanced users and operators can **intentionally downgrade** security if their use case requires it.
  * This includes allowing user+password authentication, disabling MFA, or exposing services more openly — as long as this is an explicit, conscious choice in configuration.
* **Passkey-first for humans**
  * Recommended default: passwordless login via **Passkeys (WebAuthn)** through PocketID.
* **Secret-free for agents**
  * Recommended default: workloads authenticate via certificates (mTLS), not static passwords or API keys.
* **Local-first, cloud-optional**
  * The identity stack runs entirely within the homelab. Cloud federation (kombifySphere) is an optional add-on, not a requirement.

---

## 2. Component Stack

| Component | Layer | Role | Protocol |
|-----------|-------|------|----------|
| **LLDAP** | 1 (Foundation) | Directory service, source of truth for users & groups | LDAP |
| **Step-CA** | 1 (Foundation) | PKI & certificate authority for mTLS | SCEP, ACME, x509 |
| **Traefik** | 2 (Platform) | Reverse proxy, TLS termination, auth middleware host | HTTP/HTTPS |
| **TinyAuth** | 2 (Platform) | Identity broker & auth proxy (Traefik ForwardAuth middleware) | OIDC, ForwardAuth |
| **PocketID** | 2 (Platform) | Local OIDC provider with passkey/WebAuthn support | OIDC, WebAuthn |
| **PocketBase** | Control Plane | kombify-TechStack app backend, OIDC consumer | OIDC client |

### How the components relate

```
┌─────────────────────────────────────────────────────────────┐
│  Layer 1: Foundation (always deployed)                      │
│  ┌──────────┐  ┌──────────┐                                 │
│  │  LLDAP   │  │ Step-CA  │                                 │
│  │ (users & │  │ (certs & │                                 │
│  │  groups) │  │  mTLS)   │                                 │
│  └────┬─────┘  └────┬─────┘                                 │
│       │              │                                      │
├───────┼──────────────┼──────────────────────────────────────┤
│  Layer 2: Platform (opt-in identity)                        │
│       │              │                                      │
│  ┌────▼─────┐  ┌─────▼────┐   ┌───────────────────┐        │
│  │ PocketID │◄─┤ TinyAuth │◄──┤    Traefik         │        │
│  │ (passkey │  │ (forward │   │ (ForwardAuth       │        │
│  │  OIDC)   │  │  auth)   │   │  middleware)       │        │
│  └────┬─────┘  └──────────┘   └─────────┬─────────┘        │
│       │                                 │                   │
├───────┼─────────────────────────────────┼───────────────────┤
│  Control Plane                          │                   │
│  ┌────▼──────────────┐                  │                   │
│  │ PocketBase        │                  │                   │
│  │ (kombify-TechStack)   │◄─── behind ──────┘                   │
│  │ OIDC client of    │                                      │
│  │ PocketID          │                                      │
│  └───────────────────┘                                      │
└─────────────────────────────────────────────────────────────┘
```

---

## 3. Layer Integration via Traefik

Both Dokploy and Coolify deploy Traefik as their built-in reverse proxy. The identity stack hooks into this existing Traefik instance — **no separate proxy is needed**.

### Traefik as the integration point

Traefik is the natural foundation for the identity stack because:

1. **It's already there.** Every StackKit (base-kit, modern-homelab, ha-kit) includes Traefik via the PaaS tool (Dokploy or Coolify).
2. **ForwardAuth middleware.** TinyAuth registers as a Traefik `forwardauth` middleware. Any service can be protected by adding a single label: `traefik.http.routers.myapp.middlewares=tinyauth`.
3. **PaaS-agnostic.** Whether the user chooses Dokploy, Coolify, Dockge, or Portainer — Traefik is the common denominator. Identity works the same regardless of PaaS choice.

### Middleware chain

```
Request → Traefik → [mTLS check (step-ca cert)] → [TinyAuth ForwardAuth] → Backend service
                                                          │
                                                          ▼
                                                    PocketID (OIDC)
                                                          │
                                                          ▼
                                                    LLDAP (groups/roles)
```

For public-facing services, the mTLS check is optional. For internal admin UIs, both layers are recommended.

---

## 4. Identity Types & Flows

### A. Human identities (homelab users)

* **Primary login (recommended default)**: Passkeys (WebAuthn) via PocketID.
* **Login flow**:
  1. User visits a protected service behind Traefik.
  2. Traefik's ForwardAuth calls TinyAuth.
  3. TinyAuth redirects to PocketID for OIDC login.
  4. PocketID authenticates via passkey (WebAuthn).
  5. PocketID returns OIDC token with user info + LLDAP group claims.
  6. TinyAuth sets session, Traefik forwards the request with identity headers.
* **Recovery strategy (recommended)**:
  * At least two registered passkeys (e.g. smartphone + hardware key), **or**
  * Encrypted recovery codes stored outside the homelab.

### B. Agent identities (workloads & M2M)

* **Standard model**: Certificate-based trust via Step-CA.
* **Flow**:
  1. Agent (kombify-TechStack worker, CI/CD, monitoring) requests a certificate from Step-CA.
  2. Access to target service is performed via mTLS using this certificate.
* **Benefits**:
  * No static passwords or API keys in configuration files.
  * Short-lived certificates with automated rotation.

### C. kombify-TechStack ↔ Homelab identity bridge

kombify-TechStack (PocketBase) manages the control plane. Homelab services use the identity stack. The bridge:

* **PocketBase as OIDC client of PocketID**:
  * In self-hosted mode, PocketBase registers as an OIDC client of PocketID.
  * Users log into kombify-TechStack via PocketID → passkey → OIDC token.
  * kombify-TechStack users ARE homelab users — same identity, same groups.
* **User provisioning flow**:
  1. User is created in LLDAP (source of truth for the homelab).
  2. PocketID syncs users from LLDAP (LDAP integration).
  3. User logs into kombify-TechStack via PocketID OIDC → PocketBase finds-or-creates the user.
  4. LLDAP group memberships (`owner`, `operator`, `developer`, `viewer`) map to kombify-TechStack roles.
* **Cloud/SaaS mode alternative**:
  * When kombify-TechStack runs in cloud mode (managed by kombifySphere), the OIDC provider is the platform IdP instead of PocketID.
  * The flow is the same, but the identity source is remote. See [IDENTITY-PLATFORM.md](IDENTITY-PLATFORM.md).

---

## 5. Security Architecture (Zero-Trust)

All settings are **defaults** — users can adjust any setting to match their needs.

### Device trust (mTLS)

* External access to the homelab (recommended default) requires a client certificate issued via SCEP (Step-CA).
* Without a valid certificate, the reverse proxy (Traefik) silently drops the connection.
* For local-only homelabs, mTLS can be disabled if the user accepts the trade-off.

### Identity trust (OIDC)

* After successful device validation, the user authenticates via passkey through PocketID.
* OIDC tokens contain roles and group information via LLDAP claims.
* PocketBase (kombify-TechStack) receives and validates these tokens as an OIDC client.

### RBAC via groups

* Permissions are modeled as **LLDAP groups**.
* LLDAP is the source of truth for roles and groups.
* Groups are propagated as `groups` claims in OIDC tokens to PocketBase and all other homelab services.
* Standard roles:

| Role | Permissions | LLDAP Group |
|------|------------|-------------|
| **owner** | Full access to the homelab | `homelab_owner` |
| **operator** | Deploy, update, monitor, backup | `homelab_operator` |
| **developer** | Deploy, logs, exec | `homelab_developer` |
| **viewer** | Read-only dashboards and logs | `homelab_viewer` |

---

## 6. Disaster Recovery & Emergency Plan

### Emergency admin

* Dedicated local admin account in TinyAuth (username+password, not passkey-dependent).
* Access restricted to a local management VLAN (IP allow-listing / firewall rules).

### Offline mode

* If PocketID is unreachable, TinyAuth can fall back to local LLDAP accounts with password authentication.
* If the internet is down and the user runs cloud mode, TinyAuth fails over to local LLDAP for critical infrastructure maintenance.
* kombify-TechStack (PocketBase) can always be accessed via its local email/password auth as a last resort.

### Backups & key management

* Daily automated backups of:
  * LLDAP database (SQLite or PostgreSQL).
  * Step-CA keys and PKI metadata.
  * PocketID database.
  * PocketBase database (kombify-TechStack data).
* Stored in an encrypted vault (offline backup or dedicated backup server).
* Regular restore tests to verify the emergency path works.

---

## 7. Secret & Key Management

### Secrets for applications

* Recommended: integrate a secret store (e.g. Vault, SOPS+Git, or cloud secret managers) that works with the PKI (Step-CA).
* Operators should see as few plain-text secrets as possible — ideally only encrypted values.

### Key hierarchies

| Key Type | Managed By | Scope |
|----------|-----------|-------|
| PKI root / intermediates | Step-CA | Infrastructure-wide |
| Service certificates | Step-CA (auto-issued) | Per workload / agent |
| OIDC signing keys | PocketID | Identity tokens |
| User passkeys | FIDO2 device (user-side) | Per user, never leaves device |
| Session secrets | TinyAuth, PocketBase | Per application |

---

## 8. Access Paths (Tunnels, VPN & mTLS)

Optional and advanced scenarios for external access — in addition to the standard mTLS + OIDC model.

### 8.1 Cloudflare Tunnels as "secure edge"

**Goal:** Make homelab services reachable from the internet without router port forwarding.

* **Tunnel in front of Traefik**: Cloudflare Tunnel connects to the internal Traefik instance. Cloudflare is the transport layer — device and identity trust remain in the homelab.
* **Tunnel to single services**: Dedicated tunnel endpoints for specific services (e.g. monitoring dashboard). Useful for demo or support scenarios.

**Recommended combined flow:**
1. Cloudflare Tunnel → forward to Traefik.
2. mTLS validation (Step-CA device certificate).
3. OIDC login (passkey) via TinyAuth → PocketID.

### 8.2 VPN access

VPN is an **additional network path**, not a replacement for identity checks.

* **Classic VPN**: WireGuard / OpenVPN provides IP-level access. Identity checks (mTLS + OIDC) still apply.
* **Identity-aware VPN**: VPN solutions that support OIDC can use the same PocketID/TinyAuth landscape. VPN claims (groups, roles) map to network policies (which subnets/ports are reachable).

### 8.3 Configurable access profiles

Each homelab can define its own access profile:

| Profile | Components | Use Case |
|---------|-----------|----------|
| `local-only` | Traefik + TinyAuth (LAN only) | Air-gapped or local-only labs |
| `tunnel-only` | + Cloudflare Tunnel | Remote access without VPN |
| `vpn-only` | + WireGuard/Tailscale | Full network access for operators |
| `vpn-plus-tunnel` | Both | Production multi-user setups |
| `full-zero-trust` | All + mTLS enforcement | Maximum security |

---

## 9. Public Services from the Homelab

### Service protection levels

| Type | Example | Protection |
|------|---------|-----------|
| Public anonymous | Marketing website, landing page | Edge protection (WAF, rate limits) |
| Public authenticated | Customer dashboard, self-service | OIDC login via TinyAuth |
| Non-public admin | kombify-TechStack UI, PocketBase admin, monitoring | mTLS + OIDC + optional VPN |

### Architecture for public hosting

```
Internet → Edge (Cloudflare/CDN) → Tunnel/Proxy → Traefik → Service
                                                      │
                                            mTLS (Step-CA) for internals
                                            OIDC (PocketID) for users
```

### Standards for local servers

* Local servers are **not** automatically "trusted" just because they live in the homelab.
* Network segmentation: `mgmt`, `apps`, `db`, `dmz` zones.
* Public services live in `dmz` with restricted access to the rest.
* No direct router port forwards. Exposure only via edge proxy or tunnel.
* Admin UIs never protected by password-only — always OIDC + RBAC.

### Dual certificate layers

* **Public certificate** — for edge traffic (Let's Encrypt via ACME or Cloudflare cert).
* **Internal mTLS certificate** — from Step-CA for backend communication between services.

---

## 10. Auth Modes (Password Fallback & MFA)

While the recommended standard is **passkeys + mTLS**, real-world scenarios sometimes require classic logins. kombify **does not technically restrict them**.

### Supported auth modes

| Mode | Default | Description |
|------|---------|-------------|
| `passkeys_only` | **Yes** | No password, only FIDO2/passkey via PocketID. Recommended. |
| `passkeys_plus_legacy` | No | Passkeys + user/password + optional TOTP as fallback. |
| `password_only` | No | Fully supported as conscious downgrade. Marked as high-risk. |

### Configuration in StackKits

```yaml
# kombination.yaml example
security:
  auth_mode: passkeys_only          # default
  allow_password_login: false       # default
  require_mfa_for_password: true    # if password is enabled
```

These parameters control which PocketID/TinyAuth features and flows are enabled.

### Migration paths

StackKits offer guided migrations:
1. `password_only` → `passkeys_plus_legacy` → `passkeys_only`
2. Steps: enable passkey registration alongside passwords → enforce MFA → disable new password registrations.

### Role-based guidance

* For `owner` and `operator` roles: presets suggest passkey-only or strong MFA.
* For `viewer`: password login may be acceptable (operator's explicit choice).
* The system recommends but **never blocks** any configuration.

### Audit & monitoring

* Password login attempts are tagged separately in logs.
* Brute-force patterns can trigger captcha, lockout, or enforced passkey registration.
* Passkeys + mTLS is consistently promoted as the target architecture in docs and UI.

---

## Appendix: Implementation Status

| Component | CUE Schema | Terraform Template | Seed Data | Active in Stacks |
|-----------|-----------|-------------------|-----------|-----------------|
| LLDAP | ✅ `base/identity.cue` | ✅ `base/identity/_lldap.tf.tmpl` | ✅ | base-kit |
| Step-CA | ✅ `base/identity.cue` | ✅ `base/identity/_step-ca.tf.tmpl` | ✅ | base-kit (disabled) |
| TinyAuth | ✅ `base/layers.cue` | ✅ `templates/simple/main.tf` | ✅ | base-kit |
| PocketID | ✅ `base/layers.cue` | ❌ Missing | ✅ | Disabled everywhere |
| Traefik | ✅ via PaaS config | ✅ via Dokploy/Coolify | ✅ | All stacks |
| PocketBase OIDC→PocketID | ❌ Not yet | N/A (kombify-TechStack code) | N/A | Not implemented |

### Priority gaps

1. **TinyAuth Terraform template** — needed to deploy TinyAuth via StackKit engine.
2. **PocketID Terraform template** — needed to deploy PocketID via StackKit engine.
3. **PocketBase→PocketID OIDC** — kombify-TechStack needs OIDC client config for PocketID (self-hosted mode).
4. **Enable PocketID by default** — if passkeys are the recommended auth, the passkey IdP must be on.
5. **Identity config in modern-homelab and ha-kit** — currently only base-kit has identity blocks.
