# Changelog

All notable changes to kombify-StackKits are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

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

