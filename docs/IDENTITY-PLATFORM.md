# Platform Identity Architecture (kombifySphere / SaaS)

> Identity, multi-tenancy, and federation for the kombify cloud platform.

**Last Updated**: 2026-04-17
**Scope**: This document covers the identity model for the **kombify SaaS platform** — kombifySphere, kombifyAPI, multi-tenancy, and cloud ↔ homelab federation. For the identity stack **within homelabs** (deployed by StackKits), see [IDENTITY-STACKKITS.md](IDENTITY-STACKKITS.md).

> **Status**: **CLOSED 2026-04-15** — Auth0 selected as platform IdP. Cutover from Zitadel completed 2026-04-15. Dual-client pattern live (regular_web for user login via Auth.js, non_interactive for M2M). JWT validation moved from Kong to Cloudflare Edge.

---

## 1. Context: Where SaaS Identity Meets Homelab Identity

kombify-TechStack (PocketBase) operates in two modes:

| Mode | Identity Source | How Users Authenticate |
|------|----------------|----------------------|
| **Self-hosted** | PocketID (local OIDC) + LLDAP | Passkeys via PocketID, groups from LLDAP |
| **Cloud/managed** | Platform IdP (TBD) | OIDC via platform IdP, federated to PocketBase |

The self-hosted identity model is fully defined in [IDENTITY-STACKKITS.md](IDENTITY-STACKKITS.md). This document covers the **cloud mode** and the boundary where platform identity meets local identity.

---

## 2. Platform IdP Decision (CLOSED 2026-04-15)

**Selected: Auth0.** Cutover from Zitadel completed 2026-04-15. All kombify frontends now authenticate via Auth0 OIDC/PKCE through Auth.js; all backend services validate JWTs at the Cloudflare Edge instead of inside Kong OSS.

### Decision outcome

| Option | Verdict |
|--------|---------|
| **Auth0** | ✅ **Selected** — meets all decision criteria, managed OIDC/PKCE, WebAuthn-ready, multi-tenant via organizations |
| Clerk | Rejected — vendor lock-in, cost at scale for kombify's growth model |
| PocketID (cloud instance) | Rejected — not designed for multi-tenant SaaS (stays in homelabs as local IdP) |
| Custom (PocketBase auth) | Rejected — limited OIDC features, no federation |
| Zitadel (previous) | Replaced — operational complexity exceeded value at kombify scale |

### Dual-client pattern (live since 2026-04-15)

Auth0 is configured with **two application clients** to cleanly separate human and machine flows:

| Client type | Auth0 "Application Type" | Used by | Flow |
|---|---|---|---|
| User login | `regular_web` | All kombify frontends (via Auth.js in SvelteKit) | OIDC + PKCE |
| M2M (service-to-service) | `non_interactive` | Internal services, CI, node agents | OAuth2 client credentials |

### JWT validation at Cloudflare Edge

JWT verification was moved from Kong OSS to **Cloudflare Edge** (Workers + Access). Kong OSS is no longer a production ingress. New routes go directly onto Cloudflare — see `kombify Core/standards/API-COMMUNICATION-ARCHITECTURE.md` for the current pattern.

### Implementation references

- Frontend: Auth.js config per SvelteKit app.
- Backend: `pkg/auth/auth0/` validates Auth0-issued JWTs; legacy Kong header-trust (`pkg/auth/kong/`) is deprecated and should be removed where still present.
- Secrets: `doppler secrets -p kombify-io -c prd --plain AUTH0_*`.

### Decision criteria (all met)

- ✅ OIDC with standard claims (roles, groups, org)
- ✅ Passkeys (WebAuthn) — Auth0 native support
- ✅ Multi-tenancy via Auth0 Organizations
- ✅ Federates with local PocketID in homelabs (via TinyAuth broker — see §3)

---

## 3. Identity Flow: Cloud ↔ Homelab

### User authenticates in cloud mode

```
User → kombifySphere UI → Platform IdP (OIDC login)
                              │
                              ▼
                         OIDC token (with org, role, groups)
                              │
                              ▼
                    kombifyAPI (validates JWT, injects X-User-ID, X-Org-ID)
                              │
                              ▼
                    kombify-TechStack (PocketBase)
                    → findOrCreateUser(external_id)
                    → maps org roles to local roles
```

### Homelab agents connect to cloud

```
kombify-TechStack agent (in homelab) → mTLS cert (from Step-CA)
                                      │
                                      ▼
                               kombifyAPI (validates client cert)
                                      │
                                      ▼
                               Maps cert SAN to tenant + homelab instance
```

### Federation: cloud user accesses homelab directly

```
User → Cloudflare Tunnel → Traefik (homelab)
                              │
                              ▼
                         TinyAuth (ForwardAuth)
                              │
                    ┌─────────┴─────────┐
                    ▼                   ▼
              PocketID (local)    Platform IdP (cloud)
              [local users]       [SaaS users]
                    │                   │
                    └─────────┬─────────┘
                              ▼
                    TinyAuth resolves identity
                    → LLDAP groups for authorization
```

TinyAuth acts as the federation broker: it can accept OIDC tokens from either the local PocketID or the platform IdP, and maps both to LLDAP groups for consistent authorization.

---

## 4. Role Model Across Tools

### SaaS roles (kombifySphere)

| Role | Scope | Permissions |
|------|-------|------------|
| USER | Organization | Access own homelabs / projects |
| MANAGER | Organization | Billing, team members, plans |
| ADMIN | Organization | Full access at org level |

### Homelab roles (kombify-TechStack)

| Role | Scope | Permissions |
|------|-------|------------|
| owner | One homelab | Full access |
| operator | One homelab | Deploy, update, monitor, backup |
| developer | One homelab | Deploy, logs, exec |
| viewer | One homelab | Read-only dashboards and logs |

### Internal operations (kombifyAdmin)

| Role | Scope | Purpose |
|------|-------|---------|
| support | Fleet-wide | Read access to tenant data for support |
| ops | Fleet-wide | Operational actions across tenants |

**Mapping rule**: SaaS roles determine **which homelabs** a user can access. Homelab roles determine **what they can do** within a specific homelab. These are separate concerns.

All roles appear as standardized claims (`role`, `org_role`, `lab_role`) in OIDC/JWT tokens.

---

## 5. Multi-Tenancy

### Tenant structure

```
Organization (kombifySphere)
  └── Project / Tenant
        ├── kombify-TechStack instance (homelab)
        ├── kombify Simulate instance (optional)
        └── StackKits catalog (optional)
```

### Tenant isolation

* **kombifyAPI** enforces tenant boundaries via `X-Org-ID` / `tenant_id` in all requests.
* **PocketBase** has `tenant_id` fields on core collections (stacks, nodes, jobs).
* **Database-level isolation**: all queries scoped by `owner_id` or `tenant_id` collection rules.

### Trust domains

| Domain | Scope |
|--------|-------|
| `cloud.kombify.io` | SaaS / kombifySphere / kombifyAPI |
| `homelab.local` or custom | Local kombify-TechStack instances |
| Per-environment domains | Optional dev / stage / prod split |

---

## 6. Service Accounts & API Clients

### Service accounts per tool

| Service Account | Purpose | Auth Method |
|----------------|---------|-------------|
| kombify-TechStack workers | Execute provisioning jobs | mTLS (Step-CA) |
| kombify Simulate orchestrator | Run simulations | mTLS or OAuth2 client credentials |
| CI/CD pipelines | Automated StackKit validation | OAuth2 client credentials |
| Monitoring agents | Push metrics/logs | mTLS |

### Scope model

Fine-grained scopes for API access:
- `stackkits:read`, `stackkits:write`
- `labs:operate`, `labs:read`
- `simulations:run`
- Each automation gets only the minimal required scopes.

### Token lifecycle

* Short-lived access tokens (minutes to hours).
* Certificate-based mechanisms preferred over refresh tokens.
* Policy for regular rotation of client secrets and certificates.

---

## 7. Audit, Logging & Compliance

### Central audit logs

Log security-relevant events:
- Logins, token issuance, mTLS handshakes
- Rollout actions, configuration changes, permission changes
- Aggregation in kombifyAdmin for fleet-wide analysis

### Traceability

- Correlation IDs and user/service IDs in all logs (propagated by kombifyAPI).
- Ability to reconstruct a tenant's complete change history for a homelab.

---

## 8. Identity Lifecycle

### Provisioning

- Automated user and role creation during onboarding (invite flows from kombifySphere).
- SaaS user creation triggers automatic PocketBase user provisioning in the linked homelab.

### Changes & reassignment

- Role changes in the SaaS layer can be mirrored to local LLDAP groups (optional sync).
- Team moves update group memberships in both cloud and local layers.

### Offboarding

- Immediate token invalidation at the platform IdP.
- Removal of roles and group memberships in both cloud and LLDAP.
- Optional archival of activity for compliance.

---

## 9. Open Questions

1. ~~**Platform IdP selection**: Auth0 vs. managed alternative vs. custom.~~ **RESOLVED 2026-04-15** — Auth0 selected, cutover completed. See §2.
2. **Federation protocol**: How exactly does TinyAuth broker between Auth0 and local PocketID? Needs spec (target: V6 Phase 3 ADR-0009 3-tier-provisioning reference).
3. **Provisioning direction**: Does the cloud push users into LLDAP, or does the homelab pull? Needs decision.
4. **Billing integration**: How do SaaS roles (MANAGER) tie to billing providers? Out of scope for StackKits.
5. **Environments**: How many separate Auth0 tenants per dev/stage/prod? Currently: single tenant with environment-prefixed clients. Revisit if isolation requirements change.
