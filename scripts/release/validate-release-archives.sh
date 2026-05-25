#!/usr/bin/env bash
set -euo pipefail

dist_dir="${1:-dist}"
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

fail() {
  printf 'release archive validation failed: %s\n' "$*" >&2
  exit 1
}

require_file() {
  local list_file="$1"
  local path="$2"
  grep -q "^${path}$" "$list_file" || fail "missing ${path} in ${list_file}"
}

find_archive() {
  local pattern="$1"
  find "$dist_dir" -maxdepth 1 -name "$pattern" | sort | head -1
}

full_archive="$(find_archive 'stackkits_*_linux_amd64.tar.gz')"
base_archive="$(find_archive 'stackkits-base-kit_*_linux_amd64.tar.gz')"

[ -n "$full_archive" ] || fail "missing linux/amd64 full stackkits archive"
[ -n "$base_archive" ] || fail "missing linux/amd64 base-kit archive"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

tar tzf "$full_archive" | sort > "$tmp/full-files.txt"
for path in \
  stackkit \
  stackkit-server \
  stackkit-mcp \
  tofu \
  README.md \
  LICENSE \
  cue.mod/module.cue \
  docs/ENTERPRISE_READINESS.md \
  schemas/release-evidence.schema.json \
  base/stackkit.cue \
  base-kit/stackkit.yaml \
  ha-kit/stackkit.yaml \
  modern-homelab/stackkit.yaml \
  modules/tinyauth/module.cue \
  modules/pocketid/module.cue; do
  require_file "$tmp/full-files.txt" "$path"
done

tar tzf "$base_archive" | sort > "$tmp/base-files.txt"
for path in \
  stackkit \
  stackkit-server \
  stackkit-mcp \
  tofu \
  README.md \
  LICENSE \
  cue.mod/module.cue \
  docs/ENTERPRISE_READINESS.md \
  schemas/release-evidence.schema.json \
  base/stackkit.cue \
  base-kit/stackkit.yaml \
  modules/tinyauth/module.cue \
  modules/pocketid/module.cue; do
  require_file "$tmp/base-files.txt" "$path"
done

stage_stackkits_home() {
  local extract_dir="$1"
  local home_dir="$2"
  shift 2

  mkdir -p "$home_dir/.stackkits"
  for dir in base modules cue.mod "$@"; do
    if [ -e "$extract_dir/$dir" ]; then
      rm -rf "$home_dir/.stackkits/$dir"
      cp -R "$extract_dir/$dir" "$home_dir/.stackkits/"
    fi
  done

  for kit in "$@"; do
    if [ -d "$extract_dir/base" ] && [ -d "$home_dir/.stackkits/$kit" ]; then
      rm -rf "$home_dir/.stackkits/$kit/base"
      cp -R "$extract_dir/base" "$home_dir/.stackkits/$kit/"
    fi
  done
}

smoke_basekit_init_generate() {
  local label="$1"
  local extract_dir="$2"
  local home_dir="$3"
  local project_dir="$4"

  mkdir -p "$project_dir"
  "$extract_dir/stackkit" version >/dev/null
  "$extract_dir/tofu" version >/dev/null
  "$extract_dir/stackkit-server" --help >/dev/null 2>&1
  "$extract_dir/stackkit-mcp" --help >/dev/null 2>&1

  (
    cd "$project_dir"
    HOME="$home_dir" PATH="$extract_dir:$PATH" "$extract_dir/stackkit" \
      --context local init base-kit --non-interactive --force \
      --admin-email release-smoke@example.com >"$tmp/${label}-init.log"
    HOME="$home_dir" PATH="$extract_dir:$PATH" "$extract_dir/stackkit" \
      --context local generate --force >"$tmp/${label}-generate.log"
  )

  local tfvars="$project_dir/deploy/terraform.tfvars.json"
  [ -f "$tfvars" ] || fail "$label smoke did not generate terraform.tfvars.json"
  grep -q '"admin_email": "release-smoke@example.com"' "$tfvars" ||
    fail "$label smoke did not preserve admin email"
  grep -Eq '"tinyauth_users": "release-smoke@example.com:\$2[aby]\$' "$tfvars" ||
    fail "$label smoke did not generate TinyAuth bcrypt users from module contracts"
  grep -q '"paas": "coolify"' "$tfvars" ||
    fail "$label smoke did not resolve BaseKit default to paas=coolify"
  grep -q '"reverse_proxy_backend": "coolify"' "$tfvars" ||
    fail "$label smoke did not resolve BaseKit reverse proxy to Coolify"
  grep -q '"enable_coolify": true' "$tfvars" ||
    fail "$label smoke did not enable Coolify"
  grep -q '"enable_dokploy": false' "$tfvars" ||
    fail "$label smoke did not keep Dokploy opt-in"
  grep -q '"enable_whoami": true' "$tfvars" ||
    fail "$label smoke did not enable Whoami routing diagnostics"
  grep -q '"enable_immich": true' "$tfvars" ||
    fail "$label smoke did not enable Immich"
  grep -q '"enable_jellyfin": false' "$tfvars" ||
    fail "$label smoke did not keep Jellyfin opt-in"
  grep -q 'resource "null_resource" "coolify_platform_bootstrap"' "$project_dir/deploy/main.tf" ||
    fail "$label smoke did not generate Coolify API bootstrap"
  grep -q 'STACKKIT_COOLIFY_PLATFORM_JSON=' "$project_dir/deploy/main.tf" ||
    fail "$label smoke did not emit Coolify platform config JSON"
  grep -q 'PLATFORM_CONFIG_PATH="${path.module}/.stackkit/platform.json"' "$project_dir/deploy/main.tf" ||
    fail "$label smoke did not persist Coolify adapter platform config"
  grep -q 'coolify_api_endpoint              = local.coolify_local_endpoint' "$project_dir/deploy/main.tf" ||
    fail "$label smoke did not persist the node-local Coolify endpoint"
  grep -q 'coolify_bootstrap_api_endpoint' "$project_dir/deploy/main.tf" ||
    fail "$label smoke did not generate the reachable Coolify bootstrap endpoint"
  node "$script_dir/check-l3-paas-contract.mjs" \
    --repo-root "$extract_dir" \
    --generated "$project_dir/deploy/main.tf" ||
    fail "$label smoke violated the default StackKit-owned L3 PaaS contract"
}

base_extract="$tmp/base-extract"
base_home="$tmp/base-home"
base_project="$tmp/base-project"
mkdir -p "$base_extract"
tar xzf "$base_archive" -C "$base_extract"
stage_stackkits_home "$base_extract" "$base_home" base-kit
smoke_basekit_init_generate "base-archive" "$base_extract" "$base_home" "$base_project"

full_extract="$tmp/full-extract"
full_home="$tmp/full-home"
full_project="$tmp/full-project"
mkdir -p "$full_extract"
tar xzf "$full_archive" -C "$full_extract"
stage_stackkits_home "$full_extract" "$full_home" base-kit ha-kit modern-homelab
smoke_basekit_init_generate "full-archive-cli-catalog" "$full_extract" "$full_home" "$full_project"

printf 'release archive validation passed\n'
