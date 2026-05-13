# ADR-0003: PaaS Strategy - Dokploy vs Coolify

**Status:** Accepted  
**Date:** 2026-01-22  
**Decision Makers:** StackKits Core Team

---

## Context

StackKits needs to provide a default PaaS (Platform as a Service) for homelab users. Two main options exist:

1. **Dokploy** - Simple, lightweight, great for local/no-domain setups
2. **Coolify** - Feature-rich, multi-node capable, requires own domain

Users have different needs based on their setup:
- Some users just want local access without domain configuration
- Some users have their own domain and want Git-based deployments
- Some users plan to expand to multi-node setups

## Decision

We will support **both** PaaS options with clear selection criteria:

### 2026-05-09 Amendment: Platform-Managed Rollouts

Normal StackKit rollouts require one platform adapter. L3 applications must be
registered and deployed through the selected platform API, not applied directly
with raw `docker_container` resources or `docker compose up`.

- `dokploy` is the default for local, pi, kombify.me, and cloud-without-custom-domain contexts.
- `coolify` is the default for cloud custom-domain contexts and remains the supported explicit alternative.
- `dockge` is reclassified as a lightweight Compose manager for constrained/experimental modes, not a standard PaaS.
- `paas: none` is invalid for normal Base/Modern/HA StackKits.
- `komodo` is the strongest future challenger because its stacks, resources, API, actions, and declarative Resource Sync fit a programmable StackKit rollout model. It stays behind a PoC until routing, auth, and rollback parity are proven.

### Selection Logic

| User Scenario | Recommended PaaS | Variant |
|---------------|------------------|---------|
| No domain, local network only | **Dokploy** | `default` |
| Own domain, single server | **Dokploy** or **Coolify** | `default` or `coolify` |
| Own domain, plans for multi-node | **Coolify** | `coolify` |
| Multi-node deployment | **Coolify** (required) | n/a (modern-homelab) |

### Default Behavior

- **base-kit** defaults to `Dokploy` (simpler, works without domain)
- **modern-homelab** requires `Coolify` (multi-node management)
- Users can explicitly choose `coolify` variant in base-kit if they prefer

## Consequences

### Positive

1. **Lower barrier to entry**: Users without domains can still get started
2. **Growth path**: Users can start with Dokploy and migrate to Coolify
3. **Flexibility**: Both options available based on user needs
4. **Multi-node ready**: Coolify selection prepares for future expansion

### Negative

1. **Two systems to maintain**: Need to keep both PaaS options updated
2. **Documentation complexity**: Must explain when to use each
3. **Migration path**: No automated migration from Dokploy to Coolify

## Alternatives Considered

### Alternative 1: Coolify Only
- **Rejected**: Too complex for users without domains

### Alternative 2: Dokploy Only
- **Rejected**: Cannot support multi-node deployments

### Alternative 3: Neither (Docker Compose Only)
- **Rejected**: Poor user experience for application deployment

## Implementation

1. ✅ Add `#CoolifyService` to `base-kit/services.cue`
2. ✅ Add `coolify` variant to `base-kit/stackfile.cue`
3. ✅ Update `base-kit/README.md` with PaaS selection guide
4. ✅ Mark `modern-homelab` as Coolify-only (requires own domain)
5. [ ] Create Coolify variant Terraform templates
6. [ ] Add domain detection logic to CLI

## References

- [Dokploy Documentation](https://dokploy.com)
- [Coolify Documentation](https://coolify.io)
- [ADR-0002: Docker-First Strategy](ADR-0002-docker-first-v1.md)
