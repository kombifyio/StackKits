# StackKit Options and Authoring Matrix

> Last verified: 2026-08-27

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
| Default PaaS (`standard` / `high`) | `coolify` |
| Production PaaS alternative | `komodo` |
| Draft PaaS adapter | `dokploy` |
| Invalid normal PaaS values | `dockge`, `none` |
| Dockge status | Experimental/constrained Compose manager service only; not a normal Basement Kit PaaS. |
| Low compute tier | Basement `install.computeTier: low` is standalone Compose, no Coolify, Immich without ML (`immich-lite`). CUE `computeTierGraphs.low` is authority. It is not a Coolify-gated subset of `standard` and not a Dockge switch. Cloud and Modern do not publish `low`. |
| Media | Optional Architecture v2 Jellyfin on Basement/Cloud/Modern `standard` and `high` (`docker.io/jellyfin/jellyfin:10.10.7`, digest-pinned). Library volume is owner-custodied and not a StackKits backup source. Basement `low` omits Media until a lite substitution exists. No `*arr` services. |
| Smart Home | Optional Architecture v2 Home Assistant container (`ghcr.io/home-assistant/home-assistant:2026.7.2`, digest-pinned) on Basement/Cloud/Modern. Native product MCP is `/api/mcp` on `https://smart-home.<domain>`. Generate writes the reverse-proxy baseline and Homelab owner intent (`homelab`). No HA OS/Supervisor parity, no Zigbee/MQTT runtime in this slice. |

When the PaaS contract for `standard`/`high` changes, update all of these together:
`basement-kit/stackkit.yaml`, `cloud-kit/stackkit.yaml`, `base/defaults.cue`, the Go resolver/validator,
`docs/stack-spec-reference.md`, `docs/CONCEPTS.md`, website installer copy, and
release archive smoke expectations.

When the compute-tier graph changes, edit CUE first (`basement-kit/stackfile.cue`
`computeTierGraphs` and catalog `#WorkloadContractV2.computeTiers`), then derive
YAML floors, this page, `docs/CONCEPTS.md` §3, and `docs/stack-spec-reference.md`.
Cloud YAML `low` is kitio roundtrip only and has no graph.

## Authoring Flow

1. Define or update the CUE/module contract first under `modules/`, the
   relevant kit directory, or `base/`.
2. Classify the role in kit metadata with `role`, `defaultTool`, and
   `alternatives` where applicable.
3. Add resolver or generator code only when CUE cannot express the behavior
   yet; keep Go defaults aligned with the CUE contract.
4. Update docs and website copy only after the source contract is decided.
5. Add the narrowest tests that prove the changed layer, then broaden for
   release surfaces.

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

For any option, installer, or kit-default change:

```bash
go test ./...
cue vet -c=false ./foundation/...
cue vet ./basement-kit/... ./cloud-kit/...
mise run test:cue-binding
mise run test:website
```

For release packaging:

```bash
goreleaser release --snapshot --clean
bash scripts/release/validate-release-archives.sh dist
bash tests/e2e/test_live_installers.sh
```
