---
adr: ADR-0054
title: Single Core OpenTofu Stage-1 engine across kits, Layer-2 PaaS retirement, and Coolify 4.7.5+ extensibility boundary
status: Accepted
date: 2026-11-27
last_verified: 2026-11-27
---

# ADR-0054: Single Core OpenTofu Stage-1 engine across kits, Layer-2 PaaS retirement, and Coolify 4.7.5+ extensibility boundary

**Decision owner:** Marcel (`D-2026-11-27-20`, 5-round Architecture Council Blueprint v5.1).
**Amends:** ADR-0042, ADR-0045, ADR-0049.
**Related:** ADR-0031, ADR-0044, ADR-0051, ADR-0052 (Talos retained post-1.0), ADR-0055 (`#AppModuleV3`).
*(Note: `ADR-0053` is reserved for §11.8 Media Bridge per `STACKKITS-PRODUCT-BOUNDARY-STANDARD.md`.)*

## Context

Carrying parallel Layer-2 PaaS execution engines (Komodo, Dokploy, Runtipi, Dockge, Dockhand, legacy Full-Core Coolify 4.1.2) alongside direct Compose (`nativehost`), OpenTofu Stage 1, and an env-only Stage-2 native Docker pilot fragmented maintenance, duplicated contract surfaces, and left `STATUS.md` with zero `supported` PaaS cells before Go-Live.

## Decision

### 1. Single Core Execution Engine Across Kits
- **OpenTofu Stage 1 over Compose** on the existing canonical per-stack root layout (`internal/runtimeexecutor/opentofu/layout.go`: `.stackkit/runtime/<core-dir>/opentofu`, `applications/<project>/opentofu`, `modules/<ref>/opentofu`, preserving OpenTofu resource addresses with zero relayout) is the sole 1.0 Core execution engine for all curated StackKits workloads.
- **Standard vs. Advanced (`CONSTITUTION.md` rule 18):** Standard Mode executes the per-stack OpenTofu roots sequentially with **zero Terramate**. Homelab-managed Advanced Mode orchestrates the exact same roots through Terramate (multi-stack change sets, per-stack drift, coordinated rollback).
- **Mandatory Live Drift Pair:** `status`, `diagnose`, and `terramatehost/converge` always pair `tofu plan` with live container/image-digest/health/route observation; a green `tofu plan` alone never reports healthy.
- **Unified Node Lease (`action_lease`):** All writers (CLI, `stackkit-server` `host-exec` spool, `stackkit-watchdog`, Homelab Advanced dispatch) acquire the `confinedfs` flock + generation token in `action_lease_linux.go`. No agent or watchdog may clear an active lease.
- **Command-Boundary No-Recreate Adoption:** Adoption applies strictly to unmanaged or `nativehost`-managed Compose → OpenTofu Stage 1, excluding `RootExecutionNativeDocker` roots fail-closed. The first adoption pass runs as observation/state-import-only or passes `--no-recreate` at the command boundary and fails closed on mismatch; unchanged container/volume IDs serve as post-apply verification evidence, and failed adoption never runs `docker compose down`.
- **Cloud Kit & Modern Homelab Default Switch:** Both kits switch default executor from `compose` (`nativehost`) to `opentofu` once each kit passes its own `rack-kvm` receipt. Superseded `nativehost` execution entrypoints retire only after both switches pass, retaining shared host/Compose/custody helpers in `internal/engine` / `internal/host`.
  Before that switch, select `generation.target: opentofu` in the StackSpec for an explicit OpenTofu run where the kit's execution mode permits it. After the receipt, change `Definition.generation.defaultTarget` in that kit's `stackfile.cue` and regenerate the Architecture v2 authority. `authoring.initialSpec.generation.target` follows the default; existing specs retain their explicit target.
- **ADR-0045 Stage-2 Env-Only Native Docker Pilot (`STACKKIT_OPENTOFU_NATIVE_MODULES`):** Frozen/retired until post-Go-Live so `#AppModuleV3` carries no pre-1.0 dual render surface. Existing `RootExecutionNativeDocker` roots fail closed on auto-adoption with a documented volume-preserving exit path.

### 2. Layer-2 PaaS Retirement & Pre-1.0 Cutover
- Komodo, Dokploy, Runtipi, Dockge (`experimental`), Dockhand (`prospective`), legacy Full-Core Coolify `4.1.2`, and unofficial PaaS OpenTofu providers are retired from 1.0 core execution. `k3s` and Talos (`ADR-0052`) remain post-1.0 Advanced targets.
- Per `CONSTITUTION.md` rule 12, no bespoke pre-1.0 PaaS migration engine is built. Existing test installs with legacy PaaS workloads fail closed against dual active copies and follow one write-fenced Kopia backup / fresh-install / restore runbook.
- Homelab's default Coolify relocation (`third_party/coolify-mirror/`, `homelab/internal/routes/stacks/workload_relocation.go`) is retained for optional extension relocation until explicitly retired or replaced with parity.

### 3. Optional Layer-3 Extensibility: Coolify 4.7.5+ Routing & Trust Boundary
- Coolify (`modules/coolify/`, digest-pinned `4.7.5+`, `AUTOUPDATE=false`, PocketID OIDC for owners/admins) is solely the optional Layer-3 Marketplace (359 templates) and Custom-Apps/Git-build extensibility module and never manages curated core apps.
- **Honest Two-Tier Host Trust Boundary:**
  - *Multi-Host / Proxmox / Homelab-Managed (Isolated Mode):* Coolify runs on a dedicated Extension VM/node with zero SSH access to the Core host; its internal HTTP Traefik binds to the Extension VM's private Homelab site/mesh IP (mTLS/WireGuard, zero public ports).
  - *Single-Node Homelab (Basement Standalone):* Explicitly disclosed in onboarding as `trusted-owner-only`, NOT host-isolated (a Coolify admin is host-root-equivalent because Coolify holds bridge-IP-pinned root SSH and Docker access).
- **Hardened Option A Routing & Isolated Zero-Custody Cookie-Scrubber Sidecar:**
  - Kit-Traefik + Access Gate is the sole owner of host ports `80/443`, TLS certificates, and auth policy across all 3 address zones.
  - Coolify's internal Traefik runs HTTP-only (`http://` FQDNs, ACME disabled, zero host ports) on private `coolify-edge` (or private Extension-VM mesh IP). Coolify apply/verify/upgrade admission fails closed if Coolify's proxy compose binds host `80`/`443` or enables ACME.
  - User-space traffic flows through a dedicated, unprivileged zero-custody cookie-scrubber sidecar container (`stackkit-server --mode cookie-scrubber` on pinned `alpine`, zero custody mounts, zero signer socket, zero MCP token, joined only to `[basement-core, coolify-edge]`) that strips Access-Gate cookies (`__Host-stackkit-session`, `__Host-stackkit-launch` from `internal/auth/accessgate/browser.go`) and configured TinyAuth cookie names upstream, and strips `Domain=` or gate-cookie overwrites from downstream `Set-Cookie`.
- **Enforceable Coolify 4.7.5 Agent Facade:**
  - StackKits bootstrap pre-creates one dedicated `kombify-agent-apps` Coolify project and mints a separate Agent token (`read`, `write`, `deploy` — NO `read:sensitive`, NO `delete`).
  - Coolify's `get_logs` (which requires `read:sensitive` in `GetLogs.php`) is omitted from the Coolify facade; log tails come from StackKits' redacted host observation snapshot (`ADR-0051`).
  - StackKits `/mcp` enforces both tool allowlisting and argument/resource-UUID authorization to `kombify-agent-apps`, keeping destructive, secret-exfiltration, host-root, and new-public-exposure operations behind `D-2026-11-20-06` human-approval guards.

## Consequences

- `internal/architecturev2renderer`, `internal/architecturev2/product_*_factory.go`, `internal/runtimeexecutor/fallback/platformdeploy`, legacy PaaS executors, and `v2alpha1` are retired sequentially once each replacement passes `rack-kvm` proof on `main` (PR 1 lands replacement -> KVM proves `main` SHA -> PR 2 deletes superseded code).
- Shared non-PaaS types (`HostDeployment`, `SetupPolicy`, `BundleManifest`, `DeploymentRef`) are extracted out of `fallback/platformdeploy` before its deletion, and `homelab/` consumes coordinated updates before `v2alpha1` removal.
