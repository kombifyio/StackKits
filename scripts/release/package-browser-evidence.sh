#!/usr/bin/env bash
set -euo pipefail

dist="${1:-dist}"
scenario_dir="${2:-artifacts/scenarios/SK-S1}"
manifest="${scenario_dir}/browser-evidence.json"
bundle="${dist}/stackkit-SK-S1-browser-evidence.tar.gz"

if [ ! -f "$manifest" ]; then
  echo "No SK-S1 browser evidence manifest found at ${manifest}; skipping browser evidence bundle."
  exit 0
fi

status="$(
  sed -n -E 's/^.*"status"[[:space:]]*:[[:space:]]*"([^"]+)".*$/\1/p' "$manifest" |
    head -n 1
)"

case "$status" in
  pass|fail) ;;
  *)
    echo "::error title=Invalid SK-S1 browser evidence::${manifest} must contain status pass or fail before release packaging."
    exit 1
    ;;
esac

missing=()
if [ "$status" = "pass" ]; then
  for required in \
    "${scenario_dir}/browser-evidence-preflight.json" \
    "${scenario_dir}/setup-state.yaml" \
    "${scenario_dir}/screenshots"
  do
    if [ ! -e "$required" ]; then
      missing+=("$required")
    fi
  done
  if [ -d "${scenario_dir}/screenshots" ] && ! find "${scenario_dir}/screenshots" -type f | grep -q .; then
    missing+=("${scenario_dir}/screenshots/*")
  fi
fi

if [ "${#missing[@]}" -gt 0 ]; then
  echo "::error title=Incomplete SK-S1 browser evidence::Passing browser evidence must include preflight, setup-state, and screenshots."
  for path in "${missing[@]}"; do
    echo "missing: ${path}"
  done
  exit 1
fi

mkdir -p "$dist"

tar_paths=("$manifest")
for optional in \
  "${scenario_dir}/browser-evidence-preflight.json" \
  "${scenario_dir}/setup-state.yaml" \
  "${scenario_dir}/homelab.json" \
  "${scenario_dir}/screenshots"
do
  if [ -e "$optional" ]; then
    tar_paths+=("$optional")
  fi
done

tar -czf "$bundle" "${tar_paths[@]}"
echo "Packaged SK-S1 browser evidence bundle: ${bundle}"
