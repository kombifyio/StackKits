#!/usr/bin/env sh
set -eu

TERRAMATE_VERSION="${TERRAMATE_VERSION:-0.17.1}"
OUT_DIR="${OUT_DIR:-.dist-tools/terramate}"
DOWNLOAD_DIR="${OUT_DIR}/downloads"
TARGETS="${STACKKIT_RELEASE_TOOL_TARGETS:-linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64}"

mkdir -p "$DOWNLOAD_DIR"

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

# Every archive is checked against the upstream checksums.txt of the pinned
# release before it is unpacked.
SUMS="${DOWNLOAD_DIR}/terramate_${TERRAMATE_VERSION}_checksums.txt"
curl -fsSL "https://github.com/terramate-io/terramate/releases/download/v${TERRAMATE_VERSION}/checksums.txt" -o "$SUMS"

verify_archive() {
  archive="$1"
  name="$(basename "$archive")"
  expected="$(awk -v name="$name" '$2 == name || $2 == "*" name {print $1}' "$SUMS")"
  if [ -z "$expected" ]; then
    echo "No upstream SHA256 for ${name}" >&2
    exit 1
  fi
  actual="$(sha256_of "$archive")"
  if [ "$actual" != "$expected" ]; then
    echo "SHA256 mismatch for ${name}: expected ${expected}, got ${actual}" >&2
    exit 1
  fi
}

asset_arch() {
  case "$1" in
    amd64) printf '%s' "x86_64" ;;
    arm64) printf '%s' "arm64" ;;
    *) printf '%s' "$1" ;;
  esac
}

fetch_one() {
  os="$1"
  arch="$2"
  binary="terramate"
  archive_ext="tar.gz"
  archive_arch="$(asset_arch "$arch")"

  if [ "$os" = "windows" ]; then
    binary="terramate.exe"
    archive_ext="zip"
  fi

  target_dir="${OUT_DIR}/${os}_${arch}"
  archive="${DOWNLOAD_DIR}/terramate_${TERRAMATE_VERSION}_${os}_${archive_arch}.${archive_ext}"
  url="https://github.com/terramate-io/terramate/releases/download/v${TERRAMATE_VERSION}/terramate_${TERRAMATE_VERSION}_${os}_${archive_arch}.${archive_ext}"

  mkdir -p "$target_dir"
  echo "Fetching Terramate ${TERRAMATE_VERSION} for ${os}/${arch}"
  curl -fsSL "$url" -o "$archive"
  verify_archive "$archive"
  if [ "$archive_ext" = "zip" ]; then
    unzip -q -o "$archive" "$binary" -d "$target_dir"
  else
    tar -xzf "$archive" -C "$target_dir" "$binary"
  fi
  chmod 755 "${target_dir}/${binary}"
}

for target in $TARGETS; do
  case "$target" in
    */*) fetch_one "${target%/*}" "${target#*/}" ;;
    *) echo "Invalid StackKit release-tool target: $target" >&2; exit 2 ;;
  esac
done
