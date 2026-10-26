# StackKit Options and Authoring Matrix

> Last verified: 2026-09-27

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
| Default core and application realization | Standalone Compose is the complete primary experience. New Basement and Cloud core choices resolve to `standalone-compose`; application delivery defaults to Compose. No Coolify or Komodo services are installed by the default core. |
| Optional application and operations | Marketplace owns the optional Coolify application outside default core; Komodo is the planned operations platform without a store. Existing explicit compatibility adapters retain one execution and routing owner per workload. See ADR-0042 and ADR-0049. |
| Komodo compatibility metadata | `komodo` is labelled Beta in the native workload compatibility contract; this does not prove current-pin runtime support or application backup/restore parity. |
| Layer-2 PaaS platforms | Coolify, Komodo and Dokploy are the Layer-2 PaaS platforms and later runtime targets; Dokploy's historical draft does not establish current-pin support. |
| Legacy PaaS values | Historical `paas: none` is not the native `standalone-compose` alternative. `dockge` is not a native platform alternative. |
| Dockge status | Experimental/constrained Compose manager service only; not a normal Basement Kit PaaS. |
| Native module profiles | `stackkit/v2alpha2` selects each module's `computeProfile` explicitly. The default standalone core supports its declared profiles without becoming a reduced edition. Historical `lite` module IDs remain stable; they do not force Photos or Media to the same profile. Immich Lite is a separate workload alternative. See ADR-0039 and ADR-0042. |
| Legacy low compute tier | Explicit `stackkit/v2alpha1` retains `install.computeTier: low` and CUE `computeTierGraphs.low` for compatibility. Cloud and Modern do not publish that legacy graph. It is not a native module-profile default. |
| Media | Optional Architecture v2 Jellyfin with declared module-local `standard` and `high` profiles (`docker.io/jellyfin/jellyfin:12.1`, digest-pinned). Native Core Lite does not globally exclude Media. The old v2alpha1 `low` graph still excludes it. Separately selected Navidrome music and Audiobookshelf audiobooks have native administrator/library-scan setup. Library bytes are owner-custodied and not a StackKits backup source; application state is covered separately. No `*arr` services. |
| Smart Home | Optional Architecture v2 Home Assistant container (`ghcr.io/home-assistant/home-assistant:2026.9.4`, digest-pinned) on Basement/Cloud/Modern. Native product MCP is `/api/mcp` on `https://smart-home.<domain>`. Generate writes the reverse-proxy baseline and Homelab owner intent (`homelab`). No HA OS/Supervisor parity, no Zigbee/MQTT runtime in this slice. |

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
| Smart Home | `home-assistant` | Container mode has no Supervisor or automatic MQTT/Zigbee provisioning. Use the explicitly admitted HAOS alternative for appliance integration. Planned MQTT/Zigbee integration uses existing upstream services. Cloud placement grants no Home LAN access. |
| Game | `calagopus` | `pelican` (upstream beta) and `pterodactyl` are explicit alternatives (ADR-0048); existing installations keep their recorded platform. Minecraft Java, Paper and Bedrock, Terraria and Valheim are curated profiles created by `stackkit setup game`. Each Wings holds a governed Docker lifecycle-owner approval (ADR-0043); running game servers are stopped with their own stop command around every snapshot and restore. |
| Mail | `roundcube` | Client-first webmail for an existing external mailbox; no mail server, SMTP listener or DNS. `stackkit setup mail` stores the owner's IMAP and SMTP servers (explicit `ssl`/`starttls`) and verifies a real login; before that Roundcube refuses logins and never offers a free-form server. The route serves autoconfig, autodiscover and an unsigned Apple profile. Own mail server: Stalwart default, mailcow alternative, as a separate workload. |
| Mail server | `stalwart` | Own mail server on a dedicated Cloud Kit node with a fixed public IPv4 (ADR-0046, amended 2026-09-25): it shares its node with no other application workload, and home nodes are refused. Publishes TCP 25, 465, 587, 993 and 4190; the route host is the mail host name. `stackkit setup mail-server` creates the domain and first mailbox, optionally configures an outbound relay, prints the DNS records and verifies IMAPS and submission. mailcow is a planned alternative, not declared. |
| Private AI | `private-ai` (Ollama and Open WebUI) | `anythingllm` (Ollama and AnythingLLM) is the chat alternative, selected with `--use-case-capability ai.chat=anythingllm`; it keeps the Ollama model volume and refuses the Tika/Docling add-ons. Built from capability modules: inference (required) and chat install. Web search (SearXNG, `ai-search`) and document parsing (Tika or Docling, `ai-documents`) install as routeless add-on workloads wired into Open WebUI. Image and video (ComfyUI, `ai-image-video`) installs on the CPU by default, with an optional NVIDIA accelerator profile, behind the kit login and is wired into Open WebUI image generation. The assistant (Hermes Agent, `ai-assistant`, experimental) installs behind the kit login with a pinned policy; its grants are owner toolsets. The agent harness (OpenHands, `ai-harness`) installs in a gVisor-isolated container behind the kit login and needs the host's `runsc` runtime (`stackkit host remediate --apply gvisor-runsc`). The agent control plane (Paperclip with its own PostgreSQL, `ai-control-plane`, experimental) installs the same way behind the kit login and Paperclip's own login, without egress and with budgets at zero. Speech (kombify SpeechKit, `ai-speech`, routeless, wired into both chat modules) is complete but recorded: authoring refuses it until a SpeechKit release ships the OpenAI audio routes in a public image. Observability is recorded and refused until its workload lands. Select them with `--use-case-capability ai.<capability>=<option\|off>`. CPU by default; `--module-accelerator-profile stackkits-private-ai-runtime=nvidia\|amd` runs Ollama on the GPU (see [GPU acceleration](#gpu-acceleration)). |
| Vault | `vaultwarden` | The owner creates the encrypted account through the official client; StackKits does not handle the master password. |

These defaults identify the implementation to select for an enabled application;
they do not enable every application. Native v2alpha2 records the selected
alternative and module profile explicitly.

The CUE compatibility map distinguishes the delivery adapters: standalone
Compose declares support for the native application backup/restore path.
Photos/Immich, Files/Cloudreve and Vault/Vaultwarden on Coolify and on Komodo
declare the native Kopia backup path and workload-scoped restore activation,
retaining the signed manager and exact node volume identities, as ESPHome on
Coolify already did. Every other Coolify or Komodo row keeps its own declared
capability; a row without backup/restore is refused with
`selected_adapter_backup_restore_unavailable`. Both managers remain Beta. A rendered
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
inventory never chooses a profile. An accelerator profile is optional even
when declared: no selection is the CPU runtime, and a selected profile carries
a device requirement (`accelerator`: vendor, container access, optional
`minVramGiB`, driver floor, image variants) that host admission checks. Regenerate the authority bundle and public
module-profile catalog. Missing resource facts stay unverified, not invented.

## GPU acceleration

Private AI runs on the CPU unless the owner explicitly selects the `nvidia` or
`amd` accelerator profile for `stackkits-private-ai-runtime`. NVIDIA requires
container CDI access and a driver at least as new as the selected profile's
declared floor. AMD requires ROCm device nodes and uses the digest-pinned ROCm
Ollama image. Host admission checks these device requirements before Apply.
On Ubuntu the host remediation `nvidia-driver` installs the NVIDIA driver
(reboot required) and `stackkit host gpu-check` proves the GPU under CUDA
load; other distributions and AMD drivers stay with the owner. Only Ollama
receives device access.

Changes to the retained v2alpha1 graph instead start in
`basement-kit/stackfile.cue` `computeTierGraphs` and
`#WorkloadContractV2.computeTiers`. Label derived legacy YAML and documentation
accordingly. Cloud YAML `low` is kitio roundtrip only and has no legacy graph.

## Incremental installation intent

Native `stackkit/v2alpha2` keeps the installed Foundation and each application's
recorded selection. Initializing with `--catalog-defaults` and no `--use-case`
selects only the kit's required Core. Add or configure applications afterward:

```bash
stackkit use-cases add mail
stackkit use-cases configure mail --compute-profile high
stackkit capabilities configure telemetry-collection --enable
stackkit generate
stackkit plan
stackkit apply
```

`add` accepts `--alternative`, `--platform`, and independent `--compute-profile`,
`--storage-profile` and `--accelerator-profile` choices. Omitted new selections
come from CUE; a compatible selected platform is reused for a new application.
`configure` (alias `update`) merges only named settings, secret references and
profiles. Use repeated `--setting 'key="value"'` for public JSON values and
`--secret-ref key=secret://reference` for references already in local custody.
Omitted settings, unrelated applications, data placement, routes and storage
remain intact. An existing alternative, adapter or node change requires explicit
migration and is refused by this configuration operation.

Foundation supplements use the existing capability contract:
`capabilities configure <id> [--enable] [--provider <id>]`, with the same public
settings and secret-reference flags. These commands author desired intent;
the normal resolver, plan and runtime owners determine execution admission.
They do not deploy an application or claim that it is running.

Standard Mode persists through the canonical intent store with the current
spec hash; `--expected-spec-hash` can pin the caller's reviewed baseline.
Advanced or multi-node authoring requires `--output <new-candidate.json>` and
retains the active baseline for Techstack's capability-gated change-set
lifecycle. Use `--node <existing-enabled-node>` when an addition has several
possible nodes. No node is provisioned by these authoring commands.
`--json` returns `stackkit.intent-change/v1` with the previous and candidate
spec hashes, outcome, path and `runtimeApplied: false`.

Remove one optional application with `stackkit remove --workload mail`, or its
complete selected product composition with `stackkit remove --use-case ai`.
Use-case membership comes from the CUE catalog and includes only currently
selected companions. The complete remaining dependency graph must resolve
before removal; a platform used by another application cannot be retired.
Standard removal retains data by default, records each terminal runtime result,
then retires the desired selection with one spec-hash CAS. A concurrent intent
change is retained and reported as pending reconciliation. Required Core
removal keeps its documented reinstall source.
After a successful Standard intent retirement, run `stackkit generate` and
`stackkit apply` before another runtime lifecycle command. The old generation
and Apply proof deliberately cannot authorize the new spec. Use `--use-case`
to remove a complete selected composition together, with one final intent CAS.
Access policies, data bindings and secret custody remain available for reuse.

For managed removal, author a separate candidate with
`stackkit remove --use-case ai --candidate-output without-ai.json` (or
`--workload <id>`). This changes no runtime and retains the active baseline.
The existing capability-gated Advanced change set removes baseline-only local
workloads through their applied runtime owners, retaining data, before adopting
the candidate. Its mandatory rollback checkpoint precedes removal, and the
mutation result includes terminal `removedWorkloads` evidence. Candidate
omission alone is never reported as runtime removal.
Multi-node or multi-site Standard intent also requires a separate candidate;
local runtime removal cannot silently retire another node's desired selection.

## Authoring Flow

The public CLI includes the local module authoring path. From a checkout with
the `foundation/` CUE package, render declared upstream facts and validate the
result using the same CLI:

```bash
stackkit module scaffold --facts path/to/module_facts.json --out modules/my-service
stackkit module lint --module modules/my-service
```

Scaffolding produces a draft `module.cue` and reference smoke artifacts; it
does not activate a workload or claim runtime readiness. When facts use
`imageSource`, supply `--catalog path/to/catalog.json` to resolve the existing
CUE-owned image version. Release preparation remains in the publisher build.
After authoring the atomic module, connect its explicit profiles and lifecycle
to the existing [Use Case Package definitions](../use-cases/) and kit catalog.
The [CUE package contract](../foundation/use_case.cue) defines how a use case
combines atomic modules; executable alternatives and module profiles remain
owned by the [Architecture v2 catalog](../foundation/architecture_v2_catalog.cue).

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

Media browser SSO, device limitations and the pending existing-install release hold
are documented in [Jellyfin SSO and upgrade qualification](JELLYFIN_SSO_AND_UPGRADE.md).
Smart Home's packaged OIDC setup, account-preservation policy and pending runtime
qualification are described in [Home Assistant SSO](HOME_ASSISTANT_SSO.md).
