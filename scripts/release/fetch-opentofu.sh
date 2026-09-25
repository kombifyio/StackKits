#!/usr/bin/env sh
set -eu

TOFU_VERSION="${TOFU_VERSION:-1.11.5}"
OUT_DIR="${OUT_DIR:-.dist-tools/opentofu}"
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

# Every archive is checked against the upstream SHA256SUMS of the pinned
# release before it is unpacked.
SUMS="${DOWNLOAD_DIR}/tofu_${TOFU_VERSION}_SHA256SUMS"
curl -fsSL "https://github.com/opentofu/opentofu/releases/download/v${TOFU_VERSION}/tofu_${TOFU_VERSION}_SHA256SUMS" -o "$SUMS"

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

fetch_one() {
  os="$1"
  arch="$2"
  binary="tofu"
  if [ "$os" = "windows" ]; then
    binary="tofu.exe"
  fi

  target_dir="${OUT_DIR}/${os}_${arch}"
  archive="${DOWNLOAD_DIR}/tofu_${TOFU_VERSION}_${os}_${arch}.tar.gz"
  url="https://github.com/opentofu/opentofu/releases/download/v${TOFU_VERSION}/tofu_${TOFU_VERSION}_${os}_${arch}.tar.gz"

  mkdir -p "$target_dir"
  echo "Fetching OpenTofu ${TOFU_VERSION} for ${os}/${arch}"
  curl -fsSL "$url" -o "$archive"
  verify_archive "$archive"
  tar -xzf "$archive" -C "$target_dir" "$binary"
  chmod 755 "${target_dir}/${binary}"
}

for target in $TARGETS; do
  case "$target" in
    */*) fetch_one "${target%/*}" "${target#*/}" ;;
    *) echo "Invalid StackKit release-tool target: $target" >&2; exit 2 ;;
  esac
done
