# Base Kit v5 Alignment Implementation Plan

> **For Codex:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Bring `base-kit` from its current mixed v4/v5 transition state to a coherent v5 architecture where CUE is the real source of truth, generation flows through one path, Terraform is modular, and add-ons/use-cases/contexts are wired into deployable output without regressing the current working Base Kit.

**Architecture:** Use an incremental strangler migration. First lock down current behavior with characterization tests, then make CUE/spec metadata and generator agree on one canonical model, then split the monolithic Terraform into module-driven fragments, and only after that remove the legacy variant-era compatibility layer.

**Tech Stack:** Go, CUE, OpenTofu, Go text/template renderer, Docker provider, Terramate metadata, shell-based integration tests.

---

## Recommended Migration Path

**Recommended:** compatibility-first migration.

Why:
- The current `base-kit` is deployable today, so we should not freeze delivery until the full v5 model is perfect.
- The highest risk is semantic drift between `stackkit.yaml`, `stackfile.cue`, `generate.go`, `bridge.go`, and `templates/simple/main.tf`.
- Characterization tests let us refactor aggressively while preserving the current deployable footprint.

**Do not do first:** big-bang rewrite of `base-kit` CUE plus Terraform plus modules in one pass.

Why not:
- Too many moving parts: spec model, generator, module contracts, template rendering, CI, docs.
- It would make it hard to distinguish intended architecture changes from accidental deployment regressions.

## Success Criteria

- `base-kit/stackkit.yaml`, `base-kit/stackfile.cue`, and `pkg/models/models.go` describe the same deployment model.
- `stackkit generate` has one canonical tfvars generation path.
- `base-kit/templates/simple/main.tf` is split into module-oriented fragments or generated from module contracts.
- Every service currently deployed by Base Kit has either a module contract or an explicit documented exception.
- Add-ons and use-cases affect generated output, not just metadata and CLI listing.
- CI runs CUE validation and Base Kit module/integration coverage meaningfully.

## Scope Boundaries

- In scope: `base-kit`, shared generator/runtime code, module contracts needed for Base Kit, docs/tests/CI required to make the migration safe.
- Out of scope for this plan: rewriting `modern-homelab`, `ha-kit`, or designing a new control-plane product API.

### Task 1: Freeze Current Base Kit Behavior With Characterization Tests

**Files:**
- Modify: `cmd/stackkit/commands/generate.go`
- Modify: `internal/cue/bridge.go`
- Create: `tests/unit/basekit_generate_tfvars_test.go`
- Create: `tests/unit/basekit_template_contract_test.go`
- Test: `base-kit/templates/simple/main.tf`

**Step 1: Write the failing tests**

Add tests that snapshot the generated `terraform.tfvars.json` for these cases:
- local `home.lab` standard tier
- public custom domain with HTTPS
- `kombify.me`
- low tier with `dockge`
- explicit `paas: coolify`

Add tests that assert the generated output exposes and toggles these keys:
- `enable_traefik`
- `enable_tinyauth`
- `enable_pocketid`
- `enable_dokploy`
- `enable_dockge`
- `enable_coolify`
- `enable_uptime_kuma`
- `enable_vaultwarden`
- `enable_jellyfin`
- `enable_immich`
- `enable_dnsmasq`
- `reverse_proxy_backend`
- `paas`

**Step 2: Run tests to verify they fail or expose drift**

Run:
```bash
go test ./tests/unit -run BaseKit -v
```

Expected:
- either missing tests or mismatches in current generation assumptions

**Step 3: Add minimal harness helpers**

Create helper builders for `models.StackSpec` so future refactors compare behavior against a stable contract instead of stringly ad-hoc assertions.

**Step 4: Run tests to verify the harness passes against current behavior**

Run:
```bash
go test ./tests/unit -run BaseKit -v
```

Expected:
- PASS against the current Base Kit behavior

**Step 5: Commit**

```bash
git add tests/unit/basekit_generate_tfvars_test.go tests/unit/basekit_template_contract_test.go
git commit -m "test: lock current base-kit generation behavior"
```

### Task 2: Define the Canonical Base Kit v5 Model

**Files:**
- Modify: `base-kit/stackkit.yaml`
- Modify: `base-kit/stackfile.cue`
- Modify: `pkg/models/models.go`
- Modify: `docs/ARCHITECTURE.md`
- Modify: `docs/stack-spec-reference.md`
- Create: `docs/ADR/ADR-0007-base-kit-v5-canonical-model.md`

**Step 1: Write the failing schema/documentation test**

Add a test that asserts the Base Kit spec surface includes the same top-level concerns across Go and CUE:
- mode
- context
- compute tier
- PAAS selection
- use-cases
- add-ons
- services overrides

**Step 2: Run the test to verify the mismatch is real**

Run:
```bash
go test ./tests/integration -run LayerValidation_BaseKit -v
```

Expected:
- evidence that `#BaseKitStack` remains a simplified compatibility schema

**Step 3: Write the canonical model**

Make one explicit decision and document it:
- `stackkit.yaml` is metadata plus product-facing catalog
- `stackfile.cue` is the canonical deployment contract
- `models.StackSpec` mirrors the external user spec shape

Replace or shim the current variant-era fields in `base-kit/stackfile.cue` so they align with v5 concepts:
- keep temporary compatibility inputs if needed
- mark legacy `variant` as deprecated
- express current deployable services and use-cases explicitly

**Step 4: Update reference docs**

Update architecture/spec docs so they stop claiming contradictory states, especially around:
- variants vs add-ons
- bridge vs duplicate tfvars generation
- current deployable service set

**Step 5: Run validation**

Run:
```bash
cue vet ./base/... ./base-kit/...
go test ./tests/integration -run LayerValidation_BaseKit -v
```

Expected:
- validation passes or remaining gaps are narrowed to known temporary compatibility shims

**Step 6: Commit**

```bash
git add base-kit/stackkit.yaml base-kit/stackfile.cue pkg/models/models.go docs/ARCHITECTURE.md docs/stack-spec-reference.md docs/ADR/ADR-0007-base-kit-v5-canonical-model.md
git commit -m "docs: define canonical base-kit v5 model"
```

### Task 3: Collapse tfvars Generation to One Canonical Path

**Files:**
- Modify: `cmd/stackkit/commands/generate.go`
- Modify: `internal/cue/bridge.go`
- Modify: `tests/unit/basekit_generate_tfvars_test.go`

**Step 1: Write the failing test**

Add tests proving `runGenerate` uses the bridge output and that there is no divergent fallback logic between:
- `generateTfvarsJSON()`
- `TerraformBridge.GenerateTFVarsFromSpec()`

**Step 2: Run test to verify it fails**

Run:
```bash
go test ./tests/unit -run TFVars -v
```

Expected:
- FAIL because `generate.go` still owns generation logic

**Step 3: Move the real logic into the bridge**

Expand `internal/cue/bridge.go` until it is the full canonical generator for current Base Kit variables.

Delete or inline-remove `generateTfvarsJSON()` from `cmd/stackkit/commands/generate.go`.

**Step 4: Run tests**

Run:
```bash
go test ./tests/unit -run TFVars -v
go test ./... 
```

Expected:
- PASS

**Step 5: Commit**

```bash
git add cmd/stackkit/commands/generate.go internal/cue/bridge.go tests/unit/basekit_generate_tfvars_test.go
git commit -m "refactor: unify base-kit tfvars generation via bridge"
```

### Task 4: Map Current Deployable Services to Module Contracts

**Files:**
- Modify: `modules/*/module.cue`
- Create: `modules/vaultwarden/module.cue`
- Create: `modules/jellyfin/module.cue`
- Create: `modules/immich/module.cue`
- Create: `modules/dockge/module.cue`
- Create: `modules/coolify/module.cue`
- Create: `modules/*/tests/reference-compose.yml`
- Create: `modules/*/tests/integration_test.sh`
- Modify: `docs/ADR/ADR-0005-service-modules-as-atomic-unit.md`

**Step 1: Write failing inventory tests**

Add a test that compares the currently generated Base Kit service inventory against module coverage.

Minimum expected module coverage for currently deployed services:
- traefik
- tinyauth
- pocketid
- dokploy
- dockge
- dashboard
- uptime-kuma
- whoami
- vaultwarden
- jellyfin
- immich
- coolify installer abstraction

**Step 2: Run the test to verify missing module coverage**

Run:
```bash
go test ./tests/unit -run ModuleCoverage -v
```

Expected:
- FAIL for at least `vaultwarden`, `jellyfin`, `immich`, `dockge`, `coolify`

**Step 3: Create minimal module contracts**

For each missing deployable service:
- define metadata
- define dependencies
- define contexts
- define service definitions
- add a minimal reference compose proving core behavior

Do not migrate non-deployed legacy services yet unless needed for compatibility.

**Step 4: Run module-level tests**

Run:
```bash
bash modules/_integration/integration_test.sh
```

Expected:
- more module coverage without regressing existing proven modules

**Step 5: Commit**

```bash
git add modules docs/ADR/ADR-0005-service-modules-as-atomic-unit.md
git commit -m "feat: add module contracts for deployed base-kit services"
```

### Task 5: Split the Monolithic Base Kit Terraform Template

**Files:**
- Modify: `internal/template/renderer.go`
- Modify: `base-kit/templates/simple/main.tf`
- Create: `base-kit/templates/simple/00-providers.tf.tmpl`
- Create: `base-kit/templates/simple/10-network.tf.tmpl`
- Create: `base-kit/templates/simple/20-foundation-identity.tf.tmpl`
- Create: `base-kit/templates/simple/30-platform-paas.tf.tmpl`
- Create: `base-kit/templates/simple/40-platform-dashboard.tf.tmpl`
- Create: `base-kit/templates/simple/50-apps-compose.tf.tmpl`
- Create: `base-kit/templates/simple/90-outputs.tf.tmpl`

**Step 1: Write the failing test**

Add an output-shape test that compares generated file content before and after template split for one representative spec.

**Step 2: Run test to verify it fails**

Run:
```bash
go test ./tests/unit -run TemplateSplit -v
```

Expected:
- FAIL until the renderer emits equivalent Terraform

**Step 3: Extract the template into fragments**

Preserve behavior first. Do not change service semantics while splitting files.

Keep file ordering deterministic.

**Step 4: Run tests**

Run:
```bash
go test ./tests/unit -run TemplateSplit -v
stackkit generate --force
```

Expected:
- generated deploy output remains behaviorally equivalent

**Step 5: Commit**

```bash
git add internal/template/renderer.go base-kit/templates/simple
git commit -m "refactor: split base-kit simple terraform into fragments"
```

### Task 6: Drive Generation From Modules, Use-Cases, and Add-Ons

**Files:**
- Modify: `internal/cue/bridge.go`
- Modify: `cmd/stackkit/commands/generate.go`
- Modify: `cmd/stackkit/commands/addon.go`
- Modify: `base/generated/addons.cue`
- Modify: `base-kit/stackkit.yaml`
- Modify: `base-kit/stackfile.cue`
- Modify: `tests/unit/basekit_generate_tfvars_test.go`
- Create: `tests/integration/basekit_usecase_addon_test.go`

**Step 1: Write the failing tests**

Add end-to-end generation tests showing that:
- enabling a use-case changes generated service output
- enabling an add-on changes generated service output
- compute tier and context alter module selection only through one decision path

**Step 2: Run tests to verify they fail**

Run:
```bash
go test ./tests/integration -run BaseKitUsecaseAddon -v
```

Expected:
- FAIL because add-ons are currently discoverable but not generation-driving

**Step 3: Implement module selection**

Define one decision pipeline:
- spec inputs
- context resolution
- compute tier adjustments
- default platform services
- default use-case modules
- explicit add-ons
- service overrides

Emit Terraform fragments from that resolved module graph.

**Step 4: Run tests**

Run:
```bash
go test ./tests/integration -run BaseKitUsecaseAddon -v
go test ./...
```

Expected:
- PASS

**Step 5: Commit**

```bash
git add internal/cue/bridge.go cmd/stackkit/commands/generate.go cmd/stackkit/commands/addon.go base/generated/addons.cue base-kit/stackkit.yaml base-kit/stackfile.cue tests/integration/basekit_usecase_addon_test.go
git commit -m "feat: wire base-kit use-cases and add-ons into generation"
```

### Task 7: Remove or Isolate Legacy Variant-Era Drift

**Files:**
- Modify: `base-kit/stackfile.cue`
- Modify: `base-kit/services.cue`
- Modify: `base-kit/README.md`
- Modify: `docs/ARCHITECTURE.md`
- Modify: `TECHNICAL_DEBT.md`

**Step 1: Write the failing test**

Add tests asserting there is no unsupported legacy service advertised in CUE unless one of these is true:
- it is still deployable
- it is explicitly marked legacy/deprecated
- it has a migration note to an add-on or module

**Step 2: Run the test to verify the drift**

Run:
```bash
go test ./tests/unit -run LegacyBaseKitDrift -v
```

Expected:
- FAIL because `beszel`, `dozzle`, `netdata`, `portainer`, and variant references still drift

**Step 3: Clean up**

Either:
- remove legacy-only service advertising from Base Kit, or
- move it behind explicit compatibility/deprecation markers

Update README and technical debt accordingly.

**Step 4: Run tests**

Run:
```bash
go test ./tests/unit -run LegacyBaseKitDrift -v
cue vet ./base-kit/...
```

Expected:
- PASS

**Step 5: Commit**

```bash
git add base-kit/stackfile.cue base-kit/services.cue base-kit/README.md docs/ARCHITECTURE.md TECHNICAL_DEBT.md
git commit -m "cleanup: remove legacy variant drift from base-kit"
```

### Task 8: Repair Validation and CI So the New Model Holds

**Files:**
- Modify: `.github/workflows/ci.yml`
- Modify: `base-kit/tests/run_tests.sh`
- Modify: `tests/run_validation.sh`
- Modify: `cue.mod/module.cue`
- Modify: `TECHNICAL_DEBT.md`

**Step 1: Write failing CI-focused tests or scripts**

Add reproducible local commands for:
- CUE module resolution
- module tests expected to run in CI
- Base Kit generation smoke tests

**Step 2: Reproduce the current failures**

Run:
```bash
cue vet ./base-kit/...
go test ./...
```

Expected:
- confirm current local/CI mismatch and document it

**Step 3: Fix CI**

Resolve:
- CUE module resolution for `github.com/kombifyio/stackkits/base`
- which module tests are mandatory vs quarantined
- one Base Kit generation smoke test in CI

**Step 4: Run verification**

Run:
```bash
cue vet ./base/... ./base-kit/...
go test ./...
bash base-kit/tests/run_tests.sh
```

Expected:
- green local validation path that matches CI intent

**Step 5: Commit**

```bash
git add .github/workflows/ci.yml base-kit/tests/run_tests.sh tests/run_validation.sh cue.mod/module.cue TECHNICAL_DEBT.md
git commit -m "ci: enforce base-kit v5 validation path"
```

## Execution Order

1. Task 1
2. Task 2
3. Task 3
4. Task 4
5. Task 5
6. Task 6
7. Task 7
8. Task 8

## Practical Milestones

- **Milestone A:** Tasks 1-3 complete
  - Safe refactor baseline
  - One canonical spec and tfvars generation path

- **Milestone B:** Tasks 4-5 complete
  - Deployable services mapped to modules
  - Terraform no longer monolithic

- **Milestone C:** Tasks 6-8 complete
  - v5 generation semantics live
  - legacy drift removed
  - CI enforces the new shape

## Risks and Mitigations

- **Risk:** breaking the currently deployable Base Kit while chasing architectural purity
  - Mitigation: characterization tests first, then refactor

- **Risk:** module work stalls because some services are awkward to isolate
  - Mitigation: allow explicit temporary exceptions, but make them documented and test-tracked

- **Risk:** docs continue to drift from code
  - Mitigation: update docs in the same task that changes the canonical model

- **Risk:** CI remains non-authoritative
  - Mitigation: do not call the migration done until CUE validation and Base Kit smoke tests run in CI

## First Session Recommendation

If work starts immediately, do only Tasks 1-3 in the first implementation session. That gives the team:
- one source of truth for spec semantics
- one tfvars generation path
- a safety net before touching module or Terraform fragmentation work
