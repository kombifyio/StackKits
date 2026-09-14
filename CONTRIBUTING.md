# Contributing

StackKits is built around CUE contracts and Go code.

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
- Questions and ideas: GitHub Discussions.
- Security vulnerabilities: report privately through GitHub Security
  Advisories on this repository, never in a public issue. See `SECURITY.md`.

This project follows the Contributor Covenant; see `CODE_OF_CONDUCT.md`.

## Public Release Surface

The public repository is generated from an explicit allowlist in the private
upstream. Do not add internal infrastructure details, private service URLs, or
secrets to public docs, tests, workflows, or examples.
