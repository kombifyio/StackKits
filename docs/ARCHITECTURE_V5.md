# StackKits Architecture v5.0

> **Version:** 5.0
> **Date:** 2026-03-10
> **Status:** Accepted
> **Supersedes:** [Architecture v4.0](./ARCHITECTURE_V4.md)
> **Quick Reference:** [CONCEPTS.md](./CONCEPTS.md)

---

## Executive Summary

Architecture v5 evolves v4 with four additions:

1. **Tool Role System** — per-StackKit, per-tool role assignments (default / alternative / optional / addon) managed in the kombify Administration center
2. **Use Cases as first-class concept** — the 10 use cases are WHY someone installs a StackKit; they are not add-ons
3. **Mode as User Intent** — `--mode pi` expresses intent ("lightweight"), not just hardware detection
4. **Multi-Server Scaling** — how StackKits react when nodes are added

Everything from v4 remains valid: StackKit (pattern) + Context (where/what) + Add-Ons (composable) + Progressive Capability Model (Levels 0-4) + 3-Layer Architecture.

---

## 1. Concept Definitions

### 1.1 StackKit = Architecture Pattern + Curated Default Set

A StackKit defines HOW infrastructure is organized AND WHICH use cases ship as defaults.

| StackKit | Architecture Pattern | Default Use Cases |
|----------|---------------------|-------------------|
| **Base Kit** | Single environment — all services co-located | Platform + Photos (Immich) + Media (Jellyfin+*arr) + Vault (Vaultwarden) |
| **Modern Homelab** | Hybrid infrastructure — local + cloud bridged | Platform + Photos + Media + Vault + Files + Smart Home |
| **HA Kit** | HA cluster — redundancy, failover, quorum | Platform + Vault. Other use cases opt-in based on capacity. |

**Platform** (always present, non-negotiable):
- L1 Foundation: LLDAP, Step-CA
- L2 Platform: Traefik, TinyAuth, PocketID, PAAS (Dokploy/Coolify), Dashboard
- Monitoring: OTel Collector baseline, Uptime Kuma/Beszel as uptime UX, monitoring-core (VictoriaMetrics + optional Grafana) as an add-on

A StackKit is a complete product. `stackkit apply` on a clean server produces a fully working homelab with all default use cases pre-configured and immediately usable.

### 1.2 Context = Where + What Hardware

Unchanged from v4. Auto-detected from runtime environment.

| Context | Detection | Infrastructure Effects |
|---------|-----------|----------------------|
| `local` | Physical hardware, no cloud metadata | Self-signed TLS (Step-CA), Dokploy, overlay2, local DNS |
| `cloud` | Cloud provider metadata detected | Let's Encrypt, Coolify, public IP, cloud DNS |
| `pi` | ARM arch + low memory (<4GB) | Dockge, ARM images, tmpfs, constrained resources |

Context drives infrastructure defaults. It does NOT determine which use cases are available — that is the StackKit's job combined with Compute Tier gating.

### 1.3 Compute Tier = Resource Gate

Derived from CPU, RAM, and disk during `stackkit prepare`. Sub-property of Context.

| Tier | Criteria | Effect |
|------|----------|--------|
| `high` | 8+ CPU, 16+ GB RAM, 100+ GB disk | Everything viable. OTel baseline plus monitoring-core (VictoriaMetrics + optional Grafana) is viable. |
| `standard` | 4+ CPU, 4+ GB RAM, 20+ GB disk | Most use cases viable. OTel baseline ships by default; uptime UX stays lightweight. |
| `low` | <4 CPU or <4 GB RAM | Dockge replaces Dokploy. OTel baseline stays lightweight; heavy use cases (Media, Photos, AI) are gated out. |

Compute Tier is a GATE, not a selector. The StackKit's default set + user overrides determine WHAT to deploy. The tier determines what CAN physically run. If a default exceeds the tier, it is disabled with a warning.

### 1.4 Mode = User Intent Profile

**Change from v4:** Mode is no longer just "deployment engine". It has two dimensions.

#### Deployment Engine

| Engine | What | When |
|--------|------|------|
| `simple` | OpenTofu Day-1 only (init/plan/apply) | Default for Base Kit |
| `advanced` | OpenTofu + Terramate (drift detection, rolling updates, Day-2) | Default for HA Kit. Auto when `driftDetection.enabled == true` |

#### Resource Profile (User Intent)

| Profile | Intent | Effect |
|---------|--------|--------|
| `pi` | "I need low resource requirements" | Forces compute tier = low. Dockge, minimal monitoring, lightweight images. |
| `standard` | No special constraints | Hardware auto-detection determines tier. |
| `full` | "Enable everything" | All default use cases + monitoring-core add-on become eligible. |

`--mode pi` is USER INTENT, not hardware detection. A user on an old x86 laptop can say `--mode pi` to get the lightweight experience. Mode overrides auto-detection, never the reverse.

```bash
stackkit prepare --mode pi          # user intent: lightweight
stackkit prepare --mode full        # user intent: everything
stackkit prepare                    # auto-detect from hardware
```

### 1.5 Tool Role = Per-StackKit Per-Tool Assignment

Every tool in the catalog has a ROLE relative to each StackKit. This role is assigned centrally in the kombify Administration database (`admin_sk_stackkit_tools`).

| Role | Meaning | User Action | Example |
|------|---------|-------------|---------|
| `default` | Ships enabled, pre-configured, immediately usable | None needed | Dokploy in Base Kit |
| `alternative` | Curated swap for a default in the same category | `--paas coolify` | Coolify as alt for Dokploy |
| `optional` | Available but not enabled by default | `--enable smart-home` | Game Server in Base Kit |
| `addon` | Composable infrastructure capability (not a use case) | `stackkit addon add vpn-overlay` | VPN Overlay, Backup |

#### How Alternatives Work

```bash
# Swap PaaS: Dokploy (default) → Coolify (alternative)
stackkit generate --paas coolify

# Swap uptime UX: Uptime Kuma (default dashboard) → Beszel (alternative)
# The OTel Collector baseline remains enabled.
stackkit generate --monitoring beszel

# Enable optional use case
stackkit generate --enable smart-home

# Combine
stackkit generate --paas coolify --monitoring beszel --enable ai
```

The CLI:
1. Looks up the alternative in the tool catalog
2. Verifies it has role `alternative` for the specified category
3. Disables the default tool in that category
4. Enables the alternative with its default configuration
5. Proceeds with generation

### 1.6 Use Case vs Add-On

This is the critical distinction that v4 did not make sharply enough.

**Use Case** (role: default / alternative / optional):
- WHY someone installs a StackKit
- A real-world scenario: photos, media, smart home, vault
- Has a default tool + curated alternatives
- Ships pre-configured, immediately usable with admin account
- Can be default (ships enabled) or optional (user enables)

**Add-On** (role: addon):
- Infrastructure capability extension
- Horizontal cross-cut that makes use cases work BETTER
- Nobody installs a StackKit because of an add-on
- Always explicitly composed by the user

| Category | Use Case? | Add-On? | Why |
|----------|-----------|---------|-----|
| Photos (Immich) | Yes | No | User installs StackKit FOR this |
| Media (Jellyfin) | Yes | No | User installs StackKit FOR this |
| VPN Overlay | No | Yes | Infrastructure capability |
| Backup (Restic) | No | Yes | Infrastructure capability |
| Monitoring Core (VictoriaMetrics + optional Grafana) | No | Yes | Extends the OTLP collector baseline with fan-in, retention, and dashboards |

**v4 Compatibility:** v4 lists all composable extensions under `addons/`. The directory structure stays. The ROLE field differentiates behavior: a media module with `role: default` ships enabled; a VPN module with `role: addon` requires explicit activation.

---

## 2. The 10 Use Cases

These are the reason someone installs a StackKit.

| # | Use Case | Default Tool | Curated Alternatives | Category |
|---|----------|-------------|---------------------|----------|
| 1 | Smart Home | Home Assistant (+Mosquitto, Zigbee2MQTT) | — | smart-home |
| 2 | Photo Memories | Immich | Ente Photos | photos |
| 3 | Media Streaming | Jellyfin + Sonarr/Radarr/Prowlarr/Bazarr | — | media |
| 4 | Password Vault | Vaultwarden | — | vault |
| 5 | File Sharing | Cloudreve | Nextcloud | files |
| 6 | AI / LLM | Ollama + Open WebUI | — | ai |
| 7 | Dev Platform | Gitea + Woodpecker CI | — | dev |
| 8 | Mail Server | Stalwart | — | mail |
| 9 | Game Server | Various | — | game |
| 10 | Remote Desktop | Guacamole | — | remote |

The admin-center tool evaluation (`admin_sk_tools`, `admin_sk_tool_alternatives`) decides which alternatives are curated. This list evolves over time.

---

## 3. Per-StackKit Default Matrix

| Use Case | Base Kit | Modern Homelab | HA Kit |
|----------|----------|----------------|--------|
| Photos (Immich) | **default** | **default** | optional |
| Media (Jellyfin) | **default** | **default** | optional |
| Vault (Vaultwarden) | **default** | **default** | **default** |
| Smart Home | optional | **default** | N/A |
| File Sharing | optional | **default** | optional |
| AI / LLM | optional | optional | optional |
| Dev Platform | optional | optional | optional |
| Mail Server | optional | optional | optional |
| Game Server | optional | optional | optional |
| Remote Desktop | optional | optional | optional |

**Compute Tier gating applies on top:** If a default use case exceeds the node's resources (e.g., Immich on a low-tier device), it is disabled with a warning. The user can still force-enable it.

---

## 4. Resolution Hierarchy

```
1. StackKit selected (Base Kit / Modern Homelab / HA Kit)
       |
2. Mode applied (--mode pi overrides auto-detection)
       |
3. Context auto-detected (local / cloud / pi)
       |
4. Compute Tier derived from hardware (high / standard / low)
       |
5. Default tool set resolved from admin-center roles
       |
6. User overrides applied (--paas coolify, --enable photos, etc.)
       |
7. Compute Tier gating (disable tools that exceed hardware)
       |
8. Add-ons resolved (explicit + auto-activated)
       |
9. CUE unification + validation
       |
10. Generate + Apply
```

---

## 5. Multi-Server Scaling

### Node Growth

A StackKit defines the architecture pattern, but node count can grow. The StackKit reacts:

| Phase | Nodes | Behavior |
|-------|-------|----------|
| Start | 1 | All services on one server. Standard StackKit. |
| +1 Node | 2 | Service distribution possible. Heavy use cases on stronger node. |
| +2 Nodes | 3 | Dedicated nodes: Node 1 = Platform, Node 2 = Media/Photos, Node 3 = AI/Dev. |

### Service Placement Rules

1. **Platform services** (Traefik, Auth, PAAS) stay on primary node (replicated in HA Kit)
2. **Use case services** distributed by:
   - Hardware requirements (GPU → AI node, large storage → Media node)
   - User assignment (`services.media.node: server-2` in stack-spec.yaml)
   - Automatic distribution when no explicit assignment

### StackKit Upgrade Recommendations

| Situation | Recommendation |
|-----------|---------------|
| Base Kit + additional local node | Base Kit stays. Distribute services. |
| Base Kit + cloud node | Upgrade to Modern Homelab (hybrid pattern). |
| Base Kit + 3+ nodes + HA needed | Upgrade to HA Kit. |
| Modern Homelab + more nodes | Register node, Placement Engine distributes. |

### Node Registration

```yaml
# Level 0 (CLI): Manual in stack-spec.yaml
nodes:
  - name: server-1
    host: 192.168.1.100
    role: primary
  - name: server-2
    host: 192.168.1.101
    role: worker
    services: [media, photos]  # explicit assignment

# Level 2+ (Agent): Automatic via gRPC Register()
# Agent boots → reports hardware → Placement Engine assigns services
```

---

## 6. Dead Concepts

### Variant (REMOVED in v5)

Variants were mutually exclusive service bundles (default / beszel / minimal / coolify in `stackkit.yaml`). They are replaced by the per-tool role system:

| Old Variant | v5 Equivalent |
|-------------|---------------|
| `default` | Default tool roles (no action needed) |
| `beszel` | `--monitoring beszel` (alternative swap) |
| `coolify` | `--paas coolify` (alternative swap) |
| `minimal` | `--mode pi` or compute tier `low` (automatic) |

The `variants` section is removed from `stackkit.yaml`. The `variant` field is removed from `StackSpec`.

---

## 7. Admin-Center Integration

The kombify Administration center (`admin.kombify.io`) is the source of truth for tool decisions.

### Database Schema (key tables)

| Table | Purpose |
|-------|---------|
| `admin_sk_stackkits` | StackKit definitions (base-kit, modern-homelab, ha-kit) |
| `admin_sk_tools` | Evaluated tools with status, scoring, GitHub metrics |
| `admin_sk_stackkit_tools` | **Per-StackKit tool role assignments** (default/alternative/optional/addon) |
| `admin_sk_tool_alternatives` | Which tools compete/replace each other |
| `admin_sk_categories` | Tool categories with layer hierarchy (L1/L2/L3) |

### Flow: Admin-Center → StackKit

```
Admin-Center DB (tool evaluation, role assignment)
    |
    v (manual sync for Level 0, API sync for Level 1+)
    |
CUE definitions (stackkit.yaml useCases section, addon.cue compatibility)
    |
    v
stackkit generate (reads roles, applies defaults + overrides)
    |
    v
Deploy artifacts (OpenTofu, Docker Compose)
```

For Level 0 (standalone CLI), tool roles are baked into `stackkit.yaml` and CUE files. For Level 1+ (kombify-TechStack integration), roles are synced from the admin-center API.

---

## 8. What Stays from v4

- **3-Layer Architecture** (L1 Foundation, L2 Platform, L3 Applications)
- **StackKit = Architecture Pattern** (single/hybrid/HA)
- **Context = Auto-detected environment** (local/cloud/pi)
- **Add-Ons = Composable extensions** (CUE packaging mechanism unchanged)
- **Progressive Capability Model** (Levels 0-4)
- **CUE as source of truth** for StackKit definitions
- **OpenTofu as internal IaC engine** (never Terraform, never user-facing)
- **Context-Driven Defaults Matrix** (9 cells: 3 StackKits x 3 Contexts)
- **Lifecycle Integration** (Day 0 → Day 1 → Day 2+)

---

## 9. Implementation Priority

### P0: Concept Cleanup (immediate)

1. Remove `variants` from `stackkit.yaml` and Go models
2. Add `useCases` section to `stackkit.yaml` with role assignments
3. Write `docs/CONCEPTS.md` (single-page reference)
4. Update `generate.go` to remove variant references
5. Update `.claude/CLAUDE.md` with CONCEPTS.md reference

### P1: Use Case Integration

6. Implement first use case module (Vaultwarden — lightweight, good test case)
7. Implement Photos use case (Immich)
8. Implement Media use case (Jellyfin + *arr)
9. Add `--mode pi/standard/full` CLI flag
10. Add `--enable <use-case>` CLI flag

### P2: Alternative System

11. Add `--paas coolify` style override flags
12. Implement alternative resolution in `generate.go`
13. Sync tool roles from admin-center (Level 1+ integration)

### P3: Multi-Node

14. Multi-node support in Base Kit (service distribution)
15. Node registration in stack-spec.yaml
16. Placement Engine (Level 2+)

---

## Related Documents

- [CONCEPTS.md](./CONCEPTS.md) — Single-page concept quick reference
- [Architecture v4.0](./ARCHITECTURE_V4.md) — Previous architecture (baseline, not modified)
- [ADR-0003: PaaS Strategy](./ADR/ADR-0003-paas-strategy.md) — Dokploy vs Coolify rationale
- [stack-spec-reference.md](./stack-spec-reference.md) — stack-spec.yaml field reference
