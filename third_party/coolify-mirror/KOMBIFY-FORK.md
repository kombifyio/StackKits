# kombify fork of coolify-mirror

This directory is a pinned fork of
[mtalavi/coolify-mirror](https://github.com/mtalavi/coolify-mirror)
(MIT, Copyright (c) 2026 mtalavi, see [LICENSE](LICENSE)). StackKits
uses it as the relocation engine for Coolify workloads
(`stackkit relocate`, `internal/workloadrelocation/coolify`).

| | |
| --- | --- |
| Upstream | https://github.com/mtalavi/coolify-mirror |
| Upstream commit | `f46fa093644268759fc0802e7d098e9e45dee984` (release 1.9.3, 2026-10-15) |
| Fork revision | `kombify.5` |

Revision `kombify.5` binds relocation backup and restore to the checked plan and
semantic transfer identity, emits a resource verification receipt and supports
read-only current-runtime observations. The receipt identifies the encrypted
archive by SHA-256. Applications with automatic deployments enabled or an
active deployment are refused before source drain. Restore failures after the
database commit are retained outcomes, never described as an automatic rollback.
| Tested Coolify | 4.4.6 (the StackKits pin) |

Upstream's `docs/`, `site/`, `lab/`, `scripts/` and `install.sh` are not
vendored. Everything else is upstream source, unchanged except for the
additions below. The upstream LICENSE and security policy remain unchanged.
The README adds a fork-context notice and points its four workflow/lab links
at the pinned upstream commit because those files are absent from the curated
export. All other upstream README content is retained; this documentation
change does not alter the engine pin or fork revision.

## kombify changes

- `internal/engine/relocate.go`, `cmd/coolify-mirror/relocate.go`: read-only
  `plan --json` (source inventory) and `check --plan FILE --json`
  (destination preflight, exit code 3 when blocked). A plan drops the database
  container environment and buildx builder definitions, which a backup keeps
  only inside its encrypted archive (`internal/engine/relocate_test.go`).
- `internal/engine/manifest.go`: `Pinned`, `UpstreamCommit`, `ForkRevision`;
  `version --json` reports them. StackKits accepts only this exact build.
- `internal/engine/update.go`: `update` and the release check are disabled;
  the engine changes only with a StackKits release.
- `internal/dbx/plan_test.go`, `internal/engine/deps.go`: a placeholder
  database URL in a test fixture and a comment is reworded so secret scanners
  do not report it; behavior is unchanged.
- `cmd/coolify-mirror/main.go`: the `plan` and `check` commands, the
  blocked exit code, `version --json` and their usage lines.
- `internal/engine/backup.go`, `cmd/coolify-mirror/main.go` (`kombify.2`):
  `backup` takes the archive key from `COOLIFY_MIRROR_KEY` when set and then
  writes no `.key` file next to the archive; the caller holds the key.
- `cmd/coolify-mirror/main.go` (`kombify.2`): `serve --once` stops after the
  first complete download; `serve --no-code` takes the share secret from
  `COOLIFY_MIRROR_SECRET` and prints only the address, never the share code.
  `restore` reads its source from `COOLIFY_MIRROR_SOURCE` when no argument is
  given, so the share code stays out of the command line.
- `internal/engine/drain.go`, `cmd/coolify-mirror/relocate.go`,
  `internal/coolify/runner.php` (`kombify.3`): `drain --plan FILE` stops the
  plan's applications and services through Coolify's own stop actions (no
  container or image removed; databases keep running) and writes a drain
  record; `undrain --record FILE` starts them again and waits until they run.
  `backup --drained FILE` records drained resources as running, so the
  destination starts them. The helper's capability check gains `drain`.
- `internal/engine/fetch.go`, `internal/transfer/transfer.go` (`kombify.4`):
  a download from a share link is cached under the shared file's name
  (`RemoteInfo` reads it from the share), not under the share token alone.
  A relocation derives the token from the move's secret, so a later move with
  the same Wallet item no longer restores an earlier move's download. A
  restore therefore needs the share to be up; retrying after the source
  stopped sharing means a new `relocate send`.

## Release packaging

`.goreleaser.yaml` builds this module (`dir: third_party/coolify-mirror`,
Linux amd64 and arm64 only) into every Linux StackKits archive, with this
LICENSE under `licenses/coolify-mirror/`, and into the Debian package.
`install.sh` installs it to `../lib/stackkit/coolify-mirror` beside the
`stackkit` binary, where the CLI looks first;
`scripts/release/validate-release-archives.sh` checks it is packaged and runs.

## Updating the pin

1. Diff the new upstream release against `UpstreamCommit` and review it.
2. Copy the upstream tree over this directory (same excluded paths), re-apply
   the kombify changes, and set `UpstreamCommit` and `ForkRevision` here, in
   `internal/engine/manifest.go` and in
   `internal/workloadrelocation/coolify/engine.go` (`Pinned`).
3. Build with `CGO_ENABLED=0 go build -trimpath ./cmd/coolify-mirror` and run
   `stackkit relocate plan` and `check` between two Coolify servers.

The kombify.5 checked-relocation recovery extension uses transfer-keyed source
drain journals, durable original Docker restart policies, pre-serve signed
ciphertext preparation, and a destination no-effects abort marker serialized
with the existing engine Apply lock. It refuses standalone databases whose
external writers cannot be fenced, automatic/preview deployments, and full
controller replacement through the checked relocation adapter. Requested domain
switches require exact readback. These source changes require the exact built
Preview candidate; they do not claim a newly published stable capability.
