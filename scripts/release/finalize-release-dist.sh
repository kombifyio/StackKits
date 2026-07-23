#!/usr/bin/env bash
set -euo pipefail

dist_dir="${1:-dist}"
jobs="${STACKKIT_RELEASE_COMPRESSION_JOBS:-$(nproc 2>/dev/null || printf '2')}"

fail() {
  printf 'release dist finalization failed: %s\n' "$*" >&2
  exit 1
}

[[ "$jobs" =~ ^[1-9][0-9]*$ ]] || fail "compression jobs must be a positive integer"
if [ "$jobs" -gt 8 ]; then
  jobs=8
fi
[ -d "$dist_dir" ] || fail "dist directory does not exist: $dist_dir"

mapfile -d '' tar_files < <(find "$dist_dir" -maxdepth 1 -type f -name '*.tar' -print0 | sort -z)
[ "${#tar_files[@]}" -gt 0 ] || fail "no Unix tar archives found in $dist_dir"

if command -v pigz >/dev/null 2>&1; then
  compressor=(pigz -n -1 -f)
else
  compressor=(gzip -n -1 -f)
fi

printf '%s\0' "${tar_files[@]}" |
  xargs -0 -r -n 1 -P "$jobs" "${compressor[@]}"

remaining_tar_count="$(find "$dist_dir" -maxdepth 1 -type f -name '*.tar' | wc -l | tr -d '[:space:]')"
[ "$remaining_tar_count" = 0 ] || fail "$remaining_tar_count uncompressed tar archives remain"

gzip_count="$(find "$dist_dir" -maxdepth 1 -type f -name '*.tar.gz' | wc -l | tr -d '[:space:]')"
[ "$gzip_count" -eq "${#tar_files[@]}" ] ||
  fail "expected ${#tar_files[@]} gzip archives, found $gzip_count"

(
  cd "$dist_dir"
  mapfile -d '' assets < <(
    find . -maxdepth 1 -type f \
      \( -name '*.tar.gz' -o -name '*.zip' -o -name '*.deb' -o -name '*.rpm' -o -name '*.apk' \) \
      -printf '%f\0' | sort -z
  )
  [ "${#assets[@]}" -gt 0 ] || fail "no publishable release assets found"
  printf '%s\0' "${assets[@]}" | xargs -0 sha256sum > checksums.txt.tmp
  mv checksums.txt.tmp checksums.txt
)

printf 'release dist finalization passed: %s tar archives compressed with %s worker(s)\n' \
  "${#tar_files[@]}" "$jobs"
