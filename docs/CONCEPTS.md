# StackKits Concepts

> **READ THIS FIRST** before making any architectural suggestion or code change involving
> service selection, tool roles, or StackKit structure.
>
> This is the single-page reference for all StackKits concepts.
> For full details, see [ARCHITECTURE.md](./ARCHITECTURE.md).
> V4 is the historical baseline; V5 evolves it.
> For non-negotiable current rules, see STACKKIT_GOLDEN_RULES.md.

---

## Why StackKits Exist

Nobody installs a StackKit for infrastructure. They install it because they want:
- A photo gallery (Immich)
- A media server (Jellyfin)
- A password vault (Vaultwarden)
- A smart home (Home Assistant)
- ...and more

**A StackKit delivers a complete, pre-configured Homelab platform.** The
standalone CLI installs the single-node Basement core: host/Docker baseline,
router, PocketID, TinyAuth, step-ca, Coolify, Hub, and verification endpoints.
Photos, Vault, Files, and the broader application catalog remain explicit
opt-ins and graduate after that core. The platform is owned by the local
PocketID/TinyAuth Owner bound to StackKits `ownerRef`/step-ca custody.

---

## Terminology — the overloaded word "Base"

"Base" has meant several different things across older docs. Canonical usage:

| Term | Meaning | Notes |
|------|---------|-------|
| **Foundation Layer** | The OS/host layer — one of the three layers (Application / Platform / **Foundation**). | Canonical per ADR-0015. **Never** "Base Layer" or "OS Layer" in new docs. |
| **Basement Kit** / **Cloud Kit** | The pure-site-class kits (home / cloud) — formerly "Base Kit" / "Base Homelab". | Distinct KitProfiles over shared contracts per ADR-0029. `base-kit` is retired as a kit and survives only as a migration alias. |
| **`base/`** | The shared CUE schema package (foundational contracts: stackkit, cluster, placement, context, …). | A code package name. NOT the Foundation layer, NOT a kit. |
| **Base Hub** (Node Hub) | The per-node onboarding entrypoint served at `base.<domain>`. | A UX surface. NOT a layer, NOT a kit. |

Rule of thumb: **"Base Kit" / "Base Homelab" are retired kit names** (→ Basement / Cloud);
**"Base Layer" / "OS Layer" are retired layer names** (→ Foundation). The only legitimate
remaining uses of "base" are the `base/` package and the `base.<domain>` Hub URL.

---

## The 6 Concepts

### 1. StackKit = Architecture Pattern + Default Use Case Set

A StackKit defines HOW infrastructure is organized AND WHICH use cases ship as defaults.

Kits are distinguished by permitted Site kinds, capability sets, authority placement,
and failure contract — not by node count, hardware, or a global `context` switch
(ADR-0029, Golden Rules §8). Every StackInstance has one logical ControlPlane;
the HA add-on may replicate its members.

| StackKit | Pattern | Maturity | Default Scope |
|----------|---------|----------|---------------|
| **Basement Kit** | Exactly one home Site; one node and local Control Authority | verified standalone core | Complete local CLI lifecycle, LAN access/enrollment, hardware gates, and offline autonomy |
| **Cloud Kit** | Exactly one cloud Site with one or more nodes; cloud Control Authority | `maturity: preview` | Admission of an externally supplied host, default-closed public edge, public DNS/TLS, internet hardening |
| **Modern Homelab** | At least one home and one cloud Site; home authority + five bridge contracts | alpha scaffolding | Protected Site federation, explicit publication/placement/residency/partition policy |
| **HA** *(add-on, never a kit)* | Replicates one logical ControlPlane against explicit RPO/RTO | `addons/ha` | Kit-specific active-passive/quorum realization with real nodes, failure domains, and fencing |

Basement Kit and Cloud Kit use the same neutral Site/Node/Capability contracts, but
their explicit KitDefinitions require and forbid different capabilities. Kit selection
chooses the product profile. Environment detection validates that choice; it does not
silently replace it.

### 2. Sites + Inventory Facts

Canonical v2 separates facts that the legacy `context` enum combined:

| Axis | Examples | Owner |
|------|----------|-------|
| Site kind | `home`, `cloud` | Explicit StackSpec/KitDefinition constraint |
| Reachability | private, NAT/CGNAT, public-capable | Detected inventory + validated intent |
| Hardware | amd64/arm64, `pi`, GPU, storage | Node inventory and hardware profile |
| External host custody | Opaque host binding, observed failure domain | TechStack/external executor; StackKits receives only provider-neutral binding and conformance evidence |
| Exposure | local, remote-private, public | Per-service access/publication policy |

The current CLI still accepts `--context local|cloud|pi` as a v1 compatibility
surface. The v2 migration maps it into typed Sites/hardware and emits a warning;
new contracts and consumers must not use it to choose a KitProfile.

### 3. Compute Tier = Resource Gate

Derived from CPU/RAM/disk during `stackkit prepare`. CONSTRAINS what can physically run.

| Tier | Criteria | Effect |
|------|----------|--------|
| `high` | 8+ CPU, 16+ GB RAM | Everything viable. Full monitoring possible. |
| `standard` | 4+ CPU, 4+ GB RAM | Most use cases viable. Default monitoring. |
| `low` | <4 CPU or <4 GB RAM | Required platform remains Coolify unless explicitly overridden. Heavy use cases (Media, Photos, AI) unavailable. |

The tier gates feasibility. It doesn't drive selection — the StackKit defaults + user overrides drive selection, then tier gates what's feasible.

### 4. Deployment Mode + Resource Profile

StackKits separate the deployment engine from the resource profile:

**Deployment Engine:**
- `bootstrapped` = current Basement Kit default with packaged Compose/OpenTofu, Hub, local PocketID/TinyAuth Owner binding, and setup automation
- `bare` = infrastructure and selected tools without Base Hub or setup automation
- `advanced` = bootstrapped surface plus Terramate lifecycle orchestration,
  Runtime Intelligence Layer, and coordinated drift/change/rollback/restore
  drills. Techstack can present these through its optional Orchestrator UI only
  with a short-lived offline-verifiable capability.

**Resource Profile** (user-specifiable intent, NOT just hardware detection):

| Profile | Intent | Effect |
|---------|--------|--------|
| `pi` | "Lightweight, low requirements" | Forces low compute tier, disables heavy modules, uses minimal monitoring |
| `standard` | Default, no special constraints | Auto-detected tier applies |
| `full` | "Enable everything" | All default use cases + monitoring enabled |

Use `--compute-tier low` or `--context pi` for constrained hardware intent. `--mode` is reserved for the deployment engine (`bare`, `bootstrapped`, or `advanced`).

### 5. Tool Role = Per-StackKit Per-Tool Assignment

Every tool has a ROLE relative to each StackKit. Canonical CUE contracts define
those roles. The embedded registry snapshot and any private DB projection are
derived read models that may mirror the published contract but cannot override
it or become a local lifecycle dependency.
The compact authoring and promotion matrix lives in [OPTIONS_AND_AUTHORING.md](OPTIONS_AND_AUTHORING.md).

| Role | Meaning | Example |
|------|---------|---------|
| `default` | Ships enabled, pre-configured, immediately usable | Coolify in Basement Kit |
| `alternative` | Curated swap for a default (same category) | Komodo as explicit PaaS alternative |
| `optional` | Available but off by default, user enables | Game Server |

User swaps defaults: `stackkit generate --paas komodo --monitoring beszel`
User enables optionals: `stackkit generate --enable smart-home`

### 6. Use Case vs Optional Module

**Use Case** (role: default / alternative / optional):
- WHY someone installs a StackKit
- A real-world scenario with a default tool + curated alternatives
- Ships only when its role and current milestone permit it; optional use cases
  are not part of the default readiness claim

Optional modules stay off by default and must have documented enablement,
resource limits, and generated-output behavior before they are exposed through
the public OSS surface.

---

## Curated Use-Case Catalog

The catalog is the open `base.UseCaseCatalog` CUE map. Its size is derived from
the explicitly registered product intentions; there is no fixed count or
handwritten ordering contract. Adding an add-on or guide does not create a use
case. Every package, Architecture v2 workload, lifecycle, module, adapter, test,
and evidence record joins the catalog with `useCaseRef`.

Internal development builds generate `docs/USE_CASE_DEVELOPMENT_OVERVIEW.md`.
It is intentionally excluded from the public release, whose documentation is
generated separately from immutable release manifests and omits internal gaps
and maturity conclusions.

---

## Resolution Hierarchy

```
StackKit selected (Basement Kit)
    |
    v
Deployment mode and resource profile applied
    |
    v
Sites and target membership declared
    |
    v
Inventory facts detected independently:
  reachability + provider + architecture + CPU/RAM/storage/devices
    |
    v
Compute Tier derived (high / standard / low)
    |
    v
KitDefinition + capability/provider/add-on contracts resolved
    |
    v
User overrides applied (--paas coolify, --enable photos, etc.)
    |
    v
Compute/resource gates and access/data/failure policies validated
    |
    v
One canonical ResolvedPlan + planHash produced
    |
    v
CUE unification + validation
    |
    v
Generate + Apply
```

---

## Dead Concepts

### Variant = DEAD (V5)

Variants were mutually exclusive service bundles (default/beszel/minimal/coolify).
Replaced by the per-tool role system:
- `beszel` variant → `--monitoring beszel`
- `coolify` variant → `--paas coolify`
- `minimal` variant → `--compute-tier low` or an explicit constrained hardware/resource profile (`--context pi` remains a migration alias only)

---

## Multi-Server Scaling

| Situation | Behavior |
|-----------|----------|
| One StackInstance + 1 node | Services may run on the single controller/worker node; KitProfile stays unchanged. |
| One StackInstance + additional nodes | KitProfile stays unchanged; every node references one Site and one parent StackInstance, with explicit roles/placement. |
| At least one home and one cloud Site + all five bridge contracts | This is **Modern Homelab**, not merely a remote worker join. It remains Preview until live evidence passes. |
| Several independent mains with their own workers | Several StackInstances grouped in a Fleet; no implicit shared trust, network, or quorum. |
| Replicated members of one logical ControlPlane | Same KitProfile plus `addons.ha`; explicit availability, failure-domain, and fencing contract required. |

Service placement rules:

> Note: "placement" here means node scheduling (`#ServiceDefinition.placement.nodeType`), not the `placementMode` axis (`docs/placement/`).

1. Platform services (Traefik, Auth, PAAS) stay on primary node
2. Use case services distributed by hardware requirements (GPU, storage)
3. User can explicitly assign: `services.media.node: server-2` in stack-spec.yaml

---

## If In Doubt

- **Use cases are the reason to install a StackKit.**
- **Variants are DEAD.** Use the role system (default/alternative/optional).
- **ADR-0031 defines the standalone lifecycle boundary; ADR-0029 defines kit topology.** The v1/v5 shapes are compatibility inputs during the bounded migration, not authorities for new design.
- **CUE is the technical contract source of truth; DB is the registry and operations mirror.** Never edit generated files.
- **OpenTofu, never Terraform.** Licensing violation.
- **Local default links must open as printed.** Use browser-native names such as `service.home.localhost`; never require hosts-file edits, manual DNS mapping, trust-store setup, or port suffixes for generated default links.
- **CUE contracts define tool roles; embedded registries and private databases
  are derived mirrors.**
- **Resource profile = user intent, not just hardware.** `--compute-tier low` can be chosen explicitly even when hardware auto-detection would allow more.
