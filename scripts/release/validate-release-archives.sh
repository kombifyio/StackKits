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
  tofu \
  README.md \
  LICENSE \
  cue.mod/module.cue \
  base/stackkit.cue \
  base-kit/stackkit.yaml \
  modules/tinyauth/module.cue \
  modules/pocketid/module.cue; do
  require_file "$tmp/full-files.txt" "$path"
done

tar tzf "$base_archive" | sort > "$tmp/base-files.txt"
for path in \
  stackkit \
  tofu \
  README.md \
  LICENSE \
  cue.mod/module.cue \
  base/stackkit.cue \
  base-kit/stackkit.yaml \
  modules/tinyauth/module.cue \
  modules/pocketid/module.cue; do
  require_file "$tmp/base-files.txt" "$path"
done

extract_dir="$tmp/base-extract"
home_dir="$tmp/home"
project_dir="$tmp/project"
mkdir -p "$extract_dir" "$home_dir/.stackkits" "$project_dir"
tar xzf "$base_archive" -C "$extract_dir"

"$extract_dir/stackkit" version >/dev/null
"$extract_dir/tofu" version >/dev/null

for dir in base base-kit modules cue.mod; do
  if [ -e "$extract_dir/$dir" ]; then
    cp -R "$extract_dir/$dir" "$home_dir/.stackkits/"
  fi
done
if [ -d "$extract_dir/base" ]; then
  rm -rf "$home_dir/.stackkits/base-kit/base"
  cp -R "$extract_dir/base" "$home_dir/.stackkits/base-kit/"
fi

(
  cd "$project_dir"
  HOME="$home_dir" PATH="$extract_dir:$PATH" "$extract_dir/stackkit" \
    --context local init base-kit --non-interactive --force \
    --admin-email release-smoke@example.com >/tmp/stackkit-archive-init.log
  HOME="$home_dir" PATH="$extract_dir:$PATH" "$extract_dir/stackkit" \
    --context local generate --force >/tmp/stackkit-archive-generate.log
)

tfvars="$project_dir/deploy/terraform.tfvars.json"
[ -f "$tfvars" ] || fail "archive smoke did not generate terraform.tfvars.json"
grep -q '"admin_email": "release-smoke@example.com"' "$tfvars" ||
  fail "archive smoke did not preserve admin email"
grep -Eq '"tinyauth_users": "release-smoke@example.com:\$2[aby]\$' "$tfvars" ||
  fail "archive smoke did not generate TinyAuth bcrypt users from module contracts"

printf 'release archive validation passed\n'
