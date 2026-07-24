# Changelog

All notable changes to kombify-StackKits are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [0.7.8] - 2026-07-24

> **Stable v0.x Modern Homelab federation-boundary patch** that partitions
> every generated federation handoff by runtime owner while keeping Modern in
> the public three-Kit release.

### Changed

- Modern federation Policy, Link, Control, Backup, and Observability now
  receive five distinct compiler-owned projections instead of shared
  `bridge`, `identity`, `data`, and `failurePolicy` graphs.
- Link authority is outbound-only, Control is allowlisted and
  replay-protected, Backup is limited to governed data placement and
  partition behavior, and Observability is explicitly evidence-only.
- The public release continues to ship dedicated
  `stackkits-modern-homelab` archives for Linux amd64/arm64, macOS
  amd64/arm64, and Windows amd64, plus Modern in the full StackKits bundle.

### Security

- Provider identities, credentials, endpoints, transport implementation,
  leases, server lifecycle, reverse tunnels, default routes, and general LAN
  authority cannot enter any of the five owner projections.
- Strict renderer decoding rejects extra fields and cross-owner authority;
  persisted-plan rebound rejects rehashed projection substitution.

### Known limitations

- Modern Homelab remains Preview until its remaining publication, Cloud
  verification, backend Health, partition-enforcement, and live-evidence
  owners graduate.
- Candidate, device, provider, browser, and compatibility evidence remains
  `pending/unverified` for this v0.x release.

## [0.7.7] - 2026-07-24

> **Stable v0.x Home internal-PKI runtime patch** that graduates the optional
> private TLS contract from generation-only to one authenticated authority-node
> owner without widening StackKits into certificate or infrastructure custody.

### Added

- One provider-free Product Runtime registration for the exact Home PKI
  authority node, bound to the generated artifact, Site, node, execution
  channel, request digest, and post-apply Health contract.
- Compiler-derived CA=false leaf identities carrying exact service, module,
  route, Site, node, subject, and DNS SAN authority.
- Separate root, leaf, public trust-distribution, and verification operations
  with fingerprint, public-key fingerprint, serial, validity, freshness, and
  trust-rotation continuity evidence.

### Changed

- Optional Home internal PKI is now `apply-ready`; its former unbound runtime
  owner is retired.
- Public trust-root distribution remains an exact compiler-owned Home
  Site/node target list while signing execution is restricted to the single
  explicit control-authority node.

### Security

- Root and leaf material, credentials, authenticated transport, endpoints,
  server providers, leases, and provider lifecycle remain construction-owned
  outside StackKits and cannot enter the generated policy or execution request.
- Ambiguous multi-controller CA authority continues to fail closed until a
  separate replicated/HA CA realization is defined.

### Known limitations

- Internal PKI remains optional and Home-only; it is not an implicit LAN
  discovery, public exposure, remote-access, or Cloud federation feature.
- Candidate, device, provider, browser, and compatibility evidence remains
  `pending/unverified` for this v0.x release.

## [0.7.6] - 2026-07-24

> **Stable v0.x Modern Homelab execution-boundary patch** that keeps all three
> StackKit families in the public release while graduating one exact Home-side
> bridge runtime.

### Added

- A provider-free, authenticated Modern origin-mTLS Runtime owner with one
  hash-bound artifact and one local execution target per Home origin node.
- Explicit compiler-owned `{nodeRef, instanceRef}` pairs, preventing
  independently sorted node and backend-instance sets from changing custody.
- Fresh local proxy, certificate, configuration, and revocation readback
  evidence bound to the exact service, backend, transport, and identity policy.

### Changed

- Modern origin-mTLS is `apply-ready`; its former
  `runtime-owner-unbound` blocker is retired.
- Policy evaluation and operation observation use separate trusted timestamps,
  allowing credentials issued during binding while rejecting stale, future, or
  overlong claims.
- The public release continues to ship the dedicated
  `stackkits-modern-homelab` archives and the Modern definition in the full
  StackKits archive.

### Security

- StackKits owns no certificate/private-key bytes, signing authority, proxy
  implementation, Cloud-verifier readiness, endpoints, provider lifecycle,
  leases, reverse tunnel, or general LAN access.
- Bridge publication and backend Health remain independent fail-closed
  authorities and are not implicitly graduated by this release.

### Known limitations

- Modern Homelab remains Preview until the separate publication, Cloud
  verification, backend Health, and live evidence owners graduate.
- Candidate, device, provider, browser, and compatibility evidence remains
  `pending/unverified` for this v0.x release.

## [0.7.5] - 2026-07-23

> **Stable v0.x Home-PKI architecture patch** that makes the v2 trust boundary
> explicit without claiming certificate execution that is not yet bound.

### Changed

- Home internal PKI now binds exactly one Stack-scoped root-CA authority to the
  explicit single Home control member.
- Trust distribution carries only the public root to exact compiler-derived
  Home Site/node pairs, including multi-Home-Site topologies.
- Leaf issuance is a separate, explicitly unbound contract with
  compiler-derived service subjects and SANs, `CA=false`, bounded usages, and
  required fingerprint, serial, validity, and observation evidence.
- Internal-PKI artifacts no longer carry host roles, failure domains, hardware
  inventory, or a generic private-key slot for every target node.

### Security

- Multi-controller internal PKI fails closed until a distinct CA-authority and
  HA/replication realization is defined.
- Worker and trust-distribution targets cannot acquire CA signing custody.
- Runtime materialization, rotation, and verification remain blocked until
  exact root/leaf custody and fresh postcondition owners are implemented.

### Known limitations

- Modern Homelab remains Preview. This patch changes its optional Home PKI
  contract but does not claim live provider/device compatibility or production
  federation.
- Candidate, device, provider, browser, and compatibility evidence remains
  `pending/unverified` for this v0.x release.

## [0.7.4] - 2026-07-23

> **Stable v0.x Modern Homelab runtime-boundary patch** that ships Modern
> Homelab alongside Basement Kit and Cloud Kit and advances the provider-free
> Architecture-v2 execution handoff.

### Added

- Modern Homelab bridge-publication artifacts with an exact two-Site
  Home-to-Cloud projection and a dedicated outbound-only origin-mTLS executor
  contract.
- An authenticated public-TLS runtime owner with typed materialize, renew, and
  verify operations; external ACME credentials and certificate custody remain
  outside StackKits.
- Optional Home internal-PKI, local-autonomy, and access-executor handoffs with
  closed Site, node, route, capability, and evidence scope.

### Changed

- Basement Compose generation now receives only its narrow, hash-bound
  workload handoff instead of a wider resolved-plan projection.
- Modern Homelab remains a first-class release family with dedicated Linux,
  macOS, and Windows archives in addition to the full release bundle.
- Obsolete unbound TLS and origin-identity readiness states were removed once
  their exact runtime contracts became construction-owned.

### Security

- Modern origin transport is TLS 1.3, exact-SNI, outbound-only, and cannot
  grant general LAN, reverse-tunnel, signing-key, credential, provider, lease,
  or lifecycle authority.
- Public-TLS execution rejects stale or widened claims, credential material,
  provider fields, shell authority, route substitution, and certificate-slot
  substitution.

### Known limitations

- Modern Homelab remains Preview. This release proves its public packaging and
  provider-free contract boundaries, not live provider/device compatibility
  or production federation.
- Candidate, device, provider, browser, and compatibility evidence remains
  `pending/unverified` for this v0.x release.

## [0.7.3] - 2026-07-23

> **Stable v0.x architecture patch** that closes the Cloud network and
> offsite-backup renderer boundaries without adding provider authority.

### Changed

- Cloud and Modern public-edge artifacts now contain only their exact
  compiler-owned routes, origin/backend nodes, access/TLS/Health authority,
  and minimal network posture.
- Optional Cloud private-admin-mesh artifacts now contain only the selected
  private, device-bound, default-closed routes, exact Cloud Site/node scope,
  and minimal network posture.
- Home and Cloud offsite-backup artifacts now contain only their hash-bound
  target requirements and optional external custody bindings.

### Security

- Generic storage, data, failure, DNS, domain, gateway, MTU, local
  reachability, endpoint, credential, lease, provider lifecycle, and
  server-provider fields cannot cross these migrated renderer boundaries.
- Added fields, provider substitution, route/Health/Site/node widening,
  LAN step-down, malformed network posture, and backup requirement/binding
  substitution fail closed.

### Known limitations

- These contracts remain provider-neutral. Provider selection, provisioning,
  transport, credentials, leases, and target lifecycle stay in TechStack or
  another external authority.
- Candidate, device, provider, browser, and compatibility evidence remains
  `pending/unverified` for this v0.x release.

## [0.7.2] - 2026-07-23

> **Stable v0.x patch** that publishes Modern Homelab as the third public
> StackKit family while preserving its honest Preview status.

### Added

- Dedicated Modern Homelab archives for Linux amd64/arm64, macOS amd64/arm64,
  and Windows amd64, alongside the full, Basement Kit, and Cloud Kit bundles.
- The public native-v2 Modern authority, two-Site initial-intent path, required
  federation contract, and archive-level semantic contract proof.

### Fixed

- Persisted canonical plans now decode integer health ports, timeouts, and
  expected HTTP status lists without weakening fractional-value rejection.
- Public affected CUE planning selects only roots present in the exported
  checkout while private source continues to cover every available kit root.

### Known limitations

- Modern Homelab remains Preview. Archive availability proves self-contained
  authoring and semantic contract validation, not completed live federation,
  runtime-owner graduation, provider compatibility, or production support.
- Candidate, device, provider, browser, and compatibility evidence remains
  `pending/unverified` for this v0.x release.

## [0.7.1] - 2026-07-22

> **Stable v0.x patch** for the provider-free RIL action handoff. This release
> adds an exact approval, execution, replay, and evidence boundary over the
> native Architecture-v2 plan without exposing provider lifecycle, raw host
> access, credentials, endpoints, or caller-selected commands.

### Added

- A CUE-governed catalog of seven closed RIL primitives with deterministic
  contract hashes, explicit approval and grant requirements, typed inputs,
  verification/recovery policy, and raw-authority prohibitions.
- The authenticated, tenant-isolated two-step StackKits delivery surface:
  `POST /api/v2/internal/ril-actions/resolve` binds the exact StackSpec and
  Inventory, while `POST /api/v2/internal/ril-actions/execute` accepts only the
  shared approved-action request and returns the shared redacted evidence.
- A persistence-neutral atomic execution-ledger contract with acquire, replay,
  in-progress, conflict, and token-fenced completion semantics. TechStack owns
  the durable Postgres/RLS implementation and outer dispatch custody.
- One deliberately read-only owner, `verify-stackkit-state`, which verifies
  the exact current governed plan and truthfully reports that no host/runtime
  state was observed.

### Security

- Requests with missing or expired approval/grants, stale or substituted
  plan/primitive/tenant/stack/target identity, conflicting replay, provider
  fields, raw SSH/Docker/OpenTofu authority, arbitrary paths, or caller
  commands fail before the governed owner.
- Internal resolution state is scoped by authenticated tenant plus Stack ID;
  the tenant scope cannot enter generated plans, action requests, evidence, or
  exported artifacts.

### Known limitations

- The remaining six primitives are `contract-only`. Mutating node owners,
  recovery execution, protected diagnostic retention, and product startup/API
  registration continue after this patch and do not silently fall back to v1.
- Candidate, device, provider, browser, and compatibility evidence is
  `pending/unverified` for this v0.x release. No pass is claimed; the publisher
  validates exact main, public export, archives, checksums, and release assets.

## [0.7.0] - 2026-07-22

> **Stable v0.x release** of the native Architecture-v2 line. Basement and
> Cloud use the same CUE-governed v2 identity from authoring through Apply;
> kit-specific topology, trust, ingress, backup, and runtime requirements stay
> explicit. Modern Homelab and adapters without concrete runtime evidence
> remain Preview or fail closed instead of falling back to v1 behavior.

### Highlights

- **Native v2 product path:** CLI, API, MCP, catalog, compiler, generators,
  artifacts, evidence, and governed executor handoffs bind exact v2 identities.
- **Three structural kit products:** Basement is Home-local, Cloud is
  Cloud-hosted, and Modern is Home-plus-Cloud federation. Multi-node does not
  select a kit, and High Availability remains an add-on.
- **Provider-free StackKits boundary:** external hosts, Cloud backup targets,
  Home encrypted backup targets, and federation access enter through opaque,
  hash-bound contracts. Provider lifecycle, credentials, leases, endpoints,
  and cleanup remain TechStack authority.
- **Fast beta operations:** v0.x publication validates source, public export,
  and release artifacts. Candidate, device, provider, browser, and
  compatibility evidence remains optional and is reported honestly when not
  supplied.

### Added

- Exact external Cloud and Home backup target bindings with customer/Home-held
  encryption authority, plaintext-egress denial, bounded freshness, and
  restore-verification requirements.
- Exact Modern federation and Home-access projections with typed blockers for
  missing or expired custody evidence and no general LAN or implicit transport
  fallback.
- Runtime adapter, workload, TLS, observability, backup, and executor-bundle
  contracts that remain fail closed until the responsible implementation and
  evidence are available.

### Changed

- New operational writes and rollout paths are v2-only. Legacy StackSpec v1 is
  limited to explicit read/migration compatibility and is no longer an
  operational fallback.
- Generated architecture authority, OpenAPI contracts, fixtures, and public
  export checks now track the same provider-neutral v2 source.

### Known limitations

- Modern Homelab, High Availability realizations, and runtime adapters still
  require their own implementation and fault evidence before graduation.
- Candidate E2E and compatibility evidence for this v0.x release is
  `pending/unverified`; no pass is claimed. Exact source/export/archive
  integrity is validated by the release workflow.

## [0.7.0-beta.1] - 2026-07-21

> **Prerelease** of the native Architecture-v2 line. Basement and Cloud now
> author, resolve, generate, inspect, and enter Apply through the same
> CUE-governed v2 identity without operational StackSpec-v1 or global-context
> fallback. Modern Homelab, HA realizations, external Home-access fabrics, and
> adapters without concrete evidence remain explicit Preview or fail-closed
> surfaces and do not become runtime-graduation claims.

### Highlights

- **One native v2 authority:** CLI, HTTP API, MCP, managed fetch, CAS storage,
  compiler, generators, evidence, and governed executors bind the same
  StackSpec, catalog, ResolvedPlan, artifact, and result identities.
- **Kit semantics are structural:** Basement and Cloud remain distinct
  single-Site/multi-node products over a shared foundation. Modern is the
  independent Home-plus-Cloud federation product; multi-node alone never
  selects it, and High Availability remains only an add-on.
- **Provider-free execution boundary:** StackKits consumes the merged shared
  `runtimeexecutor/v1beta1` contract while provider lifecycle, leases,
  credentials, endpoints, and cleanup remain TechStack authority.
- **Fast v0.x feedback:** normal development and publication use deterministic
  affected slices. Device, provider, browser, compatibility, and broad suites
  remain optional evidence rather than beta release gates.

### Added

- Exact StackInstance, Control Authority, multi-node, Fleet-isolation,
  device-bound identity, Home-access custody, host-conformance, route,
  federation, publication, data, failure, and executor-bundle contracts.
- Distinct Core, Basement-local, Cloud, workload, TLS, backup, observability,
  and Modern federation generation handoffs with typed readiness blockers.
- Canonical Apply requirements and evidence bounded by actual binding expiry,
  immutable artifact digests, one captured authorization instant, exact
  child-dispatch subsets, and adapter-declared access capabilities.

### Changed

- Operational StackSpec-v1 writers, mutators, runtime actions, setup/recovery
  paths, and remote transports are retired on v0.7. v1 remains only a
  read-only classification/validation and explicit migration input.
- The public OSS projection remains reproducible Basement/Cloud source. Private
  Modern authority, provider operations, credentials, and product-only
  surfaces are excluded structurally.

### Known limitations

- Modern Homelab and every HA realization remain Preview until their separate
  runtime and fault evidence exists.
- Optional external Home-access fabrics and runtime adapters without an exact
  registered implementation return typed blockers. No general LAN tunnel,
  provider lifecycle, or implicit fallback is introduced.
- Compatibility and Candidate evidence may remain `pending/unverified` for
  this v0.x prerelease; source and exported artifact integrity are still
  validated exactly.

## [0.6.0] - 2026-07-19

> **Stable** Architecture v2 contract release. This promotes the v0.6 line to
> the public `latest` while retaining the supported v1 Basement and Cloud
> rollout paths for the compatibility window. Architecture v2 remains
> deliberately fail-closed wherever a concrete renderer, executor, or runtime
> evidence contract is not implemented; this release does not claim Modern
> Homelab runtime graduation. The stable rollback baseline is `v0.5.2`.

### Highlights

- **Provider-free host admission is executable**: StackKits accepts an already
  supplied host only through an opaque, hash-bound `ExternalHostBinding` and
  produces a separate `HostConformanceReceipt`. Provider allocation, accounts,
  credentials, addresses, cleanup, and lifecycle remain outside StackKits.
- **Generation follows one governed v2 transaction**: current StackSpec and
  inventory are re-resolved, the exact plan is authorized, typed renderers run
  inside a held output root, and generated bytes, manifests, and receipts are
  verified before installation. Unsupported product slices stop before partial
  output or legacy fallback.
- **Home and Cloud trust are explicit kit contracts**: identity authorities,
  issuers, audiences, verifier placements, enrollment, and one-way verifier
  distribution are StackInstance-bound and kit-owned. Policy artifacts contain
  no credentials, signing keys, JWKS bytes, endpoints, or provider lifecycle
  data, and runtime enforcement remains evidence-blocked.
- **Kit identity is the architecture selector**: Basement, Cloud, and Modern
  Homelab now resolve their own CUE-owned capability plans. The legacy global
  `context` value no longer distinguishes products, and High Availability is
  an add-on with kit-specific realizations rather than a fourth kit.
- **Modern Homelab is the Home-plus-Cloud federation product**: Home authority,
  Cloud verifier placement, isolated publication, data authority, and
  partition behavior are modeled separately from multi-node topology. Modern
  remains Preview until its concrete bridge and runtime evidence graduate.

### Added

- Closed, compiler-owned projections and deterministic generation-only policy
  manifests for Home offline autonomy, local ingress/LAN access, optional LAN
  discovery, and Basement/Cloud/Modern identity trust.
- Provider-neutral host conformance observation and apply admission bound to
  the exact external-host binding and running StackKits binary.
- Governed Architecture v2 Runtime Action admission with an exact v2 envelope;
  provider, transport, and lifecycle verbs cannot enter the StackKits runtime
  boundary, and unimplemented execution ends at a typed fail-closed boundary.
- Explicit render instances, runtime-network instances, artifact ownership,
  current-resolution authorization, and concrete foundation/socket-proxy
  renderer contracts without inferring cardinality or node placement.

### Security

- Plans cannot self-authorize through copied labels or recomputed hashes: the
  service rebinds them to its frozen CUE definition and catalog and compares
  canonical bytes against a fresh resolution before generation or apply.
- Identity URNs, audiences, key-set references, host bindings, conformance
  receipts, artifacts, and execution receipts are bound to the exact Stack,
  plan, authority, and implementation inputs they protect.
- Home-to-Cloud verifier distribution is one-way and reference-only. Signing
  keys, credentials, Cloud-side device enrollment, reverse trust distribution,
  general LAN reachability, and provider lifecycle authority stay structurally
  unreachable.

### Compatibility

- Public compatibility claims are OS-only. Architecture, kernel, runtime,
  virtualization, device, lane, and provider facts remain private admission
  diagnostics and cannot become public support dimensions.
- Existing v0.5 StackSpec inputs remain readable for one minor compatibility
  window. New Architecture v2 plans use the governed contract and must be
  re-resolved when mandatory authority, instance, network, artifact, identity,
  or host-admission fields change.
- Basement and Cloud remain the supported rollout products. Modern Homelab is
  still Preview, and generation/apply readiness is reported per concrete
  implementation instead of being inferred from a kit name or release status.

## [0.6.0-beta.1] - 2026-07-16

> **Prerelease** of the StackKits Architecture v2 contract. This beta keeps the
> supported v1 Basement and Cloud rollout paths available while publishing the
> governed v2 resolve, migration, planning, and rendering authority for early
> integration. Architecture v2 generation and apply continue to fail closed
> where a concrete typed renderer or runtime-evidence implementation is still
> missing; this release does not claim Modern Homelab runtime graduation.

### Highlights

- **Kit identity is now architectural authority**: the admitted product
  definition selects its CUE-owned capability plan. The legacy global `context`
  value no longer decides which kit is being built.
- **Basement and Cloud share a contract spine without sharing deployment
  semantics**: Basement owns home-site, LAN, private-remote-access, and optional
  public-egress policies; Cloud owns cloud-site, private-admin-mesh, and public-
  edge policies. Both support explicit multi-node plans without redefining the
  product as a separate kit.
- **Architecture v2 is fail-closed and integration-ready**: immutable resolved
  plans bind product authority, inventory, modules, runtime networks, and owned
  artifacts; generation and apply reject missing concrete implementations
  instead of silently falling back to legacy behavior.
- **Modern Homelab is the local-plus-cloud federation product**: its contract
  requires explicit site federation, isolated bridge and publication policies,
  placement/data authority, and fail-closed partition behavior. It remains
  Preview until the concrete bridge renderer and real multi-site evidence pass.
- **High Availability remains an add-on**: `addons/ha` resolves kit- and mode-
  specific realizations; `legacy fourth-kit identifier` is rejected as a product identity and retained
  only as legacy migration material.

### Added

- A canonical Architecture v2 pipeline from StackSpec and inventory through a
  service-owned CUE definition/catalog to an immutable `ResolvedPlan`, explicit
  render instances, runtime-network instances, governed artifact ownership, and
  exact current-resolution authorization.
- CLI and API seams for fail-closed v1 migration and v2 resolution, including
  authority, catalog, plan, source, inventory, and artifact hashes consumed by
  generation and apply authorization.
- Definition-owned reachability, typed access policies, node and runtime-daemon
  placement, hardware eligibility, service endpoints, Modern publication/data
  boundaries, and device-enrollment contracts.
- A public-safe Architecture v2 projection for Basement and Cloud plus an
  isolated, non-product two-node contract fixture. Modern federation source and
  private product authority remain structurally outside the OSS export.

### Security

- Rendering is plan-pure and installation writes stay beneath a held output
  root; copied plans, forged authority, cross-kit substitution, widened routes,
  orphaned network bindings, and unapproved direct runtime sockets fail closed.
- Module versions are immutable: changed module contracts require a version
  advance, and the offline merge-base gate rejects in-place registry drift.
- The pre-beta fast path binds every public mutation to freshly fetched current
  `main` and reruns build/export/archive/public-policy validation. Optional
  signed Candidate evidence additionally binds its tool binaries, runtime, host
  identity, canonical PATH, operation ownership, cleanup proof, and phased
  receipts without becoming a prerelease prerequisite.

### Compatibility

- Provider smokes, the Proxmox OS matrix, released-content SK-S1, and browser
  evidence are advisory documentation lanes for this prerelease. Missing rows
  are published as `pending/unverified`; they do not delay the prerelease or
  become synthetic PASS results.
- Existing v0.5 StackSpec inputs remain readable for one minor compatibility
  window and can be projected through the migration seam; all newly persisted
  Architecture v2 plans use the governed contract and must be re-resolved when
  mandatory authority, instance, network, or artifact fields change.
- This prerelease publishes version-tagged artifacts without advancing the
  stable `latest` release or OCI tag. The stable rollback baseline remains
  `v0.5.2`.

## [0.5.2] - 2026-07-08

### Fixed

- **Managed Coolify rollout readiness** now waits for the Coolify API health
  endpoint before StackKit-owned platform apps are created or started. Fresh
  managed VPS rollouts no longer fail only because Coolify has started its
  containers but is still finishing API startup or migrations.
- **Runtime-action evidence tests** cover the Coolify readiness handoff so the
  TechStack integration path does not regress to a two-minute fixture timeout.

## [0.5.1] - 2026-07-07

> **Stable** Cloud Kit graduation release. Promotes the v0.5.1 line after the
> release-candidate gates passed from current source contents: SK-S1 Basement
> Fresh Ubuntu + browser evidence, SK-S2 managed `kombify.me`, and SK-S3
> provider custom-domain/BYO-domain. Becomes the public `latest`; rollback
> baseline stays `v0.5.0`.

### Highlights

- **Cloud Kit graduates to supported**: the managed `kombify.me` and custom
  domain Cloud Kit gates pass with real provider-backed installer evidence, not
  scaffolding-only contract artifacts.
- **Basement Kit browser evidence is owner-session aware**: local SK-S1 browser
  evidence now proves the Immich Owner session through `/api/users/me` before
  restoring the visible Photos route when demo-data seeding is disabled, avoiding
  false failures on local HTTP/OIDC discovery.
- **Release evidence is complete for the v0.5.1 promotion**: CI, security,
  SK-S1 browser/Fresh Ubuntu, SK-S2, and SK-S3 gates passed on the same release
  source commit.

### Fixed

- **SK-S1 browser capture** no longer accepts the Immich login route as Photos
  evidence, and it no longer blocks on visible seeded-photo text when the
  scenario intentionally runs with demo data disabled.
- **Local evidence wrapper** passes the retained Fresh VM Immich owner bootstrap
  password only through the process environment for browser verification; it is
  not written into evidence manifests or native command diagnostics.

## [0.5.1-beta.2] - 2026-06-30

> **Prerelease** — supersedes `v0.5.1-beta.1`. Same v0.5.1 content (universal
> security baseline, tiered HA add-on, three-kit lineup) plus a cloud-rollout fix.
> Does not change `latest` (stays `v0.5.0` stable).

### Fixed

- **Cloud rollout apt-lock wait**: `waitForRemotePackageManager` raised its SSH
  context (5m → 15m) above its in-script wait loop (6m → 12m) so the wait is no
  longer killed prematurely, and now outlasts cloud-VM cloud-init/unattended-
  upgrades holding the dpkg lock on first boot. Fixes SK-S2/SK-S3 Wait failing
  with "failed to install Docker: apt_wait timeout" on v0.5.1-beta.1.
- **Public mirror**: export `schemas/stackkit-rollout-event.schema.json` (linked
  from `docs/CLI.md`) so the public markdown-link check passes.

## [0.5.1-beta.1] - 2026-06-30

> **Prerelease** validating the Cloud Kit graduation gates and shipping the
> universal security baseline plus the tiered HA add-on. Does not change the
> public `latest` (stays `v0.5.0` stable). Rollback baseline stays `v0.4.4`.

### Highlights

- **Universal host security baseline**: the measured host baseline (UFW
  default-deny with SSH/HTTP/HTTPS allowed, fail2ban sshd jail, security-only
  unattended upgrades, sshd hardening, sysctl controls) is now a Foundation
  contract applied to **every kit** (Basement, Cloud, Modern Homelab) on a
  Linux/apt host — no longer `basement-kit` only. Documented in `base/security.cue`.
- **High Availability is now a node-gated add-on, not a kit.** `addons/ha` ships
  two tiers: `warm-standby` (>=2 nodes, restore-based, builds on `addons/backup`)
  and `quorum` (>=3 odd managers, etcd live-failover — the former HA-Kit body).
  HA Kit is retired from the marketed lineup and retained dormant.
- **Three-kit market lineup**: Basement (stable), Cloud (graduating), Modern
  Homelab (preview / early-access). Surfaced on the website and in ADR-0026.
- **Cloud Kit graduation**: SK-S2 (managed `kombify.me`) and SK-S3 (provider
  custom domain) cloud gates run against this prerelease with the universal
  baseline present; Cloud Kit graduates `scaffolding -> supported` when both
  pass from released contents.

### Changed

- `securityBaselineApplies` no longer restricts the baseline to `basement-kit`.
- The kombify.me/Komodo cloud verify path threads the detected homelab dir
  (cloud installs under `~/my-cloud-homelab`, not `~/my-homelab`).
- ADR-0026 amended: 3 marketed kits + HA overlay add-on + universal baseline.

## [0.5.0] - 2026-06-30

> **Stable** promotion of the v0.5 line — becomes the public `latest`. Promotes
> `v0.5.0-beta.2` after the Basement Kit gates passed from released contents.
> **Basement Kit is stable**; **Cloud Kit ships as scaffolding** and graduates in
> **v0.5.1** once its live cloud gates (SK-S2 managed `kombify.me`, SK-S3 provider
> custom domain) pass — those are blocked on a platform provider-entitlement gate
> in the Sim service, not on StackKits code. Rollback baseline stays `v0.4.4`
> (pin `STACKKIT_RELEASE_VERSION=v0.4.4`).

### Highlights

- **base-kit → base/ + basement-kit + cloud-kit derivation is GA for Basement**:
  the single `base-kit` is retired into the shared `base/` library (`#StackBase`);
  Basement Kit (`basement-kit`, local, `base.stackkit.cc`) is the verified stable
  single-environment product, Cloud Kit (`cloud-kit`, `cloud.stackkit.cc`) is the
  cloud profile (scaffolding). Forward-compat alias `base-kit → basement-kit`.
- The whole reader surface, planning (ROADMAP), Beads, workflows, OpenAPI, and
  release pipeline are consistent on the Basement/Cloud model.

### Verified

- Local SK-S1 (Basement, fresh Ubuntu/Docker) `prepare → init basement-kit →
  generate → apply → verify` green; **released-content SK-S1** green against the
  published v0.5.0-beta.2 installer (L3 apps as managed Coolify apps with external
  IDs/status); live-installer smoke green; `go test ./...`, `cue vet`,
  `goreleaser check`, `export-public.sh`, `gosec` all pass.

### Deferred to v0.5.1

- Cloud Kit graduation (SK-S2 / SK-S3 live gates) — blocked on the managed
  provider-entitlement gate in the deployed kombify-Simulate service.
- Embedded `registry_snapshot.json` kit-catalog admin-DB resync (non-functional;
  init/generate resolve kits via the filesystem).

## [0.5.0-beta.2] - 2026-06-29

> First **published** v0.5 prerelease. Supersedes the unpublished `v0.5.0-beta.1`
> candidate by completing the reader-facing, planning, and hygiene migration so
> the Basement/Cloud split is consistent everywhere. Rollback baseline stays
> `v0.4.4` (pin `STACKKIT_RELEASE_VERSION=v0.4.4`).

### Fixed

- **Website two-product surface is now reachable**: `/kits/basement` and
  `/kits/cloud` route to their detail pages (old `/kits/base` redirects to
  Basement), the nav lists both kits, and the home page reflects two kits over
  one base instead of "BaseKit is the only public OSS kit surface".
- **OSS contributor gate**: the public `CONTRIBUTING.md` Local Gate command no
  longer references the retired `base-kit/` directory.
- **backup-controller**: the `host_kind` CHECK constraint accepts `basement-kit`
  (the Go const inserts it), fixing a constraint that would reject valid rows.
- Cloud admin profiles (SK-S2 / SK-S3) now declare `cloud-kit`; the
  canonical-scenario parity test cross-checks the kit instead of hardcoding it.

### Changed

- **Documentation, planning, and contracts fully migrated** to Basement/Cloud:
  README, STATUS, CONCEPTS, the ROADMAP (v0.5.0 = Basement + Cloud Derivation),
  kit-taxonomy propagation, OpenAPI examples + website mirror, the agent-run
  manifest schema, `cue vet` command examples, and assorted runbooks/comments.
  The `base-kit` → `basement-kit` deprecation alias is preserved.
- Per-kit templates are generated from the canonical `base/templates/` source
  (`cmd/gen-kit-templates`, freshness-test guarded); `public/base` and
  `public/cloud` installers are generated from the canonical installers.
- Removed the orphaned, retired `release-please` config (publish-oss owns
  releases) and dead/vacuous test scaffolding.

### Verified

- Local SK-S1 (Basement, fresh Ubuntu via Docker) `prepare → init basement-kit →
  generate → apply → verify` is green ("Deployment is healthy"); `go test ./...`,
  `cue vet` (base/basement/cloud/modern/ha), `goreleaser check`,
  `export-public.sh`, and `gosec` all pass.

## [0.5.0-beta.1] - 2026-06-29

> This is a new, opt-in version — it does **not** overwrite the previous stable.
> **Rollback baseline:** `v0.4.4` (stable) remains the immutable previous version; pin
> `STACKKIT_RELEASE_VERSION=v0.4.4` to roll back. See the Rollback subsection below.

### Highlights

- **Basement Kit + Cloud Kit derivation**: the single `base-kit` is retired as a kit. Its shared ~90% core (the v5 stack schema `#StackBase`, the service catalog, defaults, and schema checks) now lives in the `base/` library, and two thin derived products are layered on top: **Basement Kit** (`basement-kit`, local, installer `base.stackkit.cc`) and **Cloud Kit** (`cloud-kit`, cloud, installer `cloud.stackkit.cc`), distinguished only by `context`. The taxonomy is recorded in ADR-0026.
- **Single maintained core**: shared work is developed once in `base/`; only per-scenario deltas split into the two kits. Cloud Kit is the cloud adaptation of Basement (`context cloud` + cloud-only extensions). Cloud Kit ≠ Modern Homelab (the hybrid kit), which stays separate.
- **Add-on compatibility metadata** (`#AddOnCompatibility.contexts/stackkits`) is now slated for engine enforcement so variant-only add-ons resolve only where valid.

### Changed

- **BREAKING — kit slugs**: `base-kit` is no longer an installable kit. Use `basement-kit` (local) or `cloud-kit` (cloud). The public installer `base.stackkit.cc` now installs Basement Kit; the new `cloud.stackkit.cc` installs Cloud Kit; `install.stackkit.cc` remains the generic CLI entry.
- **Forward-compatibility alias**: `stackkit init base-kit` and any `stackkit: base-kit` in an existing `stack-spec.yaml` are normalized to `basement-kit` with a deprecation warning, so pre-0.5 specs keep working.

### Migration & rollback

- **Upgrade**: re-run the installer (`curl -sSL https://base.stackkit.cc | sh`) to get Basement Kit, or `cloud.stackkit.cc` for Cloud Kit. Existing `base-kit` specs auto-normalize.
- **Rollback to the pre-change version**: the previous stable **`v0.4.4`** (and its archives) are unchanged and remain the supported rollback target. Pin it explicitly to stay on / return to the old single `base-kit`:

  ```bash
  STACKKIT_RELEASE_VERSION=v0.4.4 curl -sSL https://base.stackkit.cc | sh
  ```

  Because `v0.5.0-beta.1` is a distinct tag, no `v0.4.x` release or archive is overwritten; rollback is always available by pinning the previous version.

### Release gate

- **Met — Basement is release-ready:** `go test ./...` is green (44/0), the multi-kit release
  pipeline + both installers (`base.stackkit.cc`, `cloud.stackkit.cc`) landed, the goreleaser
  two-kit archive install-smoke passes (`stackkit init basement-kit`/`cloud-kit` → generate →
  context-correct tfvars), and the local **SK-S1 (Basement) Fresh-Ubuntu E2E** passes end-to-end
  (`init → generate → apply → verify`; deployed `StackKit: basement-kit`, all services healthy).
  A 12-agent adversarial pre-merge review confirmed the substance clean and its release-plumbing
  findings are fixed.
- **Deferred by design — Cloud is scaffolding:** Cloud Kit's live **SK-S3/SK-S2 (Cloud) gates**
  need a provider-leased custom domain and a managed `kombify.me` subdomain and are **not yet
  proven from released contents**. `cloud-kit` `init → generate` is verified, but its live cloud
  apply is not; the `cloud-kit` mode matrix marks every cell `scaffolding`, and `cloud.stackkit.cc`
  is experimental until those gates pass. Basement Kit carries the release; Cloud Kit graduates
  from scaffolding when its cloud E2E is green.

## [0.4.5-beta.1] - 2026-06-23

### Highlights

- **Custom-domain provider lease proof**: keeps SK-S3 on the canonical fresh provider-leased server path and validates the full Start, Wait, Verify, and Cleanup chain against the Sim/Lease API and Cloudflare DNS.
- **Coolify custom-domain routing**: hardens the generated Coolify proxy labels so BaseKit service routers win over fallback routers and request wildcard TLS coverage for custom-domain service hosts.
- **Deferred app readiness**: treats accepted on-demand platform apps as deferred public-readiness evidence while keeping running and required services in the public URL gate.

### Fixed

- **Coolify proxy reconciliation**: reconciles the Coolify Docker endpoint and generated router labels so the custom-domain path does not depend on host-side proxy shims.
- **Released evidence diagnostics**: emits failed scenario artifacts when public URL verification fails, preserving release-gate evidence instead of failing later without a scenario row.
- **Browser evidence setup**: completes PocketID consent during browser evidence capture so BaseKit owner/passkey setup proof remains end-to-end.

### Release Notes

- This is a pinned prerelease for official-installer verification of the current SK-S3 provider-lease and Coolify routing fixes. Install with `STACKKIT_RELEASE_VERSION=v0.4.5-beta.1`; unpinned official installers should remain on the latest stable release until released-content SK-S1, SK-S2, and SK-S3 pass for the new tag.

## [0.4.4] - 2026-06-22

### Fixed

- **SK-S3 release evidence import**: accepts run-scoped custom-domain Base Hub URLs such as `https://base.e2e-cd-<run>.kombify.pro` when they remain inside the expected `kombify.pro` zone.
- **SK-S3 scenario validator fixture**: updates the release artifact validator test fixture from the old bare/manual custom-domain model to the current bootstrapped provider-lease Coolify contract.

### Release Notes

- Supersedes `v0.4.3` for stable public testing because `v0.4.3` published successfully and the released-content matrix passed, but the evidence republish step still rejected valid SK-S3 dynamic Base Hub URLs.

## [0.4.3] - 2026-06-22

### Fixed

- **Released-content preflight snapshots**: regenerates SK-S2/SK-S3 TFVars golden snapshots so the public preflight gate matches the bootstrapped provider-lease scenario contract.
- **Installer credential verification**: accepts the current installer `Login credentials:` output header while still requiring the expected admin email and password lines.

### Release Notes

- Supersedes `v0.4.2` for stable public testing because `v0.4.2` published successfully but its released-content matrix still exposed stale golden snapshots and legacy credential-header verification.

## [0.4.2] - 2026-06-22

### Fixed

- **Stable E2E scenario contract**: aligns SK-S2 and SK-S3 with the supported bootstrapped BaseKit release path. SK-S2 remains the kombify.me Komodo provider-lease proof, and SK-S3 remains the custom-domain Coolify provider-lease proof with Cloudflare DNS and managed cleanup, but neither stable scenario claims the unsupported `advanced` or `bare` scaffolding path.
- **Released-content verify expectations**: updates the production verifier to require bootstrapped tfvars, Base Hub access summaries, public service URLs, DNS records, and Komodo/Coolify platform evidence from the official installer release.

### Release Notes

- Supersedes `v0.4.1` for stable public testing because `v0.4.1` published successfully but its released-content SK-S2/SK-S3 verify run exposed stale `advanced`/`bare` assertions.

## [0.4.1] - 2026-06-22

### Highlights

- **Stable BaseKit promotion**: promotes the `v0.4.0-beta.2` evidence set to the stable public installer path after SK-S1, SK-S2, SK-S3, SK-S5, browser evidence, public export, archive validation, SBOMs, and attestations passed.
- **Real ephemeral server E2E**: keeps SK-S2 and SK-S3 on fresh provider-leased servers through the Sim/Lease API, with SSH used only as transport and managed cleanup required for DNS records plus server leases.
- **Release evidence completeness**: the stable release carries canonical scenario rows and browser evidence instead of the earlier `v0.4.0` release's pending scenario rows.

### Fixed

- **Stable latest drift**: supersedes the older `v0.4.0` stable release evidence that still marked SK-S1/SK-S2/SK-S3/SK-S5 and browser gates as pending.
- **Roadmap and Beads state**: closes the v0.4 release-blocking tracker drift after public beta2 evidence and current main Scenario/Admin/PaaS/Runtime gates proved the BaseKit beta-hardening scope.
- **Installer semantics**: keeps prerelease pins explicit while the unpinned official installer resolves to the newest stable tag.

### Release Notes

- This is the release-ready stable BaseKit path for public testing through the official installers without a prerelease pin.
- `v0.5.0` remains the product-contract-complete follow-up for non-v0.4 scope such as native Vaultwarden Owner UX and broader Enterprise application-layer polish.

## [0.4.0-beta.2] - 2026-06-21

### Highlights

- **Ephemeral provider-server E2E contract**: SK-S3 now provisions a fresh provider-leased Ubuntu server through the Sim/Lease API, runs the custom-domain installer over provisioned SSH, captures state/evidence, and deletes the simulation/server during cleanup.
- **Uniform beta provider lane**: provider selection now uses `STACKKIT_E2E_SERVER_PROVIDER`, then `STACKKIT_E2E_CLOUD_NODE_ENGINE`, then `STACKKIT_TECHSTACK_LEASE_PROVIDER`, and finally `centron-managed`; beta providers remain `centron-managed` and `ionos-managed`.
- **Release cleanup discipline**: SK-S3 production workflow phases now preflight service auth, provider readiness, and Cloudflare DNS credentials, then run an `always()` cleanup phase that emits explicit diagnostics even when provisioning or verification fails.

### Fixed

- **BYO SSH blocker removed from canonical SK-S3**: fixed-host SSH is now an explicit local debug override via `STACKKIT_SK_S3_DEBUG_FIXED_SSH=1`, not release evidence or CI prerequisite material.
- **Scenario state and artifacts**: SK-S2/SK-S3 artifacts now record provider metadata, and SK-S3 staged state persists simulation ID, node ID, SSH material, public IP, service hosts, DNS zone, and provider for follow-up phases and cleanup.
- **Production workflow diagnostics**: isolated SK-S3 Wait/Verify/Cleanup phases skip cleanly when no Start state exists, while workflow jobs upload blocked/skipped diagnostics instead of failing later on missing artifacts.

### Release Notes

- This is the release-candidate lane for public BaseKit beta testing through a pinned prerelease: `STACKKIT_RELEASE_VERSION=v0.4.0-beta.2`.
- At prerelease publication time, unpinned installs stayed on the stable release path until released-content SK-S1, SK-S2, and SK-S3 evidence was clean.

## [0.4.0-beta.1] - 2026-06-21

### Highlights

- **Public BaseKit beta candidate**: ships the v0.4 BaseKit release candidate as a pinned prerelease for official-installer testing with `STACKKIT_RELEASE_VERSION=v0.4.0-beta.1`.
- **Released-content gates**: production workflows now include explicit released-installer SK-S1 coverage, scenario evidence import, and diagnostic artifacts for skipped SK-S2/SK-S3 paths.
- **Local E2E evidence**: the Docker Desktop Fresh Ubuntu SK-S1 gate is split into bounded Start, Wait, Verify, and browser-evidence phases under the 15-minute policy.

### Fixed

- **Public export manifest**: includes the homelab setup-action evidence scripts required by the public surface checker and release CI.
- **Prerelease installer semantics**: installer tests prove prereleases are used only when `STACKKIT_RELEASE_VERSION` pins the beta tag; unpinned installs remain on stable latest.
- **Release diagnostics**: skipped or blocked production scenarios now emit explicit diagnostics instead of failing later during artifact upload.

### Release Notes

- This is a BaseKit public beta prerelease, not stable GA. Do not promote unpinned `latest` until released-content SK-S1, SK-S2, and SK-S3 pass or the public beta scope is narrowed explicitly.
- Current broader scenario blockers are tracked separately: SK-S2 service-auth preflight and SK-S3 provider-lease/DNS prerequisites must pass before claiming multi-use-case beta readiness.

## [0.3.4] - 2026-06-08

### Highlights

- **Native MCP surface**: StackKits now publishes one user-facing `stackkit` MCP connection, with `stackkit-mcp` as the local adapter and `stackkit-server /mcp` as the protected durable endpoint after install.
- **TechStack rollout readiness**: release archives include the MCP/server pieces needed for kombify-TechStack managed installs, plus bounded MCP rollout and Fresh Ubuntu phase gates.
- **Agent discovery**: stackkit.cc now ships OpenMCP metadata, `llms.txt` updates, and installation-process guidance for local, SSH, and protected durable MCP paths.

### Fixed

- **OSS release hygiene**: the StackKits runtime-action wire contract is now local to this repo, so public release builds no longer depend on private private-source Go modules.
- **Release export**: the Docker image build no longer emits private module-auth configuration into the curated public release surface.
- **Local gates**: Beads sync, local build timing, website checks, MCP smoke tests, and timeout-budget checks are all bounded by the 15-minute command policy.

## [0.3.2] - 2026-05-26

### Fixed

- **Public release hygiene**: the public StackKits release now stays on the curated OSS export surface and release checks reject development-only paths, private workflows, internal runbooks, and test fixtures before publish.
- **Release evidence**: package artifacts are included in build attestations and attestation verification retries handle GitHub propagation delay without hiding real failures.
- **Security gates**: Go vulnerability dependencies are updated for `golang.org/x/crypto`, `golang.org/x/net`, and related `golang.org/x` modules, with lint/static/security checks restored to a clean state.

## [0.3.1] - 2026-05-25

### Highlights

- **Canonical live scenarios**: release work now focuses on SK-S1 local Coolify, SK-S2 kombify.me Komodo, and SK-S3 custom-domain Coolify, with installer gates split into bounded Start/Wait/Verify phases.
- **Auth baseline**: BaseKit rollouts restore TinyAuth/PocketID provider registration and runtime checks so protected services expose PocketID login instead of falling back to password-only TinyAuth.
- **Coolify routing**: generated Coolify rollouts now bootstrap, reconcile, and route StackKit-owned services through the managed proxy with service hostnames such as `base`, `id`, `photos`, and `kuma`.

### Fixed

- **Coolify proxy recovery**: fallback and reconciliation logic now restores file-provider routing, dynamic config mounts, proxy TLS settings, service routes, host-gateway access, and same-file dynamic-config sync handling.
- **Cloudflare DNS-01**: custom-domain Coolify rollouts pass Cloudflare Global API Key credentials to Traefik as `CF_API_KEY` when `CLOUDFLARE_EMAIL` is present, while scoped API tokens still use `CF_DNS_API_TOKEN`.
- **Installer readiness**: live installer jobs hand off VM state before verification and wait for routed services/certificates in bounded phases instead of relying on a single long-running job.
- **Runtime metrics**: restore-drill host metrics preserve legitimate zero CPU values instead of dropping them as missing data.
- **Release preflight**: `scripts/release/basekit-live-preflight.ps1` now fails closed when `go`, `node`, `npm`, `cue`, actionlint, or release helper commands return a non-zero exit code.
- **Coolify endpoint contract**: generated BaseKit rollouts keep the persisted `.stackkit/platform.json` Coolify endpoint node-local at `http://127.0.0.1:8000`, while bootstrap and readiness probes can use a separate endpoint reachable from remote Docker targets.
- **Archive validation**: release archive smoke validation now checks the current `coolify_platform_bootstrap` and `.stackkit/platform.json` contract from packaged contents instead of obsolete Coolify token API markers.
- **Release state**: STATUS and ROADMAP now treat `v0.3.1` as the next public patch candidate and keep old `v0.2.8` follow-ups as historical evidence rather than current release blockers.

### Release Notes

- `v0.3.1` is the next intended Public OSS patch release. `v0.3.0` was a private failed release attempt and is not treated as a public release.
- Production run `26420216004` on `f3419a54` was intentionally cancelled by operator request after API/Gateway, BaseKit preflight, Sim UI auth, and SK-S2 Start had passed. Complete SK-S1/SK-S2/SK-S3 end-to-end evidence should be rerun before making an Enterprise production-readiness claim.

## [0.3.0] - 2026-05-22 (private tag; not public OSS release)

> `v0.3.0` was tagged privately but did not complete the public publish path. Do not use it as public release evidence and do not retag it.

### Highlights

- **PaaS portfolio alignment**: Coolify remains the default PaaS, while Komodo is the production alternative for BaseKit rollouts. Dokploy remains draft until promoted.
- **Komodo no-UI path**: generated rollouts install Komodo Core, Periphery, and DB, create the initial admin/API key without UI, close registration, persist `.stackkit/platform.json`, and deploy StackKit-owned Compose bundles as Komodo Stack resources through the API.
- **Dokploy no-UI path**: generated rollouts set `BETTER_AUTH_SECRET`, create or confirm the first owner, establish a session, mint a non-rate-limited API key, persist both `token` and `apiKey`, deploy raw Compose resources through Dokploy, and route through `dokploy-traefik`.
- **Forge Map/Admin sync**: Admin seed and generated CUE now carry Coolify as the PaaS standard with Komodo as the production alternative; Dokploy is tracked as draft.

### Changed

- StackKit-owned L3 app deployment now has explicit selected-PaaS adapter contracts for Coolify and Komodo, with Dokploy kept behind draft adapter coverage.
- Production E2E coverage is capped at SK-S1 local Coolify, SK-S2 kombify.me Komodo, and SK-S3 custom-domain Coolify.
- Documentation, ADRs, StackSpec reference, website content, and Works-With metadata now describe the Coolify default, Komodo production alternative, and Dokploy draft status honestly.

### Fixed

- Dokploy Compose creation now persists `sourceType: raw` through a follow-up update before deploy, avoiding accidental GitHub-source deployments.
- Komodo adapter upserts now resolve canonical stack IDs on create conflicts before update/deploy evidence is recorded.
- Generated Admin/CUE artifacts are back in sync for `paas.type` and the production/draft PaaS split.

## [0.2.8] - 2026-05-17

### Highlights

- **BaseKit bootstrap-open Base Hub**: local `base.<domain>` stays reachable during first-run owner setup, shows an unprotected warning, and can be protected after PocketID/TinyAuth setup.
- **Registry-backed module release**: module release and verify now use service auth, bootstrap missing module rows through the Admin registry, and keep all 24 module contract hashes in strict parity.
- **Release gate stabilization**: AdGuard Home module tests wait for routed UI readiness after provisioning, and the module release command stays below lint complexity thresholds.

### Fixed

- Prevent stale service-catalog snapshots from re-protecting the local Base Hub by pinning `base` to identity `none` for local fallback defaults.
- Keep default L3/application services protected unless they are explicitly configured public; the Base Hub is the local onboarding exception only.
- Avoid browser-session Admin tokens in module release CI; signed service-auth requests now take precedence.

## [0.2.7] - 2026-05-17

### Highlights

- **BaseKit product-contract guardrails**: fresh Ubuntu evidence now checks protected/default anonymous rejection, node-local manifest visibility, and the Photos setup action instead of relying on container liveness only.
- **Release mirror hygiene**: the curated release export now ships a narrower documentation surface, a sanitized release roadmap, and root-relative website link validation for the Svelte/Vite site.
- **Agent and website surface**: stackkit.cc moved to the Svelte 5/Vite/Tailwind site while preserving installer routes, `llms.txt`, OpenAPI/schema mirrors, and prompt Markdown.

### Fixed

- Local website release gates now run `npm install`, `npm run check`, and `npm run build` without failing on Windows locked native modules from an existing `node_modules`.
- BaseKit docs now clarify that L3 public or unauthenticated exposure is allowed only through explicit access policy, never as the default.

## [0.2.6] - 2026-05-13

### Changed

- **StackKit standards**: codified release archives as the installable product boundary, requiring packaged `cue.mod`, shared `base/`, module contracts, packaged OpenTofu, and fresh-target archive validation for defaults.
- **Installer quality bar**: documented that public one-liner endpoints must return executable shell instead of website fallback HTML.
- **Public release helper**: hardened the public publish script around release deletion and release-existence checks.

## [0.2.5] - 2026-05-13

### Fixed

- **BaseKit release archives**: `stackkits` and `stackkits-base-kit` archives now include root `cue.mod/**` and `modules/**`, allowing installed BaseKit definitions to run composition and generate TinyAuth credentials for the one-line installer path.
- **Release validation**: the public release workflow now extracts the BaseKit archive and verifies `init` plus `generate` from released files so archive packaging regressions fail before publish.

## [0.2.4] - 2026-05-13

### Fixed

- **Runtime image build**: Dockerfile now uses Go 1.26.3 so the public StackKit server image build matches `go.mod` and can publish `ghcr.io/kombifyio/stackkits`.

## [0.2.3] - 2026-05-13

### Highlights

- **PaaS app handoff path**: BaseKit can persist optional user app handoff metadata into the stack spec, register kombify.me app service names, and expose platform app handoff state in `stackkit status --json`.
- **Runtime action bridge**: `stackkit-server` now exposes service-auth-protected internal runtime actions for TechStack-managed rollout, verification, and restore-drill handoffs with dry-run-by-default execution.
- **Scenario evidence**: SK-S2A and SK-S3A scenario definitions, golden fixtures, docs, and the public SvelteKit smoke app example are included for dev-only PaaS handoff validation.

### Added

- `stackkit app add` command coverage for SvelteKit app definitions, route defaults, env values, and secret references.
- Dev-gated base installer app handoff environment variables for local handoff validation.
- Internal service-auth JWT verification with current/next secret rotation support for runtime action callbacks.

### Changed

- App-enabled StackSpecs now generate PaaS handoff manifests without making StackKit responsible for user app deployment.
- Public export manifest includes the SvelteKit smoke example used by dev handoff validation.

## Historical — kit-update-phase-1: Base Kit Update-Lifecycle (Foundation + CLI)

> **Not an unreleased section.** This block documents the kit-update-lifecycle
> foundation that landed during the 0.3.x development cycle and is **LIVE**
> (production milestone 2026-05-08; migrations 000107–000109 on Render). It is
> retained below the `[0.2.3]` entry for historical continuity and is out of
> strict Keep-a-Changelog order by intent — the version headers above are the
> canonical release record. Do not treat it as pending work.

### Production milestone (2026-05-08) — Phase 1 LIVE

- DB Migrations 000107–000109 (renumbered from initial 000090–000092 drafts because slots 000086–000106 were claimed by other repos before apply) **LIVE on Render** `kombify-stackkits` Postgres: `release_channel` columns, `sk_node_deployment` mirror, `sk_kit_module_compat` resolver view.
- ADR-0018 implementation-status table updated: DB migrations + Admin (channel-promotion endpoints, resolver, node-deployments, UI) marked ✅ Shipped. Lessons-learned section added (sqlc-000106-fix, 000067-replay-fix, GO_VERSION 1.26.3 bump, renumbering rationale, best-effort PATCH note).
- North-Star reference doc the private kit update lifecycle doc — canonical landing page for the update lifecycle (TL;DR, diagram, three pillars, surfaces, phase roadmap, operator quick-start, cross-repo surfaces, architectural invariants). Linked from the public repository.

### Added

- **Tests/Release**: BaseKit live preflight (`scripts/release/basekit-live-preflight.ps1`), release-note parser tests, public export validation, website changelog smoke, and `production-tests.yml` inputs for the first SK-S1 fresh Ubuntu live run.
- Node Hub service-guide metadata in CUE, registry, and generated catalog paths; the generated `base.<domain>` dashboard now starts with Getting Started, important links, and a compact enabled-service matrix with public Mintlify how-to links.
- ADR-0018 — Kit-Update-Lifecycle (Channels, Atomic-Snapshot, Compatibility-Resolver). See the private ADR-0018 record.
- Kit-update design consolidated into ADR-0018, the private kit update lifecycle doc, and the operator runbooks.
- CUE — `#ToolType` (`oss`/`managed`/`hybrid`) + `#ToolCategory` (curated 18-Set) in [`base/tool_categorization.cue`](base/tool_categorization.cue).
- CUE — `#IaCDefaults` schema (`provider_versions`, `default_tags`, `backend`) in [`base/iac-defaults.cue`](base/iac-defaults.cue).
- IaC — Shared `iac/defaults/` module (`main.tf`, `variables.tf`, `outputs.tf`, `README.md`) — kits import as `module "defaults"` and consume `module.defaults.tags`.
- Go — `internal/snapshot/` package: `Kopia` CLI wrapper (`kopia.go`) + `AtomicSnapshotter` orchestrating Kopia + tfstate copy + manifest.yaml (`atomic.go`). `ErrKopiaNotConfigured` is the canonical pre-flight failure.
- Go — `internal/registry/channel_resolver.go` — client for `/api/v1/sk/compat/resolve` with `ResolveResult.SummarizeReasons()` helper.
- CLI — `stackkit kit upgrade` (`cmd/stackkit/commands/kit_upgrade.go`) with flags `--to`, `--kit-channel`, `--module-channel`, `--allow-channel-mismatch`, `--dry-run`, `--auto-approve`, `--volumes`, `--snapshot-id`, `--endpoint`, `--token`. Pre-flight Kopia + resolver call + tofu plan + atomic-snapshot + tofu apply + admin PATCH (best-effort).
- CLI — `stackkit kit upgrade rollback` (`cmd/stackkit/commands/kit_upgrade_rollback.go`) with flags `--to-snapshot`, `--auto-approve`, `--skip-volume-restore`, `--kopia-restore-only`. Restores tfstate + Kopia volumes from a previous atomic-snapshot.
- CLI — `stackkit doctor --check-updates` — queries the Admin API for newer kit-versions in the current channel; appends `updates` and `updates-cta` rows to the doctor report. Network/admin failures degrade to `warn`, never `fail`.
- Schema — `pkg/models/DeploymentState` gains additive `KitVersionID`, `KitSemver`, `KitChannel`, `LastSnapshotDir` fields (all `omitempty`); state files written by older CLI versions still load.
- Operator runbooks — the private kit-upgrade runbook + the private kit-rollback runbook: pre-flight checklists, common flows, failure modes, timing expectations, manual recovery for kit-rollback.
- DB-Migrations (LIVE; in `kombify-DB/migrations/`):
  - `000107_sk_release_channels` — Dual-Level `release_channel` + `released_at` auf `sk_stackkit` + `sk_module_version`, AFTER-Triggers für `action='channel_promote'`, `target_kind`-Spalte auf `sk_stackkit_audit_log`, Inline-Backfill bestehender Versions auf `stable`.
  - `000108_sk_node_deployment` — Server-Side-Mirror `(tenant_id, node_name) → (kit_slug, kit_version, kit_channel, module_versions, kopia_snapshot_id, tofu_state_path, status)`.
  - `000109_sk_compatibility_resolver_view` — VIEW `sk_kit_module_compat` als Resolver-Source.
- Tests — 48 new test cases (`internal/snapshot/`, `internal/registry/`, `cmd/stackkit/commands/kit_upgrade*`, `cmd/stackkit/commands/doctor_update*`); whole repo suite (30 packages) green.

### Pending (later in this phase)

- Admin: channel-promotion endpoints + resolver endpoint + node-deployments + UI pages shipped in kombify-Administration.
- Test-Coverage-Hebung Update-Pfade auf 50% (T7).
- VM-Smoketest v1.0→v1.1 + Rollback (T9).
- Out-of-scope: Multi-Node-Rolling-Update (kit-update-phase-2), Auto-Promotion (kit-update-phase-3).

### Notes

- Kopia-Repo wird Pflicht-Vorbedingung für Updates — Operator muss `stackkit backup configure` machen, bevor `stackkit kit upgrade` zugelassen wird (ADR-0018 §3).
- Multi-Node-Rolling-Update ist explizit kit-update-phase-2, nicht Phase 1.
- Auto-Promotion (edge → beta → stable über Demand-Signal) ist explizit kit-update-phase-3.

---

## Historical — Phase 1: Owner & Break-Glass Provisioning

> **Not an unreleased section.** This block documents the Owner & Break-Glass
> provisioning work that landed during the 0.3.x development cycle and is
> **LIVE**. It is retained here for historical continuity and is out of strict
> Keep-a-Changelog order by intent — the version headers above are the canonical
> release record. Do not treat it as pending work.

### Added

- `stackkit init` flags for owner provisioning:
  - `--cluster-mode={first|join}` (Phase 1: only `first` supported)
  - `--owner-source={local|cloud}` (Phase 1: only `local` supported; `cloud` errors with Phase-2 message)
  - `--owner-email`, `--owner-username`, `--owner-display-name`
  - `--recovery-passphrase-hash` (argon2id PHC; if missing, prompts interactively)
  - `--cloud-oidc-{issuer,client-id,client-secret-ref,foreign-subject}` (Phase 2 stubs)
- Per-node break-glass PocketID admin (`bg-{nodename}@local`) auto-generated during `stackkit apply`.
- Per-node TinyAuth static-cred (`bg-{nodename}-static`) as Layer-2 fallback for PocketID-down recovery.
- Encrypted recovery bundle in `/var/lib/stackkit/recovery/break-glass-{nodename}.age` (age-scrypt encryption with the user's recovery passphrase; default scrypt N=2^17, r=8, p=1).
- Plaintext convenience bundle next to the encrypted one (`.txt`, mode 0600, root-only).
- `stackkit break-glass list` / `show-bundle` / `rotate` (Phase-5 stub) sub-commands.
- PocketID `STATIC_API_KEY` lifecycle: generated by `stackkit init`, persisted in `<homelab>/.stackkit/pocketid-static-api-key` (mode 0600), wired into the pocketid container as `STATIC_API_KEY` env var via Terraform var.

### Changed

- CUE schemas:
  - `base/identity.cue` — added `#PocketIDOwner` (passkey-only; `source: local|cloud` with conditional required fields), `#TinyAuthStaticCred`.
  - `base/break-glass.cue` (new) — `#PocketIDBreakGlass`, `#BreakGlassBundle`, `#BundleContents`, `#BundlePayload`.
  - `base/cluster.cue` (new) — `#ClusterMode` stub for Phase 4.
- PocketID image pinned to `ghcr.io/pocket-id/pocket-id:v2` (currently v2.6.2). PocketID v2 is passkey-only — there is no password-based authentication.

### Out of Scope (later phases)

- `--owner-source=cloud` and Cloud-OIDC upstream (Phase 2)
- TechStack-bootstrap-token API + wallet integration (Phase 3)
- Multi-node cluster join / `stackkit cluster join-token` (Phase 4)
- `stackkit break-glass rotate` real implementation, audit logs, auto-rotation (Phase 5)

See ADR-0018, the private kit update lifecycle doc, and [ROADMAP.md](ROADMAP.md) for the current roadmap.
