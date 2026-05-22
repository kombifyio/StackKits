#!/usr/bin/env bash
set -euo pipefail

dist_dir="${1:-dist}"
image_ref="${2:-}"

command -v syft >/dev/null 2>&1 || {
  echo "syft is required to generate SBOMs" >&2
  exit 1
}

shopt -s nullglob
for artifact in "$dist_dir"/*.tar.gz "$dist_dir"/*.zip "$dist_dir"/*.deb "$dist_dir"/*.rpm "$dist_dir"/*.apk; do
  syft "$artifact" -o "spdx-json=${artifact}.spdx.json"
done

if [ -n "$image_ref" ]; then
  safe_name="$(printf '%s' "$image_ref" | tr '/:@' '___')"
  syft "$image_ref" -o "spdx-json=${dist_dir}/${safe_name}.spdx.json"
fi
