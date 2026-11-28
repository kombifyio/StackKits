---
adr: ADR-0055
title: Declarative 1-folder #AppModuleV3 contract, Core renderer placement, two-tier Clean/Bootstrapped lifecycle, and 3-zone address contract
status: Accepted
date: 2026-11-27
last_verified: 2026-11-27
---

# ADR-0055: Declarative 1-folder #AppModuleV3 contract, Core renderer placement, two-tier Clean/Bootstrapped lifecycle, and 3-zone address contract

**Decision owner:** Marcel (`D-2026-11-27-20`, 5-round Architecture Council Blueprint v5.1).
**Related:** ADR-0044, ADR-0045, ADR-0051, ADR-0054, `STACKKITS-PRODUCT-BOUNDARY-STANDARD.md`, `STACKKITS-1.0-TARGET.md`.

## Context

Curated application workloads were split across monolithic `architecture_v2*.cue` files, 55 Go `product_*_factory.go` files, 41 `*_workload_bundle.go` Go renderers, and ~147k LOC of duplicated embedded CUE bundles (`authority_bundle/`, `contract_fixture_bundle/`). Meanwhile, onboarding leaked eight internal orthogonal axes into UX and coupled Core apply health to optional per-app personalization (`appsetup`).

## Decision

### 1. Clear Split: `use-cases/` (16 Slugs) vs. Core/Platform Go Renderers vs. 1-Folder `#AppModuleV3`
- **`use-cases/<slug>/use-case.cue` (16 slugs across 13 main intents):** Remains the canonical intent, alternative, companion, legacy-slug, and public/private export selector (preserving private `automation`).
- **Core & ~25 Platform Modules (`traefik`, `pocketid`, `tinyauth`, `step-ca`, `unbound`, `socket-proxy`, `crowdsec`, etc.):** Stay typed Go renderers consolidated in `internal/engine/core/` (not forced into `#AppModuleV3` pre-Go-Live).
- **~41 Curated Layer-3 Application Modules (`modules/<app>/`):** Extended in place with `foundation.#AppModuleV3`:
  ```text
  modules/<app>/
  ├── module.cue       # Single canonical CUE contract (foundation.#AppModuleV3)
  ├── templates/       # Optional pure non-secret config templates (<= 4 apps)
  ├── golden/          # Expected rendered compose.yaml snapshot for cutover parity
  └── tests/           # Preserved module journey/fixture tests
  ```
- **Audited Typed Schema & Strict Go Escape-Hatch Cap:** Every retained container/security field from `workload_compose_model.go` and all 8 specialized trait families (`hostNetwork`/`lanListeners`, `publishedPorts`, `devicePassthrough`, `accelerator`, `sandboxRuntime`/`companionEnv`, `peerNetworks`, `bundledPlugin`, `dockerLifecycleOwner`/`ownHostSSH`) are typed, policy-checked CUE fields. Per-app Go code is restricted to two closed registries (`engine.ConfigRenderer` for <= 4 pure config renderers and `adapters.Registry` for `setup/*`, `hook/*`, `probe/*`).
- **Single-Source `go:embed`:** `foundation/` and `modules/` embed directly from repository-root/subtree `embed.go`, retiring the ~147k LOC duplicated `authority_bundle/` and `contract_fixture_bundle/` once parity passes.

### 2. Three-Axis Onboarding & Zone-Agnostic 3-Zone Address Contract
User-facing onboarding exposes three equal axes (compiled over internal policy axes with zero silent security downgrades):
1. **Workloads & Kit:** Basement Kit, Cloud Kit, or Homelab-managed Modern Homelab + selected use cases.
2. **Address Zone (3 Equal First-Class Modes — Zero `kombify.me` Lock-In):**
   - **Custom Domain (`example.com`):** ACME DNS-01/HTTP-01 directly on Kit-Traefik; PocketID at `id.example.com`, TinyAuth at `auth.example.com` (`Domain=example.com`), zero `kombify.me` dependency.
   - **`kombify.me` (`{base}.kombify.me` or flat `{base}-{app}.kombify.me`):** Zero own domain required; shared-domain TinyAuth SSO on `kombify.me` is gated strictly on effective public-suffix isolation (PSL active or proven equivalent) and defaults to `__Host-` per-host sessions until PSL is active to prevent cross-install cookie shadowing. Tunnel TLS issuance and renewal are probe-first verified before mandating ACM.
   - **Local-Only / Offline (`lab.home`):** Local Owner CA + local resolver; zero internet, cloud, or account dependency.
3. **Setup Style:** **Bootstrapped / Ready-to-Use** (default) vs. **Clean Rollout**.

### 3. Hard Two-Tier Lifecycle (`Clean Rollout` Is Always Secure)
- **Tier 1 (Mandatory in both Clean and Bootstrapped):** Core rollout + Required Safety Init (digest-pinned images, private networks, Wallet-delivered secret custody, zero default passwords, closed unsafe registration, PocketID + Access Gate, private-by-default routing). Fails closed on error.
- **Tier 2 (Bootstrapped only — Post-Rollout):** Optional `internal/appsetup/*` personalization (Owner account bootstrap, PocketID OIDC client wiring, household sync, API keys). Skipped in Clean Rollout (app stays behind Access Gate). In Bootstrapped mode, Tier-2 failure never fails Core/OpenTofu apply: it records `bootstrap_degraded`, keeps the app private/setup-pending behind Access Gate, and surfaces `retry-appsetup`.

## Consequences

- `internal/engine/**` and `modules/*/golden/**` are admitted under the `compiler` boundary in `.kombify/test-boundaries.json` (`CONSTITUTION.md` rule 13).
- `scripts/release/check-l3-paas-contract.mjs` is replaced in Phase 1D with the `#AppModuleV3` contract validator.
- Every retained workload bundle across all 16 slugs must pass golden Compose parity and `rack-kvm` verification before old Go workload renderers and factories are deleted.
