#!/bin/sh
# =============================================================================
# StackKits CLI installer — shared core used by all stackkit installers.
# =============================================================================
# Usage (direct):
#   curl -sSL https://install.stackkit.cc | sh                        # CLI, then ask to install a StackKit
#   STACKKIT_CLI_ONLY=1 curl -sSL https://install.stackkit.cc | sh    # CLI + public kit catalog only
#   curl -sSL https://install.stackkit.cc | sh -s -- basement-kit     # CLI + basement-kit
#   curl -sSL https://install.stackkit.cc | sh -s -- cloud-kit        # CLI + cloud-kit
#   curl -sSL https://install.stackkit.cc | sh -s -- modern-homelab   # CLI + Modern Homelab Preview
#
# StackKits ships Stable only (D-2026-11-07-07): the installer resolves the
# recommended release (the newest one on which every declared test lane passed,
# D-2026-11-10-18), the newest stable release while none is recommended, or the
# exact STACKKIT_RELEASE_VERSION.
#
# Called by the short website entrypoints (`base.stackkit.cc`,
# `cloud.stackkit.cc`) to provide the shared install + kit-download step
# before the kit-specific flow. The retired `base-kit` name is rejected.
# STACKKIT_CLI_ONLY=1 keeps this script from launching a kit installer.
# STACKKIT_PLACEMENT=home|cloud picks the kit for this host instead of the
# detected network; without it a public server takes Cloud Kit.
# KOMBIFY_LANG (or LC_ALL, LC_MESSAGES, LANG) sets the language of the
# installer notices: en, de, es, fr, hi, ar, zh-Hans; default en.
# =============================================================================
set -eu

# BEGIN STACKKIT_BOOTSTRAP_SOURCE
# stdin has no script directory; the website projection inlines these helpers.
case "$0" in
  */install.sh|install.sh)
    _INSTALL_SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd -P)
    # shellcheck source=install-host-bootstrap.sh
    . "$_INSTALL_SCRIPT_DIR/install-host-bootstrap.sh"
    ;;
  *) printf 'Use the complete entrypoint: curl -fsSL https://install.stackkit.cc | sh\n' >&2; exit 2 ;;
esac
# END STACKKIT_BOOTSTRAP_SOURCE

# KIT_NAME controls which kit definitions are downloaded alongside the binary.
# ""             → CLI + catalog, then ask to install a StackKit (default yes)
# "basement-kit" → CLI + basement-kit definitions
# "cloud-kit"    → CLI + cloud-kit definitions
# "modern-homelab" → CLI + Modern Homelab Preview definitions
KIT_NAME="${1:-}"
INSTALL_KITS=""
# The installer takes no options; reject one before the journal records it as
# the kit, so a rerun is not stuck on it.
case "$KIT_NAME" in
  -*)
    echo "Error: unknown option '${KIT_NAME}'. Usage: install.sh [basement-kit|cloud-kit|modern-homelab|all]" >&2
    exit 2
    ;;
esac

# Allow callers (e.g. base-install.sh) to suppress the banner.
if [ "${STACKKIT_NO_BANNER:-}" != "1" ]; then
  printf '\033[38;5;208m'
  cat <<'BANNER'

     _             _    _    _ _
 ___| |_ __ _  ___| | _| | _(_) |_
/ __| __/ _` |/ __| |/ / |/ / | __|
\__ \ || (_| | (__|   <|   <| | |_
|___/\__\__,_|\___|_|\_\_|\_\_|\__|

BANNER
  printf '\033[0m'
fi

REPO="${STACKKIT_RELEASE_REPO:-kombifyio/stackKits}"
# Releases download from the Cloudflare R2 mirror on assets.kombify.io
# (scripts/release/mirror-releases-r2.mjs); its index.json and latest.json
# keep the GitHub release-list shape. Another STACKKIT_RELEASE_REPO reads
# GitHub directly unless it names its own STACKKIT_RELEASE_MIRROR_URL.
RELEASE_MIRROR_URL="${STACKKIT_RELEASE_MIRROR_URL:-}"
if [ -z "$RELEASE_MIRROR_URL" ] && [ "$REPO" = kombifyio/stackKits ]; then
  RELEASE_MIRROR_URL="https://assets.kombify.io/stackkits/releases"
fi
RELEASE_MIRROR_URL="${RELEASE_MIRROR_URL%/}"
# An unreachable or empty mirror latest.json falls back to GitHub.
RELEASE_API_FALLBACK_URL="${STACKKIT_RELEASE_API_URL:-https://api.github.com/repos/$REPO/releases/latest}"
if [ -n "$RELEASE_MIRROR_URL" ]; then
  RELEASE_API_URL="${STACKKIT_RELEASE_API_URL:-$RELEASE_MIRROR_URL/latest.json}"
else
  RELEASE_API_URL=$RELEASE_API_FALLBACK_URL
fi
RELEASES_PAGE_URL="${STACKKIT_RELEASES_PAGE_URL:-https://github.com/$REPO/releases/latest}"
# The recommended release is the newest one on which every declared test lane
# passed (scripts/public/recommended-release.mjs); the mirror publishes it as
# recommended.json. A caller that names its own release API reads that instead.
RECOMMENDED_URL="${STACKKIT_RELEASE_RECOMMENDED_URL:-}"
if [ -z "$RECOMMENDED_URL" ] && [ -n "$RELEASE_MIRROR_URL" ] && [ -z "${STACKKIT_RELEASE_API_URL:-}" ]; then
  RECOMMENDED_URL="$RELEASE_MIRROR_URL/recommended.json"
fi
# STACKKIT_INSTALL_DIR relocates the binaries (default /usr/local/bin); the
# provider mirror goes to ../lib/stackkit/providers beside that directory.
INSTALL_DIR="${STACKKIT_INSTALL_DIR:-/usr/local/bin}"

# Kit definitions belong to the INVOKING user, even under `curl | sudo sh`:
# with plain $HOME they would land in /root/.stackkits and the printed next
# steps (stackkit init as the normal user) would not find them.
TARGET_USER="$(id -un)"
TARGET_HOME="$HOME"
if [ "$(id -u)" -eq 0 ] && [ -n "${SUDO_USER:-}" ] && [ "$SUDO_USER" != "root" ]; then
  sudo_home="$(getent passwd "$SUDO_USER" 2>/dev/null | cut -d: -f6 || true)"
  if [ -n "$sudo_home" ] && [ -d "$sudo_home" ]; then
    TARGET_USER="$SUDO_USER"
    TARGET_HOME="$sudo_home"
  fi
fi

installer_journal_begin core "$(cd "$TARGET_HOME" && pwd -P)/.stackkits/installer" "$TARGET_USER"
TMP=""
trap '_installer_exit_code=$?; installer_interrupted "$_installer_exit_code"; [ -z "$TMP" ] || rm -rf "$TMP"' EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM
# An interrupted core install retries the same release, rather than silently
# mixing a new Latest with the partially installed payload.
if [ "$INSTALLER_PREVIOUS_STATE" != complete ] && [ "${STACKKIT_RELEASE_VERSION+x}" != x ]; then
  _resume_release=$(installer_journal_read release)
  if [ -n "$_resume_release" ]; then STACKKIT_RELEASE_VERSION="$_resume_release"; export STACKKIT_RELEASE_VERSION; fi
fi
if [ "$INSTALLER_PREVIOUS_STATE" != complete ]; then
  [ "$#" -ne 0 ] || KIT_NAME=$(installer_journal_read kit)
  if [ "${STACKKIT_CLI_ONLY+x}" != x ]; then
    STACKKIT_CLI_ONLY=$(installer_journal_read cli_only)
    export STACKKIT_CLI_ONLY
  fi
fi
installer_journal_write kit "$KIT_NAME"
installer_journal_write cli_only "${STACKKIT_CLI_ONLY:-}"
installer_phase resolve-release

GITHUB_AUTH_HEADER=""
if [ -n "${STACKKIT_GITHUB_TOKEN:-}" ]; then
  GITHUB_AUTH_HEADER="Authorization: Bearer ${STACKKIT_GITHUB_TOKEN}"
fi

# The GitHub token goes to GitHub only, never to the download mirror.
curl_release() {
  if [ -n "$GITHUB_AUTH_HEADER" ]; then
    for _curl_arg in "$@"; do
      case "$_curl_arg" in
        https://api.github.com/*|https://github.com/*)
          curl -H "$GITHUB_AUTH_HEADER" "$@"
          return
          ;;
      esac
    done
  fi
  curl "$@"
}

# release_lookup <url> <fallback-url> prints release JSON from the mirror index,
# or from GitHub when the mirror is unreachable or names no release.
release_lookup() {
  if [ "$1" != "$2" ]; then
    _release_body=$(curl_release -fsSL "$1" 2>/dev/null || true)
    case "$_release_body" in
      *'"tag_name"'*) printf '%s' "$_release_body"; return 0 ;;
    esac
    echo "  -> $1 names no release; reading $2" >&2
  fi
  curl_release -sSL "$2" 2>/dev/null || true
}

extract_release_version() {
  printf '%s' "$1" | grep '"tag_name"' | head -1 | sed -E 's/.*"v?([^"]+)".*/\1/'
}

resolve_release_from_redirect() {
  effective_url="$(curl -fsSL -I -o /dev/null -w '%{url_effective}' "$RELEASES_PAGE_URL" 2>/dev/null || true)"
  printf '%s' "$effective_url" | sed -nE 's#.*/releases/tag/v?([^/?#]+).*#\1#p' | head -1
}

download_release_asset() {
  src="$1"
  dst="$2"
  case "$src" in
    https://github.com/*)
      if [ -n "$GITHUB_AUTH_HEADER" ]; then
        if curl_release -fsSL "$src" -o "$dst"; then
          return 0
        fi
        echo "  -> Authenticated download failed; retrying without GitHub token"
      fi
      ;;
  esac
  curl -fsSL "$src" -o "$dst"
}

# --- Detect platform ----------------------------------------------------------

if [ "${STACKKIT_SKIP_RELEASE_INSTALL:-}" != "1" ]; then
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
  x86_64|amd64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) echo "Error: unsupported architecture: $ARCH" >&2; exit 1 ;;
esac
case "$OS" in
  linux|darwin) ;;
  *) echo "Error: unsupported OS: $OS" >&2; exit 1 ;;
esac

# --- Resolve latest release ---------------------------------------------------

echo "Resolving the recommended stackkit release..."
RELEASE_JSON=""
RECOMMENDED=""
RECOMMENDATION_STATE=""
if [ -z "${STACKKIT_RELEASE_VERSION:-}" ] && [ -n "$RECOMMENDED_URL" ]; then
  # Only an exact stable tag counts; an explicit null says no release is
  # recommended, and anything else (no answer, an error page) is unavailable.
  _recommended_doc=$(curl -fsSL --connect-timeout 5 --max-time 10 "$RECOMMENDED_URL" 2>/dev/null || true)
  RECOMMENDED=$(printf '%s\n' "$_recommended_doc" \
    | sed -nE 's/^[[:space:]]*"recommended_tag":[[:space:]]*"v([0-9]+\.[0-9]+\.[0-9]+)",?[[:space:]]*$/\1/p' | head -1)
  if [ -n "$RECOMMENDED" ]; then
    RECOMMENDATION_STATE=recommended
  elif printf '%s\n' "$_recommended_doc" | grep -Eq '^[[:space:]]*"recommended_tag":[[:space:]]*null,?[[:space:]]*$'; then
    RECOMMENDATION_STATE=none
  else
    RECOMMENDATION_STATE=unavailable
  fi
fi
# version_newer A B: A is a later x.y.z than B.
version_newer() {
  printf '%s\n%s\n' "$1" "$2" | awk -F. 'NR == 1 { a = $1; b = $2; c = $3; next }
    { exit !(a > $1 || (a == $1 && (b > $2 || (b == $2 && c > $3)))) }'
}
# The installer never moves a host backwards: a newer stackkit already in
# INSTALL_DIR stays unless STACKKIT_RELEASE_VERSION names the release.
INSTALLED_VERSION=""
if [ -n "$RECOMMENDED" ] && [ -x "$INSTALL_DIR/stackkit" ]; then
  INSTALLED_VERSION=$("$INSTALL_DIR/stackkit" version 2>/dev/null \
    | sed -nE '1s/^stackkit version v?([0-9]+\.[0-9]+\.[0-9]+)[[:space:]]*$/\1/p' || true)
fi
# The declared test lanes run Linux amd64 guests only.
LANE_SCOPE="Linux amd64 guests"
if [ "$OS/$ARCH" != linux/amd64 ]; then
  LANE_SCOPE="Linux amd64 guests; no lane ran this ${OS}/${ARCH} build"
fi
if [ -n "${STACKKIT_RELEASE_VERSION:-}" ]; then
  LATEST=$(printf '%s' "$STACKKIT_RELEASE_VERSION" | sed -E 's/^v//')
  echo "  -> pinned to v${LATEST} via STACKKIT_RELEASE_VERSION (existence verified at download)"
elif [ -n "$INSTALLED_VERSION" ] && version_newer "$INSTALLED_VERSION" "$RECOMMENDED"; then
  LATEST=$INSTALLED_VERSION
  echo "  -> keeping the installed v${LATEST}: it is newer than the recommended v${RECOMMENDED} (STACKKIT_RELEASE_VERSION=v${RECOMMENDED} installs that one)"
elif [ -n "$RECOMMENDED" ]; then
  LATEST=$RECOMMENDED
  echo "  -> recommended release v${LATEST}: every declared test lane passed on it (${LANE_SCOPE}; https://stackkit.cc/compatibility)"
else
  case "$RECOMMENDATION_STATE" in
    none) echo "  -> No release is recommended yet: the newest release is installed, and not every declared test lane has passed on it (https://stackkit.cc/compatibility)." ;;
    unavailable) echo "  -> The recommended release could not be read from ${RECOMMENDED_URL}; the newest release is installed, which may not be the recommended one (https://stackkit.cc/compatibility)." ;;
  esac
  RELEASE_JSON=$(release_lookup "$RELEASE_API_URL" "$RELEASE_API_FALLBACK_URL")
  LATEST=$(extract_release_version "$RELEASE_JSON")
  if [ -z "$LATEST" ] && [ -n "$GITHUB_AUTH_HEADER" ]; then
    echo "  -> Authenticated latest-release lookup did not return a tag; retrying public API"
    RELEASE_JSON=$(curl -sSL "$RELEASE_API_FALLBACK_URL" 2>/dev/null || true)
    LATEST=$(extract_release_version "$RELEASE_JSON")
  fi
  if [ -z "$LATEST" ]; then
    echo "  -> Latest-release lookup did not return a tag; checking the GitHub release redirect"
    LATEST=$(resolve_release_from_redirect)
  fi
fi
if [ -z "$LATEST" ]; then
  echo "Error: could not determine latest version." >&2
  # Diagnostics (error path only): probe the API status so the failure names a
  # cause instead of swallowing it (bead kombify-StackKits-0lb). Common cases:
  # 403 = unauthenticated rate limit (set STACKKIT_GITHUB_TOKEN); 404 =
  # releases/latest has no non-prerelease (repo may carry only prereleases —
  # pin STACKKIT_RELEASE_VERSION=<tag>).
  if [ "$RELEASE_API_URL" != "$RELEASE_API_FALLBACK_URL" ]; then
    DIAG_STATUS=$(curl -sSL -o /dev/null -w '%{http_code}' "$RELEASE_API_URL" 2>/dev/null || echo "000")
    echo "  diagnostics: download mirror $RELEASE_API_URL -> HTTP ${DIAG_STATUS}" >&2
  fi
  DIAG_STATUS=$(curl_release -sSL -o /dev/null -w '%{http_code}' "$RELEASE_API_FALLBACK_URL" 2>/dev/null || echo "000")
  echo "  diagnostics: GitHub API $RELEASE_API_FALLBACK_URL -> HTTP ${DIAG_STATUS}" >&2
  case "$DIAG_STATUS" in
    403) echo "  hint: HTTP 403 usually means an unauthenticated rate limit — set STACKKIT_GITHUB_TOKEN and retry." >&2 ;;
    404) echo "  hint: HTTP 404 means no non-prerelease 'latest' exists — pin STACKKIT_RELEASE_VERSION=<tag> (see releases page)." >&2 ;;
    000) echo "  hint: HTTP 000 means the request did not complete — check network/DNS/proxy reachability to api.github.com." >&2 ;;
  esac
  echo "Check https://github.com/$REPO/releases" >&2
  exit 1
fi
echo "  -> v${LATEST} (${OS}/${ARCH})"

# --- Select archive -----------------------------------------------------------
# Kit bundles include the binary plus the public StackKit definitions.

case "$KIT_NAME" in
  basement-kit)
    ARCHIVE="stackkits-basement-kit_${LATEST}_${OS}_${ARCH}.tar.gz"
    INSTALL_KITS="basement-kit"
    ;;
  cloud-kit)
    ARCHIVE="stackkits-cloud-kit_${LATEST}_${OS}_${ARCH}.tar.gz"
    INSTALL_KITS="cloud-kit"
    ;;
  modern-homelab)
    ARCHIVE="stackkits-modern-homelab_${LATEST}_${OS}_${ARCH}.tar.gz"
    INSTALL_KITS="modern-homelab"
    ;;
  base-kit)
    echo "Error: 'base-kit' is retired. Use basement-kit." >&2
    exit 1
    ;;
  all)
    ARCHIVE="stackkits_${LATEST}_${OS}_${ARCH}.tar.gz"
    INSTALL_KITS="basement-kit cloud-kit modern-homelab"
    ;;
  "")
    ARCHIVE="stackkits_${LATEST}_${OS}_${ARCH}.tar.gz"
    INSTALL_KITS="basement-kit cloud-kit modern-homelab"
    ;;
  *)
    echo "Error: unknown kit '${KIT_NAME}'. Available: basement-kit, cloud-kit, modern-homelab, all" >&2
    exit 1
    ;;
esac

# --- Download & install binary ------------------------------------------------

DOWNLOAD_BASE_OVERRIDE="${STACKKIT_RELEASE_DOWNLOAD_BASE_URL:-}"
GITHUB_DOWNLOAD_BASE="https://github.com/$REPO/releases/download/v${LATEST}"
if [ -n "$DOWNLOAD_BASE_OVERRIDE" ]; then
  DOWNLOAD_BASE=$DOWNLOAD_BASE_OVERRIDE
elif [ -n "$RELEASE_MIRROR_URL" ]; then
  DOWNLOAD_BASE="$RELEASE_MIRROR_URL/v${LATEST}"
else
  DOWNLOAD_BASE=$GITHUB_DOWNLOAD_BASE
fi
URL="${DOWNLOAD_BASE%/}/${ARCHIVE}"
installer_journal_write release "$LATEST"
installer_phase download
TMP=$(mktemp -d)

release_asset_api_url() {
  asset_name="$1"
  printf '%s\n' "$RELEASE_JSON" | awk -v target="$asset_name" '
    /"url": "https:\/\/api.github.com\/repos\/.*\/releases\/assets\/[0-9]+"/ {
      api_url=$0
      sub(/.*"url": "/, "", api_url)
      sub(/".*/, "", api_url)
      next
    }
    /"name":/ {
      name=$0
      sub(/.*"name": "/, "", name)
      sub(/".*/, "", name)
      if (name == target && api_url != "") {
        print api_url
        exit
      }
    }
    /^[[:space:]]*}/ {
      api_url=""
    }
  '
}

ASSET_API_URL=""
if [ -n "$GITHUB_AUTH_HEADER" ] && [ -z "$DOWNLOAD_BASE_OVERRIDE" ] && [ "${STACKKIT_USE_RELEASE_ASSET_API:-0}" = "1" ]; then
  if [ -z "$RELEASE_JSON" ]; then
    RELEASE_TAG_API_URL="${STACKKIT_RELEASE_TAG_API_URL:-https://api.github.com/repos/$REPO/releases/tags/v${LATEST}}"
    RELEASE_JSON=$(curl_release -sSL "$RELEASE_TAG_API_URL")
  fi
  ASSET_API_URL=$(release_asset_api_url "$ARCHIVE")
  if [ -z "$ASSET_API_URL" ]; then
    echo "Error: release v${LATEST} does not contain asset ${ARCHIVE}." >&2
    echo "Check https://github.com/$REPO/releases/tag/v${LATEST}" >&2
    exit 1
  fi
fi

if [ -n "$ASSET_API_URL" ]; then
  echo "Downloading ${URL} via GitHub release asset API..."
  curl_release -H "Accept: application/octet-stream" -fsSL "$ASSET_API_URL" -o "$TMP/$ARCHIVE"
else
  echo "Downloading ${URL}..."
  if ! download_release_asset "$URL" "$TMP/$ARCHIVE"; then
    # The mirror keeps recent releases; an older pinned release (or one still
    # being mirrored) comes from GitHub Releases, never from a Render origin.
    if [ -n "$DOWNLOAD_BASE_OVERRIDE" ] || [ "$DOWNLOAD_BASE" = "$GITHUB_DOWNLOAD_BASE" ]; then
      exit 1
    fi
    DOWNLOAD_BASE=$GITHUB_DOWNLOAD_BASE
    URL="${DOWNLOAD_BASE}/${ARCHIVE}"
    echo "  -> v${LATEST} is not on the download mirror; downloading ${URL}..."
    download_release_asset "$URL" "$TMP/$ARCHIVE"
  fi
fi

# --- Verify the archive against the release checksums ------------------------
# Every release publishes checksums.txt next to its archives. The install fails
# closed when the checksum file is missing or does not match; set
# STACKKIT_SKIP_CHECKSUM_VERIFY=1 only after verifying the archive yourself.

CHECKSUMS_FILE="checksums.txt"

sha256_of_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  elif command -v openssl >/dev/null 2>&1; then
    openssl dgst -sha256 "$1" | awk '{print $NF}'
  else
    return 1
  fi
}

if [ "${STACKKIT_SKIP_CHECKSUM_VERIFY:-0}" != "1" ]; then
  if [ -n "$ASSET_API_URL" ]; then
    CHECKSUMS_API_URL=$(release_asset_api_url "$CHECKSUMS_FILE")
    if [ -n "$CHECKSUMS_API_URL" ]; then
      curl_release -H "Accept: application/octet-stream" -fsSL "$CHECKSUMS_API_URL" -o "$TMP/$CHECKSUMS_FILE" || true
    fi
  else
    download_release_asset "${DOWNLOAD_BASE%/}/${CHECKSUMS_FILE}" "$TMP/$CHECKSUMS_FILE" ||
      # A release pruned from the mirror after its archive download.
      { [ -z "$DOWNLOAD_BASE_OVERRIDE" ] && [ "$DOWNLOAD_BASE" != "$GITHUB_DOWNLOAD_BASE" ] &&
        download_release_asset "${GITHUB_DOWNLOAD_BASE}/${CHECKSUMS_FILE}" "$TMP/$CHECKSUMS_FILE"; } || true
  fi
  if [ ! -s "$TMP/$CHECKSUMS_FILE" ]; then
    echo "Error: could not download ${CHECKSUMS_FILE} for release v${LATEST}; refusing to install an unverified archive." >&2
    exit 1
  fi
  EXPECTED_SHA256=$(awk -v name="$ARCHIVE" '$2 == name { print $1; exit }' "$TMP/$CHECKSUMS_FILE")
  if [ -z "$EXPECTED_SHA256" ]; then
    echo "Error: ${CHECKSUMS_FILE} of release v${LATEST} has no entry for ${ARCHIVE}." >&2
    exit 1
  fi
  if ! ACTUAL_SHA256=$(sha256_of_file "$TMP/$ARCHIVE"); then
    echo "Error: no sha256sum, shasum, or openssl available to verify ${ARCHIVE}." >&2
    exit 1
  fi
  if [ "$ACTUAL_SHA256" != "$EXPECTED_SHA256" ]; then
    echo "Error: checksum mismatch for ${ARCHIVE}." >&2
    echo "  expected: ${EXPECTED_SHA256}" >&2
    echo "  actual:   ${ACTUAL_SHA256}" >&2
    exit 1
  fi
  echo "Verified ${ARCHIVE} against ${CHECKSUMS_FILE} (sha256 ${EXPECTED_SHA256})."
fi

installer_phase verify-archive
tar xzf "$TMP/$ARCHIVE" -C "$TMP"

if [ ! -f "$TMP/tofu" ]; then
  echo "Error: release archive is missing packaged OpenTofu binary." >&2
  echo "This StackKit release is invalid; OpenTofu must ship with the package." >&2
  exit 1
fi

installer_phase install-toolchain
if [ "$(id -u)" -eq 0 ]; then
  install -m 755 "$TMP/stackkit" "$INSTALL_DIR/stackkit"
  install -m 755 "$TMP/tofu" "$INSTALL_DIR/tofu"
  if [ -f "$TMP/terramate" ]; then
    install -m 755 "$TMP/terramate" "$INSTALL_DIR/terramate"
  fi
  if [ -f "$TMP/stackkit-server" ]; then
    install -m 755 "$TMP/stackkit-server" "$INSTALL_DIR/stackkit-server"
  fi
  if [ -f "$TMP/stackkit-mcp" ]; then
    install -m 755 "$TMP/stackkit-mcp" "$INSTALL_DIR/stackkit-mcp"
  fi
  if [ -f "$TMP/stackkit-content-bridge" ]; then
    install -m 755 "$TMP/stackkit-content-bridge" "$INSTALL_DIR/stackkit-content-bridge"
  fi
else
  echo "  -> Need sudo to install to $INSTALL_DIR"
  sudo install -m 755 "$TMP/stackkit" "$INSTALL_DIR/stackkit"
  sudo install -m 755 "$TMP/tofu" "$INSTALL_DIR/tofu"
  if [ -f "$TMP/terramate" ]; then
    sudo install -m 755 "$TMP/terramate" "$INSTALL_DIR/terramate"
  fi
  if [ -f "$TMP/stackkit-server" ]; then
    sudo install -m 755 "$TMP/stackkit-server" "$INSTALL_DIR/stackkit-server"
  fi
  if [ -f "$TMP/stackkit-mcp" ]; then
    sudo install -m 755 "$TMP/stackkit-mcp" "$INSTALL_DIR/stackkit-mcp"
  fi
  if [ -f "$TMP/stackkit-content-bridge" ]; then
    sudo install -m 755 "$TMP/stackkit-content-bridge" "$INSTALL_DIR/stackkit-content-bridge"
  fi
fi

# The OpenTofu executor installs providers only from the packaged offline
# mirror; the CLI resolves it at ../lib/stackkit/providers beside its binary.
if [ -d "$TMP/providers" ]; then
  as_root=""
  [ "$(id -u)" -eq 0 ] || as_root="sudo"
  $as_root mkdir -p "$INSTALL_DIR/../lib/stackkit"
  PROVIDERS_DIR="$(cd "$INSTALL_DIR/../lib/stackkit" && pwd)/providers"
  $as_root rm -rf "$PROVIDERS_DIR"
  $as_root cp -R "$TMP/providers" "$PROVIDERS_DIR"
  $as_root chmod -R a+rX,go-w "$PROVIDERS_DIR"
  echo "  -> Installed the OpenTofu provider mirror to $PROVIDERS_DIR"
fi

# The Coolify relocation engine (`stackkit relocate`, Linux only) lives beside
# the provider mirror; the CLI resolves ../lib/stackkit/coolify-mirror and
# accepts only the build pinned by this release.
if [ -f "$TMP/coolify-mirror" ]; then
  as_root=""
  [ "$(id -u)" -eq 0 ] || as_root="sudo"
  $as_root mkdir -p "$INSTALL_DIR/../lib/stackkit"
  ENGINE_PATH="$(cd "$INSTALL_DIR/../lib/stackkit" && pwd)/coolify-mirror"
  $as_root install -m 755 "$TMP/coolify-mirror" "$ENGINE_PATH"
  echo "  -> Installed the Coolify relocation engine to $ENGINE_PATH"
fi

# The offline failure resolution snapshot (`stackkit failures lookup`); the CLI
# resolves ../lib/stackkit/failure-resolutions.sqlite beside its binary.
if [ -f "$TMP/failure-resolutions.sqlite" ]; then
  as_root=""
  [ "$(id -u)" -eq 0 ] || as_root="sudo"
  $as_root mkdir -p "$INSTALL_DIR/../lib/stackkit"
  SNAPSHOT_PATH="$(cd "$INSTALL_DIR/../lib/stackkit" && pwd)/failure-resolutions.sqlite"
  $as_root install -m 644 "$TMP/failure-resolutions.sqlite" "$SNAPSHOT_PATH"
  echo "  -> Installed the failure resolution snapshot to $SNAPSHOT_PATH"
fi

# --- Optional short `sk` alias ------------------------------------------------
# Create a convenience `sk -> stackkit` symlink, but never clobber an existing
# `sk` command: `skim` (the fuzzy finder) and other tools also ship an `sk`
# binary. Only our own prior symlink is refreshed. Opt out with
# STACKKIT_SKIP_SK_SYMLINK=1.
maybe_link_sk() {
  [ "${STACKKIT_SKIP_SK_SYMLINK:-0}" = "1" ] && return 0
  sk_target="$INSTALL_DIR/sk"
  sk_existing="$(command -v sk 2>/dev/null || true)"
  # Skip when some other `sk` is on PATH, or when our target path exists as a
  # real file rather than our own symlink (do not overwrite a real binary).
  if [ -n "$sk_existing" ] && { [ "$sk_existing" != "$sk_target" ] || [ ! -L "$sk_target" ]; }; then
    echo "  -> 'sk' is already provided by $sk_existing; skipping the short alias (add 'alias sk=stackkit' if you prefer ours)"
    return 0
  fi
  if [ "$(id -u)" -eq 0 ]; then
    ln -sf "$INSTALL_DIR/stackkit" "$sk_target" 2>/dev/null && echo "  -> Linked short alias: sk -> stackkit"
  else
    sudo ln -sf "$INSTALL_DIR/stackkit" "$sk_target" 2>/dev/null && echo "  -> Linked short alias: sk -> stackkit"
  fi
  return 0
}
maybe_link_sk

# --- Install kit definitions --------------------------------------------------
# Kits are stored in ~/.stackkits/<kit-name>/ so the CLI can find them.
# Shared directories sit alongside them at ~/.stackkits/.

# install_kit_definitions <stackkits-dir> <owner> installs the shared schemas,
# module catalog and selected kits into one ~/.stackkits directory and hands
# it to its owner.
install_kit_definitions() {
  STACKKITS_DIR="$1"
  mkdir -p "$STACKKITS_DIR"

  # Shared CUE schemas — needed inside each kit dir for module resolution.
  if [ -d "$TMP/foundation" ]; then
    cp -r "$TMP/foundation" "$STACKKITS_DIR/"
  fi

  if [ -d "$TMP/cue.mod" ]; then
    cp -r "$TMP/cue.mod" "$STACKKITS_DIR/"
  fi

  # Shared module contracts/catalog — used for service URL generation,
  # composition, and module fragment rendering.
  if [ -d "$TMP/modules" ]; then
    cp -r "$TMP/modules" "$STACKKITS_DIR/"
  fi

  # Kit directories
  for kit in $INSTALL_KITS; do
    if [ -d "$TMP/$kit" ]; then
      rm -rf "$STACKKITS_DIR/$kit"
      cp -r "$TMP/$kit" "$STACKKITS_DIR/"
      # Also place shared schemas inside the kit dir for CUE module resolution.
      if [ -d "$TMP/foundation" ]; then
        rm -rf "$STACKKITS_DIR/$kit/foundation"
        cp -r "$TMP/foundation" "$STACKKITS_DIR/$kit/"
      fi
      echo "  -> Installed $kit to $STACKKITS_DIR/$kit"
    fi
  done

  # Under `curl | sudo sh` the copies are root-owned inside the invoking
  # user's home — hand them back so `stackkit init` works without sudo.
  if [ "$(id -u)" -eq 0 ] && [ "$2" != "root" ]; then
    chown -R "$2" "$STACKKITS_DIR" 2>/dev/null || true
  fi
}

if [ -n "$INSTALL_KITS" ]; then
  installer_phase install-definitions
  install_kit_definitions "$TARGET_HOME/.stackkits" "$TARGET_USER"
  # A root-run workspace (the Cloud Kit host installer runs as root) reads
  # root's own ~/.stackkits. When sudo sent the kits to the invoking user,
  # refresh an existing root copy too instead of leaving it on the previous
  # release.
  if [ "$TARGET_HOME" != "$HOME" ] && [ -d "$HOME/.stackkits" ]; then
    install_kit_definitions "$HOME/.stackkits" "$(id -un)"
  fi
fi
fi
command -v stackkit >/dev/null 2>&1 || { printf 'stackkit is not on PATH; add %s and re-run this installer.\n' "$INSTALL_DIR" >&2; exit 1; }
installer_complete

# --- Summary ------------------------------------------------------------------

if [ "${STACKKIT_SKIP_RELEASE_INSTALL:-}" != "1" ]; then
  echo ""
  stackkit version
  echo ""
  echo "StackKit CLI installed successfully."
  if command -v stackkit-mcp >/dev/null 2>&1; then
    echo "stackkit-mcp is installed for local agent/MCP workflows."
  fi
fi

print_cli_chain() {
  kit="$1"
  [ "$kit" != all ] || kit=basement-kit
  echo "  CLI path (you drive every step; fully parameterizable/automatable):"
  echo "    mkdir my-homelab && cd my-homelab"
  case "$kit" in
    cloud-kit|modern-homelab) domain_arg=" --domain <your-domain>" ;;
    *) domain_arg="" ;;
  esac
  echo "    stackkit init $kit --catalog-defaults --non-interactive --owner-source=local --owner-email you@example.com$domain_arg"
  echo "    stackkit generate                            # resolve + render artifacts"
  if [ "$kit" = modern-homelab ]; then
    echo "    stackkit plan                               # inspect the authored topology"
    echo "  Modern multi-site apply requires Techstack. The standalone CLI runs Standard Mode only."
    return 0
  fi
  echo "    stackkit apply --auto-approve                # deploy (installs Docker on demand)"
  echo "    stackkit verify --json                       # inspect the observed result"
}

can_prompt() {
  [ -t 1 ] && [ -r /dev/tty ] && [ -w /dev/tty ]
}

# Installer notices follow the person's language: KOMBIFY_LANG, then LC_ALL,
# LC_MESSAGES, LANG, else English. The value is case-insensitive and its
# language part ends at "_", ".", "-" or "@".
installer_msg_lang() {
  _locale=$(printf '%s' "${1:-}" | tr '[:upper:]' '[:lower:]')
  case "$_locale" in
    zh|zh_cn*|zh_sg*|zh-hans*|zh_hans*) printf 'zh-Hans'; return 0 ;;
    zh_tw*|zh_hk*|zh_mo*|zh-hant*) printf 'en'; return 0 ;;
  esac
  _lang=${_locale%%[_.@-]*}
  case "$_lang" in
    en|de|es|fr|hi|ar) printf '%s' "$_lang" ;;
    *) printf 'en' ;;
  esac
}
KOMBIFY_MSG_LANG=$(installer_msg_lang "${KOMBIFY_LANG:-${LC_ALL:-${LC_MESSAGES:-${LANG:-}}}}")

# kit_placement DETECTED_ENV prints the kit this host gets: an explicit
# STACKKIT_PLACEMENT wins in both directions, otherwise the detected network
# decides (public → cloud, home or unknown → home).
kit_placement() {
  case "${STACKKIT_PLACEMENT:-}" in
    home|cloud) printf '%s' "$STACKKIT_PLACEMENT"; return 0 ;;
    "") ;;
    *) printf "Unsupported STACKKIT_PLACEMENT '%s'. Expected home or cloud.\n" "$STACKKIT_PLACEMENT" >&2; return 2 ;;
  esac
  case "${1:-}" in
    vps|cloud) printf 'cloud' ;;
    *) printf 'home' ;;
  esac
}
# t KEY prints one localized installer notice.
t() {
  case "$KOMBIFY_MSG_LANG:$1" in
    de:blueprint_notice) printf 'StackKit Blueprint wird jetzt installiert. Mit STACKKIT_CLI_ONLY=1 wird nur die CLI installiert.' ;;
    es:blueprint_notice) printf 'Instalando StackKit Blueprint ahora. Define STACKKIT_CLI_ONLY=1 para instalar solo la CLI.' ;;
    fr:blueprint_notice) printf 'Installation de StackKit Blueprint en cours. Définissez STACKKIT_CLI_ONLY=1 pour n’installer que la CLI.' ;;
    hi:blueprint_notice) printf 'StackKit Blueprint अभी इंस्टॉल हो रहा है। केवल CLI इंस्टॉल करने के लिए STACKKIT_CLI_ONLY=1 सेट करें।' ;;
    ar:blueprint_notice) printf 'جارٍ تثبيت StackKit Blueprint الآن. اضبط STACKKIT_CLI_ONLY=1 لتثبيت CLI فقط.' ;;
    zh-Hans:blueprint_notice) printf '正在安装 StackKit Blueprint。设置 STACKKIT_CLI_ONLY=1 可只安装 CLI。' ;;
    *:blueprint_notice) printf 'Installing StackKit Blueprint now. Set STACKKIT_CLI_ONLY=1 to install only the CLI.' ;;
    de:cloud_notice) printf 'Öffentlicher Server: Cloud Kit wird verwendet. Mit STACKKIT_PLACEMENT=home wird stattdessen das Basement Kit verwendet.' ;;
    es:cloud_notice) printf 'Servidor público: se continúa con Cloud Kit. Define STACKKIT_PLACEMENT=home para usar Basement Kit en su lugar.' ;;
    fr:cloud_notice) printf 'Serveur public : poursuite avec Cloud Kit. Définissez STACKKIT_PLACEMENT=home pour utiliser Basement Kit à la place.' ;;
    hi:cloud_notice) printf 'सार्वजनिक सर्वर: Cloud Kit के साथ जारी। Basement Kit के लिए STACKKIT_PLACEMENT=home सेट करें।' ;;
    ar:cloud_notice) printf 'خادم عام: المتابعة باستخدام Cloud Kit. اضبط STACKKIT_PLACEMENT=home لاستخدام Basement Kit بدلًا منه.' ;;
    zh-Hans:cloud_notice) printf '公共服务器：继续使用 Cloud Kit。设置 STACKKIT_PLACEMENT=home 可改用 Basement Kit。' ;;
    *:cloud_notice) printf 'Public server: continuing with Cloud Kit. Set STACKKIT_PLACEMENT=home to use Basement Kit instead.' ;;
    de:basement_public_notice) printf 'Basement Kit bleibt mit kombify.me für den öffentlichen Zugang. Mit STACKKIT_PLACEMENT=cloud wird stattdessen das Cloud Kit verwendet.' ;;
    es:basement_public_notice) printf 'Se mantiene Basement Kit con kombify.me para el acceso público. Define STACKKIT_PLACEMENT=cloud para usar Cloud Kit en su lugar.' ;;
    fr:basement_public_notice) printf 'Basement Kit est conservé avec kombify.me pour l’accès public. Définissez STACKKIT_PLACEMENT=cloud pour utiliser Cloud Kit à la place.' ;;
    hi:basement_public_notice) printf 'सार्वजनिक पहुँच के लिए Basement Kit और kombify.me बने रहेंगे। Cloud Kit के लिए STACKKIT_PLACEMENT=cloud सेट करें।' ;;
    ar:basement_public_notice) printf 'يبقى Basement Kit مع kombify.me للوصول العام. اضبط STACKKIT_PLACEMENT=cloud لاستخدام Cloud Kit بدلًا منه.' ;;
    zh-Hans:basement_public_notice) printf '保留 Basement Kit，并用 kombify.me 提供公开访问。设置 STACKKIT_PLACEMENT=cloud 可改用 Cloud Kit。' ;;
    *:basement_public_notice) printf 'Keeping Basement Kit with kombify.me for public access. Set STACKKIT_PLACEMENT=cloud to use Cloud Kit instead.' ;;
    de:basement_notice) printf 'Heimnetz oder VPS konnte nicht erkannt werden: Basement Kit wird verwendet. Mit STACKKIT_PLACEMENT=cloud wird stattdessen das Cloud Kit verwendet.' ;;
    es:basement_notice) printf 'No se pudo distinguir entre red doméstica y VPS: se continúa con Basement Kit. Define STACKKIT_PLACEMENT=cloud para usar Cloud Kit en su lugar.' ;;
    fr:basement_notice) printf 'Impossible de distinguer réseau domestique et VPS : poursuite avec Basement Kit. Définissez STACKKIT_PLACEMENT=cloud pour utiliser Cloud Kit à la place.' ;;
    hi:basement_notice) printf 'होम नेटवर्क या VPS पहचाना नहीं गया: Basement Kit के साथ जारी। Cloud Kit के लिए STACKKIT_PLACEMENT=cloud सेट करें।' ;;
    ar:basement_notice) printf 'تعذّر تمييز الشبكة المنزلية من VPS: المتابعة باستخدام Basement Kit. اضبط STACKKIT_PLACEMENT=cloud لاستخدام Cloud Kit بدلًا منه.' ;;
    zh-Hans:basement_notice) printf '无法区分家庭网络和 VPS：继续使用 Basement Kit。设置 STACKKIT_PLACEMENT=cloud 可改用 Cloud Kit。' ;;
    *:basement_notice) printf 'Could not tell home vs VPS: continuing with Basement Kit. Set STACKKIT_PLACEMENT=cloud to use Cloud Kit instead.' ;;
    *) printf '' ;;
  esac
}

# Kit-specific callers (base.stackkit.cc / cloud.stackkit.cc) only need the
# CLI + definitions. STACKKIT_CLI_ONLY=1 is the explicit CLI-only path.
if [ -n "$KIT_NAME" ] || [ "${STACKKIT_CLI_ONLY:-}" = "1" ]; then
  if [ -n "$INSTALL_KITS" ]; then
    echo ""
    echo "  Installed kit definitions: $INSTALL_KITS"
  fi
  echo ""
  print_cli_chain "${KIT_NAME:-basement-kit}"
  echo ""
  exit 0
fi

# Website default: confirm the CLI, then hand off to the kit installer.
if [ "${STACKKIT_SKIP_RELEASE_INSTALL:-}" = "1" ]; then
  echo ""
  echo "StackKit CLI installed successfully."
fi
echo ""
echo "  $(t blueprint_notice)"
echo ""

# Copy of stop_on_hypervisor_host from install-host-bootstrap.sh: StackKits
# install into a guest VM on a hypervisor, never onto the hypervisor host.
stop_on_hypervisor_host() {
  [ -d /etc/pve ] || command -v pveversion >/dev/null 2>&1 || return 0
  [ "${STACKKIT_ALLOW_HYPERVISOR_HOST:-}" = "1" ] && return 0
  printf '\033[1;31m==> %s\033[0m\n' "This host is a Proxmox VE hypervisor. StackKits install into a guest VM, not onto the hypervisor host." >&2
  echo "  Create an Ubuntu 26.04 LTS VM on this host (or let kombify Techstack create it) and run the installer inside that VM (Ubuntu 24.04 LTS is the other target; which release has lifecycle evidence on each system is at https://stackkit.cc/compatibility):" >&2
  echo "    curl -sSL https://install.stackkit.cc | sh" >&2
  echo "  To install on the hypervisor host anyway, rerun with STACKKIT_ALLOW_HYPERVISOR_HOST=1." >&2
  exit 1
}

# Copy of refine_home_network_detection from install-host-bootstrap.sh (this
# installer runs standalone and cannot source it). A dual-stack home server
# sees its own global IPv6 as the public IP and reads as a public server; a
# "vps" verdict is re-checked over IPv4 and exported for the kit installer.
refine_home_network_detection() {
  [ "${DETECTED_ENV:-}" = "vps" ] || return 0
  [ -z "${STACKKIT_NETWORK_ENV:-}" ] || return 0
  command -v curl >/dev/null 2>&1 || return 0
  if curl -s -o /dev/null --connect-timeout 1 --max-time 2 http://169.254.169.254/ 2>/dev/null; then
    return 0
  fi
  _public_ipv4=$(curl -4 -fsS --max-time 5 https://api.ipify.org 2>/dev/null || true)
  case "$_public_ipv4" in
    ''|*[!0-9.]*) return 0 ;;
  esac
  if command -v ip >/dev/null 2>&1; then
    _local_ipv4=$(ip -4 -o addr show 2>/dev/null | awk '{print $4}' | cut -d/ -f1)
  else
    _local_ipv4=$(hostname -I 2>/dev/null || true)
  fi
  [ -n "$_local_ipv4" ] || return 0
  if printf '%s\n' $_local_ipv4 | grep -Fxq "$_public_ipv4"; then
    return 0
  fi
  DETECTED_ENV="home"
  PUBLIC_SERVER="false"
  STACKKIT_NETWORK_ENV="home"
  export STACKKIT_NETWORK_ENV
}

stop_on_hypervisor_host
echo "  Checking whether this host is a home network or a public server..."
DETECTED_ENV="${STACKKIT_NETWORK_ENV:-}"
PUBLIC_SERVER=""
if [ -z "$DETECTED_ENV" ] && command -v stackkit >/dev/null 2>&1; then
  if stackkit host environment --help >/dev/null 2>&1; then
    _env_json=$(stackkit host environment --json 2>/dev/null || true)
    DETECTED_ENV=$(printf '%s' "$_env_json" | sed -n 's/.*"environment"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')
    PUBLIC_SERVER=$(printf '%s' "$_env_json" | sed -n 's/.*"publicServer"[[:space:]]*:[[:space:]]*\(true\|false\).*/\1/p')
  fi
fi
[ -z "$DETECTED_ENV" ] && DETECTED_ENV="unknown"
if [ "$DETECTED_ENV" = "unknown" ] && [ "$PUBLIC_SERVER" = "true" ]; then
  DETECTED_ENV="vps"
fi
refine_home_network_detection
echo "  Detected network: $DETECTED_ENV"

KIT_INSTALL_URL=""
KIT_PLACEMENT=$(kit_placement "$DETECTED_ENV") || exit 2
case "$DETECTED_ENV:$KIT_PLACEMENT" in
  home:home) echo "  Home/LAN host → Basement Kit" ;;
  vps:home|cloud:home) echo "  $(t basement_public_notice)" ;;
  vps:cloud|cloud:cloud) echo "  $(t cloud_notice)" ;;
  unknown:home) echo "  $(t basement_notice)" ;;
esac
if [ "$KIT_PLACEMENT" = "cloud" ]; then
  KIT_INSTALL_URL="${STACKKIT_CLOUD_INSTALL_URL:-https://cloud.stackkit.cc}"
else
  KIT_INSTALL_URL="${STACKKIT_BASE_INSTALL_URL:-https://base.stackkit.cc}"
fi
export STACKKIT_PLACEMENT="$KIT_PLACEMENT"

echo "  Continuing with the matching kit installer..."
export STACKKIT_NO_BANNER=1
installer_run_entry "$KIT_INSTALL_URL"
