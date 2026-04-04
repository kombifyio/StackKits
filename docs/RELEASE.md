# Release Process

## Repository Topology

| Repo | Visibility | Purpose |
|------|-----------|---------|
| `KombiverseLabs/kombify-StackKits` | **Private** | Development — all source, CI, dev tools, internal docs |
| `kombifyio/stackKits` | **Public** | Curated user-facing repo — kits, docs, installer, releases |

The Go module path is `github.com/kombifyio/stackkits` (matches the public repo).

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

## Public Repo Sync

The public repo is kept in sync automatically via a whitelist-based sync script.

### How It Works

1. **On every push to `main`** on the private repo, `.github/workflows/sync-public.yml` runs
2. It executes `scripts/sync-public.sh`, which:
   - Clones `kombifyio/stackKits` to a temp directory
   - Removes everything except `.git/`
   - Copies only whitelisted files from the private repo
   - Commits and pushes if there are changes

### Whitelist Approach

Only files explicitly listed in the `INCLUDE` array in `scripts/sync-public.sh` go to the public repo. **New files added to the private repo default to private-only.** To publish a new file, add it to the `INCLUDE` array.

Currently synced:
- Go source: `cmd/`, `internal/`, `pkg/`, `api/`
- Build files: `go.mod`, `go.sum`, `Makefile`, `Dockerfile`, etc.
- Kit definitions: `base/`, `base-kit/`, `ha-kit/`, `modern-homelab/`, `addons/`, `modules/`, `platforms/`, `cue.mod/`
- Docs & examples: `docs/`, `demos/`, `README.md`, `LICENSE`, `CONTRIBUTING.md`
- Installer scripts: `base-install.sh`, `install.sh`
- Tests: `tests/`
- Release config: `.goreleaser.yaml`, `.golangci.yml`, `.env.example`
- CI: `.github/workflows/release.yml` only

### Manual Sync

The sync script handles authentication automatically with multiple fallbacks:
1. `PUBLIC_REPO_TOKEN` env var (used in CI)
2. `gh auth token` / `gh.exe auth token` (local dev)
3. Doppler `GITHUB_PAT` (fallback)

The script also syncs `v*` tags from the private repo to the public repo.

```bash
# Preview what would change (no push)
./scripts/sync-public.sh --dry-run

# Sync and push
./scripts/sync-public.sh

# CI mode (uses PAT instead of gh auth)
PUBLIC_REPO_TOKEN=ghp_... ./scripts/sync-public.sh
```

### CI Requirements

The sync workflow requires a `PUBLIC_REPO_TOKEN` secret on the private repo — a GitHub PAT with write access to `kombifyio/stackKits`. The PAT is stored in Doppler (`kombination` project, key `GITHUB_PAT`).

To set or refresh the secret:

```bash
# Get PAT from Doppler
GITHUB_PAT=$(doppler secrets get GITHUB_PAT --plain --project kombination --config prd --no-read-env)

# Set as GitHub Actions secret on the private repo
gh secret set PUBLIC_REPO_TOKEN --body "$GITHUB_PAT" --repo KombiverseLabs/kombify-StackKits
```

## Creating a Release

```bash
# 1. Ensure you're on main with all changes committed
git status  # clean working tree

# 2. Sync is automatic on push, but you can verify:
./scripts/sync-public.sh --dry-run

# 3. Tag and push to public repo (triggers release workflow)
git tag v0.X.Y
git push public v0.X.Y

# 4. Monitor the release
gh run list --repo kombifyio/stackKits --limit 3
gh run watch <run-id> --repo kombifyio/stackKits

# 5. Verify the release
gh release view v0.X.Y --repo kombifyio/stackKits
curl -sSL "https://github.com/kombifyio/stackKits/releases/download/v0.X.Y/stackkits_0.X.Y_linux_amd64.tar.gz" -o /tmp/verify.tar.gz
tar tzf /tmp/verify.tar.gz  # check all files are present
```

## Re-releasing a Version

If a release needs to be fixed:

```bash
# Delete the release and tag
gh release delete v0.X.Y --repo kombifyio/stackKits --yes
git push public :refs/tags/v0.X.Y
git tag -d v0.X.Y

# Fix, commit, push
git add . && git commit -m "fix: ..."
git push origin main && git push public main

# Re-tag and push
git tag v0.X.Y
git push public v0.X.Y
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

- Never release from `KombiverseLabs/kombify-StackKits` (private) — users can't download
- Never remove kit directories or `base/` from `.goreleaser.yaml` archive files
- Never force push to `kombifyio/stackKits` without checking existing releases
- Never change the Go module path without updating both repos
- Never add a new kit without adding a corresponding archive entry in `.goreleaser.yaml`

## Git Remote Setup

```bash
# In the local clone of KombiverseLabs/kombify-StackKits:
git remote -v
# origin    → KombiverseLabs/kombify-StackKits (private, fetch+push)
# public    → kombifyio/stackKits (public, push for releases)

# Add public remote if missing:
git remote add public https://github.com/kombifyio/stackKits.git
```

## Troubleshooting

### Sync script fails with auth error

The script tries multiple auth sources. If all fail:
1. Check `gh auth status` — you may need to re-authenticate
2. Verify the Doppler PAT is valid: `doppler secrets get GITHUB_PAT --plain --project kombination --config prd --no-read-env | head -c 10`
3. On Windows/Git Bash, `gh auth token` may not work (can't access Windows keyring). The script falls back to `gh.exe auth token` and Doppler automatically.

### Sync script hangs (pager issue)

The script uses `export GIT_PAGER=cat` and `--no-pager` flags to prevent git commands from opening `less`. If it still hangs, ensure you're running in a non-interactive terminal.

### CI lint fails after adding new code

Run `golangci-lint run` locally before pushing. The config is at [.golangci.yml](../.golangci.yml). Common issues:
- `goconst`: Strings used 3+ times need constants (add to `pkg/models/models.go`)
- `errcheck`: Check all error returns (except those in `.golangci.yml` exclusions)
- `misspell`: US English spelling only (`marshaling` not `marshalling`)

### CUE validation fails in CI

The `base-kit` has its own `cue.mod/module.cue` which can shadow the root module definition. This is a known issue. CUE validation and module test failures are pre-existing and tracked separately from the release process.

### Release worked but CI shows failure

The Release workflow (`release.yml`) is triggered by tag pushes and runs independently from CI (`ci.yml`). A release can succeed even if CI fails. Check `gh release view <tag> --repo kombifyio/stackKits` to confirm the release exists.
