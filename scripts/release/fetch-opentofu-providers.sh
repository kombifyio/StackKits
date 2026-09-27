#!/usr/bin/env sh
set -eu

# Bundle the exact providers in internal/tofu/provider_manifest.json as an
# unpacked filesystem mirror. PrepareProviderClosure authenticates both
# upstream archives and every unpacked package before writing the release lock.
LOCAL_PROVIDER_VERSION="2.5.3"
KOMODO_PROVIDER_VERSION="0.12.0"
OUT_DIR="${OUT_DIR:-.dist-tools/opentofu-providers}"
DOWNLOAD_DIR="${OUT_DIR}/downloads"
TARGETS="${STACKKIT_RELEASE_TOOL_TARGETS:-linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64}"
REGISTRY_HOST="registry.opentofu.org"
LOCAL_RELEASE_URL="https://github.com/opentofu/terraform-provider-local/releases/download/v${LOCAL_PROVIDER_VERSION}"
KOMODO_RELEASE_URL="https://github.com/sebastianfs82/terraform-provider-komodo/releases/download/v${KOMODO_PROVIDER_VERSION}"
MANIFEST_SOURCE="internal/tofu/provider_manifest.json"

if command -v go >/dev/null 2>&1; then
  GO_COMMAND="go"
elif command -v mise >/dev/null 2>&1; then
  GO_COMMAND="mise exec -- go"
else
  echo "Go is required to verify the provider package closure" >&2
  exit 1
fi

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

# The OpenTofu registry names these upstream SHA256SUMS files as shasums_url.
LOCAL_SUMS="${DOWNLOAD_DIR}/terraform-provider-local_${LOCAL_PROVIDER_VERSION}_SHA256SUMS"
KOMODO_SUMS="${DOWNLOAD_DIR}/terraform-provider-komodo_${KOMODO_PROVIDER_VERSION}_SHA256SUMS"
curl -fsSL "${LOCAL_RELEASE_URL}/$(basename "$LOCAL_SUMS")" -o "$LOCAL_SUMS"
curl -fsSL "${KOMODO_RELEASE_URL}/$(basename "$KOMODO_SUMS")" -o "$KOMODO_SUMS"

fetch_one() {
  source="$1"
  version="$2"
  os="$3"
  arch="$4"
  release_url="$5"
  sums="$6"
  name="terraform-provider-${source##*/}_${version}_${os}_${arch}.zip"
  archive="${DOWNLOAD_DIR}/${name}"
  target_dir="${providers_dir}/${REGISTRY_HOST}/${source}/${version}/${os}_${arch}"

  echo "Fetching ${source} ${version} for ${os}/${arch}"
  curl -fsSL "${release_url}/${name}" -o "$archive"
  expected="$(awk -v name="$name" '$2 == name {print $1}' "$sums")"
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
  for binary in "$target_dir"/terraform-provider-*; do
    [ -f "$binary" ] || continue
    case "$binary" in
      *.md) ;;
      *) chmod 755 "$binary" ;;
    esac
  done
  printf '%s %s %s_%s sha256:%s from %s/%s\n' \
    "$source" "$version" "$os" "$arch" "$expected" "$release_url" "$name" \
    >> "${providers_dir}/MIRROR.txt"
}

for target in $TARGETS; do
  case "$target" in
    */*) target_os="${target%/*}"; target_arch="${target#*/}" ;;
    *) echo "Invalid StackKit release-tool target: $target" >&2; exit 2 ;;
  esac
  providers_dir="${OUT_DIR}/${target_os}_${target_arch}/providers"
  mkdir -p "$providers_dir"
  : > "${providers_dir}/MIRROR.txt"
  fetch_one "hashicorp/local" "$LOCAL_PROVIDER_VERSION" "$target_os" "$target_arch" "$LOCAL_RELEASE_URL" "$LOCAL_SUMS"
  fetch_one "sebastianfs82/komodo" "$KOMODO_PROVIDER_VERSION" "$target_os" "$target_arch" "$KOMODO_RELEASE_URL" "$KOMODO_SUMS"
  cp "$MANIFEST_SOURCE" "${providers_dir}/stackkit-provider-manifest.json"
  $GO_COMMAND run ./internal/tofu/cmd/providerclosure \
    -providers-dir "$providers_dir" -os "$target_os" -arch "$target_arch" \
    -archive "${REGISTRY_HOST}/hashicorp/local=${DOWNLOAD_DIR}/terraform-provider-local_${LOCAL_PROVIDER_VERSION}_${target_os}_${target_arch}.zip" \
    -archive "${REGISTRY_HOST}/sebastianfs82/komodo=${DOWNLOAD_DIR}/terraform-provider-komodo_${KOMODO_PROVIDER_VERSION}_${target_os}_${target_arch}.zip"
done
