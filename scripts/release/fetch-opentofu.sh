#!/usr/bin/env sh
set -eu

TOFU_VERSION="${TOFU_VERSION:-1.11.5}"
OUT_DIR="${OUT_DIR:-.dist-tools/opentofu}"
DOWNLOAD_DIR="${OUT_DIR}/downloads"
TARGETS="${STACKKIT_RELEASE_TOOL_TARGETS:-linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64}"

mkdir -p "$DOWNLOAD_DIR"

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
  tar -xzf "$archive" -C "$target_dir" "$binary"
  chmod 755 "${target_dir}/${binary}"
}

for target in $TARGETS; do
  case "$target" in
    */*) fetch_one "${target%/*}" "${target#*/}" ;;
    *) echo "Invalid StackKit release-tool target: $target" >&2; exit 2 ;;
  esac
done
