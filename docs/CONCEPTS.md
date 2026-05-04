# StackKits Concepts (V5)

> **READ THIS FIRST** before making any architectural suggestion or code change involving
> service selection, tool roles, or StackKit structure.
>
> This is the single-page reference for all StackKits concepts.
> For full details, see [ARCHITECTURE_V5.md](./ARCHITECTURE_V5.md).
> V4 is the historical baseline; V5 evolves it.

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

| StackKit | Pattern | Default Use Cases |
|----------|---------|-------------------|
| **Base Kit** | Single-server | Platform + Photos (Immich) + Media (Jellyfin) + Vault (Vaultwarden) |
| **Modern Homelab** | Hybrid (local+cloud) | Platform + Photos + Media + Vault + more (Files, Smart Home) |
| **HA Kit** | HA Cluster (3+ nodes) | Platform + Vault. Use cases opt-in. Focus = reliability. |

Platform = Traefik + TinyAuth + PocketID + PAAS (Dokploy/Coolify) + Monitoring (OTel Collector baseline, optional uptime UX, optional VictoriaMetrics fan-in).
Always present regardless of StackKit.

### 2. Context = Where + What Hardware

Auto-detected during `stackkit prepare` from the runtime environment. Drives infrastructure-level defaults ONLY.

| Context | Detection | Effects |
|---------|-----------|---------|
| `local` | Home/office network (private IP, no cloud metadata) | Self-signed TLS (Step-CA), Dokploy, overlay2 |
| `cloud` | Cloud provider metadata or VPS detected (public IP, cloud signatures) | Let's Encrypt, Coolify, public IP routing |
| `pi` | ARM64 architecture + low resources (<4 cores or <4 GB RAM) | Dockge, ARM images, constrained resources |

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
| `low` | <4 CPU or <4 GB RAM | Dockge replaces Dokploy. Heavy use cases (Media, Photos, AI) unavailable. |

The tier gates feasibility. It doesn't drive selection — the StackKit defaults + user overrides drive selection, then tier gates what's feasible.

### 4. Mode = User Intent Profile

Mode has TWO dimensions:

**Deployment Engine:**
- `simple` = OpenTofu Day-1 only
- `advanced` = OpenTofu + Terramate (drift detection, Day-2 ops)

**Resource Profile** (user-specifiable intent, NOT just hardware detection):

| Profile | Intent | Effect |
|---------|--------|--------|
| `pi` | "Lightweight, low requirements" | Forces low compute tier, Dockge, minimal monitoring |
| `standard` | Default, no special constraints | Auto-detected tier applies |
| `full` | "Enable everything" | All default use cases + monitoring enabled |

`--mode pi` is user intent — a user on an old laptop can use it. It overrides auto-detection.

### 5. Tool Role = Per-StackKit Per-Tool Assignment

Every tool has a ROLE relative to each StackKit. Managed in the admin-center database
(`admin_sk_stackkit_tools`), consumed by CUE definitions.

| Role | Meaning | Example |
|------|---------|---------|
| `default` | Ships enabled, pre-configured, immediately usable | Dokploy in Base Kit |
| `alternative` | Curated swap for a default (same category) | Coolify as alt for Dokploy |
| `optional` | Available but off by default, user enables | Game Server |
| `addon` | Composable infrastructure capability (not a use case) | VPN Overlay, Backup |

User swaps defaults: `stackkit generate --paas coolify --monitoring beszel`
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
Mode applied (--mode pi overrides auto-detection)
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
Default tool set resolved (from admin-center roles per StackKit)
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
- `minimal` variant → `--mode pi` or compute tier `low`

---

## Multi-Server Scaling

| Situation | Behavior |
|-----------|----------|
| Base Kit + 1 local node | Services distributed across nodes. Base Kit stays. |
| Base Kit + cloud node | Recommend upgrade to Modern Homelab (hybrid pattern). |
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
- **CUE is the source of truth.** Never edit generated files.
- **OpenTofu, never Terraform.** Licensing violation.
- **Never localhost URLs.** Always `service.stack.local`.
- **Admin-center defines tool roles.** CUE consumes them.
- **Mode = user intent, not just hardware.** `--mode pi` works on any hardware.
