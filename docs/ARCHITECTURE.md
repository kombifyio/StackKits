# Architecture — kombify StackKits

> Last verified: 2026-07-21

This is the current implementation overview for this repo. Normative product and module rules are summarized here and in accepted ADRs.

## System Role

StackKits turns CUE-defined infrastructure contracts into deployable homelab environments:

```text
operator intent / TechStack intent
        |
        v
canonical StackSpec v2 / versioned API request
 (v1: validation and explicit migration only)
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
5. v0.6 is the sole legacy execution compatibility minor while its first-party
   init/mutation commands still write v1. The migration boundary already accepts
   a complete v2 draft, reconciles it against exact v1 bytes, synthesizes
   hash-bound `migrated-v1` lineage, resolves the final bytes through CUE, and
   persists canonical v2 intent. From v0.7/M+1, raw v1 cannot enter `generate`,
   `plan`, `apply`, `verify`, or the legacy remote verifier; v1 remains readable
   only for validation and explicit migration.

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

Every resolved non-local route also persists an exact
`capabilityRealizations[]` authority projection for each capability/role required
by that Definition rule. It binds the canonical Capability and provider contract
hashes, the provider's resolved Site scope, and—when the provider is module-backed—
exactly one selected module contract plus its resolved Site/node placement. Local
routes carry an empty projection. Definition binding rejects missing or additional
entries; catalog-body validation rejects copied hashes, foreign or ambiguous module
owners, and placement outside the provider scope. The projection remains provider-
neutral and contains no server-provider, account, region, endpoint, credential, or
lifecycle authority. Basement public routing is explicitly `egress`; Cloud public
routing is explicitly `edge`. Modern ordinary public routes are Cloud-origin only.
A Home workload can reach the Cloud edge only through the separate
`bridge.publications` graph, which already owns exact source/edge Sites, allowlisted
flows, identity, TLS, link and fail-closed behavior; `network.routes` cannot bypass it.

Public TLS custody follows that same architectural boundary. A Basement public
egress route carries `tls.mode: external` and names its exact `egress` capability
owner; CUE requires the matching Home-access requirement for the route's Home Site.
StackKits therefore describes the minimum TLS policy and binds it to the outbound
publication authorization, but does not invent a certificate issuer, endpoint,
credential, DNS provider, or inbound Home edge. Cloud public routes and Modern
Cloud-edge routes instead carry `tls.mode: terminate-at-edge` and remain bound to
the catalog-owned `public-tls` profile and issuer. A Modern Home workload still
reaches that termination point only through `bridge.publications`.

### Topology capabilities are not runtime owners

`site-local` now uses the closed provider realization `kind: topology`. The
selected `stackkits-local` provider must bind exactly the same Site kinds as its
catalog `supportedSiteKinds`, contributes no module, artifact, runtime target,
owner, Health gate, evidence gate, or Apply blocker, and cannot be used to
realize a network or host mutation. The verified StackSpec Home Site is the
fact; a generated JSON handoff cannot make that fact more true.

Operational Home extensions no longer ride on this topology provider.
`lan-dns` is a declarative service-naming contract and creates no runtime.
`private-remote-access`, `public-publish-egress`, and
`encrypted-offsite-backup` each select a different generation-only module with
one exact capability, unbound owner, operation set, Health ref, evidence ref,
and artifact. Basement selects none by default. Modern selects only private
remote access because its KitDefinition requires it. None of these boundaries
owns transport choice, endpoints, credentials, provider lifecycle, discovery,
or general LAN reachability.

Private remote access and public publication now cross an explicit two-stage
boundary. A Shadow Plan emits one hash-bound `HomeAccessRequirement` per exact
Home Site and selected access capability. The requirement contains only the
target node refs, the catalog contract owner/hash, and a closed default-deny,
Home-outbound, declared-services-only policy. An external platform may return
an `ExternalHomeAccessBinding` bound to that exact requirement, candidate, and
validity window. Its `accessFabricRef` is opaque; transport, endpoint/address,
credentials, server-provider resources, accounts, regions, discovery, and
lifecycle handles are structurally forbidden. Missing bindings preserve useful
Shadow Plan and generation output but add
`external-home-access-binding-missing` to Apply readiness. Encrypted backup is
deliberately excluded from this access seam and requires its own future backup
target/repository binding.

When Apply becomes eligible, the compiler projects every verified binding into
`ApplyRequirements` and assigns it to exactly one runtime requirement. Node
scope always comes from the StackKits-owned requirement, never from the
external binding. The shared runtime executor carries the upstream binding hash
plus a canonical projection hash, exact Site/node/capability authority, and the
same once-captured UTC instant used for evidence verification and invocation.
All validity instants use the exact canonical `time.RFC3339Nano` UTC wire form:
whole seconds or at most nine fractional digits with no trailing zero. CUE and
Go therefore admit the same bytes; offsets and equivalent but noncanonical
spellings cannot rotate hashes or fail only after compilation.
It rejects expired or replayed authority immediately before the adapter call.
An adapter may consume non-empty Home bindings only when its registry entry
declares the exact owner/provider/module/unit and capability contract; ordinary
executors reject them. Existence and behavior of the opaque external fabric
remain trusted custody/attestation of that explicitly registered Home adapter,
not a fact invented by the shared contract.

### StackInstance, ControlAuthority, and Fleet

A Basement Kit or Cloud Kit is single-Site, not single-node. One
`StackInstance` may contain a controller plus any explicitly placed worker or
storage nodes; every node belongs to one Site and the exact same StackInstance
identity. `single` means one logical and physical ControlAuthority member, not
one compute node. Warm standby and quorum are HA add-on realizations of that
same logical authority, with at least two or exactly 3/5/7 controller members.

The enabled controller set and ControlAuthority member set are identical. A
member at another Site, an unselected extra controller, a disabled controller,
or a controller-count/HA mismatch fails at StackSpec, ResolvedPlan rebound, and
Fleet projection boundaries. In Modern Homelab the authority therefore remains
at the Home Site; a Cloud edge can be a worker/edge but cannot silently become
a second main.

Several independent mains are several `StackInstance` records in a `Fleet`,
never several mains inside one StackInstance. Fleet is a provider-free
inventory/lifecycle view over exact plan/spec hashes. Stack IDs and physical or
virtual `inventoryRef` identities are Fleet-unique, and its closed isolation
contract grants no implicit network, administrator, identity, secret, quorum,
or federation trust. Fleet membership creates no runtime or provider lifecycle
authority.

### Kit-specific workload runtime ownership

`runtime-paas` is only the shared workload-delivery interface. It never selects
an engine or makes Basement, Cloud, and Modern Homelab share one executor.

| Kit | Explicit runtime composition | Current executable truth |
| --- | --- | --- |
| Basement Kit | Required `stackkits-basement-compose-runtime` on Home Sites | Exact generation-only Compose executor contract; `stackkits-basement-compose-executor` remains unbound. The Docker socket proxy is an optional implementation helper and cannot force daemon inventory during planning. |
| Cloud Kit | Dedicated host-security, public-edge, offsite-backup, and optional private-admin-mesh runtimes on Cloud Sites | Each runtime has a distinct exact unbound owner. Public DNS, topology, and placement are declarative provider-neutral authorities. No generic Cloud runtime remains. |
| Modern Homelab | Concrete workload modules on Home Sites + explicit Cloud host-security/public-edge authorities + separate federation modules | There is no generic Modern Home executor: it could not safely apply a workload without that workload's exact artifacts. Modern reuses the shared non-executable `runtime-paas` interface; its kit-specific architecture is the explicit Home+Cloud composition and bounded federation graph. |

An unbound runtime is projected through `runtimeOwnerRequirement`, including an
exact owner ID, owned capability set, target scope, closed operation set,
Health ref, and evidence ref. It adds `runtime-owner-unbound` to Apply
readiness. A generated handoff therefore proves plan completeness only, never
execution. Generic kit-level workload owners are forbidden when they cannot
carry the exact workload artifact closure. In particular, Modern Homelab does
not add a second local runtime merely to differ from Basement; each concrete
Home workload module remains its own generation and later execution authority.

Modern's `Definition` in `modern-homelab/stackfile.cue` is its sole technical
architecture authority. The adjacent `stackkit.yaml` is metadata-only for the
registry and read-only migration inventory. It cannot declare services,
contexts, PaaS, placement, federation, identity, secrets, or execution. The
former Modern service/default/context schemas were removed rather than kept as
a shadow architecture.

Cloud Site existence is separately owned by `stackkits-cloud-topology` and
creates no runtime target. Optional failure-domain placement selects the
declarative `stackkits-cloud-placement-policy`; it likewise performs no host or
server-provider lifecycle operation. Neither contract is part of the Cloud
executor handoff.

Cloud host security is separately owned by
`stackkits-cloud-host-security-runtime`. Its generation contract contains only
the Cloud module targets and provider-free network/storage/failure-policy
projection. The isolated `stackkits-cloud-host-security-executor` adapter is
restricted to applying the host firewall, applying Internet-host hardening,
and verifying that exact security boundary on one pre-authorized node. It owns
no public edge, DNS provider, backup, mesh, workload runtime, credential, or
server lifecycle and remains outside product registration until authenticated
host operations exist.

Cloud public DNS is separately selected through
`stackkits-cloud-public-dns-contract`. It records that the Stack requires a
public DNS capability, but produces no module, provider mutation, credential,
runtime target, Health gate, or evidence claim. Provider-specific DNS
materialization belongs to the external TechStack/provider-management layer.

Cloud public edge is separately owned by
`stackkits-cloud-public-edge-runtime`. Its exact generation-only contract is
restricted to Cloud targets and the `public-edge` capability, depends on the
Cloud host-security boundary, and names the unbound
`stackkits-cloud-public-edge-executor`. It does not own DNS, certificate
issuance, credentials, host hardening, backup, mesh, or server lifecycle. The
residual Cloud runtime no longer participates in a default Cloud Kit.

Cloud offsite backup is independently owned by
`stackkits-cloud-offsite-backup-runtime`. The generated contract expresses
only the Kit's provider-neutral backup-target binding intent and names the
unbound `stackkits-cloud-offsite-backup-executor`. Object-storage provider and
bucket lifecycle, endpoints, accounts, credentials, retention execution, and
backup jobs remain outside this owner.

Optional Cloud private administration is independently owned by
`stackkits-cloud-private-admin-mesh-runtime`. The capability is selected only
through explicit Kit intent and produces the exact
`cloud/admin-mesh/executor-contract.json` handoff for the unbound
`stackkits-cloud-private-admin-mesh-executor`. It depends on the Cloud host
security and identity-policy boundaries but owns no transport technology,
endpoint discovery, credentials, identity issuance, server/provider lifecycle,
Modern federation, or general LAN reachability. With this split, the generic
`stackkits-cloud-runtime` provider, module, renderer, artifact, and dependency
have been removed.

Modern federation planning is also independent from its remaining runtime
handoff. `site-federation`, `service-publication`, `cross-site-placement`,
`data-residency`, and `split-horizon-naming` each resolve through a distinct
non-executable contract provider. These authorities describe the graph,
publication decision, placement policy, data policy, and naming intent; they
create no transport, process, DNS mutation, runtime target, Health gate, or
evidence claim. Identity and federation-policy manifests depend on their
direct Home policy authorities rather than a Federation runtime. The former
residual runtime is replaced by four concrete, separately unbound modules for
the inter-Site link, outbound control agent, cross-Site backup, and bridge
observability. Each names its own capability, closed operation set, Health ref,
evidence ref, and hash-bound generation-only executor contract. The common
renderer accepts only Modern's closed bridge, identity, data, failure, Site,
target, and capability projection; credentials, endpoints, provider lifecycle,
and runtime enforcement remain excluded. No executable adapter is claimed, so
Apply remains blocked without a generic umbrella.

Overlay and remote-control security are catalog authority, not StackSpec
authority. Modern intent supplies only `overlay.contractRef`, `trafficMode`,
`peerSiteRefs`, and an `actionAllowlist`. The compiler resolves the selected
`inter-site-link` and `outbound-control-agent` providers, then projects their
exact provider hash, owner module, implementation, transport, Home-owned issuer,
audience, maximum TTL, approval class, and replay/idempotency requirements.
Unknown contracts and actions, provider/module substitution, broad route
advertisement, and destructive actions without an approval receipt fail closed.
The resulting contract remains provider-neutral: it does not contain a VPS
provider, account, endpoint, credential, lease, or server lifecycle operation.

### Home backup-target ownership

`local-backup-target` is no longer part of the residual
`stackkits-local-runtime` handoff. Basement Kit requires the capability and
therefore selects the dedicated provider/module pair
`stackkits-home-backup-target`; Modern Homelab does not inherit it merely
because its topology contains a Home Site. This is a KitDefinition decision,
not a `context`, target, hostname, or node-count convention.

The module is placed only on declared Control Plane members at the Home Control
Authority Site and receives one exact node-local policy per selected member.
It runs after `stackkits-core-host-bootstrap`: Core creates the declared safe
storage roots, while the Home owner only observes that the exact `backupRoot`
exists as a non-symlink directory with mode `0750`. It cannot create storage,
run backup jobs, discover hosts, access a network, manage a server provider, or
choose an execution channel. Its adapter requires the caller's exact opaque
`(siteRef, nodeRef, executionChannelRef)` binding and returns a matching
per-node Health observation.

This first executable slice establishes the local backup target only. Backup
scheduling, repository initialization, retention, encryption keys, off-site
copies, restore orchestration, and backup success evidence remain separate
owners. A Modern Homelab may add an explicit cross-site backup owner later;
Home+Cloud topology alone never silently enables Basement backup semantics.

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
Apply and evidence claims; the policy renderer cannot manufacture them. Its CUE
contract therefore names `stackkits-local-autonomy-enforcer` as an explicitly
`unbound` future owner, together with the exact artifact, closed operation set,
Home control-authority scope, Health ref, and evidence ref that owner must later
satisfy.

### Kit-owned identity authority and verifier distribution

`human-identity-core` and `device-trust-core` remain shared capability
definitions, but no shared Core runtime realizes them. Architecture
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

Home enrollment is not granted merely by being on a private subnet. The closed
contract requires LAN reachability together with owner step-up, local pairing or
console proof, a device-generated key, proof-of-possession, rotation/revocation,
and bounded credential and session lifetimes. Human, device, and workload
issuers remain separate. The resulting enrolled device may support a
low-friction session, but it never removes step-up from a privileged operation.

The five emitted identity policy artifacts are generation-only. They explicitly
state that runtime enforcement and credential issuance are unverified and that
credential material, JWKS bytes, private keys, endpoints, and transport
realization are not included. Each identity policy module has an exact CUE
`enforcementRequirement` with status `unbound`: Home device authority, Basement
trust verification, Cloud trust/Cloud-owned human and workload issuance, and
Modern Home-to-Cloud verifier distribution all name different future owners,
target scopes, closed operations, Health refs, and evidence refs. Apply carries
`policy-enforcement-owner-unbound` until those owners exist; the requirement is
not itself a runtime target or success claim. HA remains an add-on and must
preserve the chosen kit's trust graph.
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
both the generic `module-apply-support-missing` blocker and the exact
`policy-enforcement-owner-unbound` blocker for
`stackkits-home-access-enforcer`, its Health ref, and its evidence ref until a
separate executor implements those mechanisms.

Every routable v2 service endpoint also declares one catalog-owned
`requiredPrivilege`. Route intent and Modern publication intent must preserve it
exactly. The `admin`, `identity`, `secrets`, `vault`, and `recovery` classes
always compile to human-plus-enrolled-device authentication with owner step-up;
a caller cannot relabel those surfaces as ordinary `user` access. This is a
contract and admission guarantee. Replay prevention, revocation enforcement,
session issuance, and the live access decision remain unclaimed until the exact
typed enforcer consumes the artifact and returns fresh bound Health/evidence.

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

### Logical workload selection

Application choice is not a KitProfile capability and is not a caller-selected
module. Each KitDefinition declares which logical workloads are required,
defaulted, optional, or forbidden. StackSpec may select only the logical
workload ID, one catalog-owned alternative, typed placement, declared public
settings, and secret references. Provider, module, runtime, route, health, and
setup implementation identity remain internal catalog authority.

Every module has one closed role: `foundation`, `platform`, `workload`, or
`operations`. A workload alternative must bind exactly one `workload` module
owned by its catalog adapter, an allowed runtime kind and delivery, one service
endpoint and health contract, compatible Site kinds and data classes, and only
inputs declared by that module. The initial contract is `photos -> immich`.
Its setup remains manual and operator-owned until a separate native-v2 setup
action catalog exists; legacy setup action names are not treated as authority.

This is currently an authority checkpoint: catalog decoding, generated bundles,
and distribution hashes include the workload contract, but compiler selection
and ResolvedPlan placement are the next cutover slice. Until that lands, the
transitional `photos` capability path is not a second supported long-term API
and does not grant an Apply-readiness claim.

### Service endpoints and Modern publication backends

A route or Modern publication cannot invent an upstream from a module name and
port. Routable render units declare catalog-owned service endpoints with service
identity, allowed ingress protocols/exposures, upstream protocol and target port,
origin selector, data-class/locality requirements, and health contract. The
compiler resolves that endpoint to exact Sites, nodes, and render instances.
Each ordinary service route now references a compiler-owned backend pool whose
identity is derived from the complete selected membership. The pool persists the
catalog module/unit, selector, upstream protocol, target port, and only logical
`siteRef`/`nodeRef`/`instanceRef` members. Current renderers receive only the
closed `authority-bound-service-route-list-v4` projection. It includes the exact
capability ID and route-relative `access`, `transport`, `edge`, or `egress` role,
plus safe TLS profile/issuer IDs or the external-custody owner. Provider/module
realization metadata, addresses, daemon or socket bindings, TLS credentials, and
observed health evidence are excluded.
Changing, omitting, or relabelling a backend member rotates or invalidates the
pool identity and is rejected even when an altered plan is rehashed.

Each ordinary route also owns a compiler-derived health gate bound to that exact
route, backend pool, source module health contract, upstream protocol/port, and
every selected backend member. A matching HTTP or TCP source contract becomes a
closed probe descriptor; unsupported or mismatched contracts remain resolvable
as `contract-only` and never imply execution. The older unbound v2/v3 route input
types are no longer accepted by the CUE, compiler, API, or renderer boundary.
The current v4 projection always carries the narrow `healthProbe` and reachability
authority together, so an external egress cannot be relabelled as a Cloud edge
and an edge cannot acquire external Home custody. Internal gate identities,
provider/address/credential data, and observed status remain unreachable. Apply
is fail-closed through `route-health-executor-unbound` until a real executor is
bound, with `health-gate-not-executable` added for contract-only descriptors.

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

Public WebPKI policy is already a separate catalog-owned capability/provider/
module chain. Its generation-only executor contract contains the exact TLS
profile, ACME issuer policy, renewal health reference, sensitivity-typed logical
material slots, Cloud targets, and matching public HTTPS routes. It performs no
certificate issuance or network action and carries no credential, DNS provider,
server-provider, or management authority. Issuer materialization stays
`contract-only`, and Apply stays blocked independently from generation.

Modern publication health execution, origin mTLS, and one-way
identity-verifier distribution remain explicit graduation gates. Until those
implementations exist, every Modern publication contributes the independent
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

### Typed resolved-plan field bindings

Concrete render units can consume a small resolved-plan field without receiving
the raw StackSpec or an entire authority subtree. A catalog-owned `inputBindings`
entry binds one declared public input to one member of a closed source enum and
records its exact value type, cardinality, required flag, and typed default.
Bindings are resolved only after identity and network planning, copied unchanged
to every explicit render instance, included in module/catalog/plan authority
hashes, and reconstructed during persisted-plan verification. Module defaults and
StackSpec settings cannot override a bound target.

The first governed sources are `identity.deviceEnrollment` and `network.routes`.
They do not expose those objects verbatim: enrollment uses a public policy shape
whose lifetime key cannot alias the secret namespace, while routes use an
explicit projection that excludes TLS credentials, TLS provider authority, and
undeclared access fields. Arbitrary JSONPath, module/result-derived sources,
kit/context conditionals, secret targets, undeclared targets, type/cardinality
coercion, and missing required sources fail closed in CUE, the compiler, the
persisted-plan verifier, and the renderer parser. Existing coarse `planInputs`
remain a compatibility surface while concrete modules migrate field by field;
they are not permission to add new whole-plan projections.

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

All first-party native v2 intent persistence goes through one held-workspace
CAS authority. The candidate and any current document are independently
normalized by the same embedded CUE product authority. Missing intent is
created atomically with no-replace semantics; replacement requires the exact
current normalized `spec_hash`; stale writers, v1 targets, links, traversal,
and lock contention fail before target mutation. CLI init and MCP config
authoring share this implementation, including idempotent already-applied
retries. `--force` remains an exact-v0.6 compatibility flag and cannot reopen a
blind writer on development or v0.7+ builds. If only the accepted
`kombination.yaml` alias exists, native authoring updates that same authority
instead of creating a competing `stack-spec.yaml`.

The CLI generation boundary is executable for a generation-ready plan whose
exact renderer contracts are present in the product registry: it creates a
fresh `CurrentResolution`, exact-matches the canonical plan persisted beneath
the plan-owned output root, authorizes that one resolution, builds the exact
product renderer registry, and holds the
authorization plus workspace handle across `RenderAndInstall`. Renderer output,
manifest, receipt, and closed-tree replacement commit as one managed
transaction; cancellation reaches the renderer, and close failures remain
visible. Architecture v2 rejects legacy `--force` and `--fragments` semantics
and exact-binds an explicit `--output`. Native-v2 Plan performs the same current
resolution, plan authorization, manifest, receipt, and generated-byte checks as
the generation execution gate, then returns a deterministic read-only
inspection. The inspection includes the exact authority binding, renderer,
output root, generation and Apply readiness, Apply blockers, manifest hash, and
artifact hashes. It always reports that no executor was invoked and that an
infrastructure diff is unavailable; v0.6-only `--out` and `--destroy` semantics
fail closed on v2. Verify retains its separate typed verifier boundary. A kit's
generation readiness remains derived solely from its concrete catalog-owned
module and renderer contracts; the inspection cannot promote readiness or
remove blockers.

Generate, Apply, and Verify share one non-blocking cross-process lock per held
workspace identity and canonical `outputRoot`; a competing mutating command
fails immediately with `output_transaction_busy`. Plan creates no lock or
workspace state. It rejects a pending generation transaction and verifies the
complete closed artifact tree; a concurrent atomic generation swap can make
that verification fail closed but cannot authorize or report unverified bytes.
Generate additionally records each stage/backup/install/rollback/cleanup
boundary as immutable canonical `0600` journal data under
`.stackkits-control`, outside the swappable output tree. The
journal binds the transaction-owned stage, backup, and failed-output names to
the exact plan, manifest, and receipt digests. File contents are synced before
publication and directory metadata is synced on platforms/filesystems that
support it; an unsupported directory-sync primitive is reported as unsupported
rather than claimed as durable. A surviving, partial, forged, or contradictory
journal fails only that output root closed with
`output_transaction_recovery_required`; StackKits never guesses which tree to
delete or restore. A valid pending journal blocks only its governed output;
malformed control authority fails the workspace closed because it cannot be
safely attributed. Recovery classification is deterministic, but automated
recovery execution remains a separately governed operator path.

Native v2 Apply continues from that same held transaction and output lock into
the service-owned product executor registry. CLI and API callers can provide
only current-resolution context, the already-held filesystem capabilities,
component versions, and the canonical evidence bytes. They cannot select an
adapter, executor identity, capability set, producer trust root, or result path.
The verified result is rehashed and stored idempotently by content hash under
the plan-owned `.stackkit/apply-results/` directory.

Apply-evidence public trust is operational authority and therefore does not
belong in CUE, StackSpec, ResolvedPlan, generated output, or provider config.
Only the product service reads the fixed `stackkit/apply-producers.json` file
beneath the OS per-user config directory. The canonical document contains an
exact Ed25519 producer identity/public key and sorted allowed requirement kinds;
the service intersects that scope with the verified plan to derive exact receipt
IDs. Private signing material is never accepted or stored by this contract.
Missing trust config means empty trust and fails closed. This trust-store seam
does not itself graduate a signer or make a complete Kit Apply-ready.

Module runtime authority is explicit in CUE. `runtime.execution: executable`
means that concrete render instances may become Apply runtime targets;
`contract-handoff` means the renderer produces only a governed transition or
policy artifact. Handoff artifacts remain in the generation manifest, closed
artifact tree, plan-owned Apply requirements, and final StackKits artifact-set
hash, but they produce no runtime, health, or runtime-evidence requirement and
are never passed to the shared executor. An `apply-ready` module cannot be a
handoff, and handoffs cannot carry secret inputs, an engine, or an image. This
prevents a locality-free JSON document from being mistaken for permission to
mutate one or more hosts.

Policy enforcement is also kit-explicit. The following refs describe owners
that still have to be implemented; `status: unbound` is intentionally a blocker,
not a service-registration mechanism:

| Policy module | Selected architecture | Target scope | Unbound enforcement owner | Closed responsibility |
| --- | --- | --- | --- | --- |
| `stackkits-home-device-authority-policy-manifest` | Basement, Modern | Home control authority | `stackkits-home-device-authority-enforcer` | Device enrollment, credential issue, credential revocation |
| `stackkits-basement-identity-trust-policy-manifest` | Basement | Home control authority | `stackkits-basement-identity-trust-enforcer` | Device, human, and workload verification under Basement trust |
| `stackkits-cloud-identity-trust-policy-manifest` | Cloud | Cloud Sites | `stackkits-cloud-identity-trust-enforcer` | Cloud human/workload issue plus device, human, and workload verification; never device enrollment/issue |
| `stackkits-modern-identity-trust-policy-manifest` | Modern | Federated Home+Cloud Sites | `stackkits-modern-identity-trust-enforcer` | Home/Cloud verification and one-way verification-key/revocation distribution |
| `stackkits-home-access-policy-manifest` | Basement, Modern | Home Sites | `stackkits-home-access-enforcer` | LAN/local ingress decisions and privileged step-up; LAN presence is not identity |
| `stackkits-local-autonomy-policy-manifest` | Basement, Modern | Home control authority | `stackkits-local-autonomy-enforcer` | Link-loss policy, forbidden cross-Site denial, and preserved local control |

Every row is bound to its exact generated policy artifact set, required Health
ref, and required evidence ref in CUE. A future implementation must replace the
unbound requirement with a typed executable owner and fresh evidence in the same
change; deleting the blocker or attaching a generic no-op adapter is invalid.

#### Basement native-Apply graduation map

The current Basement plan deliberately stays Apply-blocked beyond the isolated
Security Baseline pilot. Its remaining generated documents are contracts for
future enforcement owners, not evidence that enforcement happened:

| Module | Current truth | Required independent runtime owner |
| --- | --- | --- |
| `security-baseline` | Exact node-local host adapter exists as the first bounded pilot | Retain the script/contract-bound host adapter and product evidence path |
| `stackkits-core-host-bootstrap` | Exact node-local, provider-free pilot prepares only declared local StackKit storage roots and observes an already bootstrapped Docker runtime | Bind the adapter to an authenticated execution channel before product registration; expand only through new typed operations and evidence |
| `stackkits-core-topology` | Declarative shared Home/Cloud site-topology authority; it selects no runtime module and performs no host or provider lifecycle work | Keep site/node intent in the resolved plan and lower only explicit downstream owners |
| `stackkits-service-catalog` | Declarative catalog authority selected directly into the plan; it has no module or runtime target | Keep workload/service selection in CUE and let explicit runtime owners consume the resolved catalog |
| `stackkits-access-policy-contract` | Shared declarative access-policy prerequisite; it does not enforce Home or Cloud access | Bind kit-specific enforcers to the exact resolved policy and their own Health/evidence |
| `stackkits-storage-data-policy` | Shared declarative storage/data intent; it performs no mount, migration, backup, or retention operation | Let bounded storage and workload owners consume the resolved policy explicitly |
| `stackkits-workload-runtime-contract` | Shared delivery interface required by workloads; it selects no engine and is not a Basement, Cloud, or Modern runtime | Let each concrete workload module bind its exact artifacts to an explicitly registered runtime adapter; do not recreate a kit-level runtime umbrella |
| `stackkits-immich-runtime` | Concrete generation-ready workload owner pinned to the full Immich v2.7.0 server, machine-learning, PostgreSQL, database-init, and Valkey graph. Its provider-neutral, target-bound bundle carries immutable image digests, dependencies, internal network membership, opaque secret slots, persistent/cache volumes, backup intent, and Health declarations. The shared runtime-executor SPI and isolated Immich selected-PaaS adapter now preserve the paired entry-image ref/digest, require an exact authenticated target channel, accept only this closed bundle, and require apply receipt plus full component/route readback. They never receive provider lifecycle, credentials, daemon sockets, or general host authority. | Configure a real authenticated selected-PaaS operations implementation, register only the exact catalog hashes/artifact contract, and produce fresh trusted evidence before product Apply graduation; provider/PaaS lifecycle remains TechStack authority. |
| `stackkits-basement-compose-runtime` | Required Basement-only generation contract with exact unbound runtime owner; no daemon inventory is needed to plan it | Implement the typed Compose executor and bind the socket-proxy helper only to an exact observed Docker daemon |
| `stackkits-secrets-recovery-contract`, `stackkits-backup-core-contract`, `stackkits-observability-evidence-contract`, `stackkits-lifecycle-update-contract` | Four distinct non-executable shared contracts; none creates a runtime target, artifact, Health claim, evidence claim, or host operation | Bind kit-specific recovery, backup, telemetry, and update owners independently without recreating a Core executor umbrella |
| `stackkits-home-backup-target` | Exact node-local Home Control Plane adapter observes the CUE-declared prepared backup root | Retain the observation-only boundary; add backup jobs, repository lifecycle, retention, and restore as separate typed owners |
| `stackkits-home-device-authority-policy-manifest` | Policy JSON plus exact unbound-owner requirement | Implement `stackkits-home-device-authority-enforcer` with local pairing, possession proof, revocation, and fresh Health/evidence |
| `stackkits-basement-identity-trust-policy-manifest` | Policy JSON plus exact unbound-owner requirement | Implement `stackkits-basement-identity-trust-enforcer`; no credential material enters generated output |
| `stackkits-home-access-policy-manifest` | Policy JSON plus exact unbound-owner requirement | Implement `stackkits-home-access-enforcer`; LAN presence never becomes identity |
| `stackkits-local-autonomy-policy-manifest` | Policy JSON plus exact unbound-owner requirement | Implement `stackkits-local-autonomy-enforcer` with observable link-loss behavior |

These map to three implementation classes, not seven bespoke CLI branches:
Core host execution is limited to the separately selected Security Baseline and
Host Bootstrap modules. Home-local host execution and explicit policy enforcement
remain separate.
A generic renderer or no-op policy adapter must never promote generation-only
output to Apply-ready. The corresponding execution tasks are Beads
`.8.9`, `.8.10`, and `.8.11`; evidence producer/trust graduation remains `.8.8`.

The former generation-only `stackkits-local-runtime` and
`stackkits-home-extensions` umbrella are no longer selectable. Optional Home
capabilities resolve independently: LAN DNS is contract-only, while private
remote access, public publication egress, and encrypted offsite backup have
separate generation contracts and must graduate through separate typed owners.

The Core host-bootstrap and Home backup-target pilots are intentionally not
registered by the product Apply path yet. Their constructors require one
explicit `(siteRef, nodeRef)` local binding and reject every other target before
a host operation. The neutral
shared runtime target now also carries the exact opaque `executionChannelRef`
from the matching plan-owned Host requirement when one exists; this value is
request-digest-bound and the local adapter requires an exact channel match. No
address, endpoint, credential, provider reference, or discovery input crosses
that bridge. The adapter has no
generic command, package-manager, network, provider, credential, or arbitrary
file-write capability. Multi-node product execution still requires transport
registration around the isolated dispatcher described below; neither target
order, hostname, LAN discovery, nor “current machine” may select a channel.
Until authenticated transports and their product policy are registered, a
complete multi-node Kit remains fail-closed even though each planned node has
its own rendered policy.

Node-local execution also owns node-local health. CUE health contracts marked
`scope: each-node` materialize one distinct, contract-hash-bound gate for every
selected node, retaining the exact Site/node pair. Security Baseline, Core host
bootstrap, and the Home backup target use this scope. Aggregate
application/module health remains the default and is unchanged. This prevents
a dispatcher from assigning one shared
module gate to an arbitrary worker or fabricating one aggregate success from
partial per-node results; every node-local runtime target has exactly one
independently receipted health target on the same Site and node.

`internal/runtimeexecutordispatch` is the transport-neutral composite boundary
for that model. It accepts only a sealed parent request, groups exact
single-node Runtime/Health pairs by their opaque channel, filters each child to
its referenced immutable artifacts plus plan metadata and exact Home access
binding subset, re-seals the child request under the child executor identity,
retains the parent's once-captured authorization instant, and verifies every
child result via the shared runtime contract before returning the complete
parent outcome.
Missing/unknown channels, aggregate health, cross-Site bindings, ambiguous
owners, child identity panics, partial authority sets, and cross-node artifacts
fail closed. The dispatcher is not yet in the product registry and contains no
transport implementation or credentials. Execution is intentionally serial in
this first contract slice; distributed rollback/idempotency after a later
channel fails is not claimed and must be solved before product registration.

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
| API server | `cmd/stackkit-server`, `internal/api` | HTTP surface for catalog, canonical `stackfile.cue` schemas, versioned validation, logs, capabilities, and OpenAPI. Legacy generation/setup/registry operations are exact-v0.6 compatibility surfaces and are absent from native-v0.7 capability discovery. |
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
7. On exact v0.6, first-run setup is represented separately from deployment as setup-drop metadata and the Node Hub may mutate its legacy TinyAuth, credential, and setup-run artifacts. Native v0.7 exposes none of these operations: it returns a typed unavailable response before artifact reads, credentials, external calls, or state writes until a CUE-governed v2 setup contract exists.
8. `stackkit verify` performs read-only host checks and optional HTTP URL checks.
9. `stackkit-server` exposes catalog, canonical schema, versioned validation, logs, and capability discovery over HTTP. Its Direct Connect map is exact-v0.6 in-process compatibility state, not a central Kombify, Cloudflare, or TechStack registry; native v0.7 rejects those endpoints before decode or mutation.

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
- exact-v0.6-only Node Hub setup and in-process Direct Connect registry operations; native v0.7 does not advertise them and returns a typed unavailable response

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
