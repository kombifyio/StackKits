#!/usr/bin/env bash
set -euo pipefail

dist_dir="${1:-dist}"
image_ref="${2:-}"
readonly max_jobs=4

command -v syft >/dev/null 2>&1 || {
  echo "syft is required to generate SBOMs" >&2
  exit 1
}

shopt -s nullglob
artifacts=(
  "$dist_dir"/*.tar.gz
  "$dist_dir"/*.zip
  "$dist_dir"/*.deb
  "$dist_dir"/*.rpm
  "$dist_dir"/*.apk
)

pids=()
subjects=()

wait_for_batch() {
  local failed=0
  local index
  for index in "${!pids[@]}"; do
    if ! wait "${pids[$index]}"; then
      printf 'SBOM generation failed for %s\n' "${subjects[$index]}" >&2
      failed=1
    fi
  done
  pids=()
  subjects=()
  return "$failed"
}

queue_sbom() {
  local subject="$1"
  local output="$2"
  syft "$subject" -o "spdx-json=$output" &
  pids+=("$!")
  subjects+=("$subject")
  if [ "${#pids[@]}" -ge "$max_jobs" ]; then
    wait_for_batch
  fi
}

for artifact in "${artifacts[@]}"; do
  queue_sbom "$artifact" "${artifact}.spdx.json"
done

if [ -n "$image_ref" ]; then
  safe_name="$(printf '%s' "$image_ref" | tr '/:@' '___')"
  queue_sbom "$image_ref" "${dist_dir}/${safe_name}.spdx.json"
fi

wait_for_batch
