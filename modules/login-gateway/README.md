# Module: login-gateway

Platform invariant: TinyAuth forward-auth in front of every L3 service, with PocketID as OIDC IdP. **Mandatory in V6.**

## What it does

login-gateway is a **glue module**. It does not run its own container. It:

1. Declares the `login-gateway@file` Traefik middleware that every L3 service inherits.
2. Wires TinyAuth (ForwardAuth middleware) in front of PocketID (OIDC IdP).
3. Provides the enforcement surface for the CUE Decision Logic (see ADR-0008): any L3 service not annotated with an explicit bypass must use this middleware, or `stackkit validate` fails.

## The flow

```
Client (browser)
      |
      v
https://<service>.<domain>
      |
      v
Traefik → login-gateway@file middleware
      |
      ├─ session cookie present + valid → forward to backend
      └─ no cookie → redirect to https://auth.<domain>
                                |
                                v
                           TinyAuth (ForwardAuth + session minting)
                                |
                                v
                           PocketID (OIDC authorization against LLDAP)
                                |
                                v
                           User logs in → PocketID issues code → TinyAuth mints session
                                |
                                v
                           Redirect back to <service>.<domain>, now with session cookie.
```

## Settings

| Setting | Type | Default | Kind |
|---------|------|---------|------|
| `middlewareName` | string | `login-gateway@file` | perma |
| `allowedPublicBypass` | []string | `[]` | perma |
| `sessionExpiry` | int seconds | `86400` | flexible |
| `requireMfaForAdmins` | bool | `true` | flexible |

## Bypass

Some services legitimately do their own authentication (e.g., a public landing page, a webhook receiver). To skip the forward-auth middleware, the service must:

1. Be listed in `login-gateway.allowedPublicBypass` (opt-in whitelist).
2. Set its own Traefik label: `exposed-to-public: "true"`.

Both conditions must be met. CUE Decision Logic rejects any L3 service that sets the label without being on the whitelist.

## Dependencies

- Traefik (≥ 3.0) — the reverse proxy.
- TinyAuth (≥ 4.0) — the ForwardAuth implementation.
- PocketID — the OIDC IdP.
- LLDAP (transitively, via PocketID) — the user directory.

## Status

**Scaffolded** for V6 Phase 2 (Q3/2026). Enforcement depends on the Composition Engine (Phase 1, CUE binding).

## Why mandatory

V6's test-user target is "bare Ubuntu → `curl install.kombify.io | sh` → production-ready". Every L3 service reachable over HTTPS must present a login box. Making this optional means the user can accidentally expose Immich, Jellyfin, Vaultwarden, etc. without auth. Not acceptable for a safe-by-default product.
