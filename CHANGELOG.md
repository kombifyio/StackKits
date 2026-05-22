# Changelog

All notable changes to kombify-StackKits are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [0.3.0] - 2026-05-22

### Highlights

- **PaaS portfolio expansion**: Coolify remains the default PaaS, while Komodo and Dokploy are now explicit, VM-proven alternatives for BaseKit rollouts.
- **Komodo no-UI path**: generated rollouts install Komodo Core, Periphery, and DB, create the initial admin/API key without UI, close registration, persist `.stackkit/platform.json`, and deploy StackKit-owned Compose bundles as Komodo Stack resources through the API.
- **Dokploy no-UI path**: generated rollouts set `BETTER_AUTH_SECRET`, create or confirm the first owner, establish a session, mint a non-rate-limited API key, persist both `token` and `apiKey`, deploy raw Compose resources through Dokploy, and route through `dokploy-traefik`.
- **Forge Map/Admin sync**: Admin seed and generated CUE now carry Coolify as the PaaS standard with Komodo, Dokploy, and CapRover as alternatives.

### Changed

- StackKit-owned L3 app deployment now has explicit selected-PaaS adapter contracts for Coolify, Komodo, and Dokploy.
- Production Fresh-VM coverage now includes targeted explicit PaaS gates for `paas: komodo` and `paas: dokploy`.
- Documentation, ADRs, StackSpec reference, website content, and Works-With metadata now describe the Coolify default plus Komodo/Dokploy alternatives honestly.

### Fixed

- Dokploy Compose creation now persists `sourceType: raw` through a follow-up update before deploy, avoiding accidental GitHub-source deployments.
- Komodo adapter upserts now resolve canonical stack IDs on create conflicts before update/deploy evidence is recorded.
- Generated Admin/CUE artifacts are back in sync for `paas.type` and `paas.alternatives`.

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

## [Unreleased] — kit-update-phase-1: Base Kit Update-Lifecycle (Foundation + CLI)

### Production milestone (2026-05-08) — Phase 1 LIVE

- DB Migrations 000107–000109 (renumbered from initial 000090–000092 drafts because slots 000086–000106 were claimed by other repos before apply) **LIVE on Render** `kombify-stackkits` Postgres: `release_channel` columns, `sk_node_deployment` mirror, `sk_kit_module_compat` resolver view.
- ADR-0018 implementation-status table updated: DB migrations + Admin (channel-promotion endpoints, resolver, node-deployments, UI) marked ✅ Shipped. Lessons-learned section added (sqlc-000106-fix, 000067-replay-fix, GO_VERSION 1.26.3 bump, renumbering rationale, best-effort PATCH note).
- North-Star reference doc the private kit update lifecycle doc — canonical landing page for the update lifecycle (TL;DR, diagram, three pillars, surfaces, phase roadmap, operator quick-start, cross-repo surfaces, architectural invariants). Linked from the private source repository.

### Added

- **Tests/Release**: BaseKit live preflight (`scripts/release/basekit-live-preflight.ps1`), release-note parser tests, public export validation, website changelog smoke, and `production-tests.yml` inputs for the first SK-S1 fresh Ubuntu live run.
- Node Hub service-guide metadata in CUE, registry, and generated catalog paths; the generated `base.<domain>` dashboard now starts with Getting Started, important links, and a compact enabled-service matrix with public Mintlify how-to links.
- ADR-0018 — Kit-Update-Lifecycle (Channels, Atomic-Snapshot, Compatibility-Resolver). See the private ADR-0018 record.
- Kit-update design consolidated into ADR-0018, the private kit update lifecycle doc, and the operator runbooks.
- CUE — `#ToolType` (`oss`/`managed`/`hybrid`) + `#ToolCategory` (curated 18-Set) in [`base/tool_categorization.cue`](base/tool_categorization.cue).
- CUE — `#IaCDefaults` schema (`provider_versions`, `default_tags`, `backend`) in [`base/iac-defaults.cue`](base/iac-defaults.cue).
- IaC — Shared `iac/defaults/` module (`main.tf`, `variables.tf`, `outputs.tf`, `README.md`) — kits import as `module "defaults"` and consume `module.defaults.tags`.
- Go — `internal/snapshot/` package: `Kopia` CLI wrapper (`kopia.go`) + `AtomicSnapshotter` orchestrating Kopia + tfstate copy + manifest.yaml (`atomic.go`). `ErrKopiaNotConfigured` is the canonical pre-flight failure.
- Go — `internal/registry/channel_resolver.go` — client for `/api/v1/sk/compat/resolve` with `ResolveResult.SummarizeReasons()` helper.
- CLI — `stackkit kit upgrade` (`cmd/stackkit/commands/kit_upgrade.go`) with flags `--to`, `--kit-channel`, `--module-channel`, `--allow-channel-mismatch`, `--dry-run`, `--auto-approve`, `--volumes`, `--snapshot-id`, `--endpoint`, `--token`. Pre-flight Kopia + resolver call + tofu plan + atomic-snapshot + tofu apply + admin PATCH (best-effort).
- CLI — `stackkit kit upgrade rollback` (`cmd/stackkit/commands/kit_upgrade_rollback.go`) with flags `--to-snapshot`, `--auto-approve`, `--skip-volume-restore`, `--kopia-restore-only`. Restores tfstate + Kopia volumes from a previous atomic-snapshot.
- CLI — `stackkit doctor --check-updates` — queries the Admin API for newer kit-versions in the current channel; appends `updates` and `updates-cta` rows to the doctor report. Network/admin failures degrade to `warn`, never `fail`.
- Schema — `pkg/models/DeploymentState` gains additive `KitVersionID`, `KitSemver`, `KitChannel`, `LastSnapshotDir` fields (all `omitempty`); state files written by older CLI versions still load.
- Operator runbooks — [`docs/runbooks/kit-upgrade.md`](docs/runbooks/kit-upgrade.md) + [`docs/runbooks/kit-rollback.md`](docs/runbooks/kit-rollback.md): pre-flight checklists, common flows, failure modes, timing expectations, manual recovery for kit-rollback.
- DB-Migrations (LIVE; in `kombify-DB/migrations/`):
  - `000107_sk_release_channels` — Dual-Level `release_channel` + `released_at` auf `sk_stackkit` + `sk_module_version`, AFTER-Triggers für `action='channel_promote'`, `target_kind`-Spalte auf `sk_stackkit_audit_log`, Inline-Backfill bestehender Versions auf `stable`.
  - `000108_sk_node_deployment` — Server-Side-Mirror `(tenant_id, node_name) → (kit_slug, kit_version, kit_channel, module_versions, kopia_snapshot_id, tofu_state_path, status)`.
  - `000109_sk_compatibility_resolver_view` — VIEW `sk_kit_module_compat` als Resolver-Source.
- Tests — 48 new test cases (`internal/snapshot/`, `internal/registry/`, `cmd/stackkit/commands/kit_upgrade*`, `cmd/stackkit/commands/doctor_update*`); whole repo suite (30 packages) green.

### Pending (later in this phase)

- Admin: channel-promotion endpoints + resolver endpoint + node-deployments + UI pages shipped in kombify-Administration.
- Test-Coverage-Hebung Update-Pfade auf 50% (T7).
- VM-Smoketest v1.0→v1.1 + Rollback (T9).
- Out-of-scope: Multi-Node-Rolling-Update (kit-update-phase-2), Auto-Promotion (kit-update-phase-3).

### Notes

- Kopia-Repo wird Pflicht-Vorbedingung für Updates — Operator muss `stackkit backup configure` machen, bevor `stackkit kit upgrade` zugelassen wird (ADR-0018 §3).
- Multi-Node-Rolling-Update ist explizit kit-update-phase-2, nicht Phase 1.
- Auto-Promotion (edge → beta → stable über Demand-Signal) ist explizit kit-update-phase-3.

---

## [Unreleased] — Phase 1: Owner & Break-Glass Provisioning

### Added

- `stackkit init` flags for owner provisioning:
  - `--cluster-mode={first|join}` (Phase 1: only `first` supported)
  - `--owner-source={local|cloud}` (Phase 1: only `local` supported; `cloud` errors with Phase-2 message)
  - `--owner-email`, `--owner-username`, `--owner-display-name`
  - `--recovery-passphrase-hash` (argon2id PHC; if missing, prompts interactively)
  - `--cloud-oidc-{issuer,client-id,client-secret-ref,foreign-subject}` (Phase 2 stubs)
- Per-node break-glass PocketID admin (`bg-{nodename}@local`) auto-generated during `stackkit apply`.
- Per-node TinyAuth static-cred (`bg-{nodename}-static`) as Layer-2 fallback for PocketID-down recovery.
- Encrypted recovery bundle in `/var/lib/stackkit/recovery/break-glass-{nodename}.age` (age-scrypt encryption with the user's recovery passphrase; default scrypt N=2^17, r=8, p=1).
- Plaintext convenience bundle next to the encrypted one (`.txt`, mode 0600, root-only).
- `stackkit break-glass list` / `show-bundle` / `rotate` (Phase-5 stub) sub-commands.
- PocketID `STATIC_API_KEY` lifecycle: generated by `stackkit init`, persisted in `<homelab>/.stackkit/pocketid-static-api-key` (mode 0600), wired into the pocketid container as `STATIC_API_KEY` env var via Terraform var.

### Changed

- CUE schemas:
  - `base/identity.cue` — added `#PocketIDOwner` (passkey-only; `source: local|cloud` with conditional required fields), `#TinyAuthStaticCred`.
  - `base/break-glass.cue` (new) — `#PocketIDBreakGlass`, `#BreakGlassBundle`, `#BundleContents`, `#BundlePayload`.
  - `base/cluster.cue` (new) — `#ClusterMode` stub for Phase 4.
- PocketID image pinned to `ghcr.io/pocket-id/pocket-id:v2` (currently v2.6.2). PocketID v2 is passkey-only — there is no password-based authentication.

### Out of Scope (later phases)

- `--owner-source=cloud` and Cloud-OIDC upstream (Phase 2)
- TechStack-bootstrap-token API + wallet integration (Phase 3)
- Multi-node cluster join / `stackkit cluster join-token` (Phase 4)
- `stackkit break-glass rotate` real implementation, audit logs, auto-rotation (Phase 5)

See ADR-0018, the private kit update lifecycle doc, and [ROADMAP.md](ROADMAP.md) for the current roadmap.
