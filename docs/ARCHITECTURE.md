# Architecture — kombify StackKits

> Last verified: 2026-07-29

This is the current implementation overview for this repo. Normative product and module rules are summarized here and in accepted ADRs.

## System Role

### Non-negotiable product surfaces

- Installers and the `stackkit` CLI invoke the shared StackKits rollout core
  directly. Standard Mode needs no hosted orchestrator; Advanced Mode may add
  registered external capabilities and orchestration around the same core.
- The StackKits State Console is the MCP App for local state review, plan
  inspection, Owner evidence, and operation-approval requests. It invokes only
  registered MCP operations and owns no lifecycle implementation.
- The Easy/Techie Wizard, Finder, operator-capability scoring, recommendation,
  guidance, and intent drafts belong exclusively to Techstack.

Standard Mode requires none of Techstack, Kombify Cloud, an account, provider
credentials, or hosted Kombify state. Advanced Mode is the sole sanctioned
Techstack dependency and still leaves final validation and execution with the
pinned StackKits CLI.

Every deployment managed by kombify Techstack runs in Advanced Mode, from its
first rollout onward (ADR-0031 amendment 2026-09-24). Standard Mode is the
standalone CLI/installer path, a one-shot configuration without Terramate.
Techstack does not roll out or operate a StackKit in Standard Mode.

StackKits turns CUE-defined infrastructure contracts into deployable homelab environments:

```text
operator intent / optional Techstack-approved intent
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
generated Compose / OpenTofu / metadata
        |
        v
stackkit apply + stackkit verify
```

CUE is the technical contract source of truth. Lifecycle state and evidence
remain bound to the executing Stack, whether custody is local or supplied by an
Advanced-mode owner. Remote databases may mirror registry or fleet state, but
they do not replace live CUE contracts or gate Standard Mode.

### Shared rollout core and orchestration boundary

[ADR-0031](ADR/ADR-0031-stackkits-standalone-lifecycle-boundary.md) defines the
product boundary: StackKits provides a CLI-manageable Homelab rollout core.
Standard Mode can execute it directly; Advanced Mode supplies additional
registered authority and orchestration. CUE, StackSpec, immutable ResolvedPlan,
lifecycle state, Owner custody, and evidence remain bound to the same rollout.
Attested GitHub Releases are the account-free distribution source.

For v0.9, Techstack is the Orchestrator UI, Config Unifier, and Advanced
Day-2/RIL control plane. Techstack Core sends only a closed `StackKitCommand`
over its own outbound/reverse mTLS gRPC worker channel to the node-side
Techstack Agent. The Agent revalidates the exact release pin, index, receipt,
and executable; scrubs Techstack/Kombify control environment; and starts the
exact published CLI locally. Results return only as bounded
`stackkit.command-result/v1` plus `stackkit.rollout-event/v1` JSONL. StackKits
has no gRPC server and owns no Techstack worker enrollment, certificates, or
transport.

This paragraph specifies the v0.9 target boundary. Slice 1 does not claim the
execution channel is live; executable Agent admission is Slice 2.

The Config Unifier emits a review proposal, not a ResolvedPlan. The local Owner
approves it and the pinned CLI performs final CUE validation and generation.
Advanced Terramate change sets, Advanced reconcile, coordinated rollback, and
restore drills require a short-lived offline-valid capability before rendering
or side effects. Standard CLI apply, verify, upgrade, backup, restore, and
read-only drift detection require neither Techstack nor that capability. When
Techstack manages the deployment, these operations run in Advanced Mode.

The Owner is local: PocketID holds the human directory, TinyAuth is the local
protected-service login and policy broker, and StackKits binds the PocketID
subject to `ownerRef` plus the local Ed25519 key and its step-ca certificate.
kombify Cloud may offer only a signed, time-bounded desired identity projection
for explicit local approval. Outage, unlink, expiry, staleness, or failed
verification withdraws future Cloud mutation authority without deleting or
disabling an already approved local identity. Passwords, passkeys, sessions,
private keys, CA material, and recovery secrets never cross this boundary.

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

### Target-bound LAN DNS listeners

LAN DNS declares `bindAddressSource: "node-site"` in the CUE module contract.
The compiler resolves it to `inventory.nodes.<nodeRef>.siteAddress` before
hashing the plan. The listener projection, generated Compose and admission
use that same concrete target IP. Local attestation reuses the kernel route
observation without sending traffic; external inventories supply the target
address. Missing, loopback, wildcard and non-unicast addresses fail closed when
this binding is required. The optional field does not reinterpret old plans.
Binding LAN DNS to the target interface preserves the host's loopback resolver;
StackKits does not stop or reconfigure it.

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
`external-home-access-binding-missing` to Apply readiness. Backup authority is
deliberately excluded from this access seam and uses separate Home and Cloud
target handshakes. Their infrastructure materialization belongs to the external
TechStack/provider-management layer.

Every native `StackSpecV2` also normalizes one closed
`stackkit.backup-policy/v1` intent into `ResolvedPlan.backupPolicy`. It contains
only a structured UTC cadence, bounded retention counts, a unique non-empty set
of governed data classes, and mandatory restore-verification cadence/evidence
freshness. Arbitrary cron, provider/repository/endpoint/credential selection,
paths, commands, lifecycle handles, and secret material are outside the
contract. The policy participates in spec and plan hashing and is available
only through the finite `backupPolicy` module-plan input. Defaults are resolved
before modules consume that input. The standalone core and the explicitly
selected Coolify core project the retention
counts into the same local Kopia source policy. The native runtime sets and
reads back all Kopia retention buckets explicitly, including zero hourly/latest
buckets and the `keepYearly` to `keepAnnual` mapping. Kopia stays manual-only:
retention runs when the authorized snapshot operation runs, after its existing
writer-quiescence boundary. A declared schedule or restore cadence alone still
does not establish an executing scheduler or successful restore drill.

Upgrade and restore-activation safety snapshots request recovery protection
before creation. Kopia stores the pin in the initial manifest; the resulting
snapshot ID, protection intent and receipt are bound into the signed lifecycle
anchor. Later pin/unpin operations are not used: Kopia 0.18.2 replaces the
manifest ID when its pins change. Protected anchors therefore remain retained;
ordinary snapshots follow the configured retention. Historical fieldless
policies and anchors retain their original encoding and are not retroactively
rewritten or claimed to be protected. Receipt history reports past events,
not continued availability of expired repository content.

Before creating another retention-bearing snapshot, the local lifecycle reads
the existing restore journals and authenticates their recovery/source anchors.
An unfinished restore of an unprotected source blocks snapshot creation, even
after its approval expires or a policy lineage changes. A later authorized and
successfully verified restore of that same source may supersede the pending
attempt; an older result, a future timestamp or a damaged journal cannot.
Completed history is verified independently of the current source topology.
An incompatible active restore remains blocked until an explicit owner-owned
terminal decision; this guard never deletes journals or changes repository pins.

Every native spec also normalizes a closed `stackkit.drift-policy/v1` into
`ResolvedPlan.driftPolicy`. The policy carries only an enabled bit, structured
UTC observation cadence, a finite subject set, an evidence-freshness
requirement, and a fixed report-only response whose reconciliation always
requires approval. Arbitrary cron, observer endpoints, credentials, provider
or runtime lifecycle, commands, read channels, and automatic reconciliation
are excluded. It is hash-bound and available only through the finite
`driftPolicy` module-plan input. No current module gains observation or
reconciliation authority from this immutable policy; legacy `DriftDetection`
remains open until a concrete observer binds the policy to Health/evidence and
an authenticated read channel.

Home encrypted offsite backup uses its own
`HomeBackupTargetRequirement` -> `ExternalHomeBackupTargetBinding` handshake.
The compiler binds the exact Basement StackInstance, Home Site/node,
`encrypted-offsite-backup` capability/contract, spec, and policy. Home-side
encryption is mandatory before egress, plaintext egress is forbidden, target
and credential custody remain external, and restore verification is required.
The maximum-24-hour binding carries only opaque target and custody-attestation
references plus exact Candidate/version/hash validity. Provider, account,
region, bucket, repository, endpoint, credential, resource, lease, lifecycle,
transport, address, discovery, and general-LAN details are structurally
excluded. Without the binding generation remains usable and Apply reports
`external-home-backup-target-binding-missing`; no backup executor is claimed.

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

Each selected HA realization is executable only through the shared
member-local Product Runtime Owner. CUE selects one of six exact
`{kit, warm-standby|quorum}` provider/module pairs and emits one immutable
artifact plus one module Health gate per control-plane member. The owner binds
that artifact to the sealed plan/request hashes and the exact
Site/node/execution-channel tuple, then requires apply, obsolete-state removal,
and verify readbacks for the complete member/failure-domain/fencing set.
Provider APIs, credentials, endpoints, transport selection, general LAN
authority, and independent failover authority are not part of this boundary.
Modern additionally rejects WAN quorum and any Cloud promotion; its member set
remains entirely inside the Home ControlAuthority Site.

Several independent mains are several `StackInstance` records in a `Fleet`,
never several mains inside one StackInstance. Fleet is a provider-free
inventory/lifecycle view over exact plan/spec hashes. Stack IDs and physical or
virtual `inventoryRef` identities are Fleet-unique, and its closed isolation
contract grants no implicit network, administrator, identity, secret, quorum,
or federation trust. Fleet membership creates no runtime or provider lifecycle
authority.

Every compiled plan therefore carries one closed
`stackkit.fleet-lifecycle/v1` projection. It binds current membership,
ControlAuthority, and the `add`, `replace`, `drain`, `recover`, and `remove`
operation contracts to the exact Stack, optional Fleet, specification,
inventory, and plan hashes. Each mutation requires a separately compiled target
plan, local Owner approval, resumable checkpoints, explicit recovery, and
immutable local evidence. The projection grants neither provider lifecycle nor
multi-server orchestration or credential custody; an external orchestrator may
prepare infrastructure, but StackKits only validates and executes the bounded
local member transition.

`internal/fleetlifecycle.Service` is the single execution seam for those five
contracts. It admits only the exact current/target membership delta, holds the
local state lock while checkpoint and recovery authority are prepared, and
selects every next phase from the compiled contract. State lives at
`.stackkit/lifecycle/fleet/state.json`; each completed phase is a separate
content-addressed, Owner-signed immutable document below
`.stackkit/lifecycle/fleet/evidence/`. A failed phase becomes
`recovery-required` without manufacturing later evidence, and can continue only
through explicit Owner-approved resume or recovery. `stackkit status` returns
the same contract, state, and current-plan match signal consumed by its MCP
adapter and the State Console; neither surface implements a second lifecycle.

#### Member join (Modern Cloud edge)

A Modern plan always contains its Cloud edge node, so joining that host is not
a membership delta and does not run Fleet `add`: it binds a physical host to a
member the current plan already declares. `add` stays the path for a node the
current plan does not contain. On the Foundation Node, `stackkit fleet
admit-member --node <node>` compiles the current plan from the persisted
StackSpec and Inventory and issues one Owner-signed
`stackkit.member-admission/v1` document (maximum 24-hour join window). It binds
the StackInstance, plan, spec, and inventory hashes, the compiler version, the
Home authority tuple, the member Site/node/execution-channel tuple, the member
roles, the Home owner verification key, and the plan's Home-to-member verifier
distributions, and carries the exact StackSpec and Inventory bytes. Issuance
requires the owner binding to be a Home ControlAuthority member and the member
to be an enabled non-controller at a Cloud Site; each has exactly one Inventory
`executionChannels` entry, and every verifier distribution to the member Site
must exclude signing, enrollment, private-key, and credential material.

On the Cloud host, `stackkit fleet join <admission> --home-key-id <keyId>`
verifies the signature against the key ID compared out of band, recompiles the
plan from the admitted bytes, and requires every admitted fact including the
plan hash to match. It persists the StackSpec, the Inventory, and
`stackkit.local-member-custody/v1` at `.stackkit/custody/member.json`. Member
custody holds no private key and closes enrollment, signing, credential
issuance, and ControlAuthority as `false`; its integrity is the embedded
Owner-signed admission. A workspace holds either owner custody or member
custody. On a member, every owner-custody operation (Owner signing, owner
init, Owner key certification) is denied with reason
`member_custody_verify_only`. A member never merges locally observed facts
into the shared Inventory. Both hosts therefore compute the same plan hash
and the same `stackkit.terramate-stack-graph/v1`, whose host projects carry
each node's execution channel.

#### Member-local execution (Modern Cloud edge)

Member evidence key (integrator decision 2026-09-25, reversible). ADR-0029
keeps enrollment and identity signing at Home, so a member cannot sign Apply
evidence with an Owner key. Instead, `fleet join` also generates a member
evidence key in member custody (`.stackkit/custody/member-evidence-key.json`,
capability `evidence-attestation`) and writes one
`stackkit.member-evidence-key-request/v1` file with a proof of possession.
The Foundation Node runs `stackkit fleet certify-member-key <request>
[--output <file>] [--valid-for 720h]`: it recompiles the current plan,
requires the request tuple to be the member the plan admits under the join
rules and the member plan hash to equal the Home plan hash, and returns one
Owner-signed `stackkit.member-evidence-key/v1` certificate (dedicated
signature domain, at most 90 days). The certificate binds the StackInstance,
plan hash, admission digest, Home and member Site/node/channel tuples, the
member key, and closed grants: no enrollment, identity signing, credential
issuance, ControlAuthority, or Owner authority. The Foundation Node records it
under `.stackkit/fleet/member-evidence-keys/`. On the member, `stackkit fleet
import-member-key <certificate>` verifies it against the Home key pinned at
join and its own key. `fleetmember.VerifyEvidenceKeyCertificate` is the only
acceptance rule for member evidence: exact tuple, StackInstance, plan, and
admission, inside the validity window. The relay is one file each way after
the admission.

Host-scoped Apply. `generationartifact.VerifiedPlan.WithExecutionScope`
projects the Apply requirements of a multi-host plan onto one host: its
runtime targets, the health gates the runtime registry assigns to them, the
local host fact only, and the secrets and workloads those targets own. The
generated artifact set is unchanged; only the executable artifacts of the
scoped targets cross to the executor. The scope is part of the requirements
hash, so evidence collection, authorization, execution, recovery, and result
verification all bind to it. On a member, `stackkit apply` executes exactly
the targets on its own tuple whose owner is not a remote-only process owner,
admits them through its own execution channel, and signs the host evidence
and a `stackkit.member-apply-result-receipt/v1` with the certified key; a
verifier accepts that evidence only under a valid certificate for the
member's tuple. Apply and Verify on a member require the admitted plan hash
and the shared Inventory the Home admitted (explicit `--inventory` or the
admitted `.stackkit/inventory.yaml`); a changed plan needs a new admission and
certificate. Once the Foundation Node holds a certificate record, its Apply
executes its own tuple plus the remote-only process owners (federation control
agent, bridge publication, and the other Techstack-bound owners) of every
Site through their execution channels, and no longer runs a certified
member's local owners in-process. A target that belongs to no certified member
and not to the Foundation Node fails closed. Both hosts report the other
host's targets as `out_of_scope` in the Apply outcome ledger and name the
scope in `executionScope` of `stackkit.apply-result/v2`. `stackkit verify` on
a member verifies its own scoped result and reports the member evidence key.
A single-host StackInstance and a Foundation Node without certified members
are unchanged.

Not covered yet: member change sets (Advanced admission needs Owner custody
and the Owner-approved local trust bundle on the member, and change-set
records are Owner-signed), cross-host ordering of the two hosts' Apply runs
(Techstack), key rotation or revocation before expiry, and runtime evidence.

### Kit-specific workload runtime ownership

`runtime-paas` is only the shared workload-delivery interface. It never selects
an engine or makes Basement, Cloud, and Modern Homelab share one executor.

| Kit | Explicit runtime composition | Current executable truth |
| --- | --- | --- |
| Basement Kit | Concrete workload/runtime modules on Home Sites; optional explicit `basement-compose-runtime` pilot | The Kit identity does not select a generic Compose owner. Explicit pilot intent may generate the handoff; the socket-proxy Product factory remains a separate daemon-bound helper and requires an authenticated Docker Operations owner before it can enter a runtime composition. |
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

Runtime-adapter selection is part of the Workload alternative, not the Kit or
the shared `runtime-paas` capability. An alternative declares its closed
`allowedAdapterRefs` and one governed default; StackSpec may override only with
one of those refs. Resolution then binds the unique adapter-owning provider and
module plus both versions and canonical contract hashes. Adapter providers use
the separate `runtimeAdapterRefs` namespace and may expose zero Kit
capabilities. Under [ADR-0042](ADR/ADR-0042-standalone-default-and-optional-platforms.md),
`standalone-compose` is the governed default for new application selections;
Coolify and Komodo require explicit selection. Existing persisted adapter
identities retain their authority. The umbrella delivery class is
`application-adapter`, because the same workload contract may be executed by a
PaaS or by the StackKits-owned no-PaaS Compose adapter. All three adapters are
absent from plans without a workload bound to that exact adapter.

When StackSpec declares routes for a selected Application workload, the
compiler selects exactly one delivery route. When the same service has local,
remote-private, and public routes, it selects the most externally exposed
route (`public`, then `remote-private`, then `local`) and rejects an ambiguous
tie. The workload bundle carries the exact host, ports, protocol, TLS
mode/profile/issuer, and route identity, and adapter observations must return
those fields unchanged. A workload without a declared route remains
internally reachable through its adapter but advertises no StackKits-owned
subdomain. `stackkit init --use-case` selects the workload and its runtime
adapter; it does not invent a StackSpec route or subdomain.

`standalone-compose` persists a private, digest-pinned Compose project and
resolves only owner-signed local secret custody. For a routed workload it
attaches the application to the StackKits routing network and emits the exact
Traefik host/TLS labels; an unrouted workload receives neither. It then
verifies the Compose service/image set, live route-label and network readback,
and an HTTP loopback probe. It does not install Docker, create a server, or own
provider lifecycle. Coolify and Komodo keep endpoint and credential custody in
their authenticated external owners. The CUE catalog is also the source for
the read-only per-service compatibility matrix exposed by
`stackkit app compatibility` and MCP.

Native application setup treats the Docker daemon as a trust boundary. It
rechecks the exact persisted Compose bytes, service/image identities, route
readback, container identity, and loopback port before each local API request
and after the action, but those checks cannot make daemon observation and the
subsequent socket dial atomic. A compromised Docker daemon or host can still
substitute the endpoint between checks; setup receipts do not turn that
daemon-level state into independent evidence.

Backup and restore use that same selected-delivery boundary. The local Kopia
snapshot remains one owner-approved, content-addressed snapshot of the local
Docker volume root bound to the exact Plan, manifest, Apply result, and Owner
custody. During restore activation, only Applications whose resolved adapter
is `standalone-compose` and whose CUE compatibility row explicitly enables
`backupRestore` join the mutation authority. Their backup-enabled persistent
allocations become exact Compose-qualified volume names; their private
`compose.yaml` and `.env` custody is digest-bound before mutation. The runtime
quiesces Applications before Basement, prepares deterministic per-volume
rollback copies, activates staged content, starts Basement before Applications,
and records success only after the normal host runtime verification readback.
That readback does not establish client access or application-data recovery. Coolify, Komodo,
unknown adapters, missing capability rows, substituted files, and foreign
volumes remain fail-closed.

For a selected `standalone-compose` application, the local source policy also
carries the compiler-selected runtime graph beside the backup-enabled volume
list. The graph is bound to the exact Apply bundle: component IDs, dependency
edges, Compose project/service identity, and pinned image ref/digest are checked
before mutation, while adapter-owned custody resolves one exact container ID
per component. Quiescence includes graph components without a selected
persistent volume, stops them in reverse dependency order, and starts only the
previously running exact IDs in dependency order. With no selected application
graph, the source remains the existing Core-only policy and its legacy
crash-consistent path. An active one-shot initializer prevents a new snapshot
before any container is stopped; snapshot recovery never reruns an initializer.
A persisted stop boundary requires settlement of the previous Kopia process
before writer recovery, including failures at early readiness or policy checks.
Each new snapshot attempt renews that settlement requirement on failure.
This is Docker-level crash-consistency evidence; it does
not establish native database consistency, application recovery, or client/live
reachability.

Komodo is intentionally two contracts. `stackkits-komodo-core-runtime` is the
only workload adapter and API authority. It targets Control Plane members at
the Control Authority Site. Its `agentRefs` closes onto the separately typed
`stackkits-komodo-periphery-runtime`, which targets worker nodes at that same
Site. This permits supplemental Basement and Modern Home workers without
installing Periphery on Modern's unrelated Cloud edge. Every Core and
Periphery handoff is an exact node-local instance. The contract requires
outbound TLS 1.3, mutual-key authentication, external credential custody, and
runtime/registration readback, but carries no endpoint, key material, Docker
socket, provider lifecycle, host lifecycle, discovery, or LAN authority.

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
server lifecycle. Its Product factory binds the exact Cloud Site/node/channel,
catalog hashes, and Health owner, but can enter a runtime composition only when
an authenticated host-channel implementation supplies the finite firewall,
hardening, and readback Operations.

Cloud public DNS is separately selected through
`stackkits-cloud-public-dns-contract`. It records that the Stack requires a
public DNS capability, but produces no module, provider mutation, credential,
runtime target, Health gate, or evidence claim. Provider-specific DNS
materialization belongs to the external TechStack/provider-management layer.

Cloud public edge is separately owned by
`stackkits-cloud-public-edge-runtime`. Its exact node-local executable contract
is restricted to Cloud targets and the `public-edge` capability, depends on the
Cloud host-security boundary, and binds the
`stackkits-cloud-public-edge-executor`. Its Product factory admits one exact
Cloud Site/node/channel and Health contract and requires an authenticated edge
Operations owner at construction. It does not own DNS, certificate issuance,
credentials, host hardening, backup, mesh, or server lifecycle. The residual
Cloud runtime no longer participates in a default Cloud Kit.

Cloud offsite backup is independently owned by
`stackkits-cloud-offsite-backup-runtime`. The generated contract expresses
the compiler-owned `BackupTargetRequirement` for each exact Cloud Site/node and
may include only a matching, maximum-24-hour `ExternalBackupTargetBinding`.
That binding contains opaque target and custody-attestation refs plus exact
StackInstance, capability/contract, requirement, Candidate, version, spec, and
validity hashes. Provider accounts, regions, buckets, endpoints, credentials,
resource IDs, leases, and lifecycle handles are structurally excluded. Missing
bindings preserve generation; execution still requires a fresh binding at the
actual invocation instant. The node-local Product executor accepts exactly one
binding projection per Runtime target, performs bind and obsolete-binding
reconciliation through a construction-owned authenticated Operations owner,
then verifies both a fresh backup observation and a restore/readback
observation before committing durable evidence. Missing, expired, substituted,
partial, future, clock-regressed, or digest-mismatched authority and evidence
fails closed. The module is therefore `apply-ready` as a StackKits runtime
contract without moving provider choice, object-target lifecycle, transport,
or credential custody into StackKits.

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
residual runtime is replaced by four concrete, separately owned modules for
the inter-Site link, outbound control agent, cross-Site backup, and bridge
observability. Each names its own capability, closed operation set, Health ref,
evidence ref, and hash-bound executor contract. The link owner is executable
and node-local; the other three remain explicit generation-ready handoffs.
Their renderers accept only their closed policy projections; credentials,
endpoints, provider lifecycle, lease state, and general LAN authority remain
excluded. Apply therefore advances one owner at a time without reviving a
generic Federation umbrella.

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

The inter-Site link now has an explicit external realization boundary. The
compiler emits one `FederationLinkRequirement` that binds the exact
StackInstance, Home and Cloud Sites, Site/node pairs, selected capability and
contract, complete resolved bridge hash, and default-deny outbound policy. An
external fabric authority may return only a maximum-24-hour
`ExternalFederationLinkBinding` with opaque fabric and custody-attestation
references. It cannot return transport, address, endpoint, route, credential,
relay, provider-resource, account, region, lease, or lifecycle information.
Generation remains available without that receipt; Apply exposes
`external-federation-link-binding-missing`. Once the receipt exists, the
Product-owned `stackkits-federation-link-executor` receives one exact artifact
per compiler-selected Home or Cloud node. It verifies the sealed request,
artifact digest, Site, node, execution channel, requirement/binding hashes and
`issuedAt <= now < validUntil` immediately before calling the injected local
operations. Success requires `establish -> remove obsolete -> verify` readback
that proves authenticated peers, declared flows only, default deny, no default
route, no broad/private-subnet advertisement, no general LAN access, and no
Cloud-to-Home inbound authority. StackKits still does not own the fabric,
transport implementation, endpoints, credentials, provider control plane,
lease, or lifecycle behind the opaque binding.

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

This first executable slice establishes the local backup target only. The
shared `backupPolicy` owns schedule/retention/data-class/restore-verification
intent. The local Kopia runtime separately owns repository initialization,
retention execution and owner-custodied encryption; the backup/restore lifecycle
owns signed operation evidence. Scheduling, off-site copies, restore
orchestration and backup success never follow merely from this target
observation. A Modern Homelab may add an explicit
cross-site backup owner later; Home+Cloud topology alone never silently enables
Basement backup semantics.

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

An isolated typed adapter now implements that exact consumption boundary for
both Basement and Modern semantics. It binds the policy to the service-owned
catalog hashes and exact Home Control Authority placement, then separately
denies forbidden cross-Site sessions, enforces link-loss behavior, preserves
local control, and requires a fresh readback of all three under one policy
digest. It rejects Cloud authority widening, an open Modern edge, stale or
partial evidence, and any caller execution channel. The adapter has no product
registration or authenticated operations backend yet, so the CUE owner remains
`unbound` and Apply remains blocked.

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

The isolated Home device-authority adapter configures the policy behind local
device enrollment, the possession-bound device credential issuer, and device
credential revocation. These are deployment-time configuration operations; an
Apply request does not enroll a particular device or mint/revoke an individual
credential. The CUE operation names say this explicitly. The adapter binds one
exact device issuer to the Home Control Authority, LAN-local enrollment,
bounded credential/session lifetimes, service-owned catalog hashes, and fresh
digest-bound readback. It contains no signing/private key bytes, credentials,
runtime endpoints, remote enrollment, Cloud authority, transport, or provider
lifecycle. Product registration and CUE remain `unbound` until an authenticated
authority backend supplies observable enforcement.

The isolated Basement identity-trust adapter now implements the verifier side
of that boundary without claiming product readiness. It accepts only the exact
`BasementIdentityTrustPolicy`, service-owned provider/module/unit/Health hashes,
the Home Site/node binding, and one device, human, and workload verifier. Its
closed operations configure those three verifier classes and perform a fresh
exact-policy readback; all observations bind one derived policy digest. It has
no enrollment, credential issuance, signing, key bytes, credentials, endpoints,
provider lifecycle, or generic execution capability. The adapter remains
outside product registration and its CUE requirement remains `unbound` until an
authenticated operations backend exists.

Cloud has a separate isolated adapter because its authority is materially
different. It configures only the StackKit-owned human and workload credential
issuers, consumes the external owner-bound device issuer solely as a verifier
reference, configures device/human/workload verification at Cloud Sites, and
requires a fresh digest-bound readback across all five responsibilities. The
artifact now names `cloudDeviceEnrollment: deny` and
`cloudDeviceIssuance: deny` explicitly; it no longer uses an ambiguous blanket
Cloud-issuance label that conflicted with the intentional human/workload issuer
authority. The deployment operation configures issuers; it does not mint an
end-user credential. Device enrollment/issuance, key bytes, credentials,
endpoints, provider lifecycle, Home/LAN authority, and generic signing remain
outside the adapter. Product registration and the CUE owner remain unbound.

Modern uses neither of those adapters. Its isolated two-artifact adapter binds
the jointly hashed trust and verifier-distribution policies, exact Home and
Cloud Sites/nodes, three zero-stale Home verifiers, three partition-bounded
Cloud verifiers, and one Home-to-Cloud distribution per human/device/workload
issuer. The renderer now validates the actual product graph: every principal
must have verifier coverage at every governed Home and Cloud Site, while every
Cloud Site must receive that principal's verification-key reference and
revocation state from Home. This closes an earlier fixture bug that modeled
only one Cloud device verifier and incorrectly rejected the canonical Home
verifiers. The adapter can distribute those two non-secret material classes,
enforce one-way flow, and configure Home/Cloud verification. It cannot sign,
enroll, carry keys or credentials, realize transport, open general LAN access,
or manage a provider. Product registration and CUE remain `unbound` pending an
authenticated backend and fresh operational evidence.

### Shared Home local-ingress and access policy

`local-ingress` and `lan-access-policy` are now owned by the dedicated,
provider-free `stackkits-home-access-policy` provider rather than the residual
`stackkits-local-runtime` umbrella. Basement Kit selects the module-single policy
manifest; Cloud Kit explicitly forbids the Home LAN capabilities and never
selects the module.
Modern Homelab selects the same shared Home module without making it aware of
federation; the Home+Cloud composition rule is carried by Modern's public
native-v2 profile. Its public Preview status does not graduate incomplete
runtime owners.

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

The first isolated `stackkits-home-access-enforcer` adapter now proves that
runtime boundary without changing product readiness. It accepts only the exact
CUE-rendered Home access artifact plus service-owned provider/module/unit/Health
hashes and exact Home Site/node placement. It exposes only the three closed
operations `enforce-lan-access`, `enforce-local-ingress`, and
`enforce-privileged-step-up`, followed by a fresh exact-policy readback. Every
operation and the Health result must bind the same derived policy digest; stale,
partial, substituted, or widened observations fail closed. The adapter remains
outside product registration and the CUE requirement remains `unbound` until an
authenticated operations backend exists, so this is no Apply-graduation claim.

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

Native `stackkit/v2alpha2` sizing is module-local. Each selected
resource-bearing module explicitly chooses one declared `computeProfile`; a
module that declares a storage or accelerator dimension explicitly chooses
those profiles too. Profile names such as `low`, `standard`, and `high` are
module-scoped identifiers, not a kit-wide quality promise. They refine an
already selected module and cannot enable another workload, choose a device
class, or be inferred from inventory. The kit-wide `install.computeTier`
graphs remain only in the explicit `stackkit/v2alpha1` compatibility adapter.

The compiler retains the module's architecture, virtualization, and inventory
requirements while applying any stricter profile constraints through this same
placement path. It aggregates only declared CPU, RAM, and storage facts per
target node: host floors use the maximum, reservations add, and recommendation
adds declared recommendation/reservation plus declared headroom. Missing axes
stay unknown and are never manufactured as zero or `standard`. ResolvedPlan
binds the selected profile bodies and hashes plus the aggregate demand so plan
validation detects catalog or plan drift. A declared-capacity browser result is
still not host compatibility; Apply requires the normal attested preflight.

Application profiles now reuse the existing Core floors as a deliberate
Kombify hosting policy. These are absolute requirements for the host, not
benchmarks or storage reserved for user content:

| Selected profile | CPU / RAM / host storage | Basis |
| --- | --- | --- |
| Core Standard/High | 2 / 4 GiB / 20 GiB | Existing platform policy |
| Standalone Core Low | 2 / 2 GiB / 10 GiB | Smaller resource profile of the complete standalone core |
| Immich Standard/High | 2 / 6 GiB / 20 GiB | Pinned upstream CPU/RAM; platform storage policy |
| Immich Lite Low | 2 / 4 GiB / 10 GiB | Pinned upstream RAM with ML omitted; smaller platform storage policy |
| Files, Vault, Smart Home Low | 2 / 2 GiB / 10 GiB | Smaller platform policy |
| Files, Vault, Media, Smart Home Standard/High | 2 / 4 GiB / 20 GiB | Standard platform policy |

The Immich CPU/RAM requirements come from the
[v2.7.0 requirements](https://github.com/immich-app/immich/blob/v2.7.0/docs/docs/install/requirements.md).
The other application floors are Kombify starting configurations, not numeric
manufacturer minima. Module RAM reservations remain additive. A Core Standard
plus Immich host therefore has a floor of 6 GiB rather than 10 GiB; observed
capacity must still satisfy the aggregate reservations. No profile promises
transcoding concurrency or photo-import throughput. Immich's 8 GiB whole-host
recommendation is not encoded as an additive app recommendation.

Admission also needs the applicable CPU instruction-set and filesystem
requirements; numeric floors alone do not establish runtime compatibility.
For Full Immich on amd64, every observed CPU must provide x86-64-v2. Linux
inventory reads the exposed CPU flags; incomplete observations stay unknown and
a fresh unknown observation clears a previous level. Arm64 does not acquire an
x86 requirement, and Immich Lite omits the ML component that requires it.
Both Immich profiles require persistent local POSIX storage with Unix ownership
for their PostgreSQL data. The host observer resolves the exact data path and
reads its Linux mount; unresolved paths and ambiguous mounts stay unverified,
while known network, non-POSIX, or ephemeral filesystems cannot satisfy this
profile. This filesystem classification does not prove available capacity or
writability. The Core host-bootstrap adapter also observes the actual Docker
data root on the fixed local socket before preparing storage directories; a
different daemon data root cannot reuse the declared path's admission facts.
This adapter's `host.bootstrapRuntime` contract explicitly requires rootful
Docker. A rootless request is rejected during projection rather than inspected
through the rootful socket. The Docker executable still belongs to the local
operator's trusted tool installation; this observation is not binary attestation.
The native Core renderer and its local Kopia source use the opinionated
`/var/lib/docker` data root and `/var/lib/docker/volumes` mount layout. A
separate local disk may be mounted there, but an alternate Docker data root is
outside this native bootstrap contract and is rejected; this does not narrow
the generic `system.container` schema or other adapters.
The public profile catalog exposes these declarations through the existing
planner, CLI and MCP surfaces; only target inventory can satisfy them.
The storage column excludes photo libraries, media, data growth, backup copies,
and restore staging. Those need their own explicit capacity budgets.

An explicit `DataBinding.capacityDemand` is a CUE-owned additional/new-data
budget. Its `initialGiB`, monthly growth, horizon, and reserve produce the
projected and required GiB values; omitted demand stays unknown. ResolvedPlan
admission reserves that budget once per actual `(binding, node)` placement and
does not multiply it by data classes, declared replicas, or duplicate workloads.
For local storage, the attested `storageCapacity` fact must identify the exact
resolved `storage.dataRoot` or container `system.container.dataRoot` filesystem;
generic root-filesystem free space and remote/NFS storage remain unverified.
This is a data-budget check in addition to module minima and does not claim
capacity for backup snapshots, staging, rollback, or restore copies.

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

Legacy bootstrap intent has no separate ResolvedPlan bag. Install-wide defaults
map only to `install.platform.setupPolicy.platform` and
`install.platform.setupPolicy.applicationDefault`; the selected workload then
binds its exact catalog-owned `alternative.setup` mode, owner, and action
allowlist. Legacy `full_auto`, `guided`, and `minimal` selectors, arbitrary
commands, credential envelopes, and provider bootstrap lifecycle are not
native-v2 authority.

Legacy branding is likewise not a stack-global or provider-facing settings
bag. Only the public `brand-color` and `dashboard-title` keys may be selected,
and only when the chosen UI workload alternative declares them in its settings
allowlist. The compiler binds those same values to that alternative's exact
catalog-owned workload module and render units. They cannot select another
module, carry secret material, or affect runtime/provider ownership.

Every module has one closed role: `foundation`, `platform`, `workload`, or
`operations`. A workload alternative must bind exactly one `workload` module
owned by its catalog adapter, an allowed runtime kind and delivery, one service
endpoint and health contract, compatible Site kinds and data classes, and only
inputs declared by that module. Photos declares `immich-owner-bootstrap`,
Files declares `cloudreve-owner-bootstrap`, Media declares
`jellyfin-owner-bootstrap`, Vault declares `vault-owner-invite`, and Smart Home declares
`home-assistant-owner-bootstrap` as module-owned,
on-demand actions through the same setup lifecycle stage. An action is
executable only when its selected adapter registers native support; declaration
alone does not make other adapter handoffs executable.

The compiler selects the declared workload and persists its placement and
module-local profile in the ResolvedPlan. Generated bundles and distribution
hashes carry that same contract. Apply readiness additionally requires the
observed host and capacity facts. Native Photos, Files, Media, Vault and Smart Home setup use the
exact signed Apply, application bundle and local Owner binding. Their shared
CLI/MCP operation and State Console action read the same setup metadata, and
credentials stay in a private local file. Home Assistant requires an actual
owner/admin credential login, version and running-state readback; personal
onboarding remains separate. A completed receipt from an earlier Plan or Apply
remains historical evidence.

The Console prepares an unapproved setup request with the application's
credential-file path. It cannot manufacture Owner approval: the registered
operation still requires explicit confirmation and Owner approval before CLI
dispatch. Missing or failed connector results remain unavailable. Photos checks
the applied API version both before setup and after owner readback, so a missing
or changed final version cannot become a signed success receipt.
Home Assistant's setup adapter is pinned to the deployed release's
[login-flow implementation](https://github.com/home-assistant/core/blob/2026.7.2/homeassistant/components/auth/login_flow.py)
and [token revocation contract](https://github.com/home-assistant/core/blob/2026.7.2/homeassistant/components/auth/__init__.py).
Its onboarding and credential-login sessions are temporary and both are revoked
through the bounded cleanup path.
Immich setup similarly owns its temporary session: failed bootstrap closes it,
and native setup or the legacy identity handoff closes it after its last use.
A cleanup failure remains an explicit setup failure rather than a success
receipt.

Files shares the pinned Cloudreve owner adapter with the legacy Files session
bridge, while native setup does not require that bridge or PocketID handoff.
It verifies version, password login, current-user identity and an admin-only
API readback before revoking its temporary session. Its receipt establishes the
administrator account; storage policy, quotas, registration and sharing choices
remain application settings. The [Files owner guide](../use-cases/files/agent/owner-setup/SKILL.md)
uses the same credential fields and native command as the Console metadata.

Media prepares or verifies an explicit Jellyfin administrator through the pinned
startup and login APIs. Password login always identifies the temporary StackKit
client, and authenticated user-policy and server-info readback precede session
revocation. Only an explicit completion request closes the startup wizard. The
[Media owner guide](../use-cases/media/agent/owner-setup/SKILL.md) keeps library
paths, household viewer permissions and client playback separate from the
administrator receipt. Backup of server configuration does not prove that every
external media library is protected.

Vault uses its signed Apply-bound administrator-token custody to prepare the
owner's invitation on the pinned Vaultwarden API with public signups closed.
Only a bounded identity/status readback is retained. The signed setup receipt
records preparation; it cannot complete the personal account, handle client
encryption keys or claim usability. The temporary admin cookie is logged out
and cleared, without claiming revocation of Vaultwarden's stateless JWT.
The [Vault owner guide](../use-cases/vault/agent/owner-setup/SKILL.md) places
master-password creation and the save/sync/lock/unlock/read check in the official
client, followed by separate backup and isolated-recovery evidence.

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

Origin selection is a catalog policy, never a context or provider decision.
`single-site` and `control-authority-site` retain one exact Site anchor.
`multi-zone` selects every matching resolved node-local instance across the
declared Site kinds and requires at least two site-scoped node failure domains;
it may therefore span several nodes inside one Basement or Cloud Site.
`edge-pool` selects every matching instance whose node has the explicit `edge`
role; it does not imply Cloud placement. The policy declares minimum Site,
Site-failure-domain, and node-failure-domain spread, and the resolved route
persists the exact sorted Site set plus compiler-observed failure-domain set.
Only Modern Homelab may use a selector with `minSites > 1`; Basement and Cloud
remain single-Site but multi-node. Data locality, reachability, health gates,
and backend membership are validated for every selected Site and member.

Each ordinary route also owns a compiler-derived health gate bound to that exact
route, backend pool, source module health contract, upstream protocol/port, and
every selected backend member. A matching HTTP or TCP source contract becomes a
closed probe descriptor; unsupported or mismatched contracts remain resolvable
as `contract-only` and never imply execution. The older unbound v2/v3 route input
types are no longer accepted by the CUE, compiler, API, or renderer boundary.
The current v4 projection always carries the narrow `healthProbe` and reachability
authority together, so an external egress cannot be relabelled as a Cloud edge
and an edge cannot acquire external Home custody. Internal gate identities,
provider/address/credential data, and observed status remain unreachable.

For Apply, an executable aggregate route gate is deterministically partitioned
into one Health requirement per exact backend member and bound to that member's
existing Runtime requirement. The shared provider-free executor contract carries
only that Runtime ID, exact Site/node placement, route and pool identity, and the
address-free HTTP/TCP probe. It cannot discover an endpoint or choose a runtime.
Each Runtime owner must explicitly accept and verify its route probe; the Immich
selected-PaaS owner is the first concrete consumer. Other owners stay fail-closed
until they implement the same explicit contract. HTTPS remains `contract-only`
until an executor-private contract can bind SNI, peer identity, and trust roots;
StackKits does not weaken it to a generic TCP check. Contract-only descriptors
continue to block Apply with `health-gate-not-executable`.
The current `authority-bound-service-route-list-v4` projection carries
`originSelector`, the exact `originSiteRefs`, the closed resolved selection
proof, and the route-relative capability authorities. Older route-list value
types are not accepted at this renderer boundary; a consumer cannot downgrade
a multi-Site route by fabricating a legacy `originSiteRef`.

For Modern Homelab the public listener and protected backend are deliberately
different contracts. For example, an edge listener may expose `https:443` while
the only allowed edge-to-home flow is the selected endpoint's `http:2283` plus
its exact data classes. The ResolvedPlan publication records both sides and its
module/unit/backend pool. `management-only` overlays cannot carry publications or
data flows, broad routes remain forbidden, and TLS passthrough is unavailable
while edge authorization or rate limiting depends on edge termination.
Bridge publications intentionally remain single-source-Site contracts and fail
closed for multi-zone or edge-pool service endpoints until the bridge contract
is separately versioned; a route pool never widens cross-Site authority.

Each publication also carries a compiler-derived access decision. It must bind a
public, authenticated policy and the exact allowed HTTP method set. Every selected
edge-to-origin flow is a subset of that method set; a private/LAN policy,
`authentication: none`, or a widened method fails before rendering. Endpoint data
authority intentionally uses the same stable `serviceRef` as the endpoint identity,
so a publication cannot silently switch to a second data binding.

Public WebPKI is a separate catalog-owned capability/provider/module chain with
an authenticated node-local runtime seam. Its operation-shaped artifact
contains the exact TLS profile, ACME issuer policy, renewal health reference,
sensitivity-typed logical material slots, Cloud target, and matching public
HTTPS routes. The Product Runtime binds the sealed request and artifact
digests, Site, node, and execution channel into a stable idempotency policy
digest, then captures one evaluation time before it admits materialization,
renewal, and fresh readback. ACME credentials,
certificate/private-key bytes, DNS mutation, provider resources, and
server-provider lifecycle remain construction-owned by the external Operations
implementation and cannot enter the artifact or caller request.

Home-internal PKI follows the same separation without importing Cloud/WebPKI
semantics. It is selected only by explicit `internal-pki` capability intent and
generates a Home-scoped contract with exactly one root-CA authority on the
single explicit control member. The authority binds the Stack trust domain,
root role, CA/path-length constraints, signing usages, and key algorithm.
Every governed Home target receives only the public trust-root slot; CA signing
custody is never fanned out to workers. Leaf issuance remains a separate,
explicitly unbound contract whose subjects and SANs must be compiler-derived
from exact services and routes, with `CA=false`, bounded usages, and fresh
fingerprint/serial/validity evidence. The artifact contains no material bytes,
credentials, endpoints, or host inventory. Multi-controller Home PKI therefore
fails closed until a distinct CA-authority selection/HA realization is defined,
and Apply remains blocked until authenticated root, leaf, rotation, and
postcondition owners are bound.

Modern origin mTLS has an exact provider-free, authenticated node-local Runtime
owner. For every publication, the compiler binds explicit
`{nodeRef, instanceRef}` pairs rather than independently sorted node and
instance sets. One artifact per Home origin node carries only that node's
selected local backend, TLS 1.3 SNI, possession-bound Home workload issuer, and
the configured one-way Cloud-verifier references. The executor binds the
sealed request and artifact digests, Site, node, and execution channel before
calling the closed bind/remove/verify operations. Fresh local readback must
match the exact module, unit, backend instance, protocol/port, SNI, issuer,
audience, verification key set, certificate/public-key/serial identity,
credential lifetime, and local configuration/revocation state.

This owner proves only the Home-side proxy and credential postcondition. It
does not prove Cloud-verifier readiness and cannot carry private/signing
material, reverse authority, endpoints, credentials, provider lifecycle,
leases, proxy implementation, or general LAN access.

The matching Cloud-side service-publication owner is separately executable on
the exact compiler-selected Cloud edge nodes. Its one-per-node artifact carries
only the closed public host/path/method, access, TLS, rate-limit, backend
module/unit/node/instance, origin-identity, data-binding, and Health-gate
references. When the selected endpoint exposes a matching executable HTTP or
TCP health contract, the compiler also adds an address- and credential-free
probe derived from that exact contract. The Product Runtime binds the sealed
request and artifact digests, Site, node, and execution channel to a captured
UTC evaluation time, then admits only apply, obsolete-removal, and verification
operations. Fresh readback must prove that the exact publication is configured
default-closed with origin mTLS, origin identity, TLS, authentication, and rate
limiting bound. Verification additionally requires exactly one fresh healthy
edge-to-origin readback for every declared `{nodeRef, instanceRef}` pair.
Missing, additional, foreign, stale, or unhealthy backends fail closed; Apply
or obsolete-removal observations cannot satisfy the Health gate. HTTPS
backends remain contract-only until an executor-private binding supplies SNI,
peer identity, and trust roots.

Both former publication `runtime-owner-unbound` blockers are therefore
retired. DNS mutation, certificate issuance, credentials, provider lifecycle,
leases, endpoints, transport implementation, general LAN access, Cloud
verifier readiness, and unsupported backend Health remain separate fail-closed
authorities. Public edge TLS materialization is likewise a separate hash-bound
authority; a generic module contract cannot hide or replace any of these
boundaries.

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

### Stage 1 OpenTofu wrapper roots

ADR-0045 Stage 1 executes every generation-time Compose artifact through an
OpenTofu root that embeds the byte-identical Compose payload.
`internal/architecturev2renderer/compose_payload_opentofu.go`
(`RenderComposePayloadOpenTofu`) is the one generic wrapper; the Basement core,
Basement core Lite, Cloud core and Cloud standalone core `opentofu` units (and
the Basement Terramate `main.tf`) run their module's unchanged Compose pipeline
for the same unit and wrap its bytes.

- Root location: the executor installs the generated `main.tf` at
  `.stackkit/runtime/<runtime>/opentofu/main.tf`, where `<runtime>` is the
  native runtime directory of the module (`basement-core` for Basement core
  and Lite, `cloud-core`, `cloud-core-standalone`). OpenTofu state lives in
  that directory and is captured by the executor-state checkpoint store.
- The root writes `../compose.yaml` (mode `0600`) and runs
  `docker compose --project-name <project> -f <runtime>/compose.yaml up -d
  --wait --wait-timeout 600` from `<runtime>`, with the project names the
  native executor uses (`stackkit-basement-core`, `stackkit-cloud-core`,
  `stackkit-cloud-core-standalone`). Destroy runs `down` without volumes.
- Resources (deterministic names, `<prefix>` is the unit's resource prefix,
  for example `basement_core`, `cloud_core`, `cloud_core_standalone`):
  `local_file.<prefix>_compose` writes the payload;
  `terraform_data.<prefix>_up` holds the payload (and `.env`) digests in
  `triggers_replace` and runs only the create-time `up`;
  `terraform_data.<prefix>_lifecycle` has no triggers, depends on
  `<prefix>_up` and runs `down` only on destroy. A payload or `.env` change
  therefore replaces only `<prefix>_up` and runs one `up`, which recreates
  only the changed services, as the native executor does; `down` runs only on
  `tofu destroy`, before any other resource goes. Rollback replays a payload
  with `-replace=terraform_data.<prefix>_up`. A state from the earlier
  single-resource root (`terraform_data.<prefix>`) upgrades without `down`:
  the removed block has no destroy provisioner left, so the old resource is
  dropped and the new `up` is a no-op.
- Adoption: on an existing native install the root rewrites identical bytes to
  the same file and project, so `up` is a no-op and no container is recreated.
- Environment: the root sets `STACKKIT_CUSTODY_DIR`
  (`<runtime>/../../custody`); every other Compose interpolation variable
  (owner email, stackkit-server user) must come from the environment the
  executor gives the `tofu` process, the same set the native executor uses.
- Provider pin: `hashicorp/local` `= 2.5.3`, OpenTofu `>= 1.10.0`.
- Workload bundles (Immich, Jellyfin and the other selected-PaaS bundles),
  the Kopia source policy and the runtime adapters are target-neutral
  `native-config` units; their Compose projects are materialized by the
  executor at apply time under `.stackkit/runtime/applications/<project>/`
  with a `.env` file. The executor wraps those with the same function, not
  the generator: `ComposePayloadSpec.EnvFile` passes `--env-file` and adds
  `filesha256` of the `.env` to `triggers_replace`, so a changed secret
  reapplies the project while the `.env` content never enters `main.tf` or
  OpenTofu state. `ComposePayloadSpec.NoWait` drops `--wait` for a project
  with a component that may run degraded, as the native `up` does. Generated
  core roots set neither field and are unchanged.
- `TestComposePayloadOpenTofuEmbedsTheByteIdenticalComposeArtifact` parses each
  twin with the HCL parser and asserts the evaluated `local_file` content
  equals the Compose artifact rendered from the same canonical-plan unit.
- `TestComposePayloadOpenTofuPayloadChangeRunsUpWithoutDown` applies a root
  with a real OpenTofu and a recording stand-in `docker`, changes the payload
  and asserts the plan replaces only `<prefix>_up`, `docker` saw `up` twice
  and `down` never, and `tofu destroy` runs `down` once. It runs when
  `STACKKIT_TOFU_BINARY` and `STACKKIT_TOFU_PROVIDERS_DIR` are set.

### OpenTofu runtime executor (Stage 1)

ADR-0045 makes OpenTofu the execution standard.
`internal/runtimeexecutor/opentofu` is that executor for the wrapper roots
described above; `internal/runtimeexecutor/nativehost` stays the native
Compose fallback behind the `compose` unit (S-F structure rule, see
[Execution folders](#execution-folders-adr-0045-structure-rule)).

- **Selection.** Two registrations per Core module match the runtime target
  whose `unitRef` is `opentofu` (target `opentofu`) or `terramate` (target
  `terramate`). The registry matches whole selectors, so
  `opentofu.DefaultModuleBindings` is the closed module list
  (Basement core, Basement core Lite, Cloud core, Cloud standalone core); new
  OpenTofu units extend it. The native registrations keep `unitRef: compose`.
  Workload, edge and federation owners have one selector under every target,
  so their registrations stay the native ones and switch on the generation
  target of the request's plan-owned `resolved-plan` artifact
  (`nativehost.GenerationTargetFromArtifacts`); under `compose` they
  run the native executor unchanged.
- **Root.** The executor accepts exactly one `opentofu`/`hcl` artifact owned
  by the target (under `terramate`: the `main.tf` and `stack.tm.hcl`
  `terramate`/`hcl` artifacts of the unit) and installs it as `main.tf` (mode
  0640, the stack file beside it) in `.stackkit/runtime/<runtime>/opentofu/`.
  The `tofu` process gets the native Compose interpolation environment, so
  `local-exec` resolves the same variables the native executor supplies.
- **Run.** Through `internal/tofu` with the packaged binary: `tofu init
  -input=false`, `tofu plan -input=false -detailed-exitcode -out=tfplan`, and
  `tofu apply -input=false tfplan` when the plan has changes. State stays in the
  root as the local-backend `terraform.tfstate` (0600). The executor never runs
  a refresh-only plan, `-replace`, or destroy. The apply observation (exit
  codes, plan summary, state digest, verify result) is written to
  `stackkit-apply-observation.json`; its digest is the runtime observation
  digest.
- **Native side steps.** The Core Apply has steps a Compose payload cannot
  express. Before `tofu` runs, the executor runs the native preparation
  (origin provisioner check, stackkit-server staging); after apply it runs the
  native completion against the Compose file OpenTofu wrote (stackkit-server
  recreation, step-ca reload, PocketID owner realization, and the reconciling
  `up` that binds TinyAuth). Both come from the native operations
  (`nativehost.NativeComposeRuntime`), so the two executors share
  one implementation. The native Cloud core `up` does not wait; the wrapper
  root waits for health.
- **Verify.** The native Verify of the module runs against
  `.stackkit/runtime/<runtime>/compose.yaml` after the executor proves it holds
  exactly the root's `local_file` payload: the pinned service set through
  `docker compose ps`, every governed probe, and the PocketID owner binding.
  Public `stackkit verify` (and the backup and restore post-verify that reuse
  it) accepts the Basement and Cloud Core runtimes on the `opentofu` and
  `terramate` units: it derives the Compose payload from the governed `main.tf`
  with `ExtractComposePayload` and requires the installed root `main.tf` to
  equal that artifact, the runtime `compose.yaml` to equal the payload, and
  the root to hold state.
- **Offline providers.** Release archives ship a filesystem mirror in
  `providers/` beside `tofu` (`scripts/release/fetch-opentofu-providers.sh`,
  checksum-verified against the upstream SHA256SUMS; the Debian package installs
  it at `/usr/local/lib/stackkit/providers`). The executor generates a CLI
  configuration that installs every `registry.opentofu.org` provider from that
  mirror and forbids direct installation, and it removes host
  `TF_PLUGIN_CACHE_DIR`, `TF_CLI_ARGS*`, and CLI-config overrides. The mirror
  resolves from `STACKKIT_TOFU_PROVIDERS_DIR`, else `providers/` beside the
  executable. A missing mirror or binary fails closed before any write.
- **Workload roots.** Under `opentofu` and `terramate` the ten selected-PaaS
  workload bundles run through `opentofu.WorkloadOperations`,
  which wraps the native standalone Compose owner. The native preparation
  (`PrepareWorkloadCompose`) validates the bundle and persists `compose.yaml`
  and the private `.env` (0600) under
  `.stackkit/runtime/applications/stackkit-<workloadRef>-<nodeRef>/` exactly as
  the native Apply does. The executor renders `main.tf` around that Compose
  file (`RenderComposePayloadOpenTofu` with `EnvFile: ".env"`) in the graph's
  runtime root `.../applications/<project>/opentofu/`, runs init, plan and
  apply there instead of the native `docker compose up`, proves the runtime
  Compose file holds the payload, and runs the native completion
  (`CompleteWorkloadCompose`: Wings recreation for the game node, blocking
  component and HTTP readiness) and the native observation unchanged. The
  `tofu` process gets `LANG=C`, `LC_ALL=C` and the native project name.
- **Edge and federation contract roots.** The Cloud public edge, federation
  link and bridge origin mTLS owners have no Compose project. Under `opentofu`
  and `terramate` their registrations wrap the native executor
  (`WithProductOpenTofuContractRoot`, `ContractRootExecutor`): the native
  owner operation runs first, byte for byte; after it succeeds the executor
  writes `.stackkit/runtime/modules/<moduleRef>/opentofu/main.tf`, one
  `terraform_data` resource whose `triggers_replace` is the digest of the
  owner's contract artifact, and applies it, so OpenTofu state records the
  contract the owner applied. The root starts no process because no CLI
  entrypoint re-applies a single owner safely from `local-exec`. The
  federation control agent and bridge publication are Techstack process
  owners, registered only through a bound execution channel; their roots are
  not materialized by this executor.
- **State custody.** Executor-state snapshots for the `opentofu` and
  `terramate` targets capture every root's `terraform.tfstate` and `main.tf`
  as signed blobs (`runtimeOpenTofu`): Core roots with the runtime
  `compose.yaml`, verified against the governed artifacts; workload roots
  (`applications/<project>/opentofu`) with the runtime `compose.yaml` and the
  private `.env`; contract roots (`modules/<moduleRef>/opentofu`) with state
  and configuration only. `CollectOpenTofuRootStates` finds every root through
  the root marker (`kind` is `workload` or `module` for executor-materialized
  roots, absent for Core roots) and `Recover` restores them before the
  StackSpec commit point. Compose snapshots are byte-identical to before. The
  upgrade checkpoint derives its target from the plan and now seals for
  `opentofu` and `terramate` installs too: the Core profile selects the Core
  root `main.tf` as the governed Core artifact and
  `architecturev2renderer.ExtractComposePayload` recovers the embedded Compose
  payload from it, so the Kopia managed-volume check and the Core profile are
  exactly those of the `compose` target for the same plan. The capture binds
  the Core root configuration to that artifact and its runtime `compose.yaml`
  to the payload. The restore activation recovery graph carries
  `renderTarget`, points each Compose runtime at the runtime `compose.yaml` its
  root writes and binds that root's `terraform.tfstate` digest
  (`statePath`, `stateDigest`); activation verifies the state beside the data
  and the checkpoint `Recover` restores it. Compose graphs stay byte-identical.
  The beta.4 bridge checkpoint still accepts only the `compose` target.
- **Not yet wrapped.** The Kopia runtime and the runtime adapter projects, and
  the Basement `socket-proxy` helper, which has a native executor and Product
  factory but no CLI registration and is not a Terramate stack, still run on
  the native executor under every target.

### Execution folders (ADR-0045 structure rule)

The runtime-target seam is the Product runtime-owner registry
(`ProductRuntimeOwnerRegistry` in `internal/architecturev2`). Registrations
match whole selectors, and the selector's `unitRef` names the runtime target
(`compose`, `opentofu`, `terramate`); the CLI binds every registration in
`cmd/stackkit/commands/architecture_v2_product_runtime.go`. There is no second
target registry. Executor code lives in named folders so the standard path and
the retained fallbacks read apart:

| Folder | Role |
| --- | --- |
| `internal/runtimeexecutor/opentofu` | Standard executor (ADR-0045): the `opentofu` and `terramate` units, workload roots and contract roots. |
| `internal/runtimeexecutor/nativehost` | Native host executors: the S-F Compose fallback behind `compose` (native `up` for the Core and standalone workloads), plus the shared halves the standard executor calls (Compose prepare and complete, native Verify, project naming, generation-target reading) and the native host owners that have no OpenTofu counterpart. |
| `internal/runtimeexecutor/fallback/platformdeploy` | Fallback in-house Komodo, Coolify and Dokploy HTTP adapters, retained until each platform's OpenTofu provider target has real-host parity. |

`nativehost` is not under `fallback/` because the standard executor imports
its shared halves. Its fallback-only `up` paths are methods on the same OS
operations types as those halves and share unexported helpers, so separating
them needs code changes; that split belongs to the evidence-gated P4
retirement slice, not to the structure rule. Retirement of either fallback is
a separate P4 slice per component with parity evidence.

### Terramate stack graph (Stage 1)

ADR-0045 section 2 and section 5 run Advanced Mode as Terramate over the
Stage 1 OpenTofu roots. Under the `terramate` generation target the plan
renders one stack per stack-bearing module instance (one module on one node)
and one stack graph per plan. `internal/terramatestackgraph` is the single
authority for stack identity, tags and ordering. The renderer and the graph
both use it, so the files and the graph cannot disagree.

- Project root: every host is one self-contained Terramate project rooted at
  the executor-managed runtime tree `.stackkit/runtime`. The Core host
  bootstrap module emits its `terramate.tm.hcl` once per node
  (`required_version = "~> 0.17"`, the release pins 0.17.1). The runtime tree
  is not a Git repository, so ordering is explicit and no Git change detection
  is assumed.
- Stacks and roles: `core` for Basement core, Basement core Lite, Cloud core
  and Cloud standalone core (their Terramate unit emits `main.tf` and
  `stack.tm.hcl`); `workload` for the ten selected-PaaS bundles (Immich,
  Immich Lite, Cloudreve, Vaultwarden, Pterodactyl, Private AI, Gitea,
  Paperless-ngx, Jellyfin, Home Assistant) at
  `platform/applications/<slug>/stack.tm.hcl`; `edge` for the Cloud public
  edge; `federation` for the Modern federation link, federation control agent,
  bridge publication and bridge origin mTLS owners. Stack units other than the
  cores are artifact-only companion units, so their files never reach a
  runtime executor. The local Kopia runtime is a component of the core Compose
  payload and belongs to the core stack.
- Identity: the stack ID is `stackkit-` plus the first 20 hex characters of
  `sha256("stackkit.terramate-stack/v1\n<moduleRef>\n<siteRef>\n<nodeRef>")`.
  It is stable across plan revisions of the same module, Site and node. Tags
  are `stackkit`, `role/<role>`, `site/<siteRef>`, `node/<nodeRef>` and
  `module/<moduleRef>`; Terramate 0.17 rejects `:` in tags, so the namespace
  separator is `/`.
- Order: the site core first; the edge and the workloads of a host after that
  host's cores; federation and bridge stacks after every core and edge of
  every Site, so a Modern link starts only when the Home core and the Cloud
  edge exist. In `stack.tm.hcl` this is expressed with Terramate tag queries
  (`after = ["tag:role/core"]`, and `tag:role/edge` for federation), which
  resolve inside one host project. Cross-host order exists only in the graph.
  Host and security baseline owners are not stacks.
- Runtime roots: `.stackkit/runtime/<runtime>/opentofu` for cores (the P1.2
  wrapper roots), `.stackkit/runtime/applications/stackkit-<workloadRef>-<nodeRef>/opentofu`
  for workloads and `.stackkit/runtime/modules/<moduleRef>/opentofu` for
  edge and federation stacks. Only core roots are generated. For the other
  stacks the executor materializes `main.tf` at apply time: the Compose
  project for workloads, a contract root recording the native owner's applied
  contract digest for edge and federation (see "OpenTofu runtime executor
  (Stage 1)").
- Graph: the plan-owned metadata artifact `terramate-stack-graph` at
  `<outputRoot>/.stackkit/terramate-stack-graph.json`
  (`stackkit.terramate-stack-graph/v1`, schema
  `schemas/stackkit-terramate-stack-graph-v1.schema.json`) exists only under
  the `terramate` target and is derived from plan facts only
  (`terramatestackgraph.Build`). It lists every host (Site, node, execution
  channel, project root artifact, run order) and every stack (ID, role,
  module, unit, instance, Site, node, workload, runtime root, `main.tf`
  provenance, `after`, tags, artifact IDs). Every list is sorted and the run
  order breaks ties by runtime root, which is the order
  `terramate list --run-order` prints. `terramatestackgraph.Parse` accepts only
  the canonical form whose identities and ordering equal the rules.

### Advanced change sets through Terramate (Stage 1)

ADR-0045 section 2 runs Advanced Mode Day-2 changes as Terramate over the
Stage 1 OpenTofu roots. `stackkit advanced change-set create` and
`stackkit advanced change-set apply` are that path; Standard Mode never runs
Terramate (`stackkit apply` under `compose` or `opentofu` is unchanged).

- Host project: `internal/terramatehost` derives the host layout of the local
  node (Site and node of the Owner custody binding; a one-host graph needs
  neither) from the candidate render and its stack graph. It places the Core
  host bootstrap `terramate.tm.hcl` at `.stackkit/runtime/terramate.tm.hcl`
  and every stack's `stack.tm.hcl` in that stack's graph runtime root, creating
  a missing workload, edge or federation root directory. A missing core root
  fails before any write, because only the runtime executor may create a core
  root (it writes the root marker the executor-state checkpoint requires).
  Files are rewritten only when their bytes differ, and the
  `stackkit.terramate-host-manifest/v1` record at
  `.stackkit/terramate-host-manifest.json` (schema
  `schemas/stackkit-terramate-host-manifest-v1.schema.json`) lists the stack
  IDs, roles, roots and file digests without time or machine facts, so its
  digest is known before any write.
- Terramate process: the release-packaged binary (`STACKKIT_TERRAMATE_BINARY`
  overrides it; there is no PATH fallback) runs with the working directory in
  the runtime tree, `GIT_CEILING_DIRECTORIES=<workspace>/.stackkit` so a
  surrounding Git work tree does not become the project root, the packaged
  `tofu` directory first on `PATH`, `CHECKPOINT_DISABLE=1`, and the host
  OpenTofu CLI overrides removed (`tofu.OfflineInheritedEnv`). A root that
  holds the executor's `stackkit.tofurc` gets it as `TF_CLI_CONFIG_FILE`.
- Create: `advancedchangeset.DeriveTerramateScope` maps the artifact diff to
  stacks through the candidate graph. A changed, added or removed artifact
  affects every stack whose module owns it (the candidate owner, or the
  baseline owner for a removed path); plan-owned artifacts and modules that
  are not stacks affect none. The record (`stackkit.advanced-change-set/v2`;
  the strict verifier rejects unknown fields, so v1 moved) adds
  `affectedStacks`, ordered by `terramatestackgraph.RunOrder` (the global
  graph order across hosts), and `terramateHostManifestSha256`. Apply
  re-derives both from the fresh renders and treats a difference as a stale
  change set.
- Admission memory: create, apply and Advanced reconcile resolve baseline and
  candidate through one embedded authority (each in its own authority scope,
  because both carry the same Stack ID) and render them one after the other.
  The pre-lock admission keeps only a comparable fingerprint, so the locked
  revalidation never holds two authorities. Each heavy phase first emits an
  `advanced.change-set.prepare.<phase>` `started` rollout event
  (`resolve-baseline`, `resolve-candidate`, `render-baseline`,
  `render-candidate`, and `diff` for create), so a stuck or killed run names
  its phase. Peak memory stays within one `generate`.
- Apply: the mutation skeleton stays generate, plan, apply, verify through the
  target release (see Release authority) under the lifecycle journal. Missing packaged Terramate or
  OpenTofu fails before the checkpoint. After the target apply succeeds and
  before verify starts, the parent process materializes the host project,
  requires `terramate list --run-order --tags stackkit` to equal the host run
  order of the graph (and the affected local stacks to follow it), then runs
  `terramate run --no-recursive --tags stackkit -- tofu plan
  -detailed-exitcode -input=false -no-color` in each affected local stack
  root in that order. Terramate reports every failed command as exit 1; the
  stack's OpenTofu exit code is taken from its `(in /<stack>): exit status
  <n>` line. The plan only reads state; the apply already ran through the
  runtime executor.
- Application lifecycle: apply and `stackkit drift reconcile --mode advanced`
  open the application lifecycle of every workload of the candidate plan in
  the `upgrade` stage as `stackkit.upgrade`, before the rollback checkpoint.
  Both run the public upgrade mutation (release-authority target, mandatory
  checkpoint, lifecycle journal) and bind the same `snapshot-anchor`,
  `upgrade-result` and `owner-observation` evidence as `stackkit upgrade`. The
  standalone lifecycle admits only operations of the standalone operation
  registry, so the upgrade stage lists `stackkit.upgrade` alone (95p5 removed
  the Advanced operation IDs that #554 had added to the CUE only); the Advanced
  sub-kind, capability operation and change set are recorded in the
  `upgrade-result` evidence, the `stackkit.advanced-mutation/v1` result under
  `.stackkit/advanced/results/`. A workload the change set adds (for example
  Files) starts its lifecycle history with that upgrade operation.
- Release authority: change-set apply, Advanced reconcile and coordinated
  rollback target the release that is already executing, so the running
  executable can be that release. When the target is the running release
  (exact tag and platform) and the workspace holds no release cache entry for
  it under `.stackkit/releases/<kit>/<version>/<os>-<arch>/`, the authority is
  the running executable itself: its path comes from `os.Executable()`, its
  sha256 is re-checked before each use (a replaced binary fails closed), the
  lifecycle journal records it as `authority: running-executable` with the
  executable digest and no archive digest, and the joined children run that
  same binary. This is the normal case on a Techstack-managed host: `stackkit
  init` creates no release cache, and the Techstack Agent already verified
  the pinned release, archive, index and executable before starting the CLI
  (ADR-0031 section 5). The rollback checkpoint follows the same rule for the
  prior release: when the applied release is the running one and no cache
  entry exists, the executor-state snapshot captures the running executable
  (and the `stackkit-server` beside it) with release authority
  `running-executable` instead of an attested archive identity. An existing
  cache entry must still verify, and a cross-version target (`stackkit
  upgrade --to`, or any target that is not the running release) always
  requires the Sigstore-verified workspace release cache and fails closed
  without it. The `stackkit.advanced-mutation/v1` result records the choice
  as `releaseAuthority` (`kind` `running-executable` or
  `workspace-release-cache`, `version`, `platform`, `sha256`). A target
  verify of a running-executable target reports no release receipt; the
  lifecycle join binds the verifying executable by digest instead.
- Results: per stack `converged` (exit 0), `drifted` (exit 2 after apply),
  `failed` (the plan could not run), `pending_root` (no `main.tf` in the root)
  or `other_host` (the stack belongs to another host and runs through that
  host's execution channel). `pending_root` fails the change set for core and
  workload stacks and is tolerated for edge and federation stacks, whose roots
  the executor does not materialize yet. Any drifted, failed or required
  pending stack fails the change set with
  `advanced_change_set_not_converged`, `failedPhase: advanced-terramate`, and
  the same rollback path as a failed verify. The
  `stackkit.change-set-result/v1` report (schema
  `schemas/stackkit-change-set-result-v1.schema.json`) is
  `data.changeSetResult` of the `stackkit.advanced-mutation/v1` result on
  success and failure, and each step emits `advanced.change-set.materialize-host`,
  `advanced.change-set.run-order` and per-stack `advanced.change-set.converge`
  rollout events.
- Not yet covered: cross-host dispatch of `other_host` stacks (Techstack,
  P2). A failed change set whose checkpoint belongs to a `terramate` install
  rolls back per stack ("Coordinated rollback across stacks (Stage 1)"); every
  other checkpoint keeps the existing rollback, which after the target apply
  requires explicit upgrade recovery. Per-stack drift outside a change set is
  described in the next subsection.

### Advanced drift per stack (Stage 1)

ADR-0045 section 2 and section 3 give Advanced Mode per-stack drift detection
and reconciliation; section 5 keeps container-level changes under the native
drift detection. `stackkit drift detect` combines both in one
`stackkit.drift-report/v1` document (schema
`schemas/stackkit-drift-report-v1.schema.json`).

- Native part: the existing fields (`mode`, `generationTarget`, `hasDrift`,
  `planHash`, Owner binding, `runtime`, `subjects`) are unchanged for every
  target, and `hasDrift` still means native drift only. Under `compose` and
  `opentofu` the report is byte-identical to the report before this section
  existed.
- Per-stack part, `terramate` target only: the command renders the verified
  plan, derives the local host layout through `internal/terramatehost` (Owner
  custody Site and node) and hands it to `internal/advanceddrift`. When every
  core root holds its `main.tf`, the host project is materialized as for a
  change set (same files, same manifest, rewritten only on byte change) and
  every stack of the host runs, in host run order, `terramate run
  --no-recursive --tags stackkit -- tofu plan -detailed-exitcode -input=false
  -no-color` with the change-set process environment. The plans run under the
  exclusive lifecycle lock, only read OpenTofu state, and never apply. A
  missing core root leaves the project unmaterialized and every stack
  `pending_root`; missing packaged binaries make every stack `failed`.
- Entries: `stacks[]` holds `stackId`, `role`, `moduleRef`, `siteRef`,
  `nodeRef`, `runtimeRoot`, `status` (`converged` exit 0, `drifted` exit 2,
  `failed` any other result, `pending_root` no `main.tf`), `planExitCode`
  (taken from Terramate's `(in /<stack>): exit status <n>` line), `summary`
  (`add`, `change`, `destroy` from the `Plan:` line, zero for `No changes.`),
  `durationMs` and `detail`. The report adds `mode: advanced`, `stackId`,
  `detectedAt` and an overall `status`: `drifted` when a native subject or a
  stack drifted, otherwise `unknown` when a stack failed or a core or workload
  root is pending, otherwise `clean`. A pending edge or federation root does
  not block `clean`, as in a change set.
- Streaming: every stack entry is also one `advanced.drift.stack` rollout event
  (status is the stack status; attributes carry the entry fields), so
  Techstack can map each stack to one drift subject.
- Reconcile: `stackkit drift reconcile --mode advanced` keeps the capability,
  candidate and Owner-signed change-set admission of `runAdvancedMutation`;
  a denial happens before any Terramate or OpenTofu process. After the
  mutation succeeds the command observes the full drift report again and
  returns it as `data.driftReport` next to the unchanged
  `stackkit.advanced-mutation/v1` fields. A post-reconcile status other than
  `clean` fails the command; the applied change is not rolled back
  automatically. Standard reconcile is unchanged.
- Not yet covered: the `opentofu` target has no stack graph, so its wrapper
  roots have no per-root plan yet; the saved-plan `tofu show -json` resource
  diff is not captured; stacks of other hosts are reported by their own host
  (Techstack dispatch); automatic rollback of a non-converged Advanced
  reconcile belongs to P1.6.

### Advanced operations catalog

ADR-0031 section 5 has Techstack consume an exactly pinned StackKits release
plus versioned CLI JSON contracts. `internal/advancedcatalog` is the single
source of the `stackkit.advanced-operations/v1` catalog
(`docs/data/advanced-operations/latest.json`, schema
`schemas/stackkit-advanced-operations-v1.schema.json`), rendered by
`stackkit docs emit-advanced-operations` and checked by
`mise run docs:advanced-ops:check`.

- One entry per dispatchable operation: the capability operations of
  `internal/advancedcapability` (`terramate.change-set.create`,
  `terramate.change-set.apply`, `drift.reconcile.advanced`, `restore.drill`,
  `rollback.coordinated`) plus `advanced.trust.import`,
  `advanced.trust.inspect` and the read-only `drift.detect.advanced`.
- Each entry carries the exact argv after the program name with
  `{placeholder}` words, optional argv, requirements (capability and its
  operation ID, imported trust, Owner approval, candidate spec, change set),
  input contracts, the data payload per command-result status, rollout event
  phases with their statuses, admission per mode (standard `denied`, advanced
  `capability` for the capability operations) and `sinceRelease`, the first
  release whose CLI accepts the argv (`pending` before a release carries it).
  Only `available` entries may be dispatched; `rollback.coordinated` is
  `available` as `stackkit advanced rollback run` with `sinceRelease:
  pending` until a release carries it.
- `stackkit.command-result/v1` binds the data payload of every Advanced
  command and status to its schema (`allOf` of `if`/`then` on `command` and
  `status`); `stackkit.operation-denial/v1` and its reason codes are part of
  the catalog. A nonzero exit without an envelope is an unclassified failure.
- Drift guard: a command-package test resolves every available argv against
  the Cobra tree and its flags and validates writer output of every Advanced
  command against the schemas; the release archive validation runs the
  packaged `stackkit docs emit-advanced-operations --check` against the
  archived catalog.
- Pinning: the catalog and its schemas ship in every release archive. The
  release index does not name the catalog, because Techstack and installed
  StackKits CLIs decode `stackkits-release-index/v1` with unknown fields
  rejected; the index pins the archive digest, which pins the catalog.

### Coordinated rollback across stacks (Stage 1)

ADR-0045 section 1 makes OpenTofu state the execution state owner and section
3 lists coordinated rollback as an Advanced operation. `stackkit advanced
rollback run` is that operation (`rollback.coordinated`); the failure branch
of `stackkit advanced change-set apply` runs the same path.

- Admission: an offline capability that allows `rollback.coordinated`, the
  Owner-approved issuer trust, explicit `--owner-approve`, and the packaged
  Terramate and OpenTofu binaries, all before any lock, journal or runtime
  side effect. The capability is revalidated under the lifecycle lock.
- Target: `--to` is an executor-state snapshot ID, or a stored change-set ID
  that resolves to the checkpoint sealed before its apply (recorded by its
  rollback journal or its `stackkit.advanced-mutation/v1` result). The
  checkpoint is verified and must belong to a `generation.target=terramate`
  install: its captured stack graph and stack files give the checkpoint's
  host layout, and its `runtimeOpenTofu` roots give each stack's
  `terraform.tfstate`, `main.tf`, `compose.yaml` and workload `.env`
  (`upgradelifecycle.ExecutorStateStore.LoadRollbackCustody`).
- Plan (`internal/advancedrollback`): the current generation's host layout is
  compared with the checkpoint's by runtime root. Stacks of the current graph
  run first in reverse run order: absent from the checkpoint graph is
  `destroyed`; a root that differs from the checkpoint bytes is `restored`; a
  checkpoint root without an applied root on disk is `recreated`; equal bytes,
  or a stack the checkpoint graph has without a captured root, is `unchanged`
  (a stack the checkpoint ran natively is never destroyed). Stacks only the
  checkpoint graph has are then `recreated` in run order, because they run
  after the restored cores. The plan is persisted in the rollback journal
  `.stackkit/advanced/rollbacks/<snapshot>.json` before any runtime change.
- Execution per local stack, through `terramate run --no-recursive --tags
  stackkit -- tofu ...` in the stack root with the change-set process
  environment plus the root's Compose project name and, for a Core root, the
  native Compose interpolation environment:
  - `destroyed`: `tofu destroy -auto-approve -input=false`, whose wrapper
    destroy-time provisioner runs `docker compose down` without `-v`, so data
    volumes stay; then the root (and a workload's or module's project
    directory with its `compose.yaml` and `.env`) is removed.
  - `restored` and `recreated`: the checkpoint `.env`, `compose.yaml`,
    `main.tf` and `terraform.tfstate` are written back (a recreated root must
    still hold the executor's root marker), then `tofu apply -replace` on the
    wrapper trigger forces convergence, and `tofu plan -detailed-exitcode`
    must exit 0. The trigger is read from the restored `main.tf`
    (`terramatehost.ReplaceTriggerAddress`): the `terraform_data` resource
    whose name ends in `_up` when the root splits `up` from its lifecycle
    resource, so the trigger never runs `down`; otherwise the single
    `terraform_data` resource of an older root, whose replacement runs `down`
    before `up`. Restored state and payload alone plan as a no-op even while
    newer containers run; the explicit replacement is what makes the
    containers converge to the restored payload.
- Lifecycle: the rollback runs under an upgrade-kind lifecycle mutation whose
  checkpoint is the target (its own, or the failed change set's):
  `rollback-started` (plan), `rollback-generate` (the checkpoint StackSpec and
  Inventory restored through `ExecutorStateStore.RecoverWith` with
  `ReplaceAuthority` and `SkipOpenTofuRoots`, then a joined `generate`),
  `rollback-apply` (the per-stack execution), `rollback-verify` (a joined
  `verify --json` validated against the checkpoint plan hash, release and
  Owner binding), `rollback-succeeded`, status `recovered`. The running
  release regenerates and verifies; the checkpoint's captured executable does
  only when its release differs.
- Resume: every step's outcome is written to the journal. A second
  invocation with the same `--to` reopens the recorded lifecycle mutation,
  keeps the original plan and skips converged steps; a restored root whose
  forced apply did not converge keeps its written files and repeats the
  apply. An interruption inside the joined `generate` or `verify` child needs
  explicit upgrade recovery, because the child's one-use admission may be
  consumed.
- Result: `stackkit.rollback-result/v1`
  (`schemas/stackkit-rollback-result-v1.schema.json`) with the rollback ID,
  target checkpoint, change set, per-stack `{stackId, role, action,
  planExitCode, durationMs, status}`, execution order, authority, release and
  verify flags, the checkpoint sealed after a converged standalone rollback,
  and `converged` or `failed`. A failed change set carries it as
  `data.rollbackResult`. Rollout events: `advanced.rollback.resolve-target`,
  `.plan`, `.restore-authority`, `.generate`, per-stack `.stack`, `.verify`
  and `.seal`.
- The upgrade checkpoint seals for `opentofu` and `terramate` installs (see
  State custody under the OpenTofu runtime executor), so a change set on a
  `terramate` install seals its checkpoint before the target apply and the
  rollback reports `sealStatus` `sealed` after it seals the rolled back
  runtime.
- Native side steps: a `restored` or `recreated` stack runs the native
  Apply side steps around its forced apply, through the code the runtime
  executor runs around its own `tofu apply`
  (`advancedrollback.Request.Native`, wired by the command's
  `advancedRollbackNativeSteps`). A Core root runs the Core preparation
  after its checkpoint files are written (origin provisioner check,
  stackkit-server staging of the release being restored, taken from beside
  that release's executable) and the Core completion after the apply
  (stackkit-server recreation, step-ca reload, PocketID owner realization,
  the TinyAuth reconciling `up`); a workload root runs the workload
  completion (Wings recreation for the game node, readiness, origin backend
  record) after proving that its restored `compose.yaml`, `.env` and
  configuration files are what the checkpoint's workload bundle renders.
  The completion runs before the convergence plan; a failure fails the
  stack and a resumed rollback repeats both halves. Health probes stay with
  the joined rollback verify.
- Not yet covered: data volumes are not rolled back (the Kopia anchor stays
  available for an explicit restore), `other_host` stacks need Techstack
  dispatch, and there is no runtime evidence yet (P1.9).

### Bootstrap and configuration parity across execution paths

The product is a homelab whose owner is already signed in and configured in
every tool, not only running containers. Every bootstrap and configuration
step therefore runs the same native Go code on every execution path; ADR-0045
Stage 1 keeps container execution and state in OpenTofu and orchestrates the
native steps around `tofu`. Paths: native `compose` (S-F fallback), Standard
`opentofu`, Advanced `terramate` initial apply, an Advanced change set that
adds a workload, Advanced reconcile, coordinated rollback and restore
activation. A change set and Advanced reconcile both run the target apply as
a joined `stackkit apply` child (`advanced_change_set_apply.go:903`,
`runAdvancedMutation`), so they run exactly the executor the plan's target
selects; their column equals the `terramate` column plus the lifecycle
difference noted below.

Paths: `nh` is `internal/runtimeexecutor/nativehost`, `ot` is
`internal/runtimeexecutor/opentofu`, `cmd` is `cmd/stackkit/commands`.
"shared" means one registration serves every target
(`cmd/architecture_v2_product_runtime.go`).

| Step | `compose` | `opentofu` | `terramate` initial | change set adds workload | Advanced reconcile | coordinated rollback | restore activation |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Owner custody (email, username, PocketID trust) | `stackkit init`, before any Apply | same | same | same | same | restored with the checkpoint authority | restored with the backup |
| Basement: origin provisioner check, stackkit-server staging | runs (`nh/basement_core_os.go:174`) | runs (`ot/executor.go:100` via `PrepareCompose`) | runs (same executor) | runs (joined apply) | runs (joined apply) | runs (`nh/restored_root_side_steps.go`, `PrepareNativeComposeRoot`) | not run (below) |
| Basement: stackkit-server recreate, step-ca reload | runs (`nh/basement_core_os.go:195`) | runs (`ot/executor.go:143` via `CompleteCompose`) | runs | runs | runs | runs (`NativeComposeRootSteps.Complete`) | not run |
| Basement: PocketID owner realization and TinyAuth OIDC client | runs (`nh/basement_core_os.go:216`) | runs (same completion) | runs | runs | runs | runs | not run |
| Basement: TinyAuth reconciling `up` | runs (`nh/basement_core_os.go:223`) | runs | runs | runs | runs | runs | not run |
| Cloud: identity address check, stackkit-server staging | runs (`nh/cloud_core_os.go:94`) | runs (`PrepareCompose`) | runs | runs | runs | runs (`PrepareNativeComposeRoot`) | not run |
| Cloud: stackkit-server recreate, readiness, PocketID owner and TinyAuth client, reconciling `up` | runs (`nh/cloud_core_os.go:118`) | runs (`CompleteCompose`) | runs | runs | runs | runs (readiness by container state; probes in rollback verify) | not run |
| Cloud: public TLS, identity trust policy, host security, offsite backup | runs (shared, `cmd/architecture_v2_product_runtime.go:296-373`) | runs (shared) | runs (shared) | runs (shared) | runs (shared) | not applicable (not stacks) | not applicable |
| Cloud: public edge | runs (native owner) | runs (native owner, then contract root, `:359`) | runs | runs | runs | contract root restored, owner not re-run | not applicable |
| Basement: internal PKI, identity trust, home access, LAN DNS policy | runs (shared, `:313-382`) | runs (shared) | runs (shared) | runs (shared) | runs (shared) | LAN DNS served by the restored Core payload | Core payload started |
| Workload: `.env` secrets, config files, data directory render and persist | runs (`nh/standalone_compose_workload.go:126`) | runs (`ot/workload.go:64` via `PrepareWorkloadCompose`) | runs | runs | runs | checkpoint files restored and proven against the bundle | data restored |
| Workload: Wings recreate, readiness, origin backend record | runs (`nh/standalone_compose_workload.go:165`) | runs (`ot/workload.go:106` via `CompleteWorkloadCompose`) | runs | runs | runs | runs (`CompleteRestoredWorkloadCompose`) | readiness only (own loop, `internal/restoreactivation/docker.go:239`) |
| Workload: owner account and app onboarding (`internal/appsetup`) | runs after the converged Apply (`cmd/setup_automatic.go`, `runAutomaticOwnerSetup` from `cmd/apply.go`) | runs (same) | runs (same) | runs after the change set (`cmd/advanced_change_set_apply.go`) | runs after reconcile (`cmd/drift.go`) | the account lives in the restored application data; `stackkit setup` re-verifies | `stackkit setup` re-verifies |
| Workload: per-application OIDC client | not implemented on any path | not implemented | not implemented | not implemented | not implemented | not implemented | not implemented |
| Workload: household users | PocketID household group only (`internal/localowner/household.go`), not provisioned inside applications | same | same | same | same | same | same |
| Workload: Kopia source registration (`stackkit backup configure`) | target-neutral, resolves the Core unit of any target (`cmd/backup_native_v2.go:478`) | same | same | same | same | same | not applicable |
| Application lifecycle record | `stackkit.apply`, stage `install` (`cmd/architecture_v2_execution.go:977`) | same | same | `stackkit.upgrade`, stage `upgrade` (`cmd/advanced_change_set_apply.go:267`) | same as change set (`cmd/drift.go:224`) | lifecycle mutation journal, no application record | `stackkit.restore` |

- Owner setup runs automatically after the runtime converged, on every
  path. A top-level `stackkit apply` (standalone, or the Techstack-managed
  rollout), `stackkit advanced change-set apply` and `stackkit drift
  reconcile --mode advanced` end with `runAutomaticOwnerSetup`; a joined
  child apply leaves it to its parent. It runs, per workload whose Plan
  declares one on-demand native owner setup action, the code `stackkit
  setup` runs (`executeNativeSetup`), under the `setup` lifecycle mutation
  and recorded as a `stackkit.setup` operation. Credentials come from the
  private file the action's setup guide names when the owner or
  orchestrator placed one there before Apply; otherwise they are derived
  from the owner custody identity (email, username, display name) plus a
  generated password and written owner-only to that file, so `stackkit
  setup <workload>` repairs with the same account. Owner-account actions
  qualify (Files, Photos, Media, Smart Home, and the Vault invitation
  preparation); the game server (EULA, profile) and mail (mailbox, domain)
  need owner decisions the Plan does not hold and are reported
  `owner-input-required`. A workload with a succeeded setup is not set up
  again. A failure never undoes the converged runtime: it is an
  `owner-setup.workload` rollout event with status `failed`, a failed setup
  operation, and a warning naming `stackkit setup <workload>`; the next
  Apply retries it. Applying the Plan that selects Files is the owner's
  authorization of its first owner registration.
- The setup admits the project through `WithStandaloneComposeHTTP`, which
  re-renders the bundle and requires the persisted `compose.yaml`, `.env`
  and configuration files to match. The OpenTofu root writes the identical
  Compose bytes (`local_file`, mode 0600), so the same admission holds after
  every path. `TestOwnerSetupAdmitsTheWorkloadOnEveryExecutionPath` creates
  the Files owner through a stubbed Cloudreve API after the native Apply,
  after the OpenTofu executor halves and after a coordinated-rollback
  restore; `TestAutomaticOwnerSetupRunsOwnerAccountActionsFromTheOwnerIdentity`
  covers the selection and the derived private credentials.
- A workload added by a change set starts its lifecycle history with
  `stackkit.upgrade`, not `stackkit.apply`; its `setup` operation follows
  it as after a Standard install.
- Remaining gaps, common to every target and therefore not parity gaps:
  restore activation starts Compose runtimes with its own `up` and readiness
  loop and runs neither the Core completion (owner realization, TinyAuth
  rebind) nor the workload completion (Wings recreation, origin backend
  record); no application has its own PocketID OIDC client (only TinyAuth
  and the step-up client register one, `internal/localowner/service.go`).
  Each needs its own slice.

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

The governed sources currently include `identity.deviceEnrollment`,
`network.routes`, `host.bootstrapRuntime`, `storage.hostRoots`,
`storage.backupRoot`, and the closed `backupPolicy`. They do not
expose those objects verbatim: enrollment uses a public policy shape whose
lifetime key cannot alias the secret namespace; routes exclude TLS credentials,
provider authority, and undeclared access fields; and Core host bootstrap
receives only the exact bootstrapped-Docker identity plus its container data
root and declared local StackKit storage roots. Host platform selection, host
settings, registry mirrors, external/NFS details, endpoints, credentials, and
provider lifecycle never cross that renderer seam. Home backup-target narrows
this further through `storage.backupRoot`: only one safe local path and its
`local` driver marker cross the boundary; repository, retention, restore,
external/NFS, endpoint, and credential custody remain external. Arbitrary JSONPath,
module/result-derived sources, kit/context conditionals, secret targets,
undeclared targets, type/cardinality coercion, and missing required sources fail
closed in CUE, the compiler, the persisted-plan verifier, and the renderer
parser. Existing coarse `planInputs` remain a compatibility surface while
concrete modules migrate field by field; they are not permission to add new
whole-plan projections.

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

Completed recovery custody may leave a deliberately retired tombstone under
`.stackkits-control/retired-output-stages/<transaction-id>` and
`.stackkits-control/retired-output-journals/<transaction-id>`. The explicit
`stackkit output-transaction gc-retired --transaction-id <id>` command first
reports one exact, read-only action. Mutation requires repeating that action
with `--apply --action <reported-action>` and removes at most one namespace:
the retired stage first, then the complete immutable journal after the stage is
gone. It never scans active `output-journals`, generated output, devices,
providers, or release state. Missing, partial, malformed, foreign, or symlinked
retired authority fails closed; a completed journal remains durable until stage
GC and its parent metadata sync have succeeded.

Native v2 Apply continues from that same held transaction and output lock into
the service-owned product executor registry. CLI and API callers can provide
only current-resolution context, the already-held filesystem capabilities,
component versions, and the canonical evidence bytes. They cannot select an
adapter, executor identity, capability set, producer trust root, or result path.
The verified result is rehashed and stored idempotently by content hash under
the plan-owned `.stackkit/apply-results/` directory.

The producer-facing Apply-evidence request contains only facts that can be
established before the selected executor mutates state: exact Host
requirements, opaque Secret-custody/materialization requirements, and explicit
`apply`-phase evidence gates. Workload realization, provider-owner realization,
runtime state, `verify`/`release` evidence, and Health are not preconditions for
their own execution. They remain bound by the complete Apply-requirements hash
and are accepted only from the exact post-execution result/readback boundary.
This prevents an executor from being authorized by a circular claim that it
has already completed successfully.

Apply-evidence public trust is operational authority and therefore does not
belong in CUE, StackSpec, ResolvedPlan, generated output, or provider config.
Only the product service reads the fixed `stackkit/apply-producers.json` file
beneath the OS per-user config directory. The canonical document contains an
exact Ed25519 producer identity/public key and sorted allowed requirement kinds;
the service intersects that scope with the verified plan to derive exact receipt
IDs. Product trust scopes can name only the precondition kinds `host`, `secret`,
and `evidence`; they cannot grant runtime, workload, provider-owner, or Health
authority. Private signing material is never accepted or stored by this contract.
Missing trust config means empty trust and fails closed. This trust-store seam
does not itself graduate a signer or make a complete Kit Apply-ready.

A product integration may fix one provider-free evidence collector when it
constructs the embedded service. StackKits constructs the canonical
`applyevidence.CollectionRequest` from `kombify-go-common` `2ef87ff`: it binds
the shared exact expectation request, manifest hash, product-selected executor
identity, one trusted UTC instant, and its own deterministic digest. The same
value-only wire contract can therefore be consumed by a TechStack/host/device
integration without importing or copying StackKits-internal DTOs. StackKits
validates the complete collection digest before invoking the collector and
passes a defensive copy. Host inspection and private signing material stay
inside that integration. When such a collector is installed, request-supplied
evidence bytes are forbidden; its returned canonical bundle is still verified
against the service-owned public trust at the same UTC instant before Apply is
authorized. A product service without a collector retains the separately
signed external-bundle path. No collector can select an executor, change trust,
alter the plan, or turn postconditions into preconditions.

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
| `stackkits-home-device-authority-policy-manifest` | Basement, Modern | Home control authority | `stackkits-home-device-authority-enforcer` | Configure device enrollment, credential issuer, and credential revocation policy |
| `stackkits-basement-identity-trust-policy-manifest` | Basement | Home control authority | `stackkits-basement-identity-trust-enforcer` | Device, human, and workload verification under Basement trust |
| `stackkits-cloud-identity-trust-policy-manifest` | Cloud | Cloud Sites | `stackkits-cloud-identity-trust-enforcer` | Configure Cloud human/workload issuers plus device, human, and workload verification; never device enrollment/issue |
| `stackkits-modern-home-identity-trust-policy-manifest` | Modern Home | Home control-authority node | `stackkits-modern-home-identity-trust-enforcer` | Home verification and outbound-only publication of verification-key/revocation references |
| `stackkits-modern-cloud-identity-verifier-policy-manifest` | Modern Cloud | Cloud worker nodes | `stackkits-modern-cloud-identity-verifier-enforcer` | Inbound verifier-state application and Cloud verification; issuance, enrollment, signing, and reverse distribution are denied |
| `stackkits-home-access-policy-manifest` | Basement, Modern | Home Sites | `stackkits-home-access-enforcer` | LAN/local ingress decisions and privileged step-up; LAN presence is not identity |
| `stackkits-local-autonomy-policy-manifest` | Basement, Modern | Home control authority | `stackkits-local-autonomy-enforcer` | Link-loss policy, forbidden cross-Site denial, and preserved local control |

Every row is bound to its exact generated policy artifact set, required Health
ref, and required evidence ref in CUE. A future implementation must replace the
unbound requirement with a typed executable owner and fresh evidence in the same
change; deleting the blocker or attaching a generic no-op adapter is invalid.

#### Basement native-Apply graduation map

The v0.8 single-node Basement plan has a complete local Product Apply
composition. Modules outside that closed default graph remain contracts until
their separately typed runtime owners and evidence paths exist:

| Module | Current truth | Required independent runtime owner |
| --- | --- | --- |
| `security-baseline` | Exact node-local host adapter exists as the first bounded pilot | Retain the script/contract-bound host adapter and product evidence path |
| `stackkits-core-host-bootstrap` | Exact node-local, provider-free pilot prepares only declared local StackKit storage roots and observes an already bootstrapped Docker runtime | Bind the adapter to an authenticated execution channel before product registration; expand only through new typed operations and evidence |
| `stackkits-core-topology` | Declarative shared Home/Cloud site-topology authority; it selects no runtime module and performs no host or provider lifecycle work | Keep site/node intent in the resolved plan and lower only explicit downstream owners |
| `stackkits-service-catalog` | Declarative catalog authority selected directly into the plan; it has no module or runtime target | Keep workload/service selection in CUE and let explicit runtime owners consume the resolved catalog |
| `stackkits-access-policy-contract` | Shared declarative access-policy prerequisite; it does not enforce Home or Cloud access | Bind kit-specific enforcers to the exact resolved policy and their own Health/evidence |
| `stackkits-storage-data-policy`, `stackkits-storage-allocation`, `stackkits-workload-data-binding` | Shared plan-only storage authority. Every Application Kit names its exact persistent/cache allocation and data binding in ResolvedPlan; the modules render no artifact and perform no mount, migration, backup, or provider operation. | Keep allocation and placement facts in CUE/ResolvedPlan; let bounded workload and lifecycle owners consume them without product-specific compiler or renderer branches. |
| `stackkits-workload-runtime-contract` | Shared delivery interface required by workloads; it selects no engine and is not a Basement, Cloud, or Modern runtime | Let each concrete workload module bind its exact artifacts to an explicitly registered runtime adapter; do not recreate a kit-level runtime umbrella |
| `stackkits-basement-core-runtime` | Required Basement-only generation and Apply owner. The CUE catalog fixes the complete single-node graph for Traefik, socket proxy, PocketID, TinyAuth, step-ca, Coolify/PostgreSQL/Redis/realtime, and Hub. Exact hash-bound Compose/OpenTofu artifacts carry pinned images, persistent backup-marked volumes, local-custody references, container Healthchecks, and module verification probes. The local Product Apply composition binds this graph to exact Site/node/channel custody, realizes PocketID/TinyAuth Owner binding, and records local signed evidence. An empty workload set or an unregistered/tampered renderer is rejected. | Keep the v0.8 graph single-node and local-authority bound; graduate HA and optional workloads independently in v0.9. |
| `stackkits-immich-runtime` | Concrete apply-ready workload owner pinned to the full Immich v2.7.0 server, machine-learning, PostgreSQL, database-init, and Valkey graph. Its provider-neutral, target-bound bundle carries immutable image digests, dependencies, internal network membership, opaque secret slots, persistent/cache volumes, backup intent, Health declarations, and the exact compiler-selected route/TLS descriptor. The shared runtime-executor SPI accepts only this closed bundle and requires apply receipt plus full component/route readback. Product Runtime factories bind the complete selector for Coolify, Komodo, or standalone Compose plus Site, node, channel, catalog hashes, agent authority, and Health contract. | Keep provider/PaaS lifecycle external for Coolify/Komodo; use only local Owner custody and the fixed Docker Compose capability for standalone delivery. |
| `stackkits-cloudreve-runtime`, `stackkits-vaultwarden-runtime` | Concrete apply-ready Files and Vault workload owners use the exact image ref/digest pairs in the CUE catalog. Vault rendering, setup and execution consume its generated image authority. Each has a distinct closed application-adapter bundle, parser, executor, Product factory, and three adapter registrations, while both select the unchanged common Application Lifecycle and shared storage/data/backup/recovery modules. | Retain workload-specific validation at the artifact/adapter edge and reuse the common compiler, lifecycle, state and State Console. |
| `stackkits-coolify-runtime` | Workload-scoped generation-ready adapter handoff distinct from the Basement core's locally installed Coolify service. Its node-bound handoff accepts workload-bundle v2 with `apply`/`observe`, external credential/provider-lifecycle custody, and mandatory digest, runtime, route, and Health readback. | Connect authenticated Coolify operations through the shared executor boundary; keep external provider/server lifecycle outside this artifact. |
| `stackkits-standalone-compose-runtime` | StackKits-owned no-PaaS application adapter. It persists owner-only digest-pinned Compose and environment files, resolves only signed local secret custody, attaches the entry service to `stackkit-basement-core`, emits exact Traefik host/TLS labels, and proves service/image plus loopback HTTP readback. | Keep Docker/server lifecycle outside the adapter; do not promote its fixed Compose capability into generic command, path, socket, or host authority. |
| `stackkits-komodo-core-runtime`, `stackkits-komodo-periphery-runtime` | Explicit Komodo alternative split into one workload adapter/API authority on Control Plane members and one typed Periphery node-agent on Control Authority Site workers. The generated node-bound contracts require external mutual-key custody, outbound TLS 1.3, executor-mediated host execution, and digest/Health/runtime/route/agent-registration readback without carrying endpoints, credentials, sockets, provider lifecycle, or general host/LAN authority. | Implement authenticated Core operations and Periphery onboarding in the external adapter owner, bind exact endpoint/key custody there, and prove every registered worker and workload artifact before product Apply registration. |
| `stackkits-basement-compose-runtime` | Optional Basement-only generation contract selected only by explicit capability intent; it is not part of the Kit identity. Its sole Product factory is restricted to the pinned socket-proxy unit on one exact Home Site/node/channel and cannot discover Docker. | Supply an authenticated finite Compose Operations owner only for an exact observed Docker daemon; keep real projects/workloads under their concrete workload owners rather than promoting this helper into a generic Kit runtime. |
| `stackkits-backup-core-contract`, `stackkits-backup-source`, `stackkits-snapshot`, `stackkits-restore`, `stackkits-recovery` | Shared plan-only backup lifecycle authority. The exact backup-enabled persistent allocations flow through `backupSource`; snapshot, staged restore with safety snapshot, and recovery bind to the existing common Application Lifecycle operations, phases, durable state, Owner approval, and evidence. These contracts render no artifact and own no credential, target, provider lifecycle, or multi-server orchestration. Photos/Immich, Files/Cloudreve, and Vault/Vaultwarden consume the same chain. | Keep the v0.12 Application Lifecycle as the only execution/state owner; add authenticated target-specific backup implementations only behind its existing bounded operation boundary. |
| `stackkits-secrets-recovery-contract`, `stackkits-observability-evidence-contract`, `stackkits-lifecycle-update-contract` | Three distinct non-executable shared contracts; none creates a runtime target, artifact, Health claim, evidence claim, or host operation. Drift-observation intent remains separately normalized as the closed provider-free `driftPolicy` in every ResolvedPlan. | Bind kit-specific secret recovery, drift observation/reconciliation, telemetry, and update owners independently without recreating a Core executor umbrella. |
| `stackkits-home-backup-target` | Exact node-local Home Control Plane adapter observes the CUE-declared prepared backup root | Retain the observation-only boundary; add backup jobs, repository lifecycle, retention, and restore as separate typed owners |
| `stackkits-monitoring-agent-runtime` | Optional node-local collector intent plus one exact remote-only Product Runtime target per selected node. The catalog binds apply/reconcile/verify/evidence operations and post-Apply Health to the same artifact/Site/node without carrying endpoints, credentials, provider details, sockets, or management addresses. | Supply the authenticated external Operations implementation through an admitted execution channel; keep collector/backend lifecycle and credentials outside StackKits. |
| `stackkits-home-device-authority-policy-manifest` | Policy JSON plus exact unbound-owner requirement; isolated typed configuration adapter exists | Bind an authenticated Home authority backend and product registration only with local pairing, possession proof, revocation, and fresh exact-policy readback |
| `stackkits-basement-identity-trust-policy-manifest` | Policy JSON plus exact unbound-owner requirement; isolated typed verifier adapter exists | Bind an authenticated operations backend and product registration only with fresh device/human/workload verifier readback; no enrollment, issuance, signing, or credential material enters the adapter |
| `stackkits-home-access-policy-manifest` | Policy JSON plus exact unbound-owner requirement; isolated typed adapter exists | Bind an authenticated operations backend and product registration only with fresh exact-policy readback; LAN presence never becomes identity |
| `stackkits-local-autonomy-policy-manifest` | Policy JSON plus exact unbound-owner requirement; isolated typed adapter exists | Bind an authenticated operations backend and product registration only with observable link-loss and local-control evidence |

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
fail closed. The dispatcher contains no transport implementation or
credentials. A product integration may fix the shared provider-neutral
`runtimeapply.Journal` SPI at construction. The sealed parent request digest is
the operation identity; each re-sealed child digest is one fenced CAS step.
Completed exact child results replay without repeating their executor, failed
steps can resume, and an unresolved running step fails closed for the journal's
abandoned-operation policy. Execution remains serial and no automatic rollback
is inferred.

Multiple executable owners on the same node are a separate routing dimension,
not another execution channel. A channel child can therefore be a service-owned
`OwnerRouter`: its construction binds the complete canonical `RuntimeTarget`
(including every contract, workload, artifact, access, Site, node, and channel
field) to one typed executor. It accepts routes for exactly one Site/node and
one opaque channel, partitions matching Health/access/artifact authority, and
re-seals every child request before invoking any owner. Matching only an owner
name, module, or requirement ID is forbidden. The outer channel dispatcher and
inner owner router compose without learning an endpoint, credential, provider
lifecycle, lease, generation, or transport. Journaled routing records the outer
channel operation and the inner owner operation separately, so recovery cannot
repeat a verified successful owner merely because a later owner or channel
failed. Compensation is a closed per-route declaration (`none` or `explicit`);
execution of an explicit compensation remains the owning integration's
separately receipted operation and is never guessed by StackKits.

`ProductRuntimeOwnerRegistry` is the next product-side admission boundary. Its
immutable construction maps one closed CUE/catalog selector either to one
integration-owned local factory or to an explicit `remote-only` registration;
a request can never contribute a factory or executor. A remote-only
registration intentionally carries no local Operations dependency or success
stub. Construction also requires one service-owned execution-channel
factory and fixes one valid root executor identity before authorization. The
channel DTO and factory/admission/local-builder interfaces are the canonical
`runtimeexecutor.ExecutionChannel*` contract from `kombify-go-common`
`791a699`; StackKits keeps Product-prefixed aliases only for source
compatibility. The shared request validates one opaque channel and its exact
single-Site/single-node Runtime+Health closure before the service-owned factory
can observe it. This lets TechStack or another authorized control service
implement remote routing against the same value contract without importing
StackKits internals or copying its Product DTOs. The registry implements the
shared Executor boundary itself; a sealed request with
any other root identity fails before factory or channel admission.
Preparation consumes the already sealed shared request, requires a registration and
exact Health owner for every target, rejects a channel spanning multiple
Site/node authorities, and admits every exact channel/Site/node scope before
preparing any typed owner. The returned immutable channel admission receives a
one-shot lazy builder for the channel-local `OwnerRouter`: explicit local
execution first requires every selected registration to have a real local
factory and otherwise fails before preparing any factory. An authenticated
remote transport returns its own executor without constructing local owners or
requiring their Operations dependencies. Direct in-process execution is
therefore an explicit adapter choice, never an inference from an opaque channel
ref. Both callbacks
receive defensive provider-free request data; repeated local construction,
ignored local-construction errors, panics, typed nils, and missing executors
fail closed. Endpoint, credential,
transport configuration, provider lifecycle, lease, and generation authority
remain private to the service-owned channel implementation and never enter the
StackKits or shared DTO.

Remote integrations do not reconstruct those selectors from documentation or
from an Apply request. `ProductStaticRuntimeOwnerCatalog` exposes a fresh
value-only descriptor projection for every stable static Product factory; its
typed ID is the exact CUE/catalog-owned owner ref, and
`NewProductRemoteStaticRuntimeOwnerRegistrations` resolves only an explicit
service-owned allowlist. Blank, non-normalized, unknown, or duplicate IDs fail
before Registry or channel construction. The Immich selected-PaaS owner remains
separate because its selector is incomplete without the exact service-owned
adapter ref and adapter-module ref; its remote constructor requires both and
adds no Operations dependency. The catalog carries no target, channel,
endpoint, credential, provider resource, lease, generation, or mutation
authority.

Cross-repository integrations consume that selector truth through
`pkg/productruntime`, not by importing `internal/architecturev2`. The public
package aliases the canonical owner ID/selector/descriptor and the shared
go-common execution-channel, Apply-evidence Collector, Journal, and opaque
recovery-custody interfaces, then delegates every catalog and selected-PaaS
projection back to the internal CUE/catalog authority. `NewComposition` is the
external construction root: it fixes an explicit remote-only owner allowlist,
root executor identity, channel factory, Collector, Journal, and Recovery
store before any resolution. `ApplyPrepared` and `ReconcilePrepared` accept
only an authenticated authority scope, an already-generated workspace, the
current StackSpec/Inventory, and an exact recovery digest when applicable.
They re-resolve through the embedded CUE authority, require the persisted plan
to be byte-identical, check compatibility/readiness and host-conformance
freshness, acquire StackKits' held output lock, and call the internal one-shot
Apply/recovery capability without exposing it. Caller evidence and implicit
local channels are absent from the public request shape. StackKits validates
evidence and recovery bytes on both sides of the shared custody seams and
returns only a hash-bound provider-neutral result. Endpoint selection,
observation implementation, signing keys, transport, credentials, provider
lifecycle, leases, generation, discovery, retry policy, and durable storage
remain private to the consuming service behind those interfaces.
If durable execution requires continuation, the facade projects only a typed
`ReconcileRequiredError` with the opaque exact request digest; internal child
steps and provider-native state remain behind Journal/recovery custody.

This registry is available in a journal-required form which fixes the same
integration-owned SPI across both dispatcher levels. A separate
product-service constructor fixes the root identity, complete owner
registration set, execution-channel factory, and Journal together; only that
configured service selects the multi-owner registry during
`ExecuteProductApply`. The ordinary embedded product service retains the
single-owner pilot for compatibility integrations, but production
`stackkit apply` no longer selects it.

The standalone CLI constructs the v0.8 single-node Basement Product Apply
authority from local custody created by `init --owner-source=local`. It loads
the signed Owner/runtime binding, derives exactly one Site/node/execution
channel, installs a local Owner registry, uses workspace-bound file
Journal/recovery custody, and collects evidence with the local private key.
The explicit `--local-site`, `--local-node`, and
`--local-execution-channel` flags are equality-checked overrides, not routing
or authority injection. `stackkit generate` accepts `--local-site` and
`--local-node` for the same inventory attest binding. Caller-provided evidence
and foreign channels are rejected.

That authority is deliberately host-local and provider-free. It can execute
only the exact CUE-selected Basement owners with registered implementations;
it cannot create a server, discover provider credentials, manage leases, or
claim provider/PaaS lifecycle. Techstack may invoke the same published CLI as
an optional Orchestrator UI, but cannot inject an executor, mint evidence, or
expand the resolved operation set. Multi-node/hybrid channel authority remains
a v0.9 concern.

Architecture v2 workload removal is a distinct destructive protocol, never a
negative Apply. `stackkit remove --workload <ref>` first verifies the current
Plan/generation/Apply closure and loads the exact sealed Shared request retained
by Apply recovery custody. Recovery expiry limits retry authority but does not
erase this historical record of what crossed the channel. StackKits reduces
that request to exactly one workload target and its referenced executable plus
selected-adapter artifacts, re-seals the child, and signs a fresh five-minute
authorization with established local Owner custody. The digest-pinned Standard
execution process receives
`stackkit.standard-workload-removal-request/v1` and must return
`stackkit.standard-workload-removal-result/v1`; success requires selected
runtime-owner readback of `absent` bound to the exact requirement, instance,
and applied artifact digest. The content-addressed request/result pair is local
Owner evidence. An opt-in `stackkit.workload-removal-evidence/v1` projection
can cross a pinned execution channel without exporting applied artifact
content; it is persisted content-addressed beside the local request/result pair,
and its StackKits-owned parser checks the internal equality of workload, Site,
node, channel, runtime owner, artifact, authorization window, and terminal
result. Its digest provides canonical integrity, not independent authenticity
or a new Owner identity: consumers must authenticate the pinned StackKits
producer channel, compare expected custody identity, and may authenticate the
original authorization bytes only against an Owner key already established by
separate custody.

Legacy `--purge`/`--force`, raw provider deletion, unselected
targets, and generic command execution cannot enter this path.

The native v0.8 backup path reuses that local authority rather than creating a
parallel backup control plane. Before configure, status, or snapshot side
effects it binds the full Plan identity, manifest and generation receipt, the
current Apply result and owner-signed Apply receipt, and the exact
CUE-generated Kopia policy. The passphrase is held only in owner-signed local
custody and passed through a redacted stdin boundary. Snapshot operations are
journaled under `.stackkit/backups/`; their content-addressed
`stackkit.local-backup-snapshot-anchor/v1` records are owner-signed and
idempotent by operation ID. Techstack may display or dispatch this public CLI
contract, but cannot supply repository credentials, rewrite the policy, or
replace local PocketID/TinyAuth/step-ca authority.

Full and Lite Basement Core select the same Kopia implementation with distinct
generated source policies. Full retains its existing managed volumes and
legacy source identity. Lite selects only its PocketID, step-ca and TinyAuth
volumes, plus the selected backup-enabled application volumes. Its policy has
a distinct artifact path and explicitly identifies the Lite Core module;
admission binds the policy to that exact Core runtime. Neither profile silently
includes volumes belonging to the other. Both reuse the same application
dependency graph, writer custody, snapshot and restore lifecycle.
Restore activation binds the complete source volume set to the selected Core
and standalone applications before cutover. Missing application volumes or
additional foreign volumes fail admission. Full and Lite share the runtime
owner and verifier, which validate the selected profile's exact Compose output
and health requirements.

Upgrade recovery follows the executor that actually performed Product Apply.
For the v0.8 Basement default this is Compose, not the alternative OpenTofu
renderer. The internal `stackkit.executor-state-snapshot/v1` store already
provides private content-addressed blobs, owner signatures, an operation marker
as final commit point, an immutable offline-verified installed-release proof,
byte matching of the recovery executable, and re-verification of the exact
persisted Kopia anchor plus the current PocketID Owner runtime binding. CAS
objects are atomically published under one non-blocking store lock and their
directory hierarchy is durability-synced where the platform supports it.

The public upgrade checkpoint reaches this store through the sealed
current-state authority constructor. It re-runs the current
Plan/Generation/Apply authority gate and binds every captured artifact to its
verified manifest and receipts before capture. Full and Lite select their
Compose and source-policy artifacts from the exact Plan-owned runtime; the
checkpoint never substitutes a Full artifact for Lite. New snapshots retain
the selected module and artifact identities. Historical snapshots without
profile fields remain Full-only compatibility data. These are implementation
and local verification claims; the final exact-candidate upgrade and rollback
on a test host remain pending. OpenTofu state is required only if a Product
Apply selects an OpenTofu state owner; unsupported executor targets fail before
runtime side effects rather than recording empty or unrelated state.

`ProductApplyFileJournal` is the concrete provider-free durable option for a
workspace-bound product integration. Construction opens and validates the
held workspace without creating files; the first real Journal/recovery
operation lazily creates and verifies the private control directories. It then
stores one canonical private record per
exact operation beneath the held workspace control root, serializes each
operation with a non-blocking cross-process advisory lock, atomically replaces
and syncs `0600` state, and rotates a random fence on every matching resume.
The latest Begin therefore owns recovery and every older writer loses CAS
authority; final exact state replays without executing an owner again.
Noncanonical/corrupt records, foreign operations, stale tokens, invalid state
transitions, unsafe filesystem entries, and uncertain atomic writes fail
closed. A server integration may instead inject a DB-backed implementation of
the same Shared Journal SPI. Neither store kind selects an executor, transport,
provider, lease, credential, generation, or compensation action.

Operation state alone is not restart authority. A product-configured registry
therefore also requires a separate `ProductApplyRecoveryStore`. Immediately
before the first executor call, the Shared bridge seals a canonical recovery
capsule containing the already verified internal request, its exactly
reconstructed Shared request, the plan-owned output root, and the earliest
evidence expiry. The service-owned store must return byte-identical canonical
data before execution can continue; missing custody, panics, substitutions, or
conflicting capsules fail closed. The file Journal implements this opaque
custody beside (but not inside) the Shared Journal record; DB-backed products
can implement the same interface. The public reconcile entry reacquires and
revalidates the held workspace/output authority. An access-bound request cannot
reuse its old `authorization_time` as a new invocation instant: the service
creates a `stackkits.product-apply-continuation/v1alpha1` contract with freshly
collected and verified evidence, while retaining the original recovery lookup
identity and immutable plan, artifact, executor, and requirement bindings.

The registry's reconcile core can load one exact capsule
after process restart, reject expired/foreign authority, reconstruct only the
construction-owned routing tree, and resume a no-access request through the
same fenced Journal. A persisted successful owner is not prepared for
execution again merely because a later owner failed before restart. The public
`Service.ReconcileProductApply` entry encloses that core with a fresh
CurrentResolution, held workspace/output-lock reacquisition, and immutable
plan/manifest/receipt/artifact revalidation. Access- or backup-target-bound
continuations require the service-owned evidence collector. Their verification
instant is sampled after collection; admission samples the clock again after
loading recovery custody. Both the original recovery authority and the fresh
evidence must remain valid. Slow collection or custody reads therefore cannot
extend an expired authorization. The shared request retains its sealed
evaluation instant; the independently checked admission time is not a caller
override. An expired original capsule requires a newly authorized Apply.

The v0.8 CLI registers all factories and local transport required by the
closed single-node Basement default graph. It opts into the durable file
implementation and local evidence collector explicitly; other embedded
services do not do so silently. Optional modules, Modern/HA plans, and external
runtime adapters remain fail-closed until their own authenticated owners,
channels, compensation, and verification evidence exist.

A journaled partial failure is not collapsed into a generic Apply error.
`ProductApplyReconcileRequiredError` exposes defensive copies of every exact
validated `runtimeapply.Operation` and its `reconcile-required` Snapshot from
the error chain. For nested dispatch this includes the outer channel operation
and the inner owner operation; their relationship is exact because the inner
operation ID equals its outer child-step request digest. Successful steps carry
only their verified Shared Runtime result, failed steps carry one closed failure
code, and pending work remains explicit. The original typed executor error is
retained as the cause. Provider payloads, logs, endpoints, credentials, leases,
and handles cannot enter this evidence. Reconciliation execution itself remains
an explicit integration-owned operation, not an automatic retry or rollback.

### Approved RIL action and recovery boundary

The Architecture-v2 CUE catalog retains the closed approved-action facts:
primitive identity, operation class, risk, approval and grant requirements,
target scope, recovery relationship, evidence shape, executor identity, and
explicit provider/lease/credential/transport/command prohibitions. Module
extensions remain bound to their exact module and provider contracts.

Those facts are metadata, not a StackKits-hosted RIL executor. Techstack owns
action cards, admission, durable idempotency, dispatch, transport, runtime
inventory, and provider/server lifecycle. StackKits exposes no RIL HTTP route
or replay ledger. It executes its provider-neutral local behavior through the
CUE-generated StackAction contract or the standalone CLI, revalidating local
authority and producing local lifecycle evidence. Standard Mode remains
independent of Techstack.

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

Managed dispatch follows the same ownership boundary. Techstack selects and
transports a bounded operation; StackKits accepts only the generated
`/api/v1/internal/stack-actions/*` vocabulary. The former RuntimeAction and RIL
routes and their public Go projections are absent. Shared packages still owned
by StackKits are projected from the exact pinned module during OSS export.

## Major Containers

| Container | Location | Responsibility |
| --- | --- | --- |
| CLI | `cmd/stackkit`, `internal/*` | Standalone operator workflow: init, validate, generate, plan, apply, verify, upgrade, drift, registry inspection, logs, and recovery commands. |
| API server | `cmd/stackkit-server`, `internal/api` | HTTP surface for catalog, canonical `stackfile.cue` schemas, versioned validation, logs, capabilities, and OpenAPI, plus the StackKits MCP at `POST /mcp`. Every v2 Core runs it as a component and routes only `/mcp` (see [StackKits MCP on every Core](#stackkits-mcp-on-every-core)). Legacy generation/setup/registry operations are exact-v0.6 compatibility surfaces and are absent from native-v0.7 capability discovery. |
| CUE contracts | `foundation/`, `basement-kit/`, `cloud-kit/`, `modern-homelab/`, `modules/`, `use-cases/` | Schemas, defaults, constraints, module and use-case contracts, and deployment shape. |
| Composition/generation | `internal/cue`, `internal/composition`, `internal/iac`, `internal/tofu`, `internal/terramate` | Bind CUE/spec data into generated deployment artifacts and execution adapters. |
| Public docs | `README.md`, `docs/` | Homelab/BaseKit OSS documentation and CLI install contract. |
| Release automation | `.github/workflows`, `.goreleaser.yaml`, `scripts/public/` | CI, release, server image, private website validation, and curated Homelab/Basement Kit OSS mirror sync. The old `scripts/sync-public.sh` path is intentionally deprecated. |

## Core Data Flow

1. `stackkit init --owner-source=local` verifies the exact published release,
   writes the CUE-governed `stack-spec.yaml`, and establishes owner-only local
   custody plus the desired PocketID/step-ca Owner projection.
2. `stackkit validate` resolves and validates intent without persisting rollout
   state or initializing deploy observability.
3. `stackkit generate` resolves the same intent through the embedded CUE
   authority, atomically persists the canonical ResolvedPlan, and writes only
   hash-bound deterministic Compose/OpenTofu artifacts and generation evidence.
4. `stackkit plan` previews the exact generated OpenTofu change set.
5. `stackkit apply` consumes only the local ResolvedPlan, local Owner custody,
   generated artifacts, and local execution channel. It realizes the Basement
   core, binds the PocketID subject through TinyAuth to `ownerRef` and step-ca,
   and records owner-signed local Apply evidence.
6. `stackkit verify` revalidates release receipt, generation/Apply lineage,
   Owner binding, selected services, Healthchecks, routes, and executable probes.
7. `stackkit upgrade`, backup/restore, and drift operations extend that same
   local evidence chain. Techstack may dispatch the pinned binary and display
   results, but never becomes lifecycle authority.
8. Provider creation, credentials, raw SSH transport, server allocation, and
   host lifecycle are external handoff concerns. They are not StackSpec intent
   and do not enter the standalone lifecycle.
9. `stackkit-server` exposes catalog, canonical schema, versioned validation,
   logs, and capability discovery over HTTP. Its Direct Connect map and
   Verify/Doctor/Plan operations are exact-v0.6 compatibility state; native
   v0.7+ rejects those legacy endpoints before decode or mutation.

## Routing Ownership

The standalone Compose default uses the StackKits-owned router. Each workload
has exactly one execution owner and one routing owner. Enabling an optional
platform does not transfer existing workloads or their routes implicitly.

For a workload explicitly assigned to Coolify, generated routes must be served
by the selected Coolify Traefik/proxy authority; a second StackKits router must
not serve the same route. Any transition must reconcile listener and route
ownership before activation. Dokploy has an integrated-router draft adapter,
but remains unpromoted.

Komodo is the first explicit exception: the initial `paas: komodo` contract uses exactly one StackKit-owned Traefik while Komodo owns Compose Stack deployment. The generated dashboard/status output and release evidence must label that routing ownership as StackKit-owned, not Komodo-owned.

StackKit must not add a second Traefik instance, an Nginx bridge container, a host-side proxy, or a browser/test-only forwarding workaround to make service URLs appear reachable. Such a path is a routing bypass, not production evidence. If StackKit later supports another PaaS without an integrated router, that adapter contract must explicitly include one StackKit-owned router and the generated dashboard/status output must label it as such.

### StackKits MCP on every Core

Every v2 Core module (Basement, Basement lite, Cloud and Cloud standalone)
runs a `stackkit-server` component. The Core's own router publishes its MCP
endpoint at `POST /mcp` on the base host. This is the owner principle that
every installation carries the StackKits MCP, just as it carries the CLI
(the private ADR-0044 decision record).

- The container runs the `stackkit-server` binary of the release that applied
  it, on a digest-pinned `alpine` base. Apply stages the binary from beside
  the running CLI. No `stackkit-server` image is published.
- The route matches the base host and the exact `/mcp` path. It has a router
  rate limit and no TinyAuth forward-auth. The server's dedicated MCP token is
  the only credential, and the REST API is not routed.
- Apply mints the MCP token once, into
  `.stackkit/custody/stackkit-server/mcp/token` (owner-only). The server reads
  it through `STACKKIT_MCP_TOKEN_FILE` on a read-only mount. Re-apply keeps
  it, and `stackkit remove` deletes it.
- `/mcp` inherits the base host's exposure: the site LAN address on
  Basement, the public edge on Cloud. On the host, the server listens only on
  `127.0.0.1:8082`.
- The container runs as the account that owns the credentials, drops every
  capability and has a read-only root filesystem.
- Verify probes `/health` on that listener and the router admin API for the
  `stackkit-mcp` router. Traefik loads that router only from a running,
  healthy server container, so a missing or unrouted endpoint fails Verify.

## Current Technical Stack

| Area | Current source |
| --- | --- |
| Go | `mise.toml` toolchain: `1.26.8`; `go.mod` language minimum: `1.26.5` |
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

The API server registers endpoints in `internal/api/server.go`; the general contract source is [../api/openapi/stackkits-v1.yaml](../api/openapi/stackkits-v1.yaml). Node-operational StackAction fields, vocabularies, and paths are owned by [../foundation/stack_action.cue](../foundation/stack_action.cue), which generates `internal/stackaction` and marked OpenAPI regions. No separate RuntimeAction or RIL HTTP admission surface exists in StackKits. The human summary is [API.md](API.md).

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

`init`, `prepare`, `generate`, `plan`, `apply`, `verify`, `remove`, `status`, `validate`, `app`, `break-glass`, `backup`, `cluster`, `compat`, `agent`, `kit`, `logs`, `registry`, `completion`, and `version`.

## Source Of Truth Boundaries

| Concern | Source |
| --- | --- |
| Technical deployment contract | CUE files in this repo |
| Registry and catalog | Embedded CUE-derived public registry snapshot |
| Installed release and lifecycle state | Verified GitHub Release Index cache plus local `.stackkit/` state and evidence |
| API wire shape | `api/openapi/stackkits-v1.yaml` plus server tests |
| CLI behavior | Cobra command definitions and tests |
| Architecture overview | `docs/ARCHITECTURE.md` |
| Active work | published roadmap and release notes |
| Roadmap read-view | `ROADMAP.md` |

Historical V5/V6 and CUE-audit planning content has been folded into ADRs, Beads, the architecture manifest, and this overview. Do not reintroduce standalone architecture-version or task-tracker Markdown files.


### Basement internal PKI runtime ownership

The native local internal-PKI adapter uses the existing owner-custodied Ed25519
root and online step-ca intermediate. Traefik owns ACME leaf keys, issuance and
renewal. The issuer and ingress explicitly use 24-hour leaves with the native six-hour
renewal window and ten-minute polling interval. The health check requires more
than five hours fifty minutes remaining (21,000 seconds), allowing one polling
interval without accepting an expired certificate; CA lifetime is independent of leaf lifetime. Existing legacy
ACME configuration upgrades through the signed runtime-custody journal without
replacing established CA material or custom provisioner claims.

Apply observes actual ingress certificates using the compiler DNS identity and
regular TLS verification against the custodied root. Exact SANs and acceptable
public-key strength are required. `product-local-owner-root` trust distribution
means verified consumption by the local product using the existing mounted root.
It does not assert OS trust-store installation, remote-node distribution or trust
on a second LAN client. Remote trust targets remain unsupported by this local
adapter. Catalog `native-local` describes this implementation boundary; it is not
live acceptance evidence.
