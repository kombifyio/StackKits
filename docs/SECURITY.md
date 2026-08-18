# Security

StackKits is designed around safe defaults and release evidence:

- generated deployment artifacts must not contain committed secrets,
- public and non-local services are authenticated by default unless an explicit access policy says otherwise,
- local-only services stay local-only,
- `stackkit-server` requires an API key outside local development and production profiles reject unauthenticated mode and wildcard CORS,
- examples use placeholders such as `<token>` or `secret://path`,
- release artifacts publish checksums, SBOMs, and `release-evidence.json` when the Enterprise evidence contract is active,
- GitHub Artifact Attestations must verify before release evidence marks attestation status as passed,
- at least one trusted-root document must match the reviewed digest allowlist embedded in the public binary before the release index is parsed; unknown documents are not used for verification and the release asset is an offline cache, not its own trust source.

## Supported Security Scope

The public OSS scope contains Basement Kit (`basement-kit`, local, stable),
Cloud Kit (`cloud-kit`, cloud), and Modern Homelab (`modern-homelab`, Home plus
Cloud, Preview), all built on the shared `foundation/` library. Modern archive
availability does not graduate incomplete federation runtime owners.
Unreleased extensions, internal runbooks, provider credentials, and
operator-only controller paths remain excluded from the public repository and
release archives.

## Release Evidence

Enterprise reviewers should inspect:

- `checksums.txt`,
- SBOM files ending in `.spdx.json` or `.cdx.json`,
- `release-evidence.json`,
- GitHub Artifact Attestation verification output,
- live installer and fresh Ubuntu Basement Kit evidence referenced from the release notes.

The public binary filters a release root collection to documents whose
SHA-256 is present in its embedded, source-reviewed allowlist and rejects an
empty result before it parses the release index. Rotation uses an overlap
release that trusts the old and new documents before signing moves to the new
root. Missing that overlap fails closed and requires a reviewed manual binary
update. Historical digests remain unless explicitly distrusted.

If `kombifyio/stackKits` is Internal visibility for a release, treat that
release as a customer preview instead of a broad Public OSS production
release.

Report security issues through GitHub Security Advisories on the public
repository.
