#!/usr/bin/env bash
set -euo pipefail

dist_dir="${1:-dist}"
source_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
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

# Every published bundle contains the exact license and notice files from its
# source tree, including Windows ZIPs. Checking all targets prevents a new
# archive stanza from silently omitting the notices.
check_legal_files() {
  local archive="$1"
  local entry output_dir
  output_dir="$tmp/legal-$(basename "$archive")"
  mkdir -p "$output_dir"
  case "$archive" in
    *.zip)
      python3 -c 'import sys, zipfile; zipfile.ZipFile(sys.argv[1]).extractall(sys.argv[2], sys.argv[3:])' \
        "$archive" "$output_dir" LICENSE-MPL-2.0 THIRD-PARTY-NOTICES.md ||
        fail "missing legal files in ${archive}"
      ;;
    *.tar.gz)
      tar -xzf "$archive" -C "$output_dir" LICENSE-MPL-2.0 THIRD-PARTY-NOTICES.md ||
        fail "missing legal files in ${archive}"
      ;;
    *) fail "unsupported archive ${archive}" ;;
  esac
  for entry in LICENSE-MPL-2.0 THIRD-PARTY-NOTICES.md; do
    cmp -s "$source_dir/$entry" "$output_dir/$entry" || fail "${entry} differs from source in ${archive}"
  done
}

for target in linux_amd64 linux_arm64 darwin_amd64 darwin_arm64 windows_amd64; do
  for kind in stackkits stackkits-basement-kit stackkits-cloud-kit stackkits-modern-homelab; do
    extension=tar.gz
    [ "$target" = windows_amd64 ] && extension=zip
    check_legal_files "$(find_archive "${kind}_*_${target}.${extension}")"
  done
done

mapfile -t deb_packages < <(find "$dist_dir" -maxdepth 1 -type f -name '*.deb' | sort)
if [ "${STACKKIT_REQUIRE_DEB_PACKAGES:-0}" = 1 ] && [ "${#deb_packages[@]}" -eq 0 ]; then
  fail "no Debian package in ${dist_dir}"
fi
# The post-publication archive consumer downloads only archives; the producer
# sets STACKKIT_REQUIRE_DEB_PACKAGES=1 and validates its local package output.
for package in "${deb_packages[@]}"; do
  package_root="$tmp/$(basename "$package").root"
  dpkg-deb -x "$package" "$package_root" || fail "cannot unpack ${package}"
  for entry in LICENSING.md LICENSE-APACHE LICENSE-GPL-3.0-or-later LICENSE-MPL-2.0 THIRD-PARTY-NOTICES.md; do
    cmp -s "$source_dir/$entry" "${package_root}/usr/share/doc/kombify-stackkits/${entry}" ||
      fail "missing or changed ${entry} in ${package}"
  done
done

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
    providers/stackkit-provider-manifest.json \
    providers/stackkit-provider-lock.hcl \
    providers/registry.opentofu.org/hashicorp/local/2.5.3/linux_amd64/terraform-provider-local \
    README.md \
    LICENSING.md \
    LICENSE-APACHE \
    LICENSE-GPL-3.0-or-later \
    LICENSE-MPL-2.0 \
    THIRD-PARTY-NOTICES.md \
    cue.mod/module.cue \
    docs/ENTERPRISE_READINESS.md \
    use-cases/photos/agent/family-vault/SKILL.md \
    use-cases/files/agent/owner-setup/SKILL.md \
    use-cases/media/agent/owner-setup/SKILL.md \
    use-cases/vault/agent/owner-setup/SKILL.md \
    use-cases/smart-home/agent/homelab-mcp/SKILL.md \
    schemas/release-evidence.schema.json \
    schemas/standalone-oss-e2e-receipt.schema.json \
    schemas/stackkits-use-case-catalog-v1.schema.json \
    schemas/stackkits-compatibility-v1.schema.json \
    schemas/os-compat-matrix.schema.json \
    docs/data/os-compat/latest.json \
    docs/data/advanced-operations/latest.json \
    schemas/stackkit-advanced-operations-v1.schema.json \
    schemas/stackkit-command-result-v1.schema.json \
    schemas/stackkit-rollout-event.schema.json \
    schemas/stackkit-operation-denial-v1.schema.json \
    schemas/stackkit-actionable-error-v1.schema.json \
    schemas/stackkit-advanced-trust-bundle-v1.schema.json \
    schemas/stackkit-local-advanced-trust-v1.schema.json \
    schemas/stackkit-advanced-capability-v1.schema.json \
    schemas/stackkit-advanced-change-set-v2.schema.json \
    schemas/stackkit-advanced-change-set-create-result-v2.schema.json \
    schemas/stackkit-advanced-mutation-v1.schema.json \
    schemas/stackkit-change-set-result-v1.schema.json \
    schemas/stackkit-drift-report-v1.schema.json \
    schemas/stackkit-restore-drill-report-v1.schema.json \
    schemas/stackkit-rollback-result-v1.schema.json \
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
  # The archived Advanced operations catalog is the one the packaged CLI
  # renders, so an orchestrator reading it gets this binary's exact argv.
  "$extract_dir/stackkit" --chdir "$extract_dir" docs emit-advanced-operations --check >/dev/null
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
