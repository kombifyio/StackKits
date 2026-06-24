# Enterprise Readiness

> Last verified: 2026-06-24

This page is the release contract for Enterprise review of the curated
StackKits OSS surface. It is intentionally stricter than the general beta
project status: do not claim Enterprise production readiness unless every
required evidence item below is present for the exact public release tag.

## Verdict

StackKits is not yet Enterprise production-ready for broad Public OSS use.
The supported Enterprise hardening target is BaseKit only, from released
artifacts, with explicit limitations. Unreleased kit definitions stay outside
the public beta install surface until their rollout matrices graduate.

## Supported Production Scope

| Area | Current contract |
| --- | --- |
| Kit | BaseKit only. |
| Install path | Published `kombifyio/stackKits` release archives or `https://base.stackkit.cc`. |
| Source surface | Curated public `kombifyio/stackKits` mirror only. A private-source build is not Enterprise release evidence. |
| Operating system | Fresh Ubuntu target used by the production-style VM gate. |
| Toolchain | `stackkit`, `stackkit-server`, `stackkit-mcp`, packaged OpenTofu, root `cue.mod`, shared `base/`, BaseKit, and required `modules/` from the release archive. |
| Application layer | BaseKit platform baseline, protected routes, Node Hub, explicit setup drops, and ready-to-use use cases deployed as Coolify-managed applications with manageable UI. This is not Enterprise-ready until `kombify-StackKits-85x` is closed with evidence. |

## Non-Goals

- No Enterprise claim for unreleased kit definitions.
- No claim that any non-PaaS fallback proves the Coolify-managed application-layer rollout.
- No unsupported public release path from the private development repo.
- No production claim for backup controller or backup agent scaffolds until durable storage, queueing, enrollment, and OIDC operator auth land.
- No requirement that StackKits deploy customer-owned user apps; that lifecycle remains owned by the selected PaaS or Admin product surface. Product-bundled ready-to-use use cases are in scope for the Coolify-managed application-layer gate.

## Security Model

- CUE remains the source of truth for deployment contracts, defaults, constraints, routes, access policy, and module requirements.
- Public/non-local routes must be protected by default unless the StackSpec or module access policy explicitly configures public or unauthenticated exposure.
- `stackkit-server` must require an API key for non-public endpoints outside local development. Production profiles must reject unauthenticated mode and wildcard CORS.
- Internal runtime-action routes require `X-Kombify-Service-Auth` with the TechStack caller and StackKits audience.
- Generated secrets, rollout evidence, logs, and bootstrap material must be redacted before they are published or mirrored.
- Static default credentials are forbidden.

## Release Evidence

Every Enterprise candidate release must publish `release-evidence.json` with
the release artifacts. The evidence file must conform to
[`schemas/release-evidence.schema.json`](../schemas/release-evidence.schema.json)
and include:

- release tag, source commit, source repo, release repo, workflow run ID, and release visibility,
- SHA-256 digest and byte size for release archives, checksums, SBOMs, and evidence artifacts,
- SBOM files for CLI archives in SPDX JSON or CycloneDX JSON format,
- GitHub Artifact Attestation verification status for release artifacts and the GHCR server image,
- security scan summaries for secret, vulnerability, static-analysis, repository, and image scans,
- public export leak/link check status,
- default StackKit-owned L3 PaaS-intent check status,
- live installer smoke status for `install.stackkit.cc` and `base.stackkit.cc`,
- fresh Ubuntu BaseKit evidence from the published release or installer path,
- scenario evidence rows for `SK-S1`, `SK-S2`, `SK-S3`, and `SK-S5`; missing
  rollout artifacts must appear as `pending` rows, never as an empty
  `scenarioEvidence` array,
- upgrade and rollback VM proof status,
- known limitations and support boundaries.

## Required Gates

| Gate | Evidence |
| --- | --- |
| Public export audit | `scripts/public/export-public.*` plus `scripts/public/check-public-surface.*` pass on exported contents. |
| Archive validation | `scripts/release/validate-release-archives.sh dist` passes for the full and BaseKit archives. |
| Default StackKit-owned L3 PaaS intent | `scripts/release/check-l3-paas-contract.mjs` passes for StackKit-owned/default module contracts and generated BaseKit output, and `release-evidence.json.checks.defaultL3PaaSDelivery.status` is `pass`. User-installed apps outside StackKit/PaaS manifests are state-unmanaged and out of release evidence scope. |
| Live installer smoke | `tests/e2e/test_live_installers.sh` proves shell content instead of HTML fallback. |
| Fresh Ubuntu BaseKit | Clean target installs from released archive or installer; Base Hub warning appears; protected services reject anonymous access; Photos setup runs. |
| Supply chain | Checksums, SBOMs, and artifact/image attestations exist and verify before evidence marks them as passed. |
| Security scans | TruffleHog, gitleaks, govulncheck, gosec/staticcheck, OSV, and Trivy gates pass where applicable. |
| Upgrade/rollback | Monthly Runtime Standard and Premium VM smoke proves upgrade preserves state and rollback restores state. |

## Operations Expectations

- Backup and restore procedures must be tested, not only documented, before they are included in an Enterprise support claim.
- Rollout evidence lives under `.stackkit/runs/<runId>/` locally and may be mirrored to Admin/TechStack only after redaction.
- Failure output must classify setup, auth, routing, image, platform, or app-layer causes without dumping provider tokens.
- Open P0 Beads blockers that affect BaseKit production behavior must be closed with evidence before an Enterprise release is announced; non-core P1 deferrals must be labeled outside the Enterprise claim.
- The Coolify-managed application-layer path for ready-to-use use cases is core Enterprise scope and cannot be deferred or excluded from the production-ready claim.

## Current Blockers

The current blockers for an Enterprise Public OSS production claim are:

- released-archive BaseKit VM smoke from the published release or installer path,
- live installer endpoint smoke for shell content and BaseKit execution,
- OSS mirror allowlist audit after publication,
- Coolify-managed application-layer product contract implementation and evidence, including PaaS external app IDs/status for StackKit-owned L3 apps,
- Monthly Runtime upgrade and rollback VM proof,
- Node 24-compatible release workflow verification,
- confirmation from the next release train that no noncanonical workflow creates or mutates public GitHub Releases.
