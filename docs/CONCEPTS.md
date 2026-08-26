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

Native v2 init rejects `--context`. The v1 migration maps it into typed
Sites and hardware; new contracts must not use it to choose a KitProfile,
compute tier, or architecture.

### 3. Compute Tier = declared product graph

`install.computeTier` (`low` | `standard` | `high`) is explicit intent. Init
(`--compute-tier`) and the Techstack Unifier write it. Apply executes the
selected graph; it does not choose it. Inventory and `prepare` do not
auto-detect or rewrite it.

| Tier | Meaning | Current status |
|------|---------|----------------|
| `standard` | Coolify PaaS + Immich with ML | Default; declared on every kit |
| `low` | Standalone, no Coolify, Immich without ML | Declared on Basement; Cloud/Modern fail closed |
| `high` | Standard plus `telemetry-collection` | Declared on Basement, Cloud, and Modern |

Compiler capacity (`runtimeRequirements` vs attested inventory) is a separate
admission plane. It can refuse Apply of the selected graph; it cannot change
which graph was selected.

### 4. Three live axes (do not add a fourth)

**Deployment engine** (`install.mode`):
- `bootstrapped` = current Basement Kit default with packaged Compose/OpenTofu, Hub, local PocketID/TinyAuth Owner binding, and setup automation
- `bare` = infrastructure and selected tools without Base Hub or setup automation
- `advanced` = bootstrapped surface plus Terramate lifecycle orchestration,
  Runtime Intelligence Layer, and coordinated drift/change/rollback/restore
  drills. Techstack can present these through its optional Orchestrator UI only
  with a short-lived offline-verifiable capability.

**Product graph** (`install.computeTier`): see section 3. `--mode` is never this.

**Device class** (`nodes[].hardware.profile`):
- `standard` = typical homelab/server node
- `pi` = constrained homelab device (SBC, mini-PC, low-RAM NUC). Not
  Raspberry-only and not an architecture. `hardware.arch` / inventory own
  amd64 vs arm64. Does not imply `computeTier: low`.
- `gpu` / `storage` = accelerator- or storage-class nodes

### 4.1 Use case on a graph (functions + load, not hardware class)

A use case is still *why* (Photos, Vault). `#UseCasePackage.computeTiers`
declares what that use case **is** on each kit graph. It does not select the
graph. Hardware floors stay on `computeTierGraphs` and module
`runtimeRequirements`.

| Question | Field | Not |
|----------|--------|-----|
| Which functions on this graph? | `functions` (capability tokens) | a second Photos product |
| 24/7 vs only when used? | `load.residency`: `always-on` / `on-demand` / `scheduled` | CPU millicores on the package |
| Base load while enabled? | `load.baseline`: `none` / `idle-resident` / `active-resident` | auto-detect from probe |
| Spike when used? | `load.burst`: `none` / `interactive` / `ingest` / `batch` | a premium enum |

Unifier can add `always-on` + `active-resident` as permanent load, and treat
`on-demand` / `burst` as ad-hoc query or session performance. It reads the
package fits through the use-case catalog and MCP `stackkit_use_case_compute_tiers`.
It writes `install.computeTier`; it does not treat `hardware.profile: pi` or
ARM as a graph. Photos `low` is library without ML (`idle-resident`, ingest
burst). Photos `standard` keeps the ML worker resident (`active-resident`).
Vault is `always-on` / `idle-resident` / `interactive` on every graph. Media
is omitted on `low` until a v2 lite runtime exists.

To add a use-case lite: Catalog alternative + `#WorkloadContractV2.computeTiers`
`alternativeID` + kit `moduleSubstitutions` + matching package
`computeTiers.low.functions` and `moduleSlug`. Init writes the catalog
alternative; omitted fits fail closed. Package first, catalog/graph second,
docs last.

`runtimeProfiles.contexts` is gone. Placement modes remain.

`context` (`local` | `cloud` | `pi`) is legacy migration input only.
`context: pi` maps to `site.kind: home` plus `hardware.profile: pi`.

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
install.mode and install.computeTier declared (init / Unifier)
    |
    v
Sites, node membership, and hardware.profile declared
    |
    v
Inventory facts measured independently:
  reachability + architecture + CPU/RAM/storage/devices
  (does not rewrite computeTier)
    |
    v
KitDefinition + capability/provider/add-on contracts resolved
    |
    v
User overrides applied (--paas coolify, --enable photos, etc.)
    |
    v
Compiler admits the selected graph against inventory
    |
    v
One canonical ResolvedPlan + planHash produced
    |
    v
Generate + Apply (Apply does not select the graph)
```

---

## Dead Concepts

### Variant = DEAD (V5)

Variants were mutually exclusive service bundles (default/beszel/minimal/coolify).
Replaced by the per-tool role system:
- `beszel` variant → `--monitoring beszel`
- `coolify` variant → `--paas coolify`
- `minimal` variant → `--compute-tier low` for the graph, optionally
  `hardware.profile: pi` for a constrained device. `--context pi` remains a
  migration alias to home + profile `pi` only.

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
