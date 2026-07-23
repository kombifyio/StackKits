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
# configured full/per-kit archive, then execute the semantic smoke on the
# native Linux/amd64 artifacts below.
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
    base/stackkit.cue \
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

smoke_public_backup_cli() {
  local label="$1"
  local extract_dir="$2"
  local help_log="$tmp/${label}-backup-help.log"
  local enroll_log="$tmp/${label}-backup-enroll.log"
  local export_dir="$tmp/${label}-emergency-export"

  "$extract_dir/stackkit" backup --help >"$help_log"
  local verb
  for verb in init configure status run list restore verify emergency-export migrate-from-restic; do
    grep -Eq "^[[:space:]]+${verb}[[:space:]]" "$help_log" ||
      fail "$label archive CLI is missing public backup verb: $verb"
  done
  if grep -Eq '^[[:space:]]+enroll[[:space:]]' "$help_log"; then
    fail "$label archive CLI leaked backup enroll"
  fi
  if "$extract_dir/stackkit" backup enroll >"$enroll_log" 2>&1; then
    fail "$label archive CLI unexpectedly resolved backup enroll"
  fi
  grep -qi 'unknown command "enroll"' "$enroll_log" ||
    fail "$label archive CLI did not reject backup enroll as an unknown command"

  "$extract_dir/stackkit" backup emergency-export \
    --target "$export_dir" \
    --source "$extract_dir/README.md" \
    --include-class config >"$tmp/${label}-emergency-export.log"
  [ -f "$export_dir/stackkit-emergency-export-manifest.json" ] ||
    fail "$label emergency export did not write its manifest"
  [ -f "$export_dir/RESTORE.md" ] ||
    fail "$label emergency export did not write its restore runbook"
  grep -q '"schemaVersion": "stackkit.backup-emergency-export/v1"' \
    "$export_dir/stackkit-emergency-export-manifest.json" ||
    fail "$label emergency export manifest schema drifted"
}

check_archive_contents "$full_archive" basement-kit/stackkit.yaml cloud-kit/stackkit.yaml modern-homelab/stackkit.yaml
check_archive_contents "$basement_archive" basement-kit/stackkit.yaml
check_archive_contents "$cloud_archive" cloud-kit/stackkit.yaml
check_archive_contents "$modern_archive" modern-homelab/stackkit.yaml

stage_stackkits_home() {
  local extract_dir="$1"
  local home_dir="$2"
  shift 2

  mkdir -p "$home_dir/.stackkits"
  local dir
  for dir in base modules cue.mod "$@"; do
    if [ -e "$extract_dir/$dir" ]; then
      rm -rf "$home_dir/.stackkits/$dir"
      cp -R "$extract_dir/$dir" "$home_dir/.stackkits/"
    fi
  done

  local kit
  for kit in "$@"; do
    if [ -d "$extract_dir/base" ] && [ -d "$home_dir/.stackkits/$kit" ]; then
      rm -rf "$home_dir/.stackkits/$kit/base"
      cp -R "$extract_dir/base" "$home_dir/.stackkits/$kit/"
    fi
  done
}

# Native v2 archive smoke. Init proves that the released binary can materialize
# the selected embedded KitDefinition without relying on the source checkout.
# Generation is deliberately not attempted here: it requires an admitted
# Inventory and exact ResolvedPlan, which init neither invents nor owns.
smoke_v2_authoring() {
  local label="$1"
  local extract_dir="$2"
  local home_dir="$3"
  local project_dir="$4"
  local kit="$5"
  local name="$6"
  local domain="${7:-}"

  mkdir -p "$project_dir"
  "$extract_dir/stackkit" version >/dev/null
  "$extract_dir/tofu" version >/dev/null
  "$extract_dir/terramate" version >/dev/null
  "$extract_dir/stackkit-server" --help >/dev/null 2>&1
  "$extract_dir/stackkit-mcp" --help >/dev/null 2>&1
  node "$extract_dir/scripts/release/validate-architecture-contract-fixture.mjs" \
    --repo-root "$extract_dir" --proof-only
  smoke_public_backup_cli "$label" "$extract_dir"

  local init_args=("$kit" --non-interactive --name "$name")
  if [ -n "$domain" ]; then
    init_args+=(--domain "$domain")
  fi
  (
    cd "$project_dir"
    HOME="$home_dir" PATH="$extract_dir:$PATH" "$extract_dir/stackkit" \
      init "${init_args[@]}" >"$tmp/${label}-init.log"
  )

  local spec="$project_dir/stack-spec.yaml"
  [ -f "$spec" ] || fail "$label smoke did not materialize stack-spec.yaml"
  grep -q '"apiVersion":"stackkit/v2alpha1"' "$spec" ||
    fail "$label smoke did not materialize a native v2 StackSpec"
  grep -q "\"slug\":\"${kit}\"" "$spec" ||
    fail "$label smoke selected the wrong kit profile"
  grep -q "\"name\":\"${name}\"" "$spec" ||
    fail "$label smoke did not preserve the approved deployment name override"
  if [ -n "$domain" ]; then
    grep -q "\"base\":\"${domain}\"" "$spec" ||
      fail "$label smoke did not preserve the approved domain override"
  fi
  [ ! -e "$project_dir/deploy" ] ||
    fail "$label native v2 init invented plan-owned generation output"
}

# Basement smokes: from the dedicated basement archive and from the full catalog archive.
basement_extract="$tmp/basement-extract"
basement_home="$tmp/basement-home"
basement_project="$tmp/basement-project"
mkdir -p "$basement_extract"
tar xzf "$basement_archive" -C "$basement_extract"
stage_stackkits_home "$basement_extract" "$basement_home" basement-kit
smoke_v2_authoring "basement-archive" "$basement_extract" "$basement_home" "$basement_project" \
  basement-kit release-smoke-basement

full_extract="$tmp/full-extract"
full_home="$tmp/full-home"
full_project="$tmp/full-project"
mkdir -p "$full_extract"
tar xzf "$full_archive" -C "$full_extract"
stage_stackkits_home "$full_extract" "$full_home" basement-kit cloud-kit modern-homelab
smoke_v2_authoring "full-archive-cli-catalog" "$full_extract" "$full_home" "$full_project" \
  basement-kit release-smoke-full

# Cloud smoke from the dedicated cloud archive.
cloud_extract="$tmp/cloud-extract"
cloud_home="$tmp/cloud-home"
cloud_project="$tmp/cloud-project"
mkdir -p "$cloud_extract"
tar xzf "$cloud_archive" -C "$cloud_extract"
stage_stackkits_home "$cloud_extract" "$cloud_home" cloud-kit
smoke_v2_authoring "cloud-archive" "$cloud_extract" "$cloud_home" "$cloud_project" \
  cloud-kit release-smoke-cloud cloud-smoke.example.com

# Modern smoke proves that the released Preview definition is self-contained
# and can materialize its CUE-validated initial intent without claiming live
# federation execution. Full resolution remains Inventory-bound.
modern_extract="$tmp/modern-extract"
modern_home="$tmp/modern-home"
modern_project="$tmp/modern-project"
mkdir -p "$modern_extract"
tar xzf "$modern_archive" -C "$modern_extract"
stage_stackkits_home "$modern_extract" "$modern_home" modern-homelab
smoke_v2_authoring "modern-archive" "$modern_extract" "$modern_home" "$modern_project" \
  modern-homelab release-smoke-modern modern-smoke.example.com

printf 'release archive validation passed\n'
