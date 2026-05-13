# ADR-0004: HA Domain Policy — Local and Public Domains

**Status:** Accepted  
**Date:** 2026-02-11  
**Resolves:** TD-25 (Domain Validation Inconsistency)

## Context

The `ha-kit` StackKit previously rejected local/private TLD domains (`.local`, `.lan`, `.home`, `.internal`, `.test`) via a negative regex constraint. The `base-kit` StackKit accepted any string. This created confusion about whether HA deployments required public domains.

Many homelab users operate entirely on private networks with `.local` or `.lan` domains. Requiring a public domain was an unnecessary barrier.

## Decision

**Both public and local domains are supported in all StackKits, including HA.**

The domain validation uses a positive format regex (valid hostname format) instead of a blocklist. TLS provisioning adapts to the domain type:

| Domain Type | TLS Provider | Failover Mechanism |
|------------|-------------|-------------------|
| Public (e.g. `example.com`) | Let's Encrypt (ACME) | DNS failover or cloud LB |
| Local (e.g. `home.local`) | Step-CA (internal CA) | Keepalived VIP |

## TLS Provider Options

- **`letsencrypt`** — Production ACME (default, requires public domain + DNS/HTTP challenge)
- **`letsencrypt-staging`** — Testing ACME (rate-limit-free)
- **`step-ca`** — Internal certificate authority (for `.local`, `.lan`, etc.)
- **`custom`** — User-provided certificates

## Consequences

- Users with local domains can deploy HA stacks
- TLS provider must be set to `step-ca` or `custom` when using local domains (Let's Encrypt cannot validate local domains)
- The `step-ca` option aligns with the identity layer in `base/identity/` which already has Step-CA templates
- No breaking change for existing users — public domains still work with the default `letsencrypt` provider
