# Identity Module

This module provides identity and PKI services for StackKits across Layer 1 (Foundation) and Layer 2 (Platform). PocketID is the default Homelab identity authority for human users; LLDAP is compatibility tooling for LDAP-aware services.

## Layer 1: Foundation And Compatibility Services

### LLDAP (Lightweight LDAP)

A simplified LDAP server for services that still need LDAP semantics. It is not the default source of truth for Homelab users in the PocketID-first path.

**Features:**
- LDAP/LDAPS ports for service authentication
- Web UI for administration
- Lightweight and easy to configure
- Compatible with most LDAP-aware applications

**Default Configuration:**
- Web UI: `http://lldap.stack.local:17170`
- LDAP: `ldap://localhost:3890`
- LDAPS: `ldaps://localhost:6360`
- Admin user: `admin`

**Ports:**
| Port | Service |
|------|---------|
| 17170 | Web UI (HTTP) |
| 3890 | LDAP |
| 6360 | LDAPS (TLS) |

### Step-CA (Certificate Authority)

An internal Certificate Authority based on Smallstep.

**Features:**
- ACME protocol support for automated certificates
- SCEP support for device enrollment
- JWK provisioner for service-to-service mTLS
- Certificate lifecycle management

**Default Configuration:**
- API: `https://ca.stack.local:8443`
- Health: `https://localhost:8080/health`
- Provisioner: `stackkits`

**Ports:**
| Port | Service |
|------|---------|
| 8443 | CA API (HTTPS) |
| 8080 | Health endpoint |

## Layer 2: Platform Identity Services (opt-in)

### TinyAuth (Identity Proxy & ForwardAuth)

Lightweight auth proxy that registers as a Traefik ForwardAuth middleware.

**Features:**
- Traefik ForwardAuth integration (protect any service with one label)
- OIDC federation with PocketID or external providers
- GitHub / Google OAuth support
- Local user definitions

**Default Configuration:**
- UI: `https://auth.stack.local`
- ForwardAuth URL: `http://tinyauth:3000/api/auth/verify`
- Middleware name: `tinyauth`

**Usage:** Add to any Traefik-routed service:
```
traefik.http.routers.myapp.middlewares=tinyauth
```

### PocketID (OIDC Provider with Passkey Support)

Self-hosted OIDC/OAuth2 provider with WebAuthn/Passkey support.

**Features:**
- Passkey (WebAuthn/FIDO2) authentication
- OIDC/OAuth2 provider for SSO
- Source of truth for Homelab users/groups in the default path
- Issues tokens consumed by TinyAuth and explicit OIDC clients

**Default Configuration:**
- UI: `https://id.stack.local`
- OIDC Discovery: `https://id.stack.local/.well-known/openid-configuration`

PocketID v2 has no password bootstrap. Owner activation uses a one-time setup URL for passkey enrollment.

## Integration with StackKits

Identity services are resolved through the StackKit contract. PocketID is the default user authority; LLDAP overrides are for compatibility:

```cue
// In your StackKit, identity services are already configured
stackkit: {
    // LLDAP configuration (optional compatibility overrides)
    identity: lldap: {
        enabled: true
        domain: base: "dc=myorg,dc=com"
    }

    // Step-CA configuration (optional overrides)
    identity: stepCA: {
        enabled: true
        pki: rootCommonName: "MyOrg Root CA"
    }
}
```

## Network

Identity services run on platform/foundation networks selected by the active PaaS adapter and CUE contract. Do not hand-author generated networking output.

## Security Notes

1. **No static default credentials**: owner and recovery material must be generated or referenced through approved secret paths.
2. **LDAPS**: Use LDAPS (port 6360) where LDAP compatibility is enabled in production deployments.
3. **Root CA**: Store the root CA certificate securely; it's the trust anchor for your infrastructure
4. **mTLS**: Enable mTLS for internal service communication using Step-CA provisioned certificates

## Files

| File | Layer | Purpose |
|------|-------|---------|
| `_lldap.tf.tmpl` | 1 | Terraform template for LLDAP deployment |
| `_step-ca.tf.tmpl` | 1 | Terraform template for Step-CA deployment |
| `_tinyauth.tf.tmpl` | 2 | Terraform template for TinyAuth deployment |
| `_pocketid.tf.tmpl` | 2 | Terraform template for PocketID deployment |
