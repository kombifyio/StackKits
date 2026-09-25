#!/usr/bin/env sh
set -eu

# Packages the OpenTofu providers the Stage 1 roots require as an unpacked
# filesystem mirror, so tofu init runs offline on the owner's host
# (ADR-0045 §5; internal/tofu.OfflineCLIConfig). Keep LOCAL_PROVIDER_VERSION
# equal to internal/tofu.PinnedLocalProviderVersion and to the providers/
# paths in .goreleaser.yaml.
LOCAL_PROVIDER_VERSION="${LOCAL_PROVIDER_VERSION:-2.5.3}"
OUT_DIR="${OUT_DIR:-.dist-tools/opentofu-providers}"
DOWNLOAD_DIR="${OUT_DIR}/downloads"
TARGETS="${STACKKIT_RELEASE_TOOL_TARGETS:-linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64}"
REGISTRY_HOST="registry.opentofu.org"
RELEASE_URL="https://github.com/opentofu/terraform-provider-local/releases/download/v${LOCAL_PROVIDER_VERSION}"

mkdir -p "$DOWNLOAD_DIR"

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

unzip_into() {
  if command -v unzip >/dev/null 2>&1; then
    unzip -q -o "$1" -d "$2"
  else
    python3 -m zipfile -e "$1" "$2"
  fi
}

# The OpenTofu registry publishes this SHA256SUMS file as the shasums_url of
# every hashicorp/local download.
SUMS="${DOWNLOAD_DIR}/terraform-provider-local_${LOCAL_PROVIDER_VERSION}_SHA256SUMS"
curl -fsSL "${RELEASE_URL}/terraform-provider-local_${LOCAL_PROVIDER_VERSION}_SHA256SUMS" -o "$SUMS"

fetch_one() {
  os="$1"
  arch="$2"
  name="terraform-provider-local_${LOCAL_PROVIDER_VERSION}_${os}_${arch}.zip"
  archive="${DOWNLOAD_DIR}/${name}"
  target_dir="${OUT_DIR}/${os}_${arch}/providers/${REGISTRY_HOST}/hashicorp/local/${LOCAL_PROVIDER_VERSION}/${os}_${arch}"

  echo "Fetching hashicorp/local ${LOCAL_PROVIDER_VERSION} for ${os}/${arch}"
  curl -fsSL "${RELEASE_URL}/${name}" -o "$archive"
  expected="$(awk -v name="$name" '$2 == name {print $1}' "$SUMS")"
  if [ -z "$expected" ]; then
    echo "No upstream SHA256 for ${name}" >&2
    exit 1
  fi
  actual="$(sha256_of "$archive")"
  if [ "$actual" != "$expected" ]; then
    echo "SHA256 mismatch for ${name}: expected ${expected}, got ${actual}" >&2
    exit 1
  fi
  rm -rf "$target_dir"
  mkdir -p "$target_dir"
  unzip_into "$archive" "$target_dir"
  for binary in "$target_dir"/terraform-provider-local*; do
    [ -f "$binary" ] || continue
    case "$binary" in
      *.md) ;;
      *) chmod 755 "$binary" ;;
    esac
  done
  # Provenance at the mirror root; it also anchors the archive layout, since
  # GoReleaser keeps paths relative to the common prefix of the files.
  printf 'hashicorp/local %s %s_%s sha256:%s from %s/%s\n' \
    "$LOCAL_PROVIDER_VERSION" "$os" "$arch" "$expected" "$RELEASE_URL" "$name" \
    > "${OUT_DIR}/${os}_${arch}/providers/MIRROR.txt"
}

for target in $TARGETS; do
  case "$target" in
    */*) fetch_one "${target%/*}" "${target#*/}" ;;
    *) echo "Invalid StackKit release-tool target: $target" >&2; exit 2 ;;
  esac
done
