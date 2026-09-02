# StackKit Options and Authoring Matrix

> Last verified: 2026-09-02

This page is the compact contract for adding or promoting StackKit options. CUE
is the technical source of truth. The canonical ResolvedPlan, local Owner
custody, and lifecycle evidence own deployment state. Hosted catalog or
database projections are optional mirrors; standalone operation does not
depend on them.

## Roles

| Role | Meaning | Release requirement |
| --- | --- | --- |
| `default` | Enabled by the kit without user action. | Fresh-target smoke, first-run path, auth/routing, backup classification, upgrade notes, and registry/CUE hash parity. |
| `alternative` | Curated swap for a default in the same group. | Same generated contract shape as the default, documented migration/limits, and explicit user selection. |
| `optional` | Available but off by default. | CUE validation, generate path, documented enablement, and known gaps. |

## Current Basement Kit Platform Matrix

| Concern | Release value |
| --- | --- |
| Selected-provider core realization | `coolify` is the existing default provider; the native core module profile and explicit platform intent must agree. |
| PaaS alternative | `komodo` is Beta in the native workload compatibility contract. It does not have native application backup/restore parity. |
| Draft PaaS adapter | `dokploy` |
| Invalid normal PaaS values | `dockge`, `none` |
| Dockge status | Experimental/constrained Compose manager service only; not a normal Basement Kit PaaS. |
| Native module profiles | `stackkit/v2alpha2` selects each module's `computeProfile` explicitly. Core Lite `low` means standalone Compose without Coolify, but does not force Photos or Media to the same profile. Immich Lite is a separate workload alternative, not an implicit consequence of selecting Core Lite. See ADR-0039. |
| Legacy low compute tier | Explicit `stackkit/v2alpha1` retains `install.computeTier: low` and CUE `computeTierGraphs.low` for compatibility. Cloud and Modern do not publish that legacy graph. It is not a native module-profile default. |
| Media | Optional Architecture v2 Jellyfin with declared module-local `standard` and `high` profiles (`docker.io/jellyfin/jellyfin:10.10.7`, digest-pinned). Native Core Lite does not globally exclude Media. The old v2alpha1 `low` graph still excludes it. Library volume is owner-custodied and not a StackKits backup source. No `*arr` services. |
| Smart Home | Optional Architecture v2 Home Assistant container (`ghcr.io/home-assistant/home-assistant:2026.7.2`, digest-pinned) on Basement/Cloud/Modern. Native product MCP is `/api/mcp` on `https://smart-home.<domain>`. Generate writes the reverse-proxy baseline and Homelab owner intent (`homelab`). No HA OS/Supervisor parity, no Zigbee/MQTT runtime in this slice. |

The executable selection authority is the CUE workload catalog in
`foundation/architecture_v2_catalog.cue`, combined with the selected kit's
`Definition`. A use-case package can describe planned tools or integration
profiles; that description does not make them selectable through native
`init`, `resolve`, or `apply`. Unknown alternatives fail admission.
Here, native means the StackSpec `workloads` path, including explicit v2alpha2
selections and the retained v2alpha1 graph adapter. Older `application`-based
compatibility composition can describe other tools; it does not establish
their native rollout support.

| Application | Native default alternative | Additional native alternative or limit |
| --- | --- | --- |
| Files | `cloudreve` | Nextcloud and DMS are planned product intent; no native rollout or migration parity is claimed. |
| Photos | `immich` | `immich-lite` is an explicit alternative without ML search. Core Lite does not select it implicitly. |
| Media | `jellyfin` | No bundled `*arr` stack; the owner supplies and retains custody of the media library. |
| Smart Home | `home-assistant` | Container only; Home Assistant OS, Supervisor, Home Bridge, MQTT, and Zigbee provisioning are not native alternatives. Cloud placement grants no Home LAN access. |
| Vault | `vaultwarden` | The owner creates the encrypted account through the official client; StackKits does not handle the master password. |

These defaults identify the implementation to select for an enabled application;
they do not enable every application. Native v2alpha2 records the selected
alternative and module profile explicitly.

The CUE compatibility map distinguishes the delivery adapters: standalone
Compose declares support for the native application backup/restore path;
Coolify declares support for deployment, routing/TLS, and status evidence, but has
`backupRestore: false`; Komodo declares the same limit and is Beta. A rendered
backup classification or a fallback adapter list does not establish recovery
parity or authorize an automatic adapter switch. These declarations are not
evidence of a successful live deployment or restore. Existing data stays with its
recorded runtime owner until an explicit migration is supported and approved.

Change native PaaS behavior in that CUE contract and the responsible resolver,
renderer, or runtime owner, then regenerate the affected projections and update
the user-facing documentation. Kit YAML is derived registry/migration metadata;
do not use it to introduce a second native selection authority.

Native profile changes start in the CUE module catalog and its
`computeProfiles`, `storageProfiles`, or `acceleratorProfiles`. The compiler
validates the explicit choices and aggregates declared per-node resource facts;
inventory never chooses a profile. Regenerate the authority bundle and public
module-profile catalog. Missing resource facts stay unverified, not invented.

Changes to the retained v2alpha1 graph instead start in
`basement-kit/stackfile.cue` `computeTierGraphs` and
`#WorkloadContractV2.computeTiers`. Label derived legacy YAML and documentation
accordingly. Cloud YAML `low` is kitio roundtrip only and has no legacy graph.

## Authoring Flow

1. Define or update the CUE/module contract first under `modules/`, the
   relevant kit directory, or `base/`.
2. Classify the role in kit metadata with `role`, `defaultTool`, and
   `alternatives` where applicable.
3. Add resolver or generator code only when CUE cannot express the behavior
   yet; keep Go defaults aligned with the CUE contract.
4. Update docs and website copy only after the source contract is decided.
5. Run the affected behavioral gate. Packaging, activation, and stable promotion
   use their separately declared evidence paths.

## Promotion Gates

| Promotion | Minimum tests |
| --- | --- |
| Experimental to optional | `cue vet`, module CUE validation, generate path, docs for known gaps. |
| Optional to alternative | Resolver/generator tests, compatibility with existing defaults, docs for migration and limits. |
| Alternative to default | Fresh-target smoke, release archive smoke, identity/secret checks, `stackkit verify` coverage, rollback/update notes. |
| Kit to release-ready | Public installer smoke, full archive validation, live Basement Kit-style scenario evidence, and no HTML fallback on one-line endpoints. |

## Architecture v2 Home LAN Discovery

LAN discovery is opt-in even when a service already has a local route. Author
only the explicit resolved-route allowlist:

```yaml
lanDiscovery:
  advertiseRouteRefs:
    - dashboard
```

The empty/default list advertises nothing. Every referenced route must resolve
to a Home-originated, local, non-`.localhost`, default-closed LAN policy. This
intent does not select a DNS server, interface, address, mDNS/DNS-SD runtime,
provider, or credential. Those are separate runtime and evidence contracts.

## Required Release Checks

For pre-1.0 changes, run the affected gate (target two minutes, hard limit five):

```bash
mise run check
```

For publication, follow the [public release workflow](https://github.com/kombifyio/stackKits/blob/main/.github/workflows/release.yml). The v0.x path binds a clean
checkout and built/exported artifacts to the exact current main SHA. Target,
browser, compatibility, and restore evidence remain separate diagnostics;
missing evidence is pending, never a fabricated pass. The former in-repository
live-installer harness is retired and must not be recreated as a v0.x gate.
Stable v1.0 publication additionally requires the signed exact-SHA Candidate
receipt. The option-promotion evidence above describes product confidence,
not extra v0.x publication dependencies.
