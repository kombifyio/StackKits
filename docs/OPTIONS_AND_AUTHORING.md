# StackKit Options and Authoring Matrix

> Last verified: 2026-09-01

This page is the compact contract for adding or promoting StackKit options. CUE
is the technical source of truth; the kombify database mirrors catalog,
version, rollout, and lifecycle state.

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
| Production PaaS alternative | `komodo` |
| Draft PaaS adapter | `dokploy` |
| Invalid normal PaaS values | `dockge`, `none` |
| Dockge status | Experimental/constrained Compose manager service only; not a normal Basement Kit PaaS. |
| Native module profiles | `stackkit/v2alpha2` selects each module's `computeProfile` explicitly. Core Lite `low` means standalone Compose without Coolify, but does not force Photos or Media to the same profile. Immich Lite is a separate workload alternative, not an implicit consequence of selecting Core Lite. See ADR-0039. |
| Legacy low compute tier | Explicit `stackkit/v2alpha1` retains `install.computeTier: low` and CUE `computeTierGraphs.low` for compatibility. Cloud and Modern do not publish that legacy graph. It is not a native module-profile default. |
| Media | Optional Architecture v2 Jellyfin with declared module-local `standard` and `high` profiles (`docker.io/jellyfin/jellyfin:10.10.7`, digest-pinned). Native Core Lite does not globally exclude Media. The old v2alpha1 `low` graph still excludes it. Library volume is owner-custodied and not a StackKits backup source. No `*arr` services. |
| Smart Home | Optional Architecture v2 Home Assistant container (`ghcr.io/home-assistant/home-assistant:2026.7.2`, digest-pinned) on Basement/Cloud/Modern. Native product MCP is `/api/mcp` on `https://smart-home.<domain>`. Generate writes the reverse-proxy baseline and Homelab owner intent (`homelab`). No HA OS/Supervisor parity, no Zigbee/MQTT runtime in this slice. |

When the PaaS contract for `standard`/`high` changes, update all of these together:
`basement-kit/stackkit.yaml`, `cloud-kit/stackkit.yaml`, `base/defaults.cue`, the Go resolver/validator,
`docs/stack-spec-reference.md`, `docs/CONCEPTS.md`, website installer copy, and
release archive smoke expectations.

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

For release packaging:

```bash
goreleaser release --snapshot --clean
bash scripts/release/validate-release-archives.sh dist
bash tests/e2e/test_live_installers.sh
```
