# ADR-0005: Service Modules as Atomic Unit

**Status:** Accepted
**Date:** 2026-02-23
**Resolves:** Technical debt from monolithic service collection approach

---

## Context

The original StackKits architecture defined services in large CUE collections (`#DefaultServices`, `#MinimalServices`, etc.) inside `base-kit/services.cue`. This created several problems:

1. **No isolation testing** — Services could only be tested as part of a full StackKit deployment. A misconfigured TinyAuth required a full 30-resource deploy to discover.

2. **Opaque dependencies** — There was no machine-readable declaration of what each service requires (which other services, which capabilities). The `needs: [...]` field existed but was not enforced.

3. **Variant explosion** — Adding a new service meant touching a large monolithic file and adding a new variant collection. The variant system (`default | beszel | minimal | secure`) was a poor substitute for true composability.

4. **Template coupling** — The monolithic `main.tf` (~1360 lines) had to be entirely regenerated for any service change, making partial updates impossible.

5. **No proven configurations** — Services went directly from CUE definition to full deployment. There was no intermediate step that proved the service configuration actually worked.

---

## Decision

**Service modules are the atomic unit of a StackKit.**

Each module lives in `modules/<name>/` and consists of three mandatory artifacts:

```
modules/<name>/
  module.cue              # CUE contract: metadata, requires, provides, settings, services
  tests/
    reference-compose.yml # Minimal Docker Compose proving the module works in isolation
    integration_test.sh   # Test script: health, routing, security hardening
    traefik-dynamic.yml   # (where needed) File-provider config for Traefik
```

### The module.cue contract

Every module defines `Contract: base.#ModuleContract` with:

- `metadata` — name, version, layer classification (`L1-foundation`, `L2-platform-*`, `L3-application`)
- `requires.services` — explicit service dependencies with minimum versions and required capabilities
- `requires.infrastructure` — Docker, persistent storage, shared network requirements
- `provides.capabilities` — named boolean capabilities other modules can depend on
- `provides.middleware` — Traefik middleware definitions this module creates
- `provides.endpoints` — URLs and internal endpoints this module exposes
- `settings.perma` — immutable-after-deploy settings
- `settings.flexible` — day-2-changeable settings
- `contexts` — per-context (local/cloud/pi) configuration overrides
- `services` — one or more `base.#ServiceDefinition` values

### The reference-compose principle

Before a service definition is added to a StackKit, it **must be proven** in isolation via its reference compose. The reference compose:

- Uses Traefik's file provider (not Docker provider) for cross-platform reliability
- Runs only the services needed to prove the module's core functionality
- Is used by `integration_test.sh` to verify: container health, routing, ForwardAuth flow, security hardening (no-new-privileges, cap_drop ALL, memory limits)
- Serves as the source of truth for the OpenTofu module fragment

A module.cue comment `// PROVEN CONFIG: Validated via reference-compose.yml (N/N tests pass)` indicates test status.

### The test pyramid

```
Level 4: E2E (stackkit apply on fresh VM)       ← full StackKit × Context
Level 3: Composition (modules/_integration/)    ← all Base Kit modules together
Level 2: Module (modules/<name>/tests/)         ← single module in isolation
Level 1: Schema (cue vet ./...)                 ← CUE constraint validation
```

Levels 1–3 run without a VM. Level 4 is the final gate.

### Test commands

```bash
make test-cue-binding                          # Level 1: CUE contracts + Go binding
bash modules/<name>/tests/integration_test.sh  # Level 2: single module
for d in modules/*/tests/integration_test.sh; do bash "$d"; done
bash modules/_integration/integration_test.sh  # Level 3: full-stack composition
make test-e2e                                  # Level 4: full E2E via VM
```

---

## Consequences

### Positive

- **Isolated testing** — Every service can be tested without deploying a full StackKit. A broken TinyAuth config is caught in ~30 seconds, not after a 5-minute `stackkit apply`.
- **Explicit dependencies** — The `requires.services` map makes dependency graphs machine-readable. The bridge.go rewrite can use this to determine deployment order.
- **Proven configurations** — The reference-compose is the contract between module author and StackKit generator. If reference-compose passes, the OpenTofu fragment will work.
- **Composability path** — Modules replace the variant system. Instead of selecting `default | beszel | minimal`, users (and the CLI) select which modules to enable.
- **Per-module OpenTofu fragments** — The monolithic `main.tf` can be broken into per-module fragments, each generated from its module's `#ModuleContract`.

### Negative

- **More files** — Each module requires 3+ files. For 14 Base Kit services, this is ~50 files vs the previous 2 (services.cue + main.tf).
- **bridge.go rewrite required** — The current extractor uses variant→collection lookup. It must be rewritten to iterate over `Contract.services` per module and merge them.
- **Parallel migration** — During the transition, both the old `services.cue` collections and the new `modules/` coexist. This creates short-term duplication.

### Neutral

- Module tests use Docker but not the VM. They are fast (< 60s per module) and can run in CI without the full VM infrastructure.
- The module architecture aligns with the Add-On system (M4): Add-Ons are modules that are not part of the default set.

---

## Base Kit Module Status (2026-02-23)

| Module | Layer | Reference Compose | Tests |
|--------|-------|-------------------|-------|
| socket-proxy | L1-foundation | ✅ | ✅ |
| traefik | L2-platform-ingress | ✅ | ✅ |
| tinyauth | L2-platform-identity | ✅ | ✅ (proven) |
| pocketid | L2-platform-identity | ✅ | ✅ |
| dokploy | L2-platform-paas | ✅ | ✅ |
| lldap | L2-platform-identity | ✅ | ✅ |
| step-ca | L2-platform-identity | ✅ | ✅ |
| crowdsec | L2-platform-ingress | ✅ | ✅ |
| adguard-home | L2-platform-dns | ✅ | ✅ |
| unbound | L2-platform-dns | ✅ | ✅ |
| uptime-kuma | L3-application | ✅ | ✅ |
| dozzle | L3-application | ✅ | ✅ |
| dashboard | L3-application | ✅ | ✅ |
| whoami | L3-application | ✅ | ✅ |

---

## Alternatives Considered

### Alternative 1: Keep monolithic services.cue + improve testing

Add integration tests that spin up the full stack for each service test. Rejected because: startup time is ~3 minutes (vs 30s for a module reference-compose), and it doesn't provide isolation — a single service failure fails all tests.

### Alternative 2: CUE-only module definitions without reference-compose

Define modules purely in CUE with no Docker Compose verification step. Rejected because: CUE validates schema correctness but cannot verify that Docker labels, health checks, network configurations, and ForwardAuth flows actually work at runtime. The reference-compose is the only way to prove a config works before adding it to a StackKit.

### Alternative 3: Dagger pipelines per service

Use Dagger for isolated service testing. Rejected because: adds Dagger as a required dependency, and reference-compose + bash is simpler, more portable, and already proven.
