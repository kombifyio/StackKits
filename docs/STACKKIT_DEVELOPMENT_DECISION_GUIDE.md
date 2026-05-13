# StackKit Development Decision Guide

> Status: Development guide
> Basis: [StackKit Golden Rules](STACKKIT_GOLDEN_RULES.md)
> Scope: Use this when designing, changing, reviewing, or releasing StackKits, modules, resolver logic, registry flows, installers, and TechStack integrations.

This guide expands the Golden Rules into the practical decisions developers must cover. StackKits intentionally combine homelab infrastructure, application packaging, identity, networking, storage, backups, OpenTofu generation, registry state, and future orchestration. That complexity is acceptable only when every decision point is explicit and validated.

## 1. Development Goal

Every change should preserve the core promise:

> A user can express intent, StackKits can resolve that intent into a safe default or a validated alternative, and the deployment can be generated, applied, verified, updated, and explained.

When a change adds a tool, module, context, installer, wizard answer, or registry field, ask:

1. What user intent does this satisfy?
2. Which layer owns it?
3. Which contract validates it?
4. Which default changes, if any?
5. Which users are affected?
6. What happens when required information is missing?
7. How do we verify it on a fresh target?
8. Does the released archive contain every definition and module contract needed for that fresh target?

## 2. Authority Boundaries

StackKits has multiple sources of information by design. The development risk is silent drift.

| Concern | Authority | Developer obligation |
| --- | --- | --- |
| Module schema, constraints, defaults | CUE | Add or update CUE contracts first. |
| Generated deployment shape | CUE + composition engine | Do not hand-edit generated output. |
| Tool catalog, evaluations, versions | kombify database | Keep registry entries and release metadata aligned. |
| Kit composition mirror | kombify database, imported from CUE/YAML | Treat DB as queryable mirror unless an ADR says otherwise. |
| CLI behavior | Go CLI | Bind to CUE and registry contracts; do not duplicate decision logic casually. |
| TechStack orchestration | TechStack + registry APIs | Consume the same contracts, do not create parallel defaults. |
| Public release | Git + release pipeline | Strip internal-only references and verify reproducibility. |

If a field affects deployment, it needs a validation path. If it only affects display, it should not alter generated output.

## 3. Layer Decision Checklist

### Foundation

Put a concern in Foundation when it is required before the platform can safely run:

- host prerequisites
- OS hardening
- SSH access model
- firewall baseline
- package/runtime bootstrap
- owner identity requirement
- break-glass recovery
- secrets bootstrap
- base network assumptions
- minimum telemetry contract

Foundation decisions to cover:

1. Does this run before the PaaS exists?
2. Does it need root or host-level access?
3. Can it lock out the user?
4. Does it generate or handle secrets?
5. Does it change network reachability?
6. How is it rolled back?
7. What is the preflight failure mode?

### Platform

Put a concern in Platform when it is shared by application modules:

- PaaS adapter
- reverse proxy
- TLS
- DNS
- identity provider
- auth gateway
- service discovery
- routing
- platform database/cache when shared
- logs and metrics routing
- node registration

Platform decisions to cover:

1. Which PaaS adapter owns application rollout?
2. How are services registered with the adapter?
3. How are routes generated?
4. Which services are public, VPN-only, LAN-only, or internal-only?
5. Which identity provider is used?
6. How does forward-auth protect Layer-3 services?
7. How does the platform expose health and logs?
8. How does the platform recover if an application deployment fails?

### Application

Put a concern in Application when it exists because of a user-facing use case:

- photo library
- password vault
- smart home
- development platform
- website
- file share
- secrets manager
- AI/LLM surface
- media stack

Application decisions to cover:

1. What is the human use case?
2. What is the default tool?
3. What alternatives are supported?
4. Is the module one tool or a package of cooperating tools?
5. Which platform features does it require?
6. Which databases, caches, volumes, and devices does it need?
7. How is the first user created?
8. How is access controlled?
9. How is data backed up and restored?
10. Which compute tiers can run it?
11. Which contexts are unsupported?

## 4. Intent Resolution Model

Intent resolution should move from broad product intent to concrete deployment values.

Recommended resolution order:

1. Select kit: Base Kit, Modern Homelab, HA Kit, or future kit.
2. Select mode: Standard or Advanced.
3. Capture access intent: local-only, VPN/mesh, public internet, managed subdomain, own domain.
4. Detect context: local, cloud, pi, hybrid, provider metadata, network shape.
5. Detect resources: CPU, RAM, disk, architecture, GPU, storage devices.
6. Classify compute and storage tier.
7. Resolve Foundation defaults.
8. Resolve Platform adapter and routing model.
9. Resolve Application use cases.
10. Apply user overrides.
11. Apply compatibility and compute gates.
12. Resolve add-ons.
13. Validate CUE contracts.
14. Generate artifacts.
15. Apply and verify.

Decision rules:

- Defaults are selected before overrides are applied.
- Overrides are validated after defaults are known.
- Compute gates can disable or reject heavy modules, but must explain why.
- Public exposure requires positive evidence: domain, TLS path, auth policy, route policy.
- Missing critical intent should not lead to public exposure, destructive changes, or data loss.

## 5. Required Decision Points

Every meaningful change should identify the decision points it touches.

### User And Organization

- Is there an owner email?
- Is there an owner username?
- Are additional users known?
- Are permissions needed across multiple tools?
- Is this a single household, family, team, or business?
- Is the tenant managed by TechStack?

Development impact:

- Missing owner data must have a fallback or a clear prompt.
- Modules that need first-user setup must define whether StackKits can automate it.
- User provisioning should be centralized where possible, not reimplemented per tool.

### Access

- Local-only?
- VPN/mesh?
- Public internet?
- kombify-managed subdomain?
- Own domain?
- Wildcard DNS available?
- DNS API token available?
- Email available for ACME?

Development impact:

- Access intent drives TLS, PaaS, routing, identity, and service exposure.
- Public routes require stricter validation than LAN-only routes.
- Local DNS should avoid localhost-style assumptions and use stable service names.

### Topology

- Single primary node?
- Multiple local nodes?
- Cloud-only?
- Local plus cloud?
- HA cluster?
- Storage-only nodes?
- GPU/device nodes?

Development impact:

- Base Kit can be 1..N nodes inside one trust domain.
- Modern Homelab must split local-first and remote responsibilities.
- HA Kit must define quorum and failover.
- Extra nodes need a real placement or backup purpose.

### Runtime And PaaS

- Dokploy?
- Coolify?
- Future adapter?
- Constrained compose manager?
- Standard Mode or Advanced Mode?

Development impact:

- Layer-3 application rollout should go through the selected platform adapter.
- Adapter choice affects route generation, secrets injection, deployment API, rollback, and verification.
- A new adapter needs parity criteria before it can become default.

### Identity And Auth

- PocketID/TinyAuth default?
- External identity provider?
- Passkeys required?
- Break-glass user required?
- Per-service roles?
- Forward-auth for every application?

Development impact:

- The module must declare whether it supports central auth, local auth, or both.
- Services with their own user store still need owner bootstrap behavior.
- No application route should bypass the login-gateway by accident.

### Data And Storage

- Ephemeral data?
- Persistent user data?
- Database required?
- Object storage required?
- Local disk sufficient?
- External managed database?
- Cross-node storage?
- Encryption at rest?

Development impact:

- Storage classification controls backup and restore requirements.
- Modules with user-generated data need backup definitions before default status.
- Database creation belongs in a controlled contract, not hidden container side effects.

### Backup And Recovery

- What data must be backed up?
- Where is the backup target?
- Is the target in another failure domain?
- Is restore tested?
- Are secrets included or separately recoverable?
- What is the expected RPO/RTO?

Development impact:

- Multi-server setups should use nodes to improve backup posture.
- Backup is not complete without a restore path.
- Break-glass material must be recoverable without depending on the failed service.

### Observability

- Minimal health checks?
- OpenTelemetry collector?
- Logs?
- Metrics?
- Alerts?
- User-facing dashboard?
- TechStack-managed Day-2 telemetry?

Development impact:

- Standard Mode needs at least verification and basic health.
- Advanced Mode can add drift, metrics, alerts, and lifecycle intelligence.
- Modules should emit health signals that `stackkit verify` can consume.

### Updates And Lifecycle

- Can this module be upgraded safely?
- Does it require migrations?
- Can it roll back?
- Are breaking versions pinned?
- Does the PaaS adapter understand the deployment state?
- Does TechStack manage it after Day-1?

Development impact:

- Default modules need a predictable update path.
- Advanced Mode should own drift detection and lifecycle operations.
- Generated artifacts must be reproducible from the same inputs.

## 6. Module Readiness Levels

Use these levels to decide whether a module may become default.

### Experimental

- Module exists for development only.
- Contract may be incomplete.
- Not available in normal wizard defaults.
- No release guarantee.

### Optional

- User can explicitly enable it.
- CUE contract validates.
- Generate path works.
- Known gaps are documented.

### Recommended Alternative

- Can replace a default in the same service group.
- Has comparable auth, routing, backup, and verification behavior.
- Migration implications are documented.

### Default

A module may be default only when it has:

1. automated first-run or clear owner setup
2. no static credentials
3. platform route registration
4. access policy
5. health check
6. backup classification
7. compute/context gates
8. fresh-target smoke test
9. documented upgrade behavior
10. registry and CUE hash parity

Default status is a product and engineering decision, not just a working container.

## 7. Kit-Specific Development Guidance

### Base Kit

Base Kit optimizes for the fastest safe homelab start.

Cover these cases:

- one local node
- one cloud node
- local-only access
- kombify-managed subdomain
- own domain
- constrained hardware
- optional worker/storage node
- no email/domain provided
- owner email provided
- user wants only Foundation + Platform
- user enables one or more Application modules

Base Kit should avoid requiring long-running orchestration for Day-1. It should still produce a homelab that a technical user can continue manually.

### Modern Homelab

Modern Homelab optimizes for hybrid local plus cloud architecture.

Cover these cases:

- local services that must never be public
- public entrypoints on the cloud node
- remote availability for selected services
- tunnel or mesh connectivity
- split DNS
- identity continuity across local and cloud
- backups between failure domains
- service placement across local/cloud
- public website or remote app hosting
- local data vaults with remote access policy

Modern Homelab must make the local/cloud split explicit. It should not behave like Base Kit with an extra node attached.

### HA Kit

HA Kit optimizes for reliability.

Cover these cases:

- quorum
- manager/worker roles
- stateful service placement
- storage replication
- failover routing
- backup and restore
- update sequencing
- degraded operation
- node replacement
- monitoring and alerting

HA Kit should reject topologies that look multi-node but cannot satisfy reliability requirements.

## 8. OneClick And Techie Modes

StackKits should not fork into two products. The difference is intent depth.

### OneClick/Ready

Design for:

- few questions
- strong defaults
- automatic owner setup
- default identity
- default PaaS
- default routes
- default backups where possible
- clear recovery output
- successful first login
- verified app access

Failure messages should say what is missing and how to fix it.

### Techie/Alternatives

Design for:

- explicit overrides
- alternative tools
- service placement
- custom domains/subdomains
- custom storage
- external identity
- advanced network rules
- opt-in modules
- generated plan inspection
- explainable resolver decisions

Overrides should never bypass validation. An advanced user gets more control, not an unsafe path.

## 9. Public Exposure Matrix

Every service should classify exposure.

| Exposure | Meaning | Requirements |
| --- | --- | --- |
| internal-only | Container or platform internal | No external route. |
| LAN-only | Home network only | Local DNS, auth policy if user-facing. |
| VPN-only | Mesh or private tunnel only | Mesh dependency and route policy. |
| public-authenticated | Internet reachable behind auth | Domain, TLS, auth gateway, service classification. |
| public-unauthenticated | Internet reachable without auth | Must be intentionally allowed, usually websites or public endpoints only. |

Default application services should not become public-unauthenticated. Public websites are the exception, not the pattern.

## 10. Validation And Test Expectations

For any behavior-changing change, choose the smallest test set that proves the contract.

Minimum expected checks:

- CUE validation for changed contracts
- Go unit tests for changed resolver/generator logic
- golden output tests when generated shape changes
- CUE binding tests for contract enforcement
- fresh-target smoke for default-path changes
- registry/hash verification when module or kit metadata changes
- release-archive smoke when packaging, installer, identity defaults, CUE imports, or module contracts change

Recommended commands:

```bash
cue vet ./base/...
cue vet ./base-kit/...
cue vet -c=false ./modules/...
cue vet ./modern-homelab/...
go test ./...
make test-cue-binding
stackkit generate
stackkit verify
```

If a command is skipped, document why.

For installable defaults, do not accept a green result that depends on the local checkout. Build a release snapshot, extract the archive, and run `stackkit init` plus `stackkit generate` from the extracted files. The generated `terraform.tfvars.json` must contain the requested owner identity and non-empty generated identity values; empty strings in generated secrets or TinyAuth users are release blockers.

## 11. Release And OSS Hygiene

Before public release or mirror sync:

1. Strip internal Doppler references.
2. Strip internal documentation links.
3. Remove tenant-specific data.
4. Avoid internal Coolify/Render assumptions unless they are public contract.
5. Verify examples are reproducible.
6. Verify generated snapshots match accepted source inputs.
7. Confirm license and image-source compatibility.
8. Verify every public release archive includes the CLI, packaged OpenTofu, root `cue.mod`, shared `base/`, required `modules/`, and the relevant kit directories.
9. Verify public one-liner endpoints return shell content rather than website fallback HTML.

Public release output should be useful without exposing kombify-internal operations.

## 12. Review Questions

Use this review list before merging a StackKit change.

1. Which Golden Rules does this touch?
2. Does this introduce a new default?
3. Does it affect Foundation, Platform, or Application?
4. Is the authority boundary clear?
5. Is CUE still the technical contract?
6. Is the database only storing the correct operational/registry state?
7. Is the default safe when intent is missing?
8. Are user overrides validated?
9. Does this work for OneClick users?
10. Does this preserve Techie alternatives?
11. Does this change public exposure?
12. Does it create, store, or display secrets?
13. Does it affect backup or restore?
14. Does it affect multi-server placement?
15. Does it affect Day-2 or TechStack orchestration?
16. Does `stackkit verify` have something meaningful to check?
17. Are unsupported contexts rejected clearly?
18. Are docs and registry metadata aligned?
19. Are generated artifacts reproducible?
20. Does the release archive work without the source checkout?
21. Is a new ADR needed?

## 13. When To Write An ADR

Write or update an ADR when a change:

- changes any Golden Rule
- introduces a new canonical layer, mode, kit type, or platform adapter
- promotes a module to default
- changes the CUE/database authority boundary
- changes public exposure policy
- changes identity or secrets policy
- changes kit import/export or hash parity
- changes the release or public mirror contract
- makes an irreversible migration decision

Small module additions do not always need an ADR, but default behavior changes usually do.
