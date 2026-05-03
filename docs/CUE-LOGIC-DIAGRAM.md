# CUE Schema Logic — Mermaid-Diagramme

> **Stand:** 2026-04-07
> **Scope:** Visualisierung der tatsaechlich implementierten CUE-Logik (bis jetzt)
> **Quellen:** `base/*.cue`, `base/generated/*.cue`, `base-kit/*.cue`, `modules/*/module.cue`

---

## 1. Schema-Hierarchie

Wie die CUE-Definitionen aufeinander aufbauen:

```mermaid
graph TD
    subgraph "Base Layer — base/*.cue"
        SK["#BaseStackKit"]
        SD["#ServiceDefinition"]
        MC["#ModuleContract"]
        NC["#NodeContext"]
        CC["#ContextConfig"]
        CD["#ContextDefaults"]
        VC["#VirtualizationConfig"]
        KF["#KernelFeatures"]
        CT["#CompatibilityTier"]
        SL["#ModuleLayer"]
        ST["#ServiceType"]
    end

    subgraph "Generated — base/generated/*.cue"
        GC["#ContextDefaults Registry"]
        TC["#ToolCategories"]
        TL["#ToolCatalog"]
        AR["#AddOnRegistry"]
    end

    subgraph "StackKit — base-kit/*.cue"
        BKS["#BaseKitStack"]
        SS["#ServiceSet"]
        SMD["#SmartDefaults"]
        CTD["#ComputeTierDetector"]
        DC["#DomainConfig"]
        DEP["#DeploymentConfig"]
    end

    subgraph "Modules — modules/*/module.cue"
        MT["traefik"]
        MTA["tinyauth"]
        MPK["pocketid"]
        MDK["dokploy"]
        MCO["coolify"]
        MDG["dockge"]
        MUK["uptime-kuma"]
        MVW["vaultwarden"]
        MJF["jellyfin"]
        MIM["immich"]
        MDB["dashboard"]
        MSP["socket-proxy"]
    end

    SK --> SD
    SK --> NC
    SK --> VC
    MC --> SD
    MC --> SL
    SD --> ST
    CD --> NC
    CD --> CC
    VC --> KF
    VC --> CT

    GC --> NC
    TC --> TL

    BKS --> SS
    BKS --> SMD
    BKS --> CTD
    BKS --> DC
    BKS --> DEP

    MT --> MC
    MTA --> MC
    MPK --> MC
    MDK --> MC
    MCO --> MC
    MDG --> MC
    MUK --> MC
    MVW --> MC
    MJF --> MC
    MIM --> MC
    MDB --> MC
    MSP --> MC

    style SK fill:#2563eb,color:#fff
    style BKS fill:#7c3aed,color:#fff
    style MC fill:#059669,color:#fff
    style GC fill:#d97706,color:#fff
```

---

## 2. Context-Defaults — `#ContextDefaults`

Was CUE je nach `_context` auflöst (`base/context.cue`):

```mermaid
flowchart TD
    Input["_context: #NodeContext"] --> Switch{"_context?"}

    Switch -- '"local"' --> Local["TLS: self-signed<br/>ACME: false<br/>PAAS: dokploy<br/>Tier: standard<br/>Memory: 1.0x<br/>CPU: 1024 shares<br/>Arch: amd64<br/>Access: ports<br/>Public IP: false<br/>Storage: overlay2"]

    Switch -- '"cloud"' --> Cloud["TLS: letsencrypt<br/>ACME: true<br/>PAAS: coolify<br/>Tier: standard<br/>Memory: 1.0x<br/>CPU: 1024 shares<br/>Arch: amd64<br/>Access: proxy<br/>Public IP: true<br/>Storage: overlay2"]

    Switch -- '"pi"' --> Pi["TLS: self-signed<br/>ACME: false<br/>PAAS: dockge<br/>Tier: low<br/>Memory: 0.5x<br/>CPU: 512 shares<br/>Arch: arm64<br/>Access: ports<br/>Public IP: false<br/>Storage: overlay2"]

    style Input fill:#2563eb,color:#fff
    style Local fill:#22c55e,color:#fff
    style Cloud fill:#06b6d4,color:#fff
    style Pi fill:#a855f7,color:#fff
```

### Generated Registry (`base/generated/contexts.cue`)

Ergaenzt identische Logik mit Admin-DB-Werten:

| Context | defaultPaas | defaultTlsMode | defaultComputeTier | memoryLimitMB | cpuShares | dnsStrategy | backupTarget |
|---------|-------------|----------------|--------------------|---------------|-----------|-------------|--------------|
| LOCAL | dokploy | self-signed | standard | 4096 | 1024 | local-dns | local-nas |
| CLOUD | coolify | letsencrypt | standard | 2048 | 1024 | cloud-dns | s3 |
| PI | dockge | self-signed | low | 256 | 512 | mdns | local-nas |

---

## 3. ComputeTier-Erkennung — `#ComputeTierDetector`

`base-kit/defaults.cue`:

```mermaid
flowchart TD
    Input["cpu: int, memory: int"] --> High{"cpu ≥ 8 AND<br/>memory ≥ 16?"}
    High -- ja --> TierHigh["tier: 'high'"]
    High -- nein --> Low{"cpu < 4 OR<br/>memory < 8?"}
    Low -- ja --> TierLow["tier: 'low'"]
    Low -- nein --> TierStd["tier: 'standard'<br/>(Default)"]

    style Input fill:#2563eb,color:#fff
    style TierHigh fill:#ef4444,color:#fff
    style TierStd fill:#f59e0b,color:#000
    style TierLow fill:#6b7280,color:#fff
```

---

## 4. SmartDefaults — `#SmartDefaults`

Was CUE je nach `computeTier` aktiviert (`base-kit/defaults.cue`):

```mermaid
flowchart TD
    Tier["computeTier"] --> Switch{"Wert?"}

    Switch -- '"high"' --> High["monitoring: full<br/>management: advanced<br/>logging: full<br/>Services: traefik, dockge, dozzle,<br/>netdata, portainer, prometheus, grafana<br/><br/>Docker: mem 4g, cpu 4.0, max 50<br/>Traefik: accessLog, metrics, tracing<br/>Backup: alle 6h, 14d/8w/12m"]

    Switch -- '"standard"' --> Std["monitoring: standard<br/>management: basic<br/>logging: basic<br/>Services: traefik, dockge,<br/>dozzle, netdata<br/><br/>Docker: mem 1g, cpu 1.0, max 20<br/>Traefik: metrics only<br/>Backup: taeglich 3 Uhr, 7d/4w/6m"]

    Switch -- '"low"' --> Low["monitoring: minimal<br/>management: minimal<br/>logging: basic<br/>Services: traefik, dockge,<br/>dozzle, glances<br/><br/>Docker: mem 512m, cpu 0.5, max 10<br/>Traefik: minimal<br/>Backup: woechentlich So, 3d/2w/1m"]

    style Tier fill:#2563eb,color:#fff
    style High fill:#ef4444,color:#fff
    style Std fill:#f59e0b,color:#000
    style Low fill:#6b7280,color:#fff
```

---

## 5. Deployment Mode — `#DeploymentConfig`

CUE-Konditionallogik in `base-kit/stackfile.cue`:

```mermaid
flowchart TD
    Mode["mode"] --> Switch{"Wert?"}

    Switch -- '"simple"' --> Simple["day1:<br/>  engine: opentofu<br/>  actions: init, plan, apply<br/>day2:<br/>  enabled: false"]

    Switch -- '"advanced"' --> Advanced["day1:<br/>  engine: opentofu<br/>  actions: init, plan, apply<br/>day2:<br/>  enabled: true<br/>  engine: terramate<br/>  actions: drift, update, destroy<br/>  features:<br/>    drift_detection: true<br/>    change_sets: true<br/>    rolling_updates: true<br/>    stack_ordering: true"]

    style Mode fill:#2563eb,color:#fff
    style Simple fill:#22c55e,color:#fff
    style Advanced fill:#7c3aed,color:#fff
```

---

## 6. Virtualisierung — `#VirtualizationConfig`

Kompatibilitaets-Tiers in `base/virtualization.cue`:

```mermaid
flowchart TD
    VirtType["#VirtualizationType"] --> Check{"Typ?"}

    Check -- "none / kvm" --> Full["Tier: full<br/>unshare: true<br/>overlayfs: true<br/>bridge: true<br/>iptablesNAT: true"]

    Check -- "lxc (nesting)" --> Degraded["Tier: degraded<br/>unshare: true<br/>overlayfs: evtl. false<br/>bridge: evtl. false<br/><br/>Workarounds:<br/>  vfsStorageFallback<br/>  hostNetworkFallback<br/>  dnsFallback"]

    Check -- "openvz" --> Incompat["Tier: incompatible<br/>unshare: FALSE<br/><br/>Docker kann NICHT laufen"]

    Check -- "vmware / hyperv / xen" --> FullVM["Tier: full<br/>(vollstaendige VM)"]

    Full --> Req["Requirements:<br/>unshare: true (non-negotiable)<br/>minimumTier: degraded"]
    Degraded --> Req
    FullVM --> Req
    Incompat -.->|"REJECTED"| Req

    style VirtType fill:#2563eb,color:#fff
    style Full fill:#22c55e,color:#fff
    style Degraded fill:#f59e0b,color:#000
    style Incompat fill:#ef4444,color:#fff
    style FullVM fill:#22c55e,color:#fff
```

### Auto-Workarounds (`#AutoWorkarounds`)

| Workaround | Default | Beschreibung |
|------------|---------|------------|
| `vfsStorageFallback` | true | Fallback auf vfs wenn overlay2 nicht geht |
| `hostNetworkFallback` | true | Host-Networking wenn Bridge geblockt |
| `dnsFallback` | true | 1.1.1.1 / 8.8.8.8 injizieren |
| `hostPrePull` | true | Images ueber Host-DNS vorziehen |
| `iptablesLegacyFallback` | true | iptables-legacy wenn nf_tables fehlschlaegt |

---

## 7. Tool-Kategorie-System — `#ToolCategories`

`base/generated/tool_catalog.cue` — Admin-DB-generiert:

```mermaid
flowchart LR
    subgraph "Layer 1 — Foundation"
        ID["identity<br/>Standard: lldap<br/>Alt: openldap, freeipa"]
    end

    subgraph "Layer 2 — Platform"
        RP["reverse-proxy<br/>Standard: traefik<br/>Alt: caddy, nginx-pm, haproxy"]
        PI["platform-identity<br/>Standard: tinyauth<br/>Alt: pocketid, authelia, authentik"]
        PA["paas<br/>Standard: dokploy<br/>Alt: coolify, caprover, portainer"]
        MG["management<br/>Standard: dozzle<br/>Alt: portainer, dockge, lazydocker"]
    end

    subgraph "Layer 3 — Application"
        MO["monitoring<br/>Standard: uptime-kuma<br/>Alt: beszel, netdata, prometheus, grafana"]
    end

    style ID fill:#06b6d4,color:#fff
    style RP fill:#7c3aed,color:#fff
    style PI fill:#7c3aed,color:#fff
    style PA fill:#7c3aed,color:#fff
    style MG fill:#7c3aed,color:#fff
    style MO fill:#22c55e,color:#fff
```

---

## 8. ModuleContract-Struktur — `#ModuleContract`

Jedes Modul in `modules/*/module.cue` folgt diesem Schema (`base/module.cue`):

```mermaid
flowchart TD
    MC["#ModuleContract"] --> Meta["metadata<br/>#ModuleMetadata<br/>name, displayName, version,<br/>layer, description"]
    MC --> Req["requires?<br/>#RequiresSpec"]
    MC --> Prov["provides?<br/>#ProvidesSpec"]
    MC --> Set["settings?<br/>#SettingsSpec"]
    MC --> Ctx["contexts?<br/>#ContextOverrides"]
    MC --> Svc["services<br/>[string]: #ServiceDefinition"]
    MC --> Prv["provisioners?<br/>[string]: #ProvisionerService"]
    MC --> En["enabled: bool | *true"]

    Req --> ReqSvc["services?<br/>z.B. traefik: {provides: [reverse-proxy]}"]
    Req --> ReqInfra["infrastructure?<br/>docker, dockerSocket, minMemory, arch"]

    Prov --> ProvCap["capabilities?<br/>z.B. {reverse-proxy: true}"]
    Prov --> ProvMW["middleware?<br/>z.B. forwardauth, ratelimit"]
    Prov --> ProvEP["endpoints?<br/>URL-Templates"]

    Set --> Perma["perma<br/>(nach Deploy unveraenderbar)"]
    Set --> Flex["flexible<br/>(aenderbar via Day-2)"]

    Meta --> Layer["layer:<br/>L1-foundation<br/>L2-platform-ingress<br/>L2-platform-identity<br/>L2-platform-paas<br/>L2-platform-dns<br/>L3-application"]

    style MC fill:#059669,color:#fff
    style Meta fill:#2563eb,color:#fff
    style Layer fill:#7c3aed,color:#fff
```

---

## 9. Service-Definitionen — `#ServiceDefinition`

Vollstaendige Struktur aus `base/stackkit.cue`:

```mermaid
flowchart TD
    SD["#ServiceDefinition"] --> Core["PFLICHT:<br/>name (DNS-safe)<br/>type (#ServiceType)<br/>image (Container-Image)<br/>network (#ServiceNetworkConfig)<br/>enabled (bool, default true)"]

    SD --> Optional["OPTIONAL:<br/>displayName, category<br/>tag (default: latest)<br/>needs (Dependencies)<br/>volumes, environment<br/>healthCheck, resources<br/>restartPolicy<br/>labels (Traefik-Discovery)"]

    SD --> Routing["ROUTING:<br/>subdomain?<br/>  key (TF-Map Key)<br/>  nested (eigene Domain)<br/>  flat (kombify.me)"]

    SD --> UI["DASHBOARD:<br/>dashboard?<br/>  icon (HTML-Entity)<br/>  order (Sortierung)<br/>  section (Platform|Applications)<br/>  badge (Layer-Label)<br/>  enableVar? (TF-Variable)"]

    Core --> Types["#ServiceType — 26 Typen:<br/>reverse-proxy, paas,<br/>compose-manager, auth,<br/>directory, pki, monitoring,<br/>media, application, ..."]

    style SD fill:#2563eb,color:#fff
    style Routing fill:#f59e0b,color:#000
    style UI fill:#7c3aed,color:#fff
```

---

## 10. Domain-Berechnung — `#DomainConfig`

CUE-Comprehension in `base-kit/defaults.cue`:

```mermaid
flowchart TD
    Input["domain: string<br/>subdomainPrefix: string"] --> Check{"subdomainPrefix?"}

    Check -- "nicht leer (kombify.me)" --> Flat["URLs via Comprehension:<br/>for k, v in subdomains:<br/>  urls[k] = {prefix}-{v.flat}.{domain}<br/><br/>Beispiel:<br/>  sh-mylab-abc-dash.kombify.me<br/>  sh-mylab-abc-dokploy.kombify.me<br/>  sh-mylab-abc-kuma.kombify.me"]

    Check -- "leer (eigene Domain)" --> Nested["URLs via Comprehension:<br/>for k, v in subdomains:<br/>  urls[k] = {v.nested}.{domain}<br/><br/>Beispiel:<br/>  base.kmbchr.de<br/>  dokploy.kmbchr.de<br/>  kuma.kmbchr.de"]

    Flat --> Map
    Nested --> Map

    Map["Subdomain-Map (13 Eintraege):<br/>dashboard: base / dash<br/>traefik: traefik / traefik<br/>auth: auth / tinyauth<br/>pocketid: id / id<br/>dokploy: dokploy / dokploy<br/>coolify: coolify / coolify<br/>dockge: dockge / dockge<br/>kuma: kuma / kuma<br/>whoami: whoami / whoami<br/>vault: vault / vault<br/>media: media / media<br/>photos: photos / photos<br/>logs: logs / logs"]

    style Input fill:#2563eb,color:#fff
    style Flat fill:#a855f7,color:#fff
    style Nested fill:#22c55e,color:#fff
```

---

## 11. Constraint-Validierung

CUE-Validierungen die beim `cue vet` greifen:

```mermaid
flowchart TD
    subgraph "String-Constraints"
        S1["hostname: =~'^[a-z][a-z0-9-]*[a-z0-9]$'"]
        S2["email: =~'^[a-zA-Z0-9._%+-]+@...'"]
        S3["semver: =~'^v?[0-9]+.[0-9]+.[0-9]+...'"]
        S4["cidr: =~'^((25[0-5]|...)\.){3}...'"]
    end

    subgraph "Numerische Constraints"
        N1["#PortRange: uint16 & >0 & <=65535"]
        N2["cpuCores: int & >=1 & <=256"]
        N3["ramGB: int & >=2"]
        N4["storageGB: int & >=20"]
        N5["cpuShares: int & >=256 & <=2048"]
        N6["memoryFactor: number & >=0.25 & <=2.0"]
    end

    subgraph "Listen-Constraints"
        L1["nodes: MinItems(1) & MaxItems(1)<br/>(Base Kit = Single Server)"]
        L2["HA nodes: MinItems(2) & MaxItems(100)"]
    end

    subgraph "Enum-Disjunctions"
        E1["context: 'local' | 'cloud' | 'pi'"]
        E2["mode: 'simple' | 'advanced'"]
        E3["variant: 'default' | 'coolify' | 'beszel' | 'minimal'"]
        E4["tier: 'high' | 'standard' | 'low'"]
        E5["paas: 'dokploy' | 'coolify' | 'dockge' | 'none'"]
        E6["virtualization: 'kvm' | 'openvz' | 'lxc' | ..."]
        E7["tlsMode: 'acme' | 'self-signed' | 'custom' | 'none'"]
        E8["storageDriver: 'overlay2' | 'fuse-overlayfs' | 'vfs'"]
        E9["compatibilityTier: 'full' | 'degraded' | 'incompatible'"]
    end

    style S1 fill:#06b6d4,color:#fff
    style N1 fill:#f59e0b,color:#000
    style L1 fill:#ef4444,color:#fff
    style E1 fill:#7c3aed,color:#fff
```

---

## 12. Layer-Architektur in CUE

Wie die 3 Layer in CUE abgebildet sind:

```mermaid
flowchart TD
    subgraph "Layer 1 — Foundation"
        direction LR
        L1A["#SystemConfig"]
        L1B["#SSHHardening"]
        L1C["#FirewallPolicy"]
        L1D["#LLDAPConfig<br/>(enabled: true — Pflicht)"]
        L1E["#StepCAConfig<br/>(enabled: true — Pflicht)"]
        L1F["#VirtualizationConfig<br/>(unshare: true — Pflicht)"]
    end

    subgraph "Layer 2 — Platform"
        direction LR
        L2A["#TraefikService<br/>(required: true — immer an)"]
        L2B["#PAASConfig<br/>dokploy | coolify | dockge"]
        L2C["#TinyAuthConfig<br/>(Standard Identity Proxy)"]
        L2D["#PocketIDConfig<br/>(Optional OIDC Provider)"]
        L2E["socket-proxy<br/>(Docker Socket Security)"]
    end

    subgraph "Layer 3 — Applications"
        direction LR
        L3A["uptime-kuma"]
        L3B["vaultwarden"]
        L3C["dashboard"]
        L3D["jellyfin"]
        L3E["immich"]
        L3F["whoami"]
    end

    L1D --> L2C
    L1E --> L2A
    L1F --> L2B
    L2A --> L2B
    L2A --> L3A
    L2A --> L3B
    L2A --> L3C
    L2A --> L3D
    L2A --> L3E
    L2E --> L2B

    style L1D fill:#06b6d4,color:#fff
    style L1E fill:#06b6d4,color:#fff
    style L1F fill:#06b6d4,color:#fff
    style L2A fill:#7c3aed,color:#fff
    style L2B fill:#7c3aed,color:#fff
    style L2C fill:#7c3aed,color:#fff
    style L2D fill:#7c3aed,color:#fff
    style L3A fill:#22c55e,color:#fff
    style L3B fill:#22c55e,color:#fff
    style L3C fill:#22c55e,color:#fff
```

---

## 13. HA-Kit Quorum-Logik

CUE-Konditionals fuer Docker Swarm (`ha-kit/stackfile.cue`):

```mermaid
flowchart TD
    Managers["managerCount"] --> Q3{"== 3?"}
    Q3 -- ja --> R3["quorum: 2<br/>maxFailures: 1"]
    Q3 -- nein --> Q5{"== 5?"}
    Q5 -- ja --> R5["quorum: 3<br/>maxFailures: 2"]
    Q5 -- nein --> Q7{"== 7?"}
    Q7 -- ja --> R7["quorum: 4<br/>maxFailures: 3"]

    Managers --> Nodes["nodes: MinItems(2)<br/>MaxItems(100)"]

    style Managers fill:#2563eb,color:#fff
    style R3 fill:#22c55e,color:#fff
    style R5 fill:#f59e0b,color:#000
    style R7 fill:#ef4444,color:#fff
```

---

## 14. Addon-System — `#AddOnBase`

Struktur (`base/context.cue`) + Registry (`base/generated/addons.cue`):

```mermaid
flowchart TD
    Addon["#AddOnBase"] --> Meta["_addon: #AddOnMetadata<br/>name, displayName, version,<br/>layer, description"]
    Addon --> Compat["_compatibility:<br/>stackkits: [...] (leer = alle)<br/>contexts: [...] (leer = alle)<br/>requires: [...] (Abhaengigkeiten)<br/>conflicts: [...] (Konflikte)"]
    Addon --> Enable["enabled: bool | *true"]

    Meta --> Registry["13 registrierte Addons"]

    subgraph "Addon-Registry"
        A1["media — Jellyfin + *arr"]
        A2["monitoring — Prometheus/Grafana/Loki"]
        A3["backup — Restic"]
        A4["vpn-overlay — Headscale/Tailscale"]
        A5["vault — Password Manager"]
        A6["smart-home — Home Assistant"]
        A7["tunnel — CGNAT Bypass"]
        A8["ci-cd — Gitea + Drone"]
        A9["mail, calendar, authelia,<br/>dev-platform, file-sharing,<br/>gpu-workloads"]
    end

    Registry --> A1
    Registry --> A2
    Registry --> A3
    Registry --> A4
    Registry --> A5
    Registry --> A6
    Registry --> A7
    Registry --> A8
    Registry --> A9

    style Addon fill:#059669,color:#fff
```

---

## 15. Gesamtfluss: Input → CUE → Output

```mermaid
flowchart TD
    User["User Input:<br/>context, tier, mode,<br/>domain, paas, addons"] --> BKS["#BaseKitStack<br/>Unified Schema"]

    BKS --> CD2["#ContextDefaults<br/>if context == local/cloud/pi"]
    BKS --> CTD2["#ComputeTierDetector<br/>if cpu/memory thresholds"]
    BKS --> SMD2["#SmartDefaults<br/>if tier == high/standard/low"]
    BKS --> DEP2["#DeploymentConfig<br/>if mode == simple/advanced"]
    BKS --> DC2["#DomainConfig<br/>if subdomainPrefix set/leer"]

    CD2 --> Resolved["Aufgeloeste Konfiguration"]
    CTD2 --> Resolved
    SMD2 --> Resolved
    DEP2 --> Resolved
    DC2 --> Resolved

    Resolved --> Validate["cue vet<br/>Constraint-Pruefung:<br/>Typen, Ranges, Regex, Enums"]
    Validate --> Export["cue export → JSON<br/>→ Bridge (Go) liest<br/>→ specToTFVars()"]

    BKS --> Modules["Module-Contracts<br/>(#ModuleContract)"]
    Modules --> Comp["Composition Engine (Go)<br/>Resolve → Sort → Settings"]
    Comp --> Overlay["Overlay auf TFVars:<br/>EnableFlags + Identity"]

    Export --> TFVars["terraform.tfvars.json"]
    Overlay --> TFVars

    style User fill:#2563eb,color:#fff
    style BKS fill:#7c3aed,color:#fff
    style Resolved fill:#f59e0b,color:#000
    style TFVars fill:#10b981,color:#fff
    style Validate fill:#ef4444,color:#fff
```
