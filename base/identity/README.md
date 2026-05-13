# Identity Module

This module provides identity and PKI services for StackKits across Layer 1 (Foundation) and Layer 2 (Platform).

## Layer 1: Foundation Services (always deployed)

### LLDAP (Lightweight LDAP)

A simplified LDAP server for user and group management.

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
- LDAP sync with LLDAP for user/group management
- Issues tokens consumed by TinyAuth, PocketBase, etc.

**Default Configuration:**
- UI: `https://id.stack.local`
- OIDC Discovery: `https://id.stack.local/.well-known/openid-configuration`

## Integration with StackKits

Both services are automatically available to all StackKits through Layer 1 inheritance:

```cue
// In your StackKit, identity services are already configured
stackkit: {
    // LLDAP configuration (optional overrides)
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

Both services run on the `identity_net` Docker network:
- Subnet: `172.28.0.0/16`
- Other services can reach LLDAP and Step-CA via container names

## Security Notes

1. **Default Passwords**: Change default passwords in production using proper secret management
2. **LDAPS**: Always use LDAPS (port 6360) for production deployments
3. **Root CA**: Store the root CA certificate securely; it's the trust anchor for your infrastructure
4. **mTLS**: Enable mTLS for internal service communication using Step-CA provisioned certificates

## Files

| File | Layer | Purpose |
|------|-------|---------|
| `_lldap.tf.tmpl` | 1 | Terraform template for LLDAP deployment |
| `_step-ca.tf.tmpl` | 1 | Terraform template for Step-CA deployment |
| `_tinyauth.tf.tmpl` | 2 | Terraform template for TinyAuth deployment |
| `_pocketid.tf.tmpl` | 2 | Terraform template for PocketID deployment |
