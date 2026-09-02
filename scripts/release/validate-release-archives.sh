#!/usr/bin/env bash
set -euo pipefail

dist_dir="${1:-dist}"
fail() {
  printf 'release archive validation failed: %s\n' "$*" >&2
  exit 1
}

require_file() {
  local list_file="$1"
  local path="$2"
  grep -q "^${path}$" "$list_file" || fail "missing ${path} in ${list_file}"
}

forbid_file() {
  local list_file="$1"
  local path="$2"
  if grep -q "^${path}$" "$list_file"; then
    fail "forbidden ${path} present in ${list_file}"
  fi
}

find_archive() {
  local pattern="$1"
  local label="${2:-$pattern}"
  mapfile -t matches < <(find "$dist_dir" -maxdepth 1 -type f -name "$pattern" | sort)
  [ "${#matches[@]}" -eq 1 ] ||
    fail "expected exactly one ${label} archive matching ${pattern}, found ${#matches[@]}"
  printf '%s\n' "${matches[0]}"
}

require_archive_matrix() {
  local target extension
  for target in linux_amd64 linux_arm64 darwin_amd64 darwin_arm64; do
    extension='tar.gz'
    find_archive "stackkits_*_${target}.${extension}" "full ${target}" >/dev/null
    find_archive "stackkits-basement-kit_*_${target}.${extension}" "basement-kit ${target}" >/dev/null
    find_archive "stackkits-cloud-kit_*_${target}.${extension}" "cloud-kit ${target}" >/dev/null
    find_archive "stackkits-modern-homelab_*_${target}.${extension}" "modern-homelab ${target}" >/dev/null
  done
  target='windows_amd64'
  extension='zip'
  find_archive "stackkits_*_${target}.${extension}" "full ${target}" >/dev/null
  find_archive "stackkits-basement-kit_*_${target}.${extension}" "basement-kit ${target}" >/dev/null
  find_archive "stackkits-cloud-kit_*_${target}.${extension}" "cloud-kit ${target}" >/dev/null
  find_archive "stackkits-modern-homelab_*_${target}.${extension}" "modern-homelab ${target}" >/dev/null
}

# GoReleaser builds every supported target before validation. Require every
# configured full/per-kit archive and inspect the native Linux/amd64 archive
# layouts without executing lifecycle commands before release trust exists.
require_archive_matrix

full_archive="$(find_archive 'stackkits_*_linux_amd64.tar.gz' 'full linux_amd64')"
basement_archive="$(find_archive 'stackkits-basement-kit_*_linux_amd64.tar.gz' 'basement-kit linux_amd64')"
cloud_archive="$(find_archive 'stackkits-cloud-kit_*_linux_amd64.tar.gz' 'cloud-kit linux_amd64')"
modern_archive="$(find_archive 'stackkits-modern-homelab_*_linux_amd64.tar.gz' 'modern-homelab linux_amd64')"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# Required entries inside an archive: the common toolchain/contract files plus
# any kit-specific stackkit.yaml passed as extra args.
check_archive_contents() {
  local archive="$1"
  shift
  local list="$tmp/$(basename "$archive").files.txt"
  tar tzf "$archive" | sort > "$list"
  local p
  for p in \
    stackkit \
    stackkit-server \
    stackkit-mcp \
    tofu \
    terramate \
    README.md \
    LICENSE \
    cue.mod/module.cue \
    docs/ENTERPRISE_READINESS.md \
    schemas/release-evidence.schema.json \
    schemas/standalone-oss-e2e-receipt.schema.json \
    schemas/stackkits-use-case-catalog-v1.schema.json \
    schemas/stackkits-compatibility-v1.schema.json \
    schemas/stackkits-os-compatibility-input-v1.schema.json \
    docs/data/os-compat/latest.json \
    scripts/e2e/validate-standalone-oss-e2e.mjs \
    scripts/e2e/validate-standalone-runtime-e2e.mjs \
    scripts/release/validate-architecture-contract-fixture.mjs \
    architecture/v2/fixtures/contract-two-node.yaml \
    architecture/v2/fixtures/contract-two-node.inventory.yaml \
    architecture/v2/fixtures/contract-two-node.resolved-plan.json \
    architecture/v2/fixtures/contract-fixtures.manifest.json \
    architecture/v2/contractfixture/catalog.cue \
    addons/backup/README.md \
    addons/backup/addon.cue \
    addons/backup/integrity.cue \
    addons/backup/restic-importer.cue \
    foundation/stackkit.cue \
    modules/tinyauth/module.cue \
    modules/pocketid/module.cue; do
    require_file "$list" "$p"
  done
  for p in "$@"; do
    require_file "$list" "$p"
  done
  for p in \
    addons/backup/managed.cue \
    cmd/stackkit/commands/backup_managed.go; do
    forbid_file "$list" "$p"
  done
}

check_archive_contents "$full_archive" basement-kit/stackkit.yaml cloud-kit/stackkit.yaml modern-homelab/stackkit.yaml
check_archive_contents "$basement_archive" basement-kit/stackkit.yaml
check_archive_contents "$cloud_archive" cloud-kit/stackkit.yaml
check_archive_contents "$modern_archive" modern-homelab/stackkit.yaml

# Verify packaged executables and the bundled contract fixture before
# publication. Lifecycle behavior belongs to separately invoked diagnostics.
validate_public_archive_executables() {
  local extract_dir="$1"

  "$extract_dir/stackkit" version >/dev/null
  "$extract_dir/tofu" version >/dev/null
  "$extract_dir/terramate" version >/dev/null
  "$extract_dir/stackkit-server" --help >/dev/null 2>&1
  "$extract_dir/stackkit-mcp" --help >/dev/null 2>&1
  node "$extract_dir/scripts/release/validate-architecture-contract-fixture.mjs" \
    --repo-root "$extract_dir" --proof-only
}

full_extract="$tmp/full-extract"
mkdir -p "$full_extract"
tar xzf "$full_archive" -C "$full_extract"
validate_public_archive_executables "$full_extract"

# This gate proves archive contents and executability. It does not claim
# runtime or recovery evidence; v0.x publication does not require those runs.
printf 'pre-trust release archive structural validation passed\n'
