# Contributing

StackKits is built around CUE contracts and Go code.

## This repository is a generated release mirror

`kombifyio/StackKits` is produced from an explicit allowlist in the private
upstream repository. Every release replaces the tree here with the curated
export of that release. Pull requests cannot be merged into this repository:
a merge would be overwritten by the next sync.

Contributions are welcome and land through the upstream tree:

1. Open an issue (bug or feature template) or a Discussion that describes the
   change and, if you have one, links to your branch or fork.
2. A maintainer ports the patch upstream, reviews and merges it there, and it
   appears here with the next release. You are credited in the release notes.

If you open a pull request anyway, an automatic comment explains this and the
pull request is closed once the change has been ported or declined.

## Local Development

Build the changed product surface locally. Additional checks are optional and
must not block a pre-1.0 release:

```sh
cue vet -c=false ./foundation/...
cue vet ./basement-kit/... ./cloud-kit/... ./modern-homelab/...
```

When changing generated rollout output, update the CUE or Go source and
regenerate instead of patching generated files directly.

## Reporting

- Bugs and feature requests: use the issue templates on this repository.
- Questions and ideas: GitHub Discussions (see `SUPPORT.md`).
- Security vulnerabilities: report privately through GitHub Security
  Advisories on this repository, never in a public issue. See `SECURITY.md`.

This project follows the Contributor Covenant; see `CODE_OF_CONDUCT.md`.

## Public Release Surface

Do not add internal infrastructure details, private service URLs, or secrets
to public docs, tests, workflows, or examples.
