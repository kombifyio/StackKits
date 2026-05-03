# CUE Audit & Professionalization Plan

> **Date**: 2026-04-07 (audit), updated 2026-04-17 (V5/V6 priority alignment)
> **Scope**: BaseKit CUE schemas, Go generation pipeline, module contracts
> **Goal**: Identify gaps between CUE architecture and actual code generation, plan the path to CUE-driven composition with real dependency resolution.
> **V6 alignment:** This plan's Phase 3–5 is the concrete implementation of what V6 calls **"CUE Decision Logic"** (ADR-0008). Completing Phases 3–5 is the prerequisite for V6 acceptance.
> **Priority (Q2–Q3/2026):** Phase 3 first (unblocks fragment generation), then Phase 5 (composition engine → unblocks wizard/intent), then Phase 4 (context integration can run in parallel with Phase 5).

---

## Part 1: Audit — Where CUE und Go Diverge

### 1.1 The Core Problem: CUE validates, Go generates

The StackKits architecture claims "CUE is the single source of truth." In reality, CUE and Go operate as **parallel, independent systems**:

| Concern | CUE Schema | Go Pipeline | Aligned? |
|---------|-----------|-------------|----------|
| Service catalog | `services.cue` (15 services) | `catalog.go` (11 entries + dashboard) | **Manual mirror** |
| Domain computation | `#DomainConfig` with prefix logic | `catalog.go` DomainEntries() | **Manual mirror** |
| PAAS auto-selection | `#ContextDefaults` (local→Dokploy, cloud→Coolify) | `bridge.go::specToTFVars()` hardcoded switch | **Parallel logic** |
| Compute tier defaults | `#SmartDefaults` (tier→services, docker limits, backup) | `bridge.go` hardcoded tier checks | **Parallel logic** |
| Module dependencies | `#ModuleContract.requires/provides` in 14 modules | **Not read by Go at all** | **Ignored** |
| Context overrides | `#ContextOverrides` per module (resources, TLS mode) | **Not read by Go** | **Ignored** |
| Settings classification | `#SettingsSpec` (perma vs flexible) | **Not enforced** | **Ignored** |
| Security constraints | `#ContainerSecurityContext` in each module | Hardcoded in `main.tf` | **Parallel** |
| Network isolation | `base/network.cue` network definitions | Hardcoded in `main.tf` | **Parallel** |
| Layer validation | `base/layers.cue` L1/L2/L3 enforcement | **Not used** | **Ignored** |
| Deployment mode | `#DeploymentConfig` simple/advanced with day1/day2 | Go only uses mode to pick template dir | **Partial** |
| Resource limits | Per-module in each `module.cue` | Hardcoded per-service in `main.tf` | **Parallel** |

**Severity**: 6 concepts are completely ignored by the generation pipeline. 5 exist as parallel implementations that can drift. Only 2 are properly aligned (via manual mirrors created last session).

### 1.2 What the Module Contracts Define But Never Use

Each of the 14 module contracts (`modules/*/module.cue`) defines:

```
requires: { services: {...}, infrastructure: {...} }
provides: { capabilities: [...], middleware: {...}, endpoints: {...} }
settings: { perma: {...}, flexible: {...} }
contexts: { local: {...}, cloud: {...}, pi: {...} }
services: { name: #ServiceDefinition }
```

**Example — Traefik depends on socket-proxy:**
```cue
// modules/traefik/module.cue
requires: {
    services: {
        "socket-proxy": { provides: ["docker-api-proxy"] }
    }
}
```

**Example — TinyAuth depends on Traefik AND socket-proxy:**
```cue
// modules/tinyauth/module.cue
requires: {
    services: {
        traefik: { minVersion: "3.0", provides: ["reverse-proxy", "forwardauth-host"] }
        "socket-proxy": { provides: ["docker-api-proxy"] }
    }
}
```

These dependency declarations exist in CUE but are **never validated at generation time**. The Go pipeline generates everything regardless of whether dependencies are satisfied. If someone disables `socket-proxy`, Traefik will fail at runtime — no build-time error.

### 1.3 BaseKit Mode/Variant Gaps

**Modes (simple vs advanced):**
- CUE `#DeploymentConfig` defines day1 (OpenTofu) and day2 (Terramate) operations
- Go uses `spec.Mode` only to select template dir (`templates/simple/` vs `templates/advanced/`)
- **Gap**: No `templates/advanced/` directory exists. Advanced mode falls back to simple.
- **Gap**: Day2 operations (drift detection, rolling updates, stack ordering) are schema-only — no Go implementation

**Variants (default/coolify/beszel/minimal):**
- CUE `#ServiceSet` defines which services are valid per variant
- Tests validate 4 variants (default, coolify, beszel, minimal)
- Go `bridge.go` ignores variants entirely — uses PAAS/tier/context to determine services
- **Gap**: `variant` field in `#BaseKitStack` isn't connected to generation. It's CUE test scaffolding only.
- **Status**: Architecture v5 explicitly deprecated variants in favor of use-case/tool-role model (`docs/ARCHITECTURE_V5.md`). The variant field remains for backwards compatibility with CUE tests.

**Context-driven behavior:**
- CUE modules define context overrides (e.g., Traefik local→self-signed, cloud→letsencrypt)
- Go `bridge.go` has independent context logic (isLocalMode, isKombifyMe, etc.)
- **Gap**: Module-level context overrides are never read. Go reimplements the same decisions.

**Compute tier → service selection:**
- CUE `#SmartDefaults` maps tiers to service sets, Docker limits, backup policies
- Go `bridge.go` has independent tier logic (`isStandardPlus` for Jellyfin/Immich)
- **Gap**: CUE tier defaults aren't used. Go hardcodes which services are tier-gated.

### 1.4 What BaseKit Gets Right

- **71 CUE files** with consistent schemas — the design is solid
- **14 production module contracts** with real dependency metadata
- **Full test coverage** — CUE tests validate all variants, modes, tiers
- **CI validation** — `cue vet` runs on every PR
- **Security hardening** — All module contracts define security constraints
- **First-boot provisioning** — Provisioner pattern formalized in CUE
- **3-layer architecture** — Clean L1/L2/L3 separation in schemas
- **catalog.go** — At least dashboard/domain generation now uses a catalog pattern (from last session)

---

## Part 2: Professionalization Plan

### Priority Order

The dependencies between changes dictate the order:

```
Phase 1: CUE Reader         (Go can read module contracts)
    ↓
Phase 2: Dependency Graph    (topological sort, validation)
    ↓
Phase 3: Module Fragments    (per-module .tf generation)
    ↓
Phase 4: Context Integration (context overrides at generation time)
    ↓
Phase 5: Composition Engine  (full CUE-driven assembly)
```

Each phase is independently valuable and deployable.

---

### Phase 1: CUE Contract Reader (Foundation) — ✅ COMPLETED

**Completed**: 2026-04-07

**What was done:**
- Added `subdomain` and `dashboard` fields to 11 module CUE contracts (traefik, tinyauth, pocketid, dokploy, dozzle, whoami, uptime-kuma, dashboard, vaultwarden, jellyfin, immich)
- Extended `ServiceDef` in `extractor.go` with subdomain/dashboard fields
- Updated `extractService()` to read subdomain and dashboard from CUE values
- Created `ServiceCatalogFromModules()` and `DomainEntriesFromModules()` in `catalog.go`
- Wired `generate.go` to use CUE-driven catalog with graceful fallback to hardcoded entries
- Kept `ServiceCatalog()` and `DomainEntries()` as deprecated fallbacks
- Coolify and Dockge remain as fallback entries (no module CUE contracts yet)
- All tests pass

---

### Phase 2: Dependency Graph Resolution — ✅ COMPLETED

**Completed**: 2026-04-07

**What was done:**
- Created `internal/composition/graph.go` with `DependencyGraph` type
- `BuildGraph()` constructs directed graph from module contracts' `requires.services`
- `TopologicalSort()` returns deployment order using Kahn's algorithm
- `Validate()` checks: missing modules, missing capabilities, cycle detection
- `DependenciesOf()` and `TransitiveDependencies()` for querying
- Wired into `generate.go`: validates dependency graph at generation time, logs deployment order
- 8 unit tests covering: build, topological sort, validation (OK, missing module, missing capability), cycle detection, direct deps, transitive deps
- All tests pass

---

### Phase 3: Per-Module Terraform Fragments — **PRIORITY 1 (Q2/2026)**

**Goal**: Replace the 2000-line monolithic `main.tf` with per-module `.tf` fragments generated from module contracts.

**Why third / Priority 1**: Phases 1+2 give us readable contracts and generation order. Phase 3 uses that to generate modular Terraform. **Without Phase 3, V6 BaseKit cannot ship with out-of-the-box hardening modules** (security-baseline, admin-bootstrap, login-gateway), because adding them to the monolithic main.tf is untestable. Phase 3 is the V6 unblock.

**Implementation:**

1. **Module template pattern**
   - Each module gets a template: `modules/{name}/templates/main.tf.tmpl`
   - Template uses Go template syntax with CUE-extracted data
   - Variables, resources, outputs per module

2. **Shared infrastructure fragments**
   - `providers.tf` — Terraform providers (always generated)
   - `networks.tf` — Docker networks (from enabled modules' network requirements)
   - `variables.tf` — All variables used by enabled modules
   - `outputs.tf` — Aggregated outputs

3. **Generation pipeline**
   - For each enabled module (in dependency order):
     - Read module contract (Phase 1)
     - Render module template with contract data
     - Write `modules/{name}.tf` to output dir
   - Generate shared fragments
   - Generate dashboard from catalog data

4. **Migration path**
   - Keep monolithic `main.tf` template as fallback during migration
   - Add `--fragments` flag to `stackkit generate` for new pipeline
   - Remove monolithic template when all modules have fragment templates

**Validation**: `tofu validate` on generated output, integration test with `tofu plan`.

**Estimated scope**: 14 module templates (~100-200 lines each), updated generator (~200 lines), shared fragment generator (~300 lines).

---

### Phase 4: Context-Driven Generation — **PRIORITY 3 (Q3/2026, parallel to Phase 5)**

**Goal**: Apply CUE module context overrides at generation time instead of hardcoding context logic in Go.

**Why fourth / Priority 3**: Phase 3 gives us per-module generation. Phase 4 makes that generation context-aware using CUE data. **Can run in parallel with Phase 5** because they touch different parts of the pipeline (4 = generation, 5 = resolution).

**Implementation:**

1. **Read context overrides from CUE**
   - Each module's `contexts: { local: {...}, cloud: {...}, pi: {...} }` provides overrides
   - Extract at contract load time (Phase 1 extension)

2. **Apply overrides in template rendering**
   - TLS mode: local→self-signed, cloud→letsencrypt (from module CUE, not Go hardcode)
   - Resource limits: pi context reduces memory/CPU allocations
   - Log levels: pi context uses WARN instead of INFO
   - PAAS selection: from CUE context defaults instead of Go switch statement

3. **Remove parallel logic from bridge.go**
   - Delete hardcoded context switches
   - Context decisions flow from CUE through Go to templates
   - Single source of truth achieved

**Validation**: Generate for all 3 contexts, diff output to verify correct context overrides.

**Estimated scope**: Contract reader extension (~100 lines), bridge.go cleanup (~-200 lines deleted).

---

### Phase 5: Composition Engine — **PRIORITY 2 (Q3/2026)**

**Goal**: Full CUE-driven stack assembly — user declares intent, engine resolves modules + dependencies + configuration.

**Why / Priority 2**: This is the capstone. **Required before the 4-question wizard (both TechStack web and StackKits CLI)** can produce deployable stacks. The wizard outputs use-case intent; Phase 5 turns intent into a fully-resolved stack.

**Implementation:**

1. **Use-case resolution**
   - User declares `useCases: { photos: { enabled: true } }` in stack-spec.yaml
   - Engine resolves: photos → immich module → requires traefik + tinyauth → requires socket-proxy
   - Full dependency chain resolved automatically

2. **Add-on composition**
   - User declares `addons: ["monitoring", "backup"]`
   - Engine resolves addon modules and their dependencies
   - Validates compatibility (e.g., monitoring addon requires standard+ tier)

3. **Settings propagation**
   - Perma settings locked after first deploy (schema enforcement)
   - Flexible settings changeable at any time
   - Cross-module settings propagation (e.g., domain flows to all module endpoints)

4. **Stack validation**
   - Full CUE validation of composed stack before generation
   - Resource budget check
   - Network conflict detection
   - Port conflict detection

**Validation**: End-to-end test: stack-spec.yaml → CUE composition → fragment generation → `tofu plan`.

**Estimated scope**: New `internal/composition/engine.go` (~500 lines), stack-spec.yaml schema extension.

---

## Part 3: Immediate Actions

### Documentation Updates Needed

| Document | What to update |
|----------|---------------|
| `TASKS.md` | Mark CUE catalog (last session) as done; add Phase 1-5 tasks |
| `TECHNICAL_DEBT.md` | Add TD: CUE/Go parallel logic as active debt |
| `OVERVIEW.md` | No change needed — accurately describes the vision |
| `docs/CONCEPTS.md` | Add "Module Contract" as 7th core concept |
| `docs/ARCHITECTURE_V5.md` | Add section on CUE-to-Go generation pipeline status |

### Recommended Start: Phase 1

Phase 1 (CUE Contract Reader) is the foundation for everything else. It's self-contained, testable, and immediately valuable:

- **Eliminates `catalog.go` manual mirror** — no more dual maintenance
- **Proves the CUE→Go extraction pattern** — validates the approach for all later phases
- **Incremental** — doesn't break existing generation, adds a new code path
- **Testable** — load all 14 contracts, verify extraction against known values

### What NOT to Do Yet

- Don't break the monolithic `main.tf` until Phase 3 — it works, and premature fragmentation without dependency resolution will create more problems
- Don't implement the composition engine (Phase 5) before the graph resolver (Phase 2) — you need validated dependency ordering first
- Don't remove `variants` from CUE tests — they provide regression coverage until use-case model is fully implemented
- Don't try to make CUE generate Terraform directly — the Go template approach is correct, CUE should provide data, not generate HCL

---

## Part 4: Success Criteria

### Phase 1 Complete When:
- [ ] `go test ./internal/cue/...` loads all 14 module contracts from CUE
- [ ] Service catalog is extracted from CUE, not hardcoded in Go
- [ ] `catalog.go` is deleted
- [ ] `stackkit generate` output is byte-identical to current output

### Phase 2 Complete When:
- [ ] Disabling socket-proxy while traefik is enabled produces a clear error
- [ ] `stackkit generate` prints dependency tree
- [ ] Resource budget warning when total exceeds node capacity

### Phase 3 Complete When:
- [ ] `stackkit generate --fragments` produces per-module .tf files
- [ ] `tofu plan` succeeds on fragmented output
- [ ] Monolithic `main.tf` template deleted

### Phase 4 Complete When:
- [ ] Context overrides come from CUE, not Go hardcode
- [ ] `bridge.go` context switch statements removed
- [ ] All 3 contexts produce correct output from CUE data

### Phase 5 Complete When:
- [ ] `useCases: { photos: enabled: true }` auto-enables immich + dependencies
- [ ] Add-on composition validates compatibility
- [ ] Full CUE validation before generation
