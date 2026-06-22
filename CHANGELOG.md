# Changelog

All notable changes to kombify-StackKits are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [0.4.4] - 2026-06-22

### Fixed

- **SK-S3 release evidence import**: accepts run-scoped custom-domain Base Hub URLs such as `https://base.e2e-cd-<run>.kombify.pro` when they remain inside the expected `kombify.pro` zone.
- **SK-S3 scenario validator fixture**: updates the release artifact validator test fixture from the old bare/manual custom-domain model to the current bootstrapped provider-lease Coolify contract.

### Release Notes

- Supersedes `v0.4.3` for stable public testing because `v0.4.3` published successfully and the released-content matrix passed, but the evidence republish step still rejected valid SK-S3 dynamic Base Hub URLs.

## [0.4.3] - 2026-06-22

### Fixed

- **Released-content preflight snapshots**: regenerates SK-S2/SK-S3 TFVars golden snapshots so the public preflight gate matches the bootstrapped provider-lease scenario contract.
- **Installer credential verification**: accepts the current installer `Login credentials:` output header while still requiring the expected admin email and password lines.

### Release Notes

- Supersedes `v0.4.2` for stable public testing because `v0.4.2` published successfully but its released-content matrix still exposed stale golden snapshots and legacy credential-header verification.

## [0.4.2] - 2026-06-22

### Fixed

- **Stable E2E scenario contract**: aligns SK-S2 and SK-S3 with the supported bootstrapped BaseKit release path. SK-S2 remains the kombify.me Komodo provider-lease proof, and SK-S3 remains the custom-domain Coolify provider-lease proof with Cloudflare DNS and managed cleanup, but neither stable scenario claims the unsupported `advanced` or `bare` scaffolding path.
- **Released-content verify expectations**: updates the production verifier to require bootstrapped tfvars, Base Hub access summaries, public service URLs, DNS records, and Komodo/Coolify platform evidence from the official installer release.

### Release Notes

- Supersedes `v0.4.1` for stable public testing because `v0.4.1` published successfully but its released-content SK-S2/SK-S3 verify run exposed stale `advanced`/`bare` assertions.

## [0.4.1] - 2026-06-22

### Highlights

- **Stable BaseKit promotion**: promotes the `v0.4.0-beta.2` evidence set to the stable public installer path after SK-S1, SK-S2, SK-S3, SK-S5, browser evidence, public export, archive validation, SBOMs, and attestations passed.
- **Real ephemeral server E2E**: keeps SK-S2 and SK-S3 on fresh provider-leased servers through the Sim/Lease API, with SSH used only as transport and managed cleanup required for DNS records plus server leases.
- **Release evidence completeness**: the stable release carries canonical scenario rows and browser evidence instead of the earlier `v0.4.0` release's pending scenario rows.

### Fixed

- **Stable latest drift**: supersedes the older `v0.4.0` stable release evidence that still marked SK-S1/SK-S2/SK-S3/SK-S5 and browser gates as pending.
- **Roadmap and Beads state**: closes the v0.4 release-blocking tracker drift after public beta2 evidence and current main Scenario/Admin/PaaS/Runtime gates proved the BaseKit beta-hardening scope.
- **Installer semantics**: keeps prerelease pins explicit while the unpinned official installer resolves to the newest stable tag.

### Release Notes

- This is the release-ready stable BaseKit path for public testing through the official installers without a prerelease pin.
- `v0.5.0` remains the product-contract-complete follow-up for non-v0.4 scope such as native Vaultwarden Owner UX and broader Enterprise application-layer polish.

## [0.4.0-beta.2] - 2026-06-21

### Highlights

- **Ephemeral provider-server E2E contract**: SK-S3 now provisions a fresh provider-leased Ubuntu server through the Sim/Lease API, runs the custom-domain installer over provisioned SSH, captures state/evidence, and deletes the simulation/server during cleanup.
- **Uniform beta provider lane**: provider selection now uses `STACKKIT_E2E_SERVER_PROVIDER`, then `STACKKIT_E2E_CLOUD_NODE_ENGINE`, then `STACKKIT_TECHSTACK_LEASE_PROVIDER`, and finally `centron-managed`; beta providers remain `centron-managed` and `ionos-managed`.
- **Release cleanup discipline**: SK-S3 production workflow phases now preflight service auth, provider readiness, and Cloudflare DNS credentials, then run an `always()` cleanup phase that emits explicit diagnostics even when provisioning or verification fails.

### Fixed

- **BYO SSH blocker removed from canonical SK-S3**: fixed-host SSH is now an explicit local debug override via `STACKKIT_SK_S3_DEBUG_FIXED_SSH=1`, not release evidence or CI prerequisite material.
- **Scenario state and artifacts**: SK-S2/SK-S3 artifacts now record provider metadata, and SK-S3 staged state persists simulation ID, node ID, SSH material, public IP, service hosts, DNS zone, and provider for follow-up phases and cleanup.
- **Production workflow diagnostics**: isolated SK-S3 Wait/Verify/Cleanup phases skip cleanly when no Start state exists, while workflow jobs upload blocked/skipped diagnostics instead of failing later on missing artifacts.

### Release Notes

- This is the release-candidate lane for public BaseKit beta testing through a pinned prerelease: `STACKKIT_RELEASE_VERSION=v0.4.0-beta.2`.
- At prerelease publication time, unpinned installs stayed on the stable release path until released-content SK-S1, SK-S2, and SK-S3 evidence was clean.

## [0.4.0-beta.1] - 2026-06-21

### Highlights

- **Public BaseKit beta candidate**: ships the v0.4 BaseKit release candidate as a pinned prerelease for official-installer testing with `STACKKIT_RELEASE_VERSION=v0.4.0-beta.1`.
- **Released-content gates**: production workflows now include explicit released-installer SK-S1 coverage, scenario evidence import, and diagnostic artifacts for skipped SK-S2/SK-S3 paths.
- **Local E2E evidence**: the Docker Desktop Fresh Ubuntu SK-S1 gate is split into bounded Start, Wait, Verify, and browser-evidence phases under the 15-minute policy.

### Fixed

- **Public export manifest**: includes the homelab setup-action evidence scripts required by the public surface checker and release CI.
- **Prerelease installer semantics**: installer tests prove prereleases are used only when `STACKKIT_RELEASE_VERSION` pins the beta tag; unpinned installs remain on stable latest.
- **Release diagnostics**: skipped or blocked production scenarios now emit explicit diagnostics instead of failing later during artifact upload.

### Release Notes

- This is a BaseKit public beta prerelease, not stable GA. Do not promote unpinned `latest` until released-content SK-S1, SK-S2, and SK-S3 pass or the public beta scope is narrowed explicitly.
- Current broader scenario blockers are tracked separately: SK-S2 service-auth preflight and SK-S3 provider-lease/DNS prerequisites must pass before claiming multi-use-case beta readiness.

## [0.3.4] - 2026-06-08

### Highlights

- **Native MCP surface**: StackKits now publishes one user-facing `stackkit` MCP connection, with `stackkit-mcp` as the local adapter and `stackkit-server /mcp` as the protected durable endpoint after install.
- **TechStack rollout readiness**: release archives include the MCP/server pieces needed for kombify-TechStack managed installs, plus bounded MCP rollout and Fresh Ubuntu phase gates.
- **Agent discovery**: stackkit.cc now ships OpenMCP metadata, `llms.txt` updates, and installation-process guidance for local, SSH, and protected durable MCP paths.

### Fixed

- **OSS release hygiene**: the StackKits runtime-action wire contract is now local to this repo, so public release builds no longer depend on private kombify Go modules.
- **Release export**: the Docker image build no longer emits private module-auth configuration into the curated public release surface.
- **Local gates**: Beads sync, local build timing, website checks, MCP smoke tests, and timeout-budget checks are all bounded by the 15-minute command policy.

## [0.3.2] - 2026-05-26

### Fixed

- **Public release hygiene**: the public StackKits release now stays on the curated OSS export surface and release checks reject development-only paths, private workflows, internal runbooks, and test fixtures before publish.
- **Release evidence**: package artifacts are included in build attestations and attestation verification retries handle GitHub propagation delay without hiding real failures.
- **Security gates**: Go vulnerability dependencies are updated for `golang.org/x/crypto`, `golang.org/x/net`, and related `golang.org/x` modules, with lint/static/security checks restored to a clean state.

## [0.3.1] - 2026-05-25

### Highlights

- **Canonical live scenarios**: release work now focuses on SK-S1 local Coolify, SK-S2 kombify.me Komodo, and SK-S3 custom-domain Coolify, with installer gates split into bounded Start/Wait/Verify phases.
- **Auth baseline**: BaseKit rollouts restore TinyAuth/PocketID provider registration and runtime checks so protected services expose PocketID login instead of falling back to password-only TinyAuth.
- **Coolify routing**: generated Coolify rollouts now bootstrap, reconcile, and route StackKit-owned services through the managed proxy with service hostnames such as `base`, `id`, `photos`, and `kuma`.

### Fixed

- **Coolify proxy recovery**: fallback and reconciliation logic now restores file-provider routing, dynamic config mounts, proxy TLS settings, service routes, host-gateway access, and same-file dynamic-config sync handling.
- **Cloudflare DNS-01**: custom-domain Coolify rollouts pass Cloudflare Global API Key credentials to Traefik as `CF_API_KEY` when `CLOUDFLARE_EMAIL` is present, while scoped API tokens still use `CF_DNS_API_TOKEN`.
- **Installer readiness**: live installer jobs hand off VM state before verification and wait for routed services/certificates in bounded phases instead of relying on a single long-running job.
- **Runtime metrics**: restore-drill host metrics preserve legitimate zero CPU values instead of dropping them as missing data.
- **Release preflight**: `scripts/release/basekit-live-preflight.ps1` now fails closed when `go`, `node`, `npm`, `cue`, actionlint, or release helper commands return a non-zero exit code.
- **Coolify endpoint contract**: generated BaseKit rollouts keep the persisted `.stackkit/platform.json` Coolify endpoint node-local at `http://127.0.0.1:8000`, while bootstrap and readiness probes can use a separate endpoint reachable from remote Docker targets.
- **Archive validation**: release archive smoke validation now checks the current `coolify_platform_bootstrap` and `.stackkit/platform.json` contract from packaged contents instead of obsolete Coolify token API markers.
- **Release state**: STATUS and ROADMAP now treat `v0.3.1` as the next public patch candidate and keep old `v0.2.8` follow-ups as historical evidence rather than current release blockers.

### Release Notes

- `v0.3.1` is the next intended Public OSS patch release. `v0.3.0` was a private failed release attempt and is not treated as a public release.
- Production run `26420216004` on `f3419a54` was intentionally cancelled by operator request after API/Gateway, BaseKit preflight, Sim UI auth, and SK-S2 Start had passed. Complete SK-S1/SK-S2/SK-S3 end-to-end evidence should be rerun before making an Enterprise production-readiness claim.

## [0.3.0] - 2026-05-22 (private tag; not public OSS release)

> `v0.3.0` was tagged privately but did not complete the public publish path. Do not use it as public release evidence and do not retag it.

### Highlights

- **PaaS portfolio alignment**: Coolify remains the default PaaS, while Komodo is the production alternative for BaseKit rollouts. Dokploy remains draft until promoted.
- **Komodo no-UI path**: generated rollouts install Komodo Core, Periphery, and DB, create the initial admin/API key without UI, close registration, persist `.stackkit/platform.json`, and deploy StackKit-owned Compose bundles as Komodo Stack resources through the API.
- **Dokploy no-UI path**: generated rollouts set `BETTER_AUTH_SECRET`, create or confirm the first owner, establish a session, mint a non-rate-limited API key, persist both `token` and `apiKey`, deploy raw Compose resources through Dokploy, and route through `dokploy-traefik`.
- **Forge Map/Admin sync**: Admin seed and generated CUE now carry Coolify as the PaaS standard with Komodo as the production alternative; Dokploy is tracked as draft.

### Changed

- StackKit-owned L3 app deployment now has explicit selected-PaaS adapter contracts for Coolify and Komodo, with Dokploy kept behind draft adapter coverage.
- Production E2E coverage is capped at SK-S1 local Coolify, SK-S2 kombify.me Komodo, and SK-S3 custom-domain Coolify.
- Documentation, ADRs, StackSpec reference, website content, and Works-With metadata now describe the Coolify default, Komodo production alternative, and Dokploy draft status honestly.

### Fixed

- Dokploy Compose creation now persists `sourceType: raw` through a follow-up update before deploy, avoiding accidental GitHub-source deployments.
- Komodo adapter upserts now resolve canonical stack IDs on create conflicts before update/deploy evidence is recorded.
- Generated Admin/CUE artifacts are back in sync for `paas.type` and the production/draft PaaS split.

## [0.2.8] - 2026-05-17

### Highlights

- **BaseKit bootstrap-open Base Hub**: local `base.<domain>` stays reachable during first-run owner setup, shows an unprotected warning, and can be protected after PocketID/TinyAuth setup.
- **Registry-backed module release**: module release and verify now use service auth, bootstrap missing module rows through the Admin registry, and keep all 24 module contract hashes in strict parity.
- **Release gate stabilization**: AdGuard Home module tests wait for routed UI readiness after provisioning, and the module release command stays below lint complexity thresholds.

### Fixed

- Prevent stale service-catalog snapshots from re-protecting the local Base Hub by pinning `base` to identity `none` for local fallback defaults.
- Keep default L3/application services protected unless they are explicitly configured public; the Base Hub is the local onboarding exception only.
- Avoid browser-session Admin tokens in module release CI; signed service-auth requests now take precedence.

## [0.2.7] - 2026-05-17

### Highlights

- **BaseKit product-contract guardrails**: fresh Ubuntu evidence now checks protected/default anonymous rejection, node-local manifest visibility, and the Photos setup action instead of relying on container liveness only.
- **Release mirror hygiene**: the curated release export now ships a narrower documentation surface, a sanitized release roadmap, and root-relative website link validation for the Svelte/Vite site.
- **Agent and website surface**: stackkit.cc moved to the Svelte 5/Vite/Tailwind site while preserving installer routes, `llms.txt`, OpenAPI/schema mirrors, and prompt Markdown.

### Fixed

- Local website release gates now run `npm install`, `npm run check`, and `npm run build` without failing on Windows locked native modules from an existing `node_modules`.
- BaseKit docs now clarify that L3 public or unauthenticated exposure is allowed only through explicit access policy, never as the default.

## [0.2.6] - 2026-05-13

### Changed

- **StackKit standards**: codified release archives as the installable product boundary, requiring packaged `cue.mod`, shared `base/`, module contracts, packaged OpenTofu, and fresh-target archive validation for defaults.
- **Installer quality bar**: documented that public one-liner endpoints must return executable shell instead of website fallback HTML.
- **Public release helper**: hardened the public publish script around release deletion and release-existence checks.

## [0.2.5] - 2026-05-13

### Fixed

- **BaseKit release archives**: `stackkits` and `stackkits-base-kit` archives now include root `cue.mod/**` and `modules/**`, allowing installed BaseKit definitions to run composition and generate TinyAuth credentials for the one-line installer path.
- **Release validation**: the public release workflow now extracts the BaseKit archive and verifies `init` plus `generate` from released files so archive packaging regressions fail before publish.

## [0.2.4] - 2026-05-13

### Fixed

- **Runtime image build**: Dockerfile now uses Go 1.26.3 so the public StackKit server image build matches `go.mod` and can publish `ghcr.io/kombifyio/stackkits`.

## [0.2.3] - 2026-05-13

### Highlights

- **PaaS app handoff path**: BaseKit can persist optional user app handoff metadata into the stack spec, register kombify.me app service names, and expose platform app handoff state in `stackkit status --json`.
- **Runtime action bridge**: `stackkit-server` now exposes service-auth-protected internal runtime actions for TechStack-managed rollout, verification, and restore-drill handoffs with dry-run-by-default execution.
- **Scenario evidence**: SK-S2A and SK-S3A scenario definitions, golden fixtures, docs, and the public SvelteKit smoke app example are included for dev-only PaaS handoff validation.

### Added

- `stackkit app add` command coverage for SvelteKit app definitions, route defaults, env values, and secret references.
- Dev-gated base installer app handoff environment variables for local handoff validation.
- Internal service-auth JWT verification with current/next secret rotation support for runtime action callbacks.

### Changed

- App-enabled StackSpecs now generate PaaS handoff manifests without making StackKit responsible for user app deployment.
- Public export manifest includes the SvelteKit smoke example used by dev handoff validation.

