#!/usr/bin/env sh
set -eu

TERRAMATE_VERSION="${TERRAMATE_VERSION:-0.17.1}"
OUT_DIR="${OUT_DIR:-.dist-tools/terramate}"
DOWNLOAD_DIR="${OUT_DIR}/downloads"

mkdir -p "$DOWNLOAD_DIR"

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
  if [ "$archive_ext" = "zip" ]; then
    unzip -q -o "$archive" "$binary" -d "$target_dir"
  else
    tar -xzf "$archive" -C "$target_dir" "$binary"
  fi
  chmod 755 "${target_dir}/${binary}"
}

fetch_one linux amd64
fetch_one linux arm64
fetch_one darwin amd64
fetch_one darwin arm64
fetch_one windows amd64
