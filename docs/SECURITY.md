# Security

StackKits is designed around safe defaults and release evidence:

- generated deployment artifacts must not contain committed secrets,
- public and non-local services are authenticated by default unless an explicit access policy says otherwise,
- local-only services stay local-only,
- `stackkit-server` requires an API key outside local development and production profiles reject unauthenticated mode and wildcard CORS,
- examples use placeholders such as `<token>` or `secret://path`,
- release artifacts publish checksums, SBOMs, and `release-evidence.json` when the Enterprise evidence contract is active,
- GitHub Artifact Attestations must verify before release evidence marks attestation status as passed.

## Supported Security Scope

The public OSS scope is the Basement Kit (`basement-kit`, local, stable) and the
Cloud Kit (`cloud-kit`, cloud, shipped as scaffolding and graduating in v0.5.1),
both built on the shared `base/` foundation. Unreleased kit definitions, optional
extension catalogs, internal runbooks, and operator-only controller paths are
intentionally excluded from the public repository and release archives.

## Release Evidence

Enterprise reviewers should inspect:

- `checksums.txt`,
- SBOM files ending in `.spdx.json` or `.cdx.json`,
- `release-evidence.json`,
- GitHub Artifact Attestation verification output,
- live installer and fresh Ubuntu Basement Kit evidence referenced from the release notes.

If `kombifyio/stackKits` is Internal visibility for a release, treat that
release as a customer preview instead of a broad Public OSS production
release.

Report security issues through GitHub Security Advisories on the public
repository.
