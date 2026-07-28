#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 4 ]; then
  printf 'usage: %s <artifacts-root> <tag> <source-commit> <source-digest>\n' "$0" >&2
  exit 2
fi

root="$(realpath "$1")"
tag="$2"
commit="$3"
source_digest="$4"
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/../.." && pwd)"

node "$script_dir/compose-modern-terminal-receipt.mjs" \
  --archive-evidence "$root/modern-archive-live-proof-evidence.json" \
  --runtime-receipt "$root/modern-runtime-live-receipt.json" \
  --ha-receipt "$root/modern-warm-standby-live-receipt.json" \
  --plan "$root/resolved-plan.json" \
  --inventory "$root/inventory.json" \
  --apply "$root/apply-result.json" \
  --verify "$root/verify.json" \
  --transcript "$root/modern-runtime-process-transcript.json" \
  --partition "$root/docker-partition-evidence.json" \
  --process "$root/modern-runtime-process" \
  --tag "$tag" \
  --source-commit "$commit" \
  --source-digest "$source_digest" \
  --output "$root/modern-terminal-live-receipt.json"

node "$repo_root/scripts/release/validate-modern-terminal-receipt.mjs" \
  --receipt "$root/modern-terminal-live-receipt.json" \
  --artifacts-root "$root" \
  --tag "$tag" \
  --source-commit "$commit" \
  --source-digest "$source_digest"
