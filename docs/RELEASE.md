# Release Process

This document describes the release process for the public `kombifyio/stackKits` repository.

The Go module path is `github.com/kombifyio/stackkits`.

## How Releases Work

Releases are published to the **public** `kombifyio/stackKits` repo. The release workflow:

1. Tag push triggers `.github/workflows/release.yml`
2. **Test job** — runs `go test ./...`
3. **Validate archive job** — builds a snapshot, verifies required files are present (base-kit/, base/, binary)
4. **Release job** — GoReleaser builds cross-platform binaries and publishes to GitHub Releases

### Release Archives

Each release produces **4 archive types** per platform:

| Archive | Contents |
|---------|----------|
| `stackkits_VERSION_OS_ARCH` | Full bundle — CLI + all kits + base schemas |
| `stackkits-base-kit_VERSION_OS_ARCH` | CLI + base-kit + base schemas |

Per-kit archives for ha-kit and modern-homelab will be added when they graduate from alpha.

Every archive includes the CLI binary + `base/` schemas (shared by all kits) + the specific kit directory. This lets users install just the kit they need.

These are configured in `.goreleaser.yaml` under `archives`. **When adding a new kit archive, also add validation in `release.yml`.**

### Kit Versioning

Kits version independently from the CLI and from each other:

| Component | Version | Where |
|-----------|---------|-------|
| CLI binary | From git tag (e.g. `v4.0.1`) | GoReleaser ldflags |
| base-kit | `4.0.0` | `base-kit/stackkit.yaml` |
| ha-kit | `1.0.0-alpha` | `ha-kit/stackkit.yaml` |
| modern-homelab | `1.0.0-alpha` | `modern-homelab/stackkit.yaml` |

A CLI release bundles whatever kit versions are in the repo at that point. To release only a specific kit's changes, just tag and release — the per-kit archive (`stackkits-base-kit_*`) contains only that kit.

## Source Curation

The public repo only documents and ships the public release surface.
Internal source-sync or private maintainer workflows are intentionally not documented here.

For this repo, the release-relevant source of truth is simply the content currently committed on `main`.

## Creating a Release

```bash
# 1. Ensure you're on main with all changes committed
git status  # clean working tree

# 2. Tag and push to origin (triggers release workflow)
git tag v0.X.Y
git push origin v0.X.Y

# 3. Monitor the release
gh run list --repo kombifyio/stackKits --limit 3
gh run watch <run-id> --repo kombifyio/stackKits

# 4. Verify the release
gh release view v0.X.Y --repo kombifyio/stackKits
curl -sSL "https://github.com/kombifyio/stackKits/releases/download/v0.X.Y/stackkits_0.X.Y_linux_amd64.tar.gz" -o /tmp/verify.tar.gz
tar tzf /tmp/verify.tar.gz  # check all files are present
```

## Re-releasing a Version

If a release needs to be fixed:

```bash
# Delete the release and tag
gh release delete v0.X.Y --repo kombifyio/stackKits --yes
git push origin :refs/tags/v0.X.Y
git tag -d v0.X.Y

# Fix, commit, push
git add . && git commit -m "fix: ..."
git push origin main

# Re-tag and push
git tag v0.X.Y
git push origin v0.X.Y
```

## Safeguards

### CI Validation (validate-archive job)
The release workflow includes a `validate-archive` job that:
- Builds a dry-run archive with `goreleaser --snapshot`
- Checks **all 4 archive types** (full + 3 per-kit) for required files
- Verifies each kit archive contains its kit directory + base schemas + CLI binary
- **Blocks the release if any required file is missing from any archive**

### E2E Install Test
Run locally before releasing:

```bash
./tests/e2e/test_install.sh          # tests latest public release
./tests/e2e/test_install.sh local    # tests local build
```

### What NOT to Do

- Never remove kit directories or `base/` from `.goreleaser.yaml` archive files
- Never force push to `kombifyio/stackKits` without checking existing releases
- Never change the Go module path without updating all release consumers and installation docs
- Never add a new kit without adding a corresponding archive entry in `.goreleaser.yaml`

## Git Remote Setup

```bash
# In the local clone of kombifyio/stackKits:
git remote -v
# origin    → kombifyio/stackKits (fetch+push)
```

## Troubleshooting

### CI lint fails after adding new code

Run `golangci-lint run` locally before pushing. The config is at [.golangci.yml](../.golangci.yml). Common issues:
- `goconst`: Strings used 3+ times need constants (add to `pkg/models/models.go`)
- `errcheck`: Check all error returns (except those in `.golangci.yml` exclusions)
- `misspell`: US English spelling only (`marshaling` not `marshalling`)

### CUE validation fails in CI

The `base-kit` has its own `cue.mod/module.cue` which can shadow the root module definition. This is a known issue. CUE validation and module test failures are pre-existing and tracked separately from the release process.

### Release worked but CI shows failure

The Release workflow (`release.yml`) is triggered by tag pushes and runs independently from CI (`ci.yml`). A release can succeed even if CI fails. Check `gh release view <tag> --repo kombifyio/stackKits` to confirm the release exists.
