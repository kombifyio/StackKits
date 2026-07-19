# Architecture — kombify StackKits

> Last verified: 2026-07-17

This is the current implementation overview for this repo. Normative product and module rules are summarized here and in accepted ADRs.

## System Role

StackKits turns CUE-defined infrastructure contracts into deployable homelab environments:

```text
operator intent / TechStack intent
        |
        v
StackSpec v1/v2 or API request
        |
        v
selected KitDefinition + inventory + capability adapters + add-ons
        |
        v
CUE-validated, immutable ResolvedPlan + planHash
        |
        v
generated OpenTofu / tfvars / metadata
        |
        v
stackkit apply + stackkit verify
```

CUE is the technical contract source of truth. The kombify database mirrors registry and operations state, but it does not replace live CUE contracts.

## Architecture v2 Keystone

The active architecture program is Beads `kombify-StackKits-dqcp` / Linear
`KOM-774`; its normative decision is
the private ADR-0029 decision record. The current runtime still
contains transitional global-`context`, independent Go validation, and legacy
Modern/HA surfaces. New work MUST target the v2 spine instead of adding consumers
to those compatibility paths.

The v2 boundary is:

1. Kit selection chooses a concrete `#KitDefinition` with required, default,
   optional, and forbidden capabilities.
2. Site locality, trust/failure domains, node hardware, reachability, placement,
   and availability are independent typed axes.
3. Kit definition, user intent, detected inventory, implementation adapters, and add-ons compile
   once into a canonical secret-free `#ResolvedPlan`.
4. CLI, API, generation, apply, Node Hub, registry, and downstream consumers use
   the same schema/compiler version and `planHash`.
5. Legacy v0.5 specs are dual-read for one minor release; v2 is the only new write
   format. Compatibility projections may feed existing renderers temporarily but
   may not change kit identity or weaken validation.

### Provider-free external-host boundary

StackKits never owns a server-provider resource. TechStack selects and
manages provider accounts, regions, images, sizes, credentials, leases, ownership,
cleanup, and native resource IDs. StackKits receives only an opaque
`ExternalHostBinding` for a host that already exists. The binding is hash-bound to
the Stack, node, normalized Spec, exact host requirements, inventory snapshot, and
a bounded execution channel; it contains no provider name, raw management address,
or lifecycle operation.

Host inspection produces a separate `HostConformanceReceipt`. Its OS tuple is the
only StackKits compatibility claim. Architecture, kernel, virtualization, and
container-runtime observations remain provider-neutral admission diagnostics. A
Shadow Plan may carry empty binding and receipt maps so prerelease development is
not blocked; once evidence is supplied it must match exactly, and a later executor
must reject an expired binding at its recorded apply instant. Absence is reported
as pending/unverified rather than inferred as success.

The CUE catalog type `CapabilityProvider` is retained as implementation-adapter
terminology for host-local, external-service, renderer, mesh, or PaaS realizations.
It is not a server provider and may not acquire server-provider fields by reuse of
that name.

### Service-owned plan authority and rehash boundary

Architecture v2 does not trust a persisted plan to identify its own authority by
labels and hashes alone. At compiler construction, the service CUE-normalizes and
freezes its loaded `KitDefinition` set and catalog. For each persisted plan, the
service selects the Definition for `kit.slug` from that private set and injects the
exact normalized body into `#ResolvedPlanDefinitionBinding`; the plan cannot supply
or replace that validation input. The binding covers every Definition-observable
plan decision, including kit and failure policy, topology and Control Authority,
capability bounds, generation/network posture, access and routes, device
enrollment, and the Modern bridge/data/edge-verifier contract.

The service also checks the selected capabilities, providers, modules, render
units, host/external owners, privileged approvals, compatibility contract, and
catalog-owned gate bodies against the exact normalized catalog bodies, rather than
accepting copied `contractHash` values as proof. Compiler-owned projections are
recomputed and must be exact: provider/module selection and placement, owner and
module-input projections, generation artifacts, health/evidence/apply gates and
owner gate references, privileged-interface approvals, and
`executionReadiness`. In particular, readiness is rebuilt from the exact resolved
provider/module realization, artifacts, evidence, renderer ID, output root, and
Modern publication set. A caller cannot turn a blocked plan into a ready one merely
by editing blockers and recomputing `planHash`.

This is a service-bound structural and derivation check, not a digital signature
or a general proof of source provenance. A plan-only verifier does not possess the
raw StackSpec or inventory body. It can enforce internal source-hash consistency
and every consequence derivable from the loaded Definition/catalog and retained
plan fields, but it cannot generally prove that an otherwise allowed dynamic
setting came from the claimed source, or distinguish an explicit value from a
default, when the document and its untrusted hashes are rewritten together. The
execution seam therefore requires a `CurrentResolution`: the same service
re-resolves the current raw StackSpec and inventory and compares the exact
canonical plan bytes. Offline provenance without those source bodies would require
a trusted external signature or attestation rooted outside the plan; `planHash`,
`catalogHash`, and contract hashes do not provide that property by themselves.

### Definition-owned reachability

Route exposure is part of the selected `KitDefinition`, not a consequence of a
global `context`, a provider name, or the number of nodes. Every definition
declares the allowed access-policy exposures, whether LAN step-down is permitted,
and one rule for each resolved route exposure. A rule identifies both the
capabilities that must actually be selected and the site kinds from which the
resolved module may originate.

| Kit | Access policies | `local` route | `remote-private` route | `public` route |
| --- | --- | --- | --- | --- |
| Basement Kit | private, LAN, public; enrolled-device LAN step-down allowed | Home; no additional capability | Home + `private-remote-access` | Home + `public-publish-egress` |
| Cloud Kit | private or public; LAN and LAN step-down rejected | Cloud; no additional capability | Cloud + `private-admin-mesh` | Cloud + `public-edge` |

The CUE binding rejects invalid intent before compilation. The Go compiler repeats
the decision against the effective resolved capability set and the module's exact
resolved origin site. This is deliberate defense in depth: enabling a route in raw
intent is insufficient, and a renderer is never allowed to infer or widen
reachability.

### Shared Home-site offline-autonomy policy

`offline-autonomy` is a Home-site architecture capability, not a Basement rollout
shortcut and not a consequence of `site.kind: home`. Basement Kit and Modern
Homelab select the same dedicated `stackkits-local-autonomy-policy` provider and
module-single policy manifest because both promise a Home authority that survives
link loss. Cloud Kit does not select this capability. The residual
`stackkits-local-runtime` therefore no longer owns or implicitly supplies it.

The manifest receives only the compiler-owned projection `stackId`, `kit`, safe
`sites`, `controlPlane`, `identity`, `data`, and `failurePolicy`. It contains no
provider lifecycle, management address, credential, socket, network-tunnel, or
general LAN authority. Basement requires local-only enrollment, Home data/control
authority, no Cloud verifier, and zero stale verification. Modern additionally
requires explicit Home and Cloud Sites, exact Cloud verifier coverage, local
continuation during Cloud/link loss, a fail-closed Cloud edge, and explicit policy
for any Cloud data copy.

This is deliberately a generation-only contract. The emitted
`local/autonomy/policy.json` states that runtime enforcement is unverified and
air-gapped installation is not included. A later runtime component must earn the
Apply and evidence claims; the policy renderer cannot manufacture them.

### Kit-owned identity authority and verifier distribution

`human-identity-core` and `device-trust-core` remain shared capability
definitions, but the residual Core module no longer realizes them. Architecture
v2 uses a separate closed `identityTrust` graph so kit identity cannot be inferred
from legacy `context` or widened through the compatibility `identity` object.
Definitions own logical authorities, credential issuers, audiences, key-set
references, verifier placements, and one-way distribution rules. The compiler
materializes exact Site refs and binds issuer/audience/key-set URNs to `stackId`.
Module inputs never contain selectors, keys, credentials, endpoints, addresses,
provider accounts, or lifecycle authority.

Basement composes a shared Home authority/issuance owner with a Basement-local
trust/verifier owner. Enrollment is LAN-local, all authority and verification
stay Home, and revocation staleness is zero. Cloud owns Cloud human/workload
authority and verification, but its device authority is an explicit external
owner-bound contract; Cloud cannot enroll or issue device credentials. Modern
keeps all enrollment and signing Home-side, places verifier-only instances at
Home and every Cloud Site, and distributes only verification-key references and
revocation state Home-to-Cloud. Reverse distribution, private/signing keys,
credentials, Cloud enrollment/issuance, and general LAN reachability are closed
as `false`.

The five emitted identity policy artifacts are generation-only. They explicitly
state that runtime enforcement and credential issuance are unverified and that
credential material, JWKS bytes, private keys, endpoints, and transport
realization are not included. Required runtime evidence therefore keeps Apply
blocked. HA remains an add-on and must preserve the chosen kit's trust graph.
These contracts authenticate StackKit access only; they are not a Companion,
SpeechKit, Home Assistant, or general smart-home authority.

### Shared Home local-ingress and access policy

`local-ingress` and `lan-access-policy` are now owned by the dedicated,
provider-free `stackkits-home-access-policy` provider rather than the residual
`stackkits-local-runtime` umbrella. Basement Kit selects the module-single policy
manifest; Cloud Kit explicitly forbids the Home LAN capabilities and never
selects the module.

The compiler does not expose raw `access` or `network` objects to this renderer.
It derives one closed `localReachability` view containing only sorted `local`
routes whose origins are Home Sites, their logical origin refs, the effective
access decision, and non-secret TLS metadata. The projection retains the source
policy exposure (`private` versus `lan`), device-bound LAN step-down, explicit
site scope, and default-closed decision. Public, remote-private, and Cloud-origin
routes are omitted. Network configuration, DNS/provider configuration,
credential refs, CIDRs, management addresses, bridge state, runtime networks,
interfaces, and sockets are structurally unreachable.

The deterministic `local/network/access-policy.json` is generation-only. It
does not claim a listening reverse proxy, firewall rule, certificate, interface,
IP selection, DNS/mDNS availability, or runtime policy enforcement. Apply keeps
an explicit `module-apply-support-missing` blocker until a separate executor and
evidence contract implement those mechanisms.

### Explicit Home LAN discovery policy

`lan-discovery` has its own provider-free owner,
`stackkits-home-lan-discovery-policy`, and is no longer supplied by the residual
Local umbrella or the Home access module. Basement Kit and Modern Homelab select
that owner; Cloud Kit forbids it. StackSpec carries the separate
`lanDiscovery.advertiseRouteRefs` allowlist, whose default is empty. A local
route therefore never becomes an mDNS, DNS-SD, or LAN-DNS advertisement merely
because it is reachable.

The compiler resolves only explicitly named routes and requires each one to be
Home-originated, `local`, governed by an effective `lan` policy, default-closed,
and addressed by a non-`.localhost` host. It then exposes the closed
`homeLANDiscovery` projection: sorted Home Site refs plus route/service/origin,
listener protocol/port/host, and the minimal LAN/default-closed policy proof.
It cannot carry raw network or access objects, providers, credentials, CIDRs,
management addresses, internal target ports, TLS state, bridge data, interfaces,
sockets, or runtime networks.

The deterministic `local/network/discovery-policy.json` is generation-only and
default-deny. LAN DNS ownership, address and interface selection, mDNS/DNS-SD
adapters, runtime enforcement, and runtime evidence are explicitly not included
or unverified. Apply remains blocked until a separate runtime owner earns those
claims.

### Catalog-owned module placement and hardware eligibility

Module placement is no longer equivalent to "every enabled node of a supported
Site kind". A `ModuleContract` may bind a typed `nodeSelection` over Control
Authority Site, Control Plane membership, required roles, and exact labels, plus
`runtimeRequirements` for architecture, minimum CPU/RAM/storage, allowed
virtualization, and mandatory inventory facts. The compiler evaluates these
contracts only against StackSpec topology and the separate attested
`InventoryFacts`; it does not inspect the compiling host or silently downgrade a
requirement. The exact selector and requirement bodies are persisted in the
resolved module and reconstructed from the service-owned catalog during plan
validation.

This boundary is the foundation for the provider- and device-neutral OS
compatibility matrix: a matrix result names the same normalized OS facts used for
host admission, while architecture, kernel, runtime, virtualization, and hardware
remain diagnostics. Plan-only validation retains the inventory hash, not the raw
inventory document, so apply still requires an exact `CurrentResolution`
recompile when source provenance matters.

The OS-only public status document and its closed unverified reason codes are documented in
[OS_COMPATIBILITY.md](OS_COMPATIBILITY.md).

### Service endpoints and Modern publication backends

A route or Modern publication cannot invent an upstream from a module name and
port. Routable render units declare catalog-owned service endpoints with service
identity, allowed ingress protocols/exposures, upstream protocol and target port,
origin selector, data-class/locality requirements, and health contract. The
compiler resolves that endpoint to exact Sites, nodes, and render instances.

For Modern Homelab the public listener and protected backend are deliberately
different contracts. For example, an edge listener may expose `https:443` while
the only allowed edge-to-home flow is the selected endpoint's `http:2283` plus
its exact data classes. The ResolvedPlan publication records both sides and its
module/unit/backend pool. `management-only` overlays cannot carry publications or
data flows, broad routes remain forbidden, and TLS passthrough is unavailable
while edge authorization or rate limiting depends on edge termination.

Each publication also carries a compiler-derived access decision. It must bind a
public, authenticated policy and the exact allowed HTTP method set. Every selected
edge-to-origin flow is a subset of that method set; a private/LAN policy,
`authentication: none`, or a widened method fails before rendering. Endpoint data
authority intentionally uses the same stable `serviceRef` as the endpoint identity,
so a publication cannot silently switch to a second data binding.

Route-specific executable backend-pool health probes, catalog-owned TLS issuers,
and one-way identity-verifier distribution remain explicit graduation gates. Until
those implementations exist, every publication contributes the independent
generation and apply blockers `bridge-renderer-missing`,
`origin-identity-unbound`, `tls-profile-unbound`, and
`health-gate-not-executable`; a generic module contract blocker cannot hide or
replace them.

### Executable render instances

The resolved plan keeps each logical render-unit contract and its logical output
bindings unchanged, but materializes the exact executions that a renderer is
allowed to perform:

- A `module`/`single` unit has exactly one locality-free instance named
  `<unitID>-logical`. It retains the logical artifact ID and output path and MUST
  NOT invent a site, node, daemon, or implicit first-node placement.
- A `node-local` unit has an exact instance for its resolved placement: one exact
  node for `single`, one instance for every resolved node for `one-per-node`, or
  one instance for every resolved daemon binding for `one-per-daemon`. Node-local
  instance IDs and locality fields are plan-owned, not renderer choices.
- Every instance output maps one-to-one to a concrete generation artifact.
  Node-local artifact IDs use
  `<logicalArtifactID>-instance-<instanceID>` and their paths are
  `instances/<moduleID>/<instanceID>/<logicalOutput>` relative to
  `generation.outputRoot`. Every generated artifact has an exact owner: either
  `{kind: "plan"}` or `{kind: "render-instance", moduleRef, unitRef,
  instanceRef, outputRef}`.

The renderer is invoked exactly once for each explicit instance and receives its
immutable ID, scope, output contracts, and optional locality. It may read the
defensive logical site/node sets needed by a module-scoped aggregate renderer, but
it MUST NOT derive execution cardinality, select a first node, reinterpret daemon
bindings, change artifact identity or paths, or widen placement.

### Runtime network instances

`networkRef` is only a logical endpoint label. It never proves that two workloads
share a concrete network. The resolved plan therefore materializes each provider
network as an owner-bound runtime object:

- Every exact provider render instance owns a distinct network identity named
  `<providerInstanceRef>-network-<networkRef>-interface-<providerInterfaceRef>`.
  The object carries its exact site, node, daemon contract, observed daemon
  instance, and immutable module/unit/instance/interface owner tuple.
- The network has an explicit closed membership list. Its single provider member
  must equal the owner; every consumer member identifies one exact consumer render
  instance and local requirement. The same logical label under another owner is a
  different network, not an alias.
- Provider requirement bindings carry the provider instance, consumer instance,
  daemon instance, and runtime-network instance IDs. Both render instances carry
  the reciprocal `networkBindings` projection handed to their renderer. Resolution
  and rendering reject missing, orphaned, duplicate, cross-site, cross-node,
  cross-daemon, owner-drifted, or label-only bindings.

`runtimeNetworks`, per-instance `networkBindings`, `instances`, and generated-
artifact `owner` are mandatory resolved-plan fields. Persisted Architecture v2
plans from before these contract changes fail closed and must be re-resolved from
current intent and inventory; there is no compatibility fallback that reconstructs
execution placement or runtime connectivity inside the renderer.

The canonical `contract-two-node` fixture now proves this complete boundary from
raw Basement intent and detected inventory through the current compiler, CUE
verification, canonical plan authorization, and the plan-pure renderer registry.
It expands a rootless Docker daemon on each of two home nodes into exact
`one-per-daemon` proxy instances, binds a consumer on each node to only its local
provider-owned runtime network, and preserves the distinct exact
`/run/user/1000/docker.sock` and `/run/user/1001/docker.sock` backing paths plus
one central evidence-bound approval. The logical direct endpoint declares
`pathSource: daemon-binding`; a fixed `path` remains available only when every
selected daemon binding is required to expose that same path.
Every persisted daemon socket path is canonical portable ASCII and at most 107
UTF-8 bytes, matching Linux `sockaddr_un.sun_path` after reserving its NUL byte.
Reverse node declaration order produces identical canonical bytes and `planHash`;
unknown or wrong same-label networks, duplicate bindings, and orphan bindings fail
the governed CUE verification before a renderer can be constructed.

This proof is intentionally isolated in the separate
`architecture/v2/contractfixture` CUE package, `contractFixtureCatalog`
authority document, and `internal/architecturev2/contract_fixture_bundle`.
Product services load only `authority_bundle`; fixture source drift therefore
cannot break CLI/API product startup. Every plan carries the exact authority
class/document/eligibility tuple plus the order-normalized CUE catalog hash.
Verification binds the selected normalized Definition, catalog-owned bodies,
compiler-derived projections, compiler, renderer, and evidence to that
service-owned namespace; relabeling and rehashing a fixture as a product plan
fails closed. This closes cross-authority substitution, but it does not broaden
the source-provenance guarantee described above: execution still requires the
exact current `CurrentResolution` bytes.
Its manifest entry is `scope: contract` and `graduationEligible: false`; it does
not enter any product catalog, make Basement generation-ready, or weaken the
Apply blockers. Product runtime graduation still requires kit-owned renderers
and same-SHA functional execution evidence. Public OS support is a separate
controlled policy projection and is never inferred from a lab matrix.

The CLI generation boundary is executable for a generation-ready plan whose
exact renderer contracts are present in the product registry: it creates a
fresh `CurrentResolution`, exact-matches the canonical plan persisted beneath
the plan-owned output root, authorizes that one resolution, builds the exact
product renderer registry, and holds the
authorization plus workspace handle across `RenderAndInstall`. Renderer output,
manifest, receipt, and closed-tree replacement commit as one managed
transaction; cancellation reaches the renderer, and close failures remain
visible. Architecture v2 rejects legacy `--force` and `--fragments` semantics
and exact-binds an explicit `--output`. Plan and Verify validate the artifact
closure before returning their typed executor/verifier boundary. This wiring
does not make Basement generation-ready: its residual Core, Local, and Basement
Compose umbrellas remain honest blockers until concrete owners and modules
replace them.

### External infrastructure authority

StackKits has no demo/test server-provider lifecycle authority. The former
demo/test CUE contract and its server cardinality, lease,
provider-resource ownership, mutation, cleanup, and second-provider rules were
removed when the superseded private ADR-0030 decision record was
superseded.

TechStack owns provider adapters, execution authority, durable allocation and
cleanup ledgers, and native absence proof. Simulate may exercise those contracts
as an optional harness. StackKits receives only an already supplied host through
`ExternalHostBinding` and returns OS/host evidence through
`HostConformanceReceipt`; it neither knows nor reconstructs the resource behind
the opaque references.

The productive on-host flow is `stackkit host conformance --binding <file>`.
The command validates the closed provider-free binding, hashes the exact running
StackKits executable (the running inode on Linux), requires that version and
digest to equal the Candidate authorized by the binding, and performs only
read-only allowlisted local probes of the
Linux OS tuple, architecture, kernel, container-runtime binary, virtualization,
and nested-virtualization flag. It makes no network, SSH, provider, lifecycle,
mount, bridge, container-run, or external-IP probe. Its stdout is exactly one
Receipt JSON document; `--output` creates a new non-overwriting `0600` artifact.
The command is excluded from deploy logging and rollout telemetry so provider,
tenant, and node environment metadata cannot enter this evidence path.

Receipt production does not complete host admission by itself. The orchestrator
attaches the exact Binding and Receipt to the same base Inventory node and then
resolves the final canonical plan. The final `planHash` covers both envelopes.
Architecture v2 Apply requires one fresh `conformant` Receipt for every external
Binding and rejects missing, stale, degraded, incompatible, or unverified
evidence before readiness or executor handoff. A plan with no external Binding
needs no Receipt. This is execution admission for an external handoff, not a
compatibility-matrix or prerelease gate.

Without a versioned CUE-owned OS support policy, the producer deliberately emits
an `unverified` OS check. The future controlled public projector is the only
component allowed to turn admitted, current Receipts into positive OS support
documentation; provider or device runs can never do so directly.

Provider/device runs may be recorded as separate operational evidence, but they
do not define kit compatibility and do not gate pre-beta releases. Concrete
addresses, provider/device locators, credentials, ownership, and cleanup state
remain outside StackKits contracts and public evidence.

Managed runtime admission follows the same boundary. The temporary
`/api/v1/internal/runtime-actions/*` surface accepts the historical node-side
transport shape only when `api_version` is absent or explicitly
`stackkit.runtime-action/v1`; it rejects `stack_spec` and every other version.
The physically separate `/api/v2/internal/runtime-actions/stackkit-rollout`
and `stackkit-verify` routes decode explicit `stackkit.runtime-action/v2alpha1`
through the shared closed Go contract. They contain only StackSpec, Inventory,
expected plan hash, and stack/tenant/owner identity. StackKits re-resolves that
intent with its embedded CUE authority and binds both stack ID and plan hash
before execution admission. Until the governed V2 renderer/executor exists,
the V2 routes return a typed 501 and have no code path to dry-run readiness,
caller-chosen OpenTofu directories, raw SSH, TechStack lease identifiers, or
legacy verify.
The private source consumes the exact shared Go module pin; the curated OSS
export deterministically projects that same verified package into a local path
and removes the private module dependency before its public build gate.

## Major Containers

| Container | Location | Responsibility |
| --- | --- | --- |
| CLI | `cmd/stackkit`, `internal/*` | Operator workflow: init, prepare, validate, generate, plan, apply, verify, update, registry, logs, and recovery commands. |
| API server | `cmd/stackkit-server`, `internal/api` | HTTP surface for catalog, schema, validation, generation preview, logs, capabilities, OpenAPI, and Direct Connect registry lifecycle. |
| CUE contracts | `base/`, `basement-kit/`, `cloud-kit/`, `modules/` | Schemas, defaults, constraints, module contracts, and deployment shape. |
| Composition/generation | `internal/cue`, `internal/composition`, `internal/iac`, `internal/tofu`, `internal/terramate` | Bind CUE/spec data into generated deployment artifacts and execution adapters. |
| Public docs | `README.md`, `docs/` | Homelab/BaseKit OSS documentation and CLI install contract. |
| Release automation | `.github/workflows`, `.goreleaser.yaml`, `scripts/public/` | CI, release, server image, private website validation, and curated Homelab/Basement Kit OSS mirror sync. The old `scripts/sync-public.sh` path is intentionally deprecated. |

## Core Data Flow

1. `stackkit init` creates a `stack-spec.yaml` from user intent.
2. `stackkit prepare` validates prerequisites, can install Docker on supported targets, and verifies the StackKit-packaged OpenTofu binary.
3. The current v1 path applies generic Go validation and builds kit CUE packages
   separately; it does not yet prove that the selected KitDefinition and user
   spec unify. Architecture v2 replaces this with the single ResolvedPlan
   compiler before generation.
4. `stackkit generate` writes generated rollout artifacts under `deploy/`.
5. `stackkit plan` and `stackkit apply` execute OpenTofu through the Go adapter.
6. After OpenTofu bootstraps the selected PaaS, `stackkit apply` consumes the generated platform manifest. StackKit may operate StackKit-owned system apps and StackKit-owned L3 application use cases through the platform adapter, but customer-owned user apps remain PaaS handoff metadata and are deployed, updated, and operated by the selected external PaaS tooling.
7. First-run setup is represented separately from deployment as setup-drop metadata. Local Base Node Hub routes are intentionally bootstrap-open until the operator clicks `Protect Base Hub` after owner setup; that action persists the protection setting and switches the local router behind TinyAuth. Public/non-local Base routes stay protected when TinyAuth is enabled. The default `bootstrapped` mode uses `automatic` setup for L1/L2 platform services and `on_demand` setup actions for L3 applications; `bare` forces manual setup and `advanced` is the Terramate Plus lifecycle mode with day-2 orchestration, Runtime Intelligence, Frontend Intelligence, and managed TechStack handoff surfaces.
8. `stackkit verify` performs read-only host checks and optional HTTP URL checks.
9. `stackkit-server` exposes the same catalog, validation, generation-preview, log, and registry concepts over HTTP and is deployed as a platform-managed system app in the normal Basement Kit path.

## Routing Ownership

StackKit does not own a separate router when the selected PaaS already includes one. For Coolify, generated StackKit routes must be served by Coolify's Traefik/proxy. In those environments, the PaaS router is the StackKit router. Dokploy has an integrated-router draft adapter, but it is not part of the production E2E standard until promoted.

Komodo is the first explicit exception: the initial `paas: komodo` contract uses exactly one StackKit-owned Traefik while Komodo owns Compose Stack deployment. The generated dashboard/status output and release evidence must label that routing ownership as StackKit-owned, not Komodo-owned.

StackKit must not add a second Traefik instance, an Nginx bridge container, a host-side proxy, or a browser/test-only forwarding workaround to make service URLs appear reachable. Such a path is a routing bypass, not production evidence. If StackKit later supports another PaaS without an integrated router, that adapter contract must explicitly include one StackKit-owned router and the generated dashboard/status output must label it as such.

## Current Technical Stack

| Area | Current source |
| --- | --- |
| Go | `go.mod` and `mise.toml`: `1.26.5` |
| CUE library | `cuelang.org/go v0.15.4` |
| CLI | Cobra `v1.10.2` |
| HTTP server | Go `net/http` with `ServeMux` |
| IaC engine | OpenTofu, packaged with StackKit release artifacts |
| Task runner | `mise.toml` |
| Public release checks | `scripts/release/*.mjs`, `.github/workflows` |

## StackKit Layers

Every StackKit resolves through the canonical layers:

- `foundation`: host bootstrap, security baseline, owner/break-glass, secrets bootstrap, base network, minimal telemetry, and preflight policy.
- `platform`: runtime, PaaS adapter, reverse proxy, DNS/TLS, identity provider, login gateway, service registration, logs, and health.
- `application`: user-facing use-case modules such as photos, vault, media, files, smart home, dev, and AI.

Layer definitions are enforced by CUE contracts.

## API Surface

The API server registers endpoints in `internal/api/server.go`; the contract source is [../api/openapi/stackkits-v1.yaml](../api/openapi/stackkits-v1.yaml). The human summary is [API.md](API.md).

Public unauthenticated endpoints:

- `GET /health`
- `GET /api/v1/health`
- `GET /api/v1/openapi.yaml`

Protected endpoints cover:

- capabilities
- StackKit list/get/schema/defaults
- full and partial validation
- tfvars and preview generation
- deploy log list/get/stream
- Direct Connect registry register/deregister/heartbeat

## CLI Surface

The implemented top-level command groups are documented in [CLI.md](CLI.md):

`init`, `prepare`, `generate`, `plan`, `apply`, `verify`, `remove`, `status`, `validate`, `app`, `break-glass`, `backup`, `cluster`, `compat`, `doctor`, `kit`, `logs`, `module`, `registry`, `wizard`, `completion`, and `version`.

## Source Of Truth Boundaries

| Concern | Source |
| --- | --- |
| Technical deployment contract | CUE files in this repo |
| Registry, catalog, lifecycle mirror | kombify database / Admin API |
| API wire shape | `api/openapi/stackkits-v1.yaml` plus server tests |
| CLI behavior | Cobra command definitions and tests |
| Architecture overview | `docs/ARCHITECTURE.md` |
| Active work | published roadmap and release notes |
| Roadmap read-view | `ROADMAP.md` |

Historical V5/V6 and CUE-audit planning content has been folded into ADRs, Beads, the architecture manifest, and this overview. Do not reintroduce standalone architecture-version or task-tracker Markdown files.
