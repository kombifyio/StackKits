#!/usr/bin/env sh
set -eu

TOFU_VERSION="${TOFU_VERSION:-1.11.5}"
OUT_DIR="${OUT_DIR:-.dist-tools/opentofu}"
DOWNLOAD_DIR="${OUT_DIR}/downloads"

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

fetch_one linux amd64
fetch_one linux arm64
fetch_one darwin amd64
fetch_one darwin arm64
fetch_one windows amd64

