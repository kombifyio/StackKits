# StackKits Concepts (V5/V6 Canonical)

> **READ THIS FIRST** before making any architectural suggestion or code change involving
> service selection, tool roles, or StackKit structure.
>
> This is the single-page reference for all StackKits concepts.
> For full details, see [ARCHITECTURE.md](./ARCHITECTURE.md).
> V4 is the historical baseline; V5 evolves it.
> For non-negotiable current rules, see [STACKKIT_GOLDEN_RULES.md](STACKKIT_GOLDEN_RULES.md).

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

## The 6 Concepts

### 1. StackKit = Architecture Pattern + Default Use Case Set

A StackKit defines HOW infrastructure is organized AND WHICH use cases ship as defaults.

| StackKit | Pattern | Default Scope |
|----------|---------|---------------|
| **Base Kit** | Single environment 1..N | Platform + verified default application modules; heavier modules are enabled only after their first-run path passes release gates |
| **Modern Homelab** | Hybrid (local+cloud) | Platform + use cases split across public-facing and local-first nodes |
| **HA Kit** | HA Cluster (3+ nodes) | Platform + reliability-focused defaults. Use cases opt-in unless they satisfy HA placement, backup, and failover rules. |

Platform target = routing + identity implementation + access gateway + PaaS adapter + platform observability. Current release gates may keep individual platform services opt-in until their first-run UX and verification path are ready.

### 2. Context = Where + What Hardware

Auto-detected during `stackkit prepare` from the runtime environment. Drives infrastructure-level defaults ONLY.

| Context | Detection | Effects |
|---------|-----------|---------|
| `local` | Home/office network (private IP, no cloud metadata) | Browser-native `.localhost` links, Coolify, overlay2 |
| `cloud` | Cloud provider metadata or VPS detected (public IP, cloud signatures) | Let's Encrypt, Coolify, public IP routing |
| `pi` | ARM64 architecture + low resources (<4 cores or <4 GB RAM) | Standard PaaS contract remains Coolify unless explicitly overridden; heavy modules are gated and lightweight managers remain experimental |

**How auto-detection works:**

1. Network environment detection (`netenv.Detect()`) checks cloud metadata endpoints, public/private IPs, and environment variables to classify as `home`, `vps`, or `cloud`.
2. Hardware detection identifies CPU architecture (amd64/arm64) and resource levels (cores, RAM).
3. `ResolveNodeContext()` combines both signals: ARM64 + low resources → `pi`, cloud/VPS → `cloud`, home network → `local`.
4. The `--context` CLI flag can override auto-detection (e.g., `--context pi` on an old laptop).

Context does NOT determine which use cases are available. That's the StackKit's job + Compute Tier gating.

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
- `simple` = OpenTofu Day-1 only
- `advanced` = OpenTofu + Terramate (drift detection, Day-2 ops)

**Resource Profile** (user-specifiable intent, NOT just hardware detection):

| Profile | Intent | Effect |
|---------|--------|--------|
| `pi` | "Lightweight, low requirements" | Forces low compute tier, disables heavy modules, uses minimal monitoring |
| `standard` | Default, no special constraints | Auto-detected tier applies |
| `full` | "Enable everything" | All default use cases + monitoring enabled |

Use `--compute-tier low` or `--context pi` for constrained hardware intent. `--mode` is reserved for the deployment engine (`simple` or `advanced`).

### 5. Tool Role = Per-StackKit Per-Tool Assignment

Every tool has a ROLE relative to each StackKit. Roles are managed in the StackKits registry tables and consumed by CUE-backed release contracts with hash parity.
The compact authoring and promotion matrix lives in [OPTIONS_AND_AUTHORING.md](OPTIONS_AND_AUTHORING.md).

| Role | Meaning | Example |
|------|---------|---------|
| `default` | Ships enabled, pre-configured, immediately usable | Coolify in Base Kit |
| `alternative` | Curated swap for a default (same category) | Komodo as explicit PaaS alternative |
| `optional` | Available but off by default, user enables | Game Server |
| `addon` | Composable infrastructure capability (not a use case) | VPN Overlay, Backup |

User swaps defaults: `stackkit generate --paas komodo --monitoring beszel`
User enables optionals: `stackkit generate --enable smart-home`

### 6. Use Case vs Add-On

**Use Case** (role: default / alternative / optional):
- WHY someone installs a StackKit
- A real-world scenario with a default tool + curated alternatives
- Ships pre-configured, immediately usable with admin account

**Add-On** (role: addon):
- Infrastructure capability extension (horizontal cross-cut)
- Makes use cases work BETTER, but nobody installs a StackKit because of an add-on
- Examples: VPN Overlay, Backup, Full Monitoring Stack, Tunnel, GPU Passthrough

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
StackKit selected (Base Kit / Modern Homelab / HA Kit)
    |
    v
Deployment mode and resource profile applied
    |
    v
Context auto-detected:
  netenv.Detect() → network environment (home/vps/cloud)
  + hardware info (arch, CPU cores, RAM)
  → ResolveNodeContext() → local / cloud / pi
  (--context flag overrides if set)
    |
    v
Compute Tier derived (high / standard / low)
    |
    v
Default tool set resolved (from StackKits registry roles per kit)
    |
    v
User overrides applied (--paas coolify, --enable photos, etc.)
    |
    v
Compute Tier gating (disable tools that exceed hardware)
    |
    v
Add-ons resolved (explicit + auto-activated)
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
- `minimal` variant → `--compute-tier low`, `--context pi`, or an explicit constrained resource profile

---

## Multi-Server Scaling

| Situation | Behavior |
|-----------|----------|
| Base Kit + 1 local node | Services run on the primary node. Base Kit stays. |
| Base Kit + additional worker/storage nodes in the same trust domain | Base Kit stays; placement is used for capacity, storage, backup, or device-specific workloads. |
| Base Kit + separate cloud/local trust domains | Recommend upgrade to Modern Homelab (hybrid pattern). |
| Base Kit + 3+ nodes | Recommend HA Kit if high availability needed. |
| Modern Homelab + nodes | Register node, Placement Engine distributes services. |

Service placement rules:
1. Platform services (Traefik, Auth, PAAS) stay on primary node
2. Use case services distributed by hardware requirements (GPU, storage)
3. User can explicitly assign: `services.media.node: server-2` in stack-spec.yaml

---

## If In Doubt

- **Use cases are NOT add-ons.** Use cases are the reason to install a StackKit.
- **Variants are DEAD.** Use the role system (default/alternative/optional/addon).
- **V4 is the baseline.** V5 evolves V4, never contradicts it.
- **CUE is the technical contract source of truth; DB is the registry and operations mirror.** Never edit generated files.
- **OpenTofu, never Terraform.** Licensing violation.
- **Local default links must open as printed.** Use browser-native names such as `service.home.localhost`; never require hosts-file edits, manual DNS mapping, trust-store setup, or port suffixes for generated default links.
- **StackKits registry defines tool roles; CUE contracts enforce them.**
- **Resource profile = user intent, not just hardware.** `--compute-tier low` can be chosen explicitly even when hardware auto-detection would allow more.
