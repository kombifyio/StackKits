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

**A StackKit delivers a complete, pre-configured homelab.** Install it, and everything works
immediately with the admin user account. The infrastructure (Traefik, Auth, PAAS) is just the
platform that enables the use cases.

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
| **Basement Kit** | One or more home Sites; local Control Authority | stable runtime; v2 profile migration active | Local provisioning, LAN access/enrollment, hardware gates, offline autonomy |
| **Cloud Kit** | One or more cloud Sites; cloud Control Authority | stable runtime; v2 profile migration active | VPS provisioning, default-closed public edge, public DNS/TLS, internet hardening |
| **Modern Homelab** | At least one home and one cloud Site; home authority + five bridge contracts | preview/early-access | Protected Site federation, explicit publication/placement/residency/partition policy |
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
| Provider | VPS/cloud account, region, failure domain | Site provider reference + observed facts |
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
- `bootstrapped` = current Basement Kit default with packaged OpenTofu, Base Hub, owner bootstrap, and setup-run automation
- `bare` = infrastructure and selected tools without Base Hub or setup automation
- `advanced` = bootstrapped surface plus Terramate Plus lifecycle orchestration, Runtime Intelligence Layer, Frontend Intelligence handoff, drift/change/rollback/restore-drill surfaces, and managed TechStack lifecycle handoff

**Resource Profile** (user-specifiable intent, NOT just hardware detection):

| Profile | Intent | Effect |
|---------|--------|--------|
| `pi` | "Lightweight, low requirements" | Forces low compute tier, disables heavy modules, uses minimal monitoring |
| `standard` | Default, no special constraints | Auto-detected tier applies |
| `full` | "Enable everything" | All default use cases + monitoring enabled |

Use `--compute-tier low` or `--context pi` for constrained hardware intent. `--mode` is reserved for the deployment engine (`bare`, `bootstrapped`, or `advanced`).

### 5. Tool Role = Per-StackKit Per-Tool Assignment

Every tool has a ROLE relative to each StackKit. Roles are managed in the StackKits registry tables and consumed by CUE-backed release contracts with hash parity.
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
- Ships pre-configured, immediately usable with admin account

Optional modules stay off by default and must have documented enablement,
resource limits, and generated-output behavior before they are exposed through
the public OSS surface.

---

## The 10 Use Cases

| # | Use Case | Default Tool | Category |
|---|----------|-------------|----------|
| 1 | Smart Home | Home Assistant | smart-home |
| 2 | Photo Memories | Immich | photos |
| 3 | Media Streaming | Jellyfin + *arr stack | media |
| 4 | Password Vault | Vaultwarden | vault |
| 5 | File Sharing | Cloudreve / Nextcloud | files |
| 6 | AI / LLM | Ollama + Open WebUI | ai |
| 7 | Dev Platform | Gitea + CI | dev |
| 8 | Mail Server | Stalwart | mail |
| 9 | Game Server | Various | game |
| 10 | Remote Desktop | Guacamole | remote |

Each use case may have curated alternatives (e.g., Ente instead of Immich for photos).
The admin-center tool evaluation decides which alternatives we offer.

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

> Hinweis: „placement" hier = Node-Scheduling (`#ServiceDefinition.placement.nodeType`), nicht die `placementMode`-Achse (`docs/placement/`).

1. Platform services (Traefik, Auth, PAAS) stay on primary node
2. Use case services distributed by hardware requirements (GPU, storage)
3. User can explicitly assign: `services.media.node: server-2` in stack-spec.yaml

---

## If In Doubt

- **Use cases are the reason to install a StackKit.**
- **Variants are DEAD.** Use the role system (default/alternative/optional).
- **ADR-0029 is the target architecture.** The v1/v5 shapes are compatibility inputs during the bounded migration, not authorities for new design.
- **CUE is the technical contract source of truth; DB is the registry and operations mirror.** Never edit generated files.
- **OpenTofu, never Terraform.** Licensing violation.
- **Local default links must open as printed.** Use browser-native names such as `service.home.localhost`; never require hosts-file edits, manual DNS mapping, trust-store setup, or port suffixes for generated default links.
- **StackKits registry defines tool roles; CUE contracts enforce them.**
- **Resource profile = user intent, not just hardware.** `--compute-tier low` can be chosen explicitly even when hardware auto-detection would allow more.
