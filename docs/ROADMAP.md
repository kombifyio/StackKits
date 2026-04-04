# StackKits Roadmap

> **Last Updated:** 2026-02-23
> **Status:** Active Development — Phase 1 (IaC Pipeline)
> **Current Version:** v1.0.0-beta  
> **Architecture:** [ARCHITECTURE_V4.md](./ARCHITECTURE_V4.md)  
> **Evaluation:** [EVALUATION_REPORT_2026-02-07.md](./EVALUATION_REPORT_2026-02-07.md)

---

## Executive Summary

StackKits v4 is a fundamental redesign around **three concepts**:

1. **StackKit** = Architecture pattern (base / modern / ha)
2. **Node-Context** = Runtime environment (local / cloud / pi), auto-detected
3. **Add-Ons** = Composable extensions replacing monolithic variants

Combined with a **Progressive Capability Model** (Levels 0–4), this replaces the old variant-based, node-count-driven design.

This roadmap consolidates all planned work into a single milestone-based plan (M0–M9), covering CUE schema fixes, v4 migration, StackKit completion, cross-repo consistency, and ecosystem integration.

---

## Current State Assessment (2026-02-21)

| Component | Status | Notes |
|-----------|--------|-------|
| CUE base schemas | 95% | ~2800 lines, production-quality. `base/platform/` and `base/schema/` deleted (TD-01/02) |
| base-kit | 85% | E2E verified (2026-02-22): 31 resources, 9 healthy containers. bridge.go TFVars aligned with main.tf |
| modern-homelab | 0% | Entirely K8s/k3s-based — needs **complete rewrite** for Docker multi-node (TD-08) |
| ha-kit | 0% | Schema only, 8 explicit TODOs |
| stackkit CLI | 95% | 12 commands functional: init (interactive), generate, validate, plan, apply, destroy, status (--json), prepare (memory), completion, version, prompt, serve |
| **Service Modules** | **65%** | **NEW** — 14 modules implemented in `modules/`, each with module.cue + reference-compose + integration tests |
| Add-On system | 0% | Replaces monolithic variants. 17 CUE schemas exist but no code generation |
| Context system | 0% | Replaces manual compute tier selection |
| kombify-TechStack integration | 30% | Unifier pipeline exists, needs v4 alignment |
| API server | 95% | 13 endpoints, API key auth (TD-28), rate limiting (TD-33), CORS (TD-40), 42 test cases (TD-34), structured errors (TD-32), pagination (TD-41) |
| Documentation | 50% | v4 docs current, K8s refs removed from active docs. Cross-repo Mintlify docs still outdated |

---

## Cross-Repo Consistency Audit (K1–K15)

Before implementation, a full audit identified critical inconsistencies across StackKits, kombify-TechStack, kombify Core, and docs repos.

### Critical Findings

| # | Finding | Repos Affected | Severity |
|---|---------|----------------|----------|
| K1 | **K8s references in docs** — Mintlify pages describe K8s/k3s despite removal | docs | High |
| K2 | **License inconsistency** — different licenses cited in different places | docs, StackKits, Stack | High (✅ fixed) |
| K3 | **Naming inconsistency** — "kombifyStack", "kombify-TechStack", "kombify-TechStack" etc. | all | High |
| K4 | **Duplicate concept pages** — 3× StackKits explanations, 2× spec-driven pages | docs | Medium |
| K5 | **ha-kit description** — docs say K8s, code is Docker Swarm | docs | High |
| K6 | **modern-homelab.mdx** — 591 lines entirely about K8s/k3s/FluxCD/Longhorn | docs | High |
| K7 | ~~**GitHub org references**~~ — RESOLVED 2026-03-04. Module path changed to kombifyio, all refs fixed | docs, Stack | ~~Medium~~ |
| K8 | **URL casing** — /Cloud/ vs /cloud/ mixed | docs | Low |
| K9 | **Outdated service references** — "Authelia", "Portainer" instead of "TinyAuth", "Dokploy" | docs | Medium |
| K10 | **"Terraform" on marketing** — should be "OpenTofu" | StackKits | Medium |
| K11 | **Empty Core README** — kombify Core README has no content | Core | Low |
| K12 | **Beyond-IaC undocumented** — gRPC Agents, AI concepts not in public docs | docs | High |
| K13 | **Add-On system undocumented** — neither concept nor schema described | docs | Medium |
| K14 | **Persona system undocumented** — Wizard decision tree missing | docs | Medium |
| K15 | **Local/Cloud split undocumented** — Backend differentiation not documented | docs | Medium |

---

## Milestones

### M0: Hygiene & v4 Migration (Weeks 1–3) — MOSTLY COMPLETE

**Goal:** Clean slate. Remove all v3/old-concept artifacts from code and docs. Establish v4 as the only source of truth.

#### Docs Cleanup (this repo)

- [x] Archive `docs/architecture.md` → superseded by `ARCHITECTURE_V4.md`
- [x] Archive `docs/variants.md` → replaced by Add-On + Context model
- [x] Archive `docs/STATUS_QUO.md` → pre-v4, no longer accurate
- [x] Archive `docs/DEFAULT_SPECS_README.md` → references K8s, old variants
- [x] Archive `docs/EVALUATION_REPORT.md` → corrupt encoding, superseded by 2026-02-07 version
- [x] Archive `docs/CODE_REVIEW_2026-01-27.md` → duplicates root CODE_REVIEW_TECHNICAL_REPORT.md
- [x] Archive `docs/cleanup/` (8 files) → consolidated into Cleanup-Plan.md already
- [x] Update `docs/README.md` → new index reflecting v4 docs
- [ ] Update `docs/creating-stackkits.md` → deferred to M4 (variant dirs still exist in code, TD-12)
- [x] Update `docs/stack-spec-reference.md` → removed K8s sections and examples
- [ ] Update `docs/templates.md` → deferred to M4 (variant variable matches actual HCL code)
- [x] Update `docs/TARGET_STATE.md` → removed K8s prep references
- [ ] Update `docs/CLI.md` → deferred to M2 (Add-On/Context commands not yet implemented)

#### Root Files Cleanup

- [x] Delete `{{range` (junk file at root)
- [x] Delete `stackkit.exe` (committed binary — already in .gitignore)
- [x] Add `stackkit.exe` to `.gitignore` explicitly
- [x] Update root `README.md` → remove variant references in "StackKit Specification" section
- [x] Update `DEPLOYMENT.md` → remove "Kubernetes manifests" reference
- [x] Update `DEPLOYMENT_CONTRACT.md` → fix naming inconsistencies
- [x] Update `stack-spec.yaml` → remove `variant: default`, `mode: simple`
- [x] Archive root `CODE_REVIEW_TECHNICAL_REPORT.md` → pre-v4

#### CUE/Schema Consistency

- [x] File naming: `modern-homelab/stackkit.cue` → `stackfile.cue`
- [x] Remove duplicate schema definitions (`base/layers.cue` vs `base/platform/identity.cue`)
- [x] Fix package declarations in `base/platform/*.cue` (declares `package base` in subdirectory)
- [x] Fix package declarations in `base/schema/*.cue` (same issue)
- [x] Compute tier naming: Go `minimal/standard/performance` → CUE `low/standard/high` (align to CUE)
- [x] Platform type: remove `kubernetes` from Go validator (ADR-0002)
- [x] Fix Layer 3 PAAS validation logic (currently inverted in Go)
- [x] Consolidate `#BaseKitStack` vs `#BaseKitKit` → single canonical schema
- [x] Fix Coolify image typo: `coolabsio` → `coollabsio`
- [x] Fix whoami service missing `host` port in PortMapping

#### Cross-Repo (docs Mintlify repo)

- [ ] Remove all K8s references from concept pages (K1, K5, K6)
- [ ] Enforce naming standard: "kombify-TechStack", "kombify Sim", "kombify StackKits", "kombify Cloud" (K3)
- [ ] Consolidate duplicate concept pages (K4)
- [ ] Fix URL casing (K8)
- [ ] Update service names: Authelia → TinyAuth/PocketID, Portainer → Dokploy (K9)
- [ ] Unify GitHub org references (K7)
- [ ] Rewrite `modern-homelab.mdx` for Docker multi-node (K6)
- [ ] Update `ha-kit.mdx` for Docker Swarm (K5)

**Done Criteria:** `cue vet ./base/... ./base-kit/...` passes. No K8s/variant references in active docs. Consistent naming everywhere.

---

### M1: Core IaC Pipeline (Weeks 3–6) — IN PROGRESS

**Goal:** CUE schemas produce real, deployable infrastructure. Base Kit end-to-end.

#### Service Module Architecture (Active Work)

Service modules are the atomic unit of a StackKit. Each module in `modules/<name>/` defines:
- `module.cue` — `#ModuleContract` (metadata, requires, provides, settings, services)
- `tests/reference-compose.yml` — isolated Docker Compose for testing the module in isolation
- `tests/integration_test.sh` — test script (container health, routing, security hardening)

**Modules implemented (14 of 14 Base Kit services):**
traefik, tinyauth, pocketid, dokploy, socket-proxy, uptime-kuma, dozzle, dashboard, whoami, lldap, step-ca, crowdsec, adguard-home, unbound

**Test pyramid per module:**
1. CUE vet (`mise run test:cue`) — schema validation
2. Module integration test (`mise run test:module <name>`) — isolated reference-compose
3. Full-stack composition test (`mise run test:compose`) — all modules together
4. E2E via VM (`mise run dev`) — stackkit apply on fresh server

**Active work:**
- [ ] Run and verify all module integration tests (11 tests per module) — needs Docker
- [ ] Break monolithic `main.tf` into per-module OpenTofu fragments (StackKits-0t0.6)
- [ ] Remove legacy variant system from `base-kit/stackfile.cue` (StackKits-x2u)

#### IaC Pipeline (Active Work)
- [ ] CUE-as-SSoT: CUE validates + exports `tfvars.json` (not template rendering)
- [ ] base-kit end-to-end: `validate → generate → plan → apply`
- [ ] JSON schema export for IDE support (`cue export --schema`)
- [ ] Port collision detection as CUE constraint
- [ ] Service dependency validation (`needs[]` references enabled services)

#### Completed
- [x] **`bridge.go` TFVars aligned with `main.tf`** — `TFVars` struct rebuilt with actual main.tf variables (`enable_traefik`, `enable_tinyauth`, `enable_pocketid`, `enable_dokploy`, `enable_dokploy_apps`, `enable_dashboard`, `domain`, `network_subnet`, etc.). `specToTFVars()` now generates correct tfvars.json. (StackKits-r1p.2, 2026-02-23)
- [x] **`bridge.go` rewrite: module-based CUE extraction** — `ExtractServicesFromModules()` replaces `variantToCollection()`. Reads `Contract.services` from each `modules/<name>/module.cue` (2026-02-23)
- [x] **`generate.go` variant switch removed** — service enablement is now module-based (all enabled by default), per-service override via `spec.Services` (2026-02-23)
- [x] **`extractor.go` variant cleanup** — `variantToCollection()` deleted, `ExtractServices(variant)` replaced with `ExtractServicesFromModules(modulesDir)` (StackKits-xxt, 2026-02-23)
- [x] CI/CD pipeline: `cue vet ./...`, Go tests, lint on every push (`.github/workflows/ci.yml`)
- [x] **E2E verified (2026-02-22):** `stackkit apply` deploys 31 resources, 9 healthy containers, TinyAuth ForwardAuth protects all services
- [x] `#ModuleContract` CUE schema defined in `base/module.cue`
- [x] All 14 Base Kit service modules implemented with `module.cue` + reference-compose + integration tests
- [x] mise tasks: `test:cue`, `test:module`, `test:modules`, `test:compose`
- [x] `modules/_integration/` full-stack composition test (all modules together)
- [x] **API hardening: Fix filesystem write vulnerability in `handleGenerateTFVars`** (TD-27, P0 — resolved 2026-02-11)
- [x] **API hardening: Add authentication middleware** (TD-28, P0 — resolved 2026-02-11)
- [x] **API hardening: Fix compute tier enum mismatch in OpenAPI spec** (TD-29, P0 — resolved 2026-02-11)
- [x] **API hardening: Add rate limiting middleware** (TD-33, P1 — resolved 2026-02-11)
- [x] **API hardening: Add API handler test coverage** (TD-34, P1 — resolved 2026-02-11, 42 test cases)
- [x] **API hardening: Capture response status in logging middleware** (TD-35, P1 — resolved 2026-02-11)
- [x] Fix `base.#Layer3Applications.services` constraint (Array vs Map — TD-09, resolved 2026-02-13)

**Done Criteria:** `stackkit validate && stackkit generate && stackkit plan` works for base-kit. All module integration tests pass.

---

### M2: Context System & Backend Split (Weeks 5–8)

**Goal:** Node-Context replaces manual compute tier selection. Local vs Cloud differentiation works.

#### Context System Implementation

- [ ] Define context detection criteria in CUE constraints
- [ ] Create `contexts/local.cue` — full Docker, local TLS, Dokploy
- [ ] Create `contexts/cloud.cue` — Let's Encrypt, Coolify, egress-aware
- [ ] Create `contexts/pi.cue` — ARM images, reduced services, tmpfs
- [ ] Context-driven PAAS selection (Dokploy for local, Coolify for cloud)
- [ ] Context-driven TLS strategy (self-signed vs Let's Encrypt)
- [ ] Context-driven resource limits

#### Backend Split

- [ ] `base-kit-local` vs `base-kit-cloud` CUE differentiation
- [ ] `#NodeDefinition.type` extension: `"local" | "cloud"` with different SSH defaults
- [ ] Cloud provider abstraction: Hetzner module as first cloud backend
- [ ] VPN bridging schema for hybrid setups (Headscale/WireGuard)

**Done Criteria:** `stackkit apply` works with `--context local` and `--context cloud`. Context auto-detection from hardware.

---

### M3: StackKit Completion (Weeks 9–12)

**Goal:** All three StackKits implemented as architecture patterns per v4.

#### Base Kit Refinement

- [x] Remove old `variants/` directory (replaced by Add-Ons and Contexts) — TD-12, resolved 2026-02-14
- [x] Consolidate to single schema (`#BaseKitStack` only) — TD-07, resolved 2026-02-12
- [ ] Update spec format to v2 `kombination.yaml`
- [ ] Context × base matrix tests (local, cloud, pi)

#### Modern Homelab — COMPLETE REWRITE

Current state: entirely K8s/k3s-based. Must be rewritten as **hybrid Docker multi-node**.

- [ ] Delete all K8s/FluxCD/Longhorn schemas and tests
- [ ] Define VPN overlay networking as core requirement
- [ ] Implement Coolify as default PAAS (required for multi-environment)
- [ ] Add split DNS configuration (local vs public)
- [ ] Define service placement rules (which services go where)
- [ ] Implement `modern × local` and `modern × cloud` contexts
- [ ] Create E2E test with 2-node deployment (1 local + 1 cloud)

#### High Availability Kit

- [ ] Implement Docker Swarm orchestration config
- [ ] Add Keepalived VIP for load balancing
- [ ] Define quorum-based consensus rules in CUE
- [ ] Implement LLDAP cluster + Step-CA HA
- [ ] Implement `ha × local` and `ha × cloud` contexts
- [ ] Mark `ha × pi` as not recommended (resource validation)

**Done Criteria:** All StackKit x Context combinations validate (`ha × pi` excluded). Each StackKit has ≥1 deployable configuration.

---

### M4: Add-On System (Weeks 11–14)

**Goal:** Composable Add-Ons replace monolithic variants. First Add-Ons are functional.

#### Add-On Infrastructure

- [ ] Define `#AddOn` CUE schema (metadata, compatibility, constraints, resources, services)
- [ ] Create `addons/` directory structure with `_schema/addon.cue`
- [ ] Implement Add-On dependency resolution in CUE
- [ ] CLI commands: `stackkit addon add/list/remove/search`

#### Migrate Variants → Add-Ons

- [ ] `base-kit/variants/service/coolify.cue` → `addons/coolify-paas/`
- [ ] `base-kit/variants/service/beszel.cue` → `addons/monitoring/` (subset)
- [ ] `base-kit/variants/service/minimal.cue` → `contexts/pi.cue` defaults (fold in)
- [ ] `base-kit/variants/service/default.cue` → base defaults (fold in)
- [ ] `base-kit/variants/compute/compute.cue` → Context system
- [ ] `base-kit/variants/os/*.cue` → OS detection (auto, not user-chosen)
- [ ] Delete `base-kit/variants/` directory after migration

#### Core Add-Ons

- [ ] `addons/monitoring/` — Prometheus + Grafana + Alertmanager
- [ ] `addons/backup/` — Restic + configurable targets
- [ ] `addons/vpn-overlay/` — Headscale/Tailscale mesh
- [ ] `addons/gpu-workloads/` — NVIDIA/AMD GPU passthrough (local/cloud only)
- [ ] `addons/media/` — Jellyfin + *arr stack
- [ ] `addons/smart-home/` — Home Assistant + MQTT (local/pi only)

**Done Criteria:** `#AddOn` schema defined. ≥3 Add-Ons migrated from variants. CLI commands functional.

---

### M5: CUE Decision Logic (Weeks 13–15)

**Goal:** All documented-but-unimplemented CUE constraints are enforced.

#### Priority A (Block incorrect deployments)

- [ ] D1: Network mode decision (local → Bridge, public → Traefik+ACME, hybrid → VPN+Split-DNS)
- [ ] D2: PAAS auto-selection (local domain → Dokploy, public → Coolify)
- [ ] D4: Firewall port auto-generation (from `services[*].network.ports`)
- [ ] D9: TLS ACME domain constraint (ACME + .local = error)
- [ ] D14: Container image version policy (no `latest` in production)

#### Priority B (Security & stability)

- [ ] D3: Identity provider cascade (zeroTrust → TinyAuth OR PocketID must be active)
- [ ] D5: Volume backup filter (auto from `volumes[backup==true]`)
- [ ] D7: Resource budget validation (sum services RAM ≤ node RAM)
- [ ] D8: Port collision detection (duplicate host ports)
- [ ] D10: Node count platform constraint (docker-swarm → min 3 nodes)

#### Priority C (Extended logic)

- [ ] D6: Service dependency validation (`needs[]` references enabled service)
- [ ] D11: Variant→Add-On feature matrix (CUE logic, not manual tests)
- [ ] D12: Upgrade path validation (allowed Add-On transitions)
- [ ] D13: mTLS service policy (StepCAMTLSPolicy enforced)

**Done Criteria:** `cue vet ./...` checks all constraints. Invalid configs rejected with clear messages.

---

### M6: Terramate & Day-2 Operations (Weeks 15–17)

**Goal:** Terramate integrated into CLI. Drift detection functional.

- [ ] Terramate integration in CLI: `stackkit drift` command
- [ ] Terramate change detection: `terramate run --changed` workflow
- [ ] Terramate stack tags for layer assignment (`stack.tags = ["layer:1", "identity"]`)
- [ ] Drift detection as scheduled run
- [ ] OpenTofu state backend strategy: S3 for prod, local for dev, per Context
- [ ] OpenTofu provider locking (`.terraform.lock.hcl`)
- [ ] CUE schema versioning

**Done Criteria:** `stackkit drift --check` detects deviations.

---

### M7: kombify-TechStack Integration (Weeks 17–21)

**Goal:** Unifier pipeline in kombify-TechStack understands StackKits v4.

- [ ] Update `resolver.go`: StackKit selection by architecture pattern (not node count)
- [ ] Update `addons.go`: load Add-Ons from `addons/` directory
- [ ] Update `analyze.go`: generate Node-Context from agent hardware reports
- [ ] Update `unify.go`: merge StackKit + Context + Add-Ons into unified CUE evaluation
- [ ] Update `stackkit_loader.go`: load `contexts/*.cue` alongside StackKit schemas
- [ ] Align CUE module path across repos
- [ ] Update web wizard for 3-concept flow (pattern → nodes → add-ons → customize → deploy)
- [ ] Agent context auto-detection via `Register` RPC hardware reports

**Done Criteria:** Unifier pipeline processes StackKit + Context + Add-Ons. Web wizard reflects v4.

---

### M8: Beyond-IaC & AI Foundation (Weeks 19–25)

**Goal:** Runtime intelligence layer prototype. AI-assisted operations as SaaS concept.

#### gRPC Agent Integration

- [ ] CUE outputs consumable by gRPC agent
- [ ] `kombination.yaml` structure harmonized with StackKit schemas
- [ ] Agent capabilities as CUE schema (`#NodeCapabilities`)
- [ ] Service placement algorithm (filter → score → place) as CUE constraints

#### Integration Paths v1

- [ ] CUE schema for `#IntegrationPath` (type, direction, auth, events)
- [ ] First implementations: Cloudflare DNS, Slack/Discord webhooks
- [ ] Integration events: `service.deployed`, `health.degraded`, `backup.completed`

#### AI Self-Healing (Prototype)

- [ ] Pipeline: Detect → Diagnose → Heal
- [ ] Escalation model: Low (auto-restart) → Medium (rollback) → High (rebalance) → Critical (notify)
- [ ] Health score calculation (0–100)
- [ ] Anomaly detection baseline

**Done Criteria:** base-kit deployment sends status via gRPC agent and creates DNS record at Cloudflare.

---

### M9: Documentation & Public Readiness (Parallel, Weeks 15–25)

**Goal:** Public docs are current, consistent, and complete.

#### Mintlify Docs

- [ ] Beyond-IaC concept page (K12)
- [ ] Add-On system concept page (K13)
- [ ] Persona system concept page (K14)
- [ ] Local/Cloud split documentation (K15)
- [ ] All StackKit pages updated to v4
- [ ] Migration guides (base → ha, base → modern)
- [ ] Visual decision tree (Mermaid): which StackKit for which use case
- [ ] CLI + Add-On documentation

#### Marketing & Website

- [ ] Fix "Terraform" → "OpenTofu" on marketing site (K10)
- [ ] Remove K8s references from marketing
- [ ] Consolidate `marketing/` vs `website-v2/` (decide canonical site)

#### API Documentation

- [ ] Catalog endpoints (`GET /api/v1/stackkits`, `GET /api/v1/stackkits/{name}`)
- [ ] Validation endpoint (`POST /api/v1/validate`)
- [ ] Generation endpoint (`POST /api/v1/generate/tfvars`)

**Done Criteria:** Every Mintlify page shows current content. No dead links. No K8s references.

---

## Timeline Overview

```
2026 Q1 (Feb–Mar)
  ├── M0: Hygiene & v4 Migration ────────┤  (Weeks 1–3, IN PROGRESS)
  ├── M1: Core IaC Pipeline ────────────┤  (Weeks 3–6)
  └── M2: Context & Backend Split ──────┤  (Weeks 5–8)

2026 Q2 (Apr–May)
  ├── M3: StackKit Completion ──────────┤  (Weeks 9–12)
  ├── M4: Add-On System ────────────────┤  (Weeks 11–14)
  └── M5: CUE Decision Logic ──────────┤  (Weeks 13–15)

2026 Q2–Q3 (Jun–Jul)
  ├── M6: Terramate & Day-2 ───────────┤  (Weeks 15–17)
  ├── M7: kombify-TechStack Integration ───┤  (Weeks 17–21)
  ├── M8: Beyond-IaC & AI ────────────┤  (Weeks 19–25)
  └── M9: Docs & Readiness ───────────┤  (parallel, Weeks 15–25)
```

**Overlaps are intentional:** M9 (Docs) runs parallel to M6–M8. M1–M2 overlap on CUE/OpenTofu work.

---

## Dependency Graph

```
M0 (Hygiene)
 │
 ├──── M1 (IaC Pipeline) ──── M2 (Context) ──── M3 (StackKit Completion)
 │                                                       │
 ├──── M9 (Docs) [parallel from M0] ────────────────────┤
 │                                                       │
 │                                      M4 (Add-Ons) ───┤
 │                                      M5 (CUE Logic) ─┤
 │                                                       │
 │                                      M6 (Terramate) ─┤
 │                                      M7 (Stack Int.) ─┤
 │                                                       │
 │                                      M8 (Beyond-IaC) ─┤
```

**Blocking:** M0→M1→M2→M3→M4, M1→M6, M3→M7, M7→M8  
**Non-blocking:** M0 starts now. M9 runs anytime. M4+M5 can begin parallel to M3.

---

## Risks

| Risk | Probability | Impact | Mitigation |
|------|:-----------:|:------:|------------|
| CUE export complexity underestimated | Medium | High | Prototype with 1 service first, not all at once |
| modern-homelab rewrite scope | High | Medium | Clear differentiation from ha-kit in M3 |
| AI features too ambitious | High | Low | M8 explicitly scoped as prototype |
| Cross-repo synchronization drift | Medium | Medium | M0 creates the basis, M9 maintains |
| Single-developer bottleneck | High | High | Small milestones, feedback loops, pipeline automation |
| Old concepts leaking into new code | Medium | High | M0 archival + TECHNICAL_DEBT.md tracking |

---

## Success Metrics

| Metric | Target |
|--------|--------|
| `cue vet` passes for all StackKits | 100% |
| E2E deployment success (base, local) | > 95% |
| StackKit × Context combinations validated | 8/9 (ha×pi excluded) |
| All variants migrated to Add-Ons | 100% |
| Unifier processes v4 format | Yes |
| Documentation coverage | > 90% |
| Time to deploy (base, local, no add-ons) | < 10 min |
| Zero K8s/variant references in active docs | Yes |

---

## Database & API Status

**StackKits does NOT use a database.** It's a CUE schema + OpenTofu repo.

The **StackKit catalog/admin UI** stores data in `kombify-DB` under `content_stackkits`, `content_stackkit_tools`, etc.

| What | Where |
|------|-------|
| CUE schemas, OpenTofu configs | This repo |
| StackKit catalog data | `kombify-DB` → `content_*` tables |
| Prisma schema for TS admin UI | `kombify-DB/prisma/schema.prisma` |
| SQL migrations | `kombify-DB/migrations/000003_content.up.sql` |

**API server status:**

| Component | Status |
|-----------|--------|
| HTTP scaffold (Go, port 8082) | ✅ Done |
| OpenAPI spec at `/api/v1/openapi.yaml` | ✅ Done |
| Catalog endpoints (list, get, schema, defaults, variants) | ✅ Done (5 endpoints) |
| Validation endpoints (full + partial) | ✅ Done (2 endpoints) |
| Generation endpoints (tfvars + preview) | ✅ Done (2 endpoints) |
| Utility endpoints (health, capabilities) | ✅ Done (2 endpoints) |
| Authentication middleware | ✅ Done (TD-28, API key with `--api-key` flag / `STACKKITS_API_KEY` env) |
| Rate limiting | ✅ Done (TD-33, per-IP sliding window, `--rate-limit` flag, default 60/min) |
| API handler tests | ✅ Done (TD-34, 42 test cases covering all handlers + middleware) |
| Filesystem write fix (outputDir) | ✅ Done (TD-27, removed from request, uses temp dir) |
| CORS configuration | ✅ Done (TD-40, `--cors-origins` flag with per-request matching) |
| Pagination | ✅ Done (TD-41, `?limit=N&offset=M` with envelope response) |
| Structured errors | ✅ Done (TD-32, category/code/suggestions in JSON responses) |

---

## Architecture Reference

See [ARCHITECTURE_V4.md](./ARCHITECTURE_V4.md) for detailed architecture documentation.

Key concepts:
- **StackKit** = Architecture pattern (base / modern / ha)
- **Node-Context** = Runtime environment (local / cloud / pi), auto-detected
- **Add-Ons** = Composable extensions (replace variants)
- **Progressive Capability Model** = Levels 0–4 (CLI → SaaS)
- **3-Layer Architecture** = L1 Foundation, L2 Platform, L3 Applications (preserved)

---

*This document is updated at each milestone completion. Next review: after M1.*
