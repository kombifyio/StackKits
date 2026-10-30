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
#   curl -sSL https://install.stackkit.cc | sh -s -- --channel early-access  # Early Access (unsigned prerelease)
#
# --channel stable|early-access selects the release channel (default: stable,
# or the choice saved in ~/.stackkits/channel by `stackkit channel set`). It can
# also be given as STACKKIT_CHANNEL. An unknown channel fails closed.
# early-access installs the newest Early Access build the owner switched live,
# or the newest stable release when that is newer. A withdrawn (rolled back)
# Early Access build is replaced by the channel's current build even when that
# is older. Returning to stable never downgrades a running prerelease build; it
# waits for the next stable release at least as new (STACKKIT_ALLOW_DOWNGRADE=1
# or an exact STACKKIT_RELEASE_VERSION overrides that). The internal Nightly
# (preview) is for the StackKits team and is not offered to users.
#
# Called by the short website entrypoints (`base.stackkit.cc`,
# `cloud.stackkit.cc`) to provide the shared install + kit-download step
# before the kit-specific flow. The retired `base-kit` name is rejected.
# STACKKIT_CLI_ONLY=1 keeps this script from launching a kit installer.
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

# Take --channel out of the arguments; the first remaining argument is the kit.
CHANNEL_FLAG=""
CHANNEL_FLAG_SET=""
_stackkit_argc=$#
while [ "$_stackkit_argc" -gt 0 ]; do
  _stackkit_arg=$1
  shift
  _stackkit_argc=$((_stackkit_argc - 1))
  case "$_stackkit_arg" in
    --channel)
      if [ "$_stackkit_argc" -eq 0 ]; then
        echo "Error: --channel needs a value: stable or early-access." >&2
        exit 2
      fi
      CHANNEL_FLAG=$1
      CHANNEL_FLAG_SET=1
      shift
      _stackkit_argc=$((_stackkit_argc - 1))
      ;;
    --channel=*)
      CHANNEL_FLAG=${_stackkit_arg#--channel=}
      CHANNEL_FLAG_SET=1
      ;;
    *) set -- "$@" "$_stackkit_arg" ;;
  esac
done

# KIT_NAME controls which kit definitions are downloaded alongside the binary.
# ""             → CLI + catalog, then ask to install a StackKit (default yes)
# "basement-kit" → CLI + basement-kit definitions
# "cloud-kit"    → CLI + cloud-kit definitions
# "modern-homelab" → CLI + Modern Homelab Preview definitions
KIT_NAME="${1:-}"
INSTALL_KITS=""

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
# Early Access and the internal Nightly read the release list (prereleases are
# never "latest"). An unreachable or empty mirror index falls back to GitHub.
RELEASE_API_FALLBACK_URL="${STACKKIT_RELEASE_API_URL:-https://api.github.com/repos/$REPO/releases/latest}"
PREVIEW_RELEASES_FALLBACK_URL="${STACKKIT_PREVIEW_RELEASES_API_URL:-https://api.github.com/repos/$REPO/releases?per_page=100}"
if [ -n "$RELEASE_MIRROR_URL" ]; then
  RELEASE_API_URL="${STACKKIT_RELEASE_API_URL:-$RELEASE_MIRROR_URL/latest.json}"
  PREVIEW_RELEASES_API_URL="${STACKKIT_PREVIEW_RELEASES_API_URL:-$RELEASE_MIRROR_URL/index.json}"
else
  RELEASE_API_URL=$RELEASE_API_FALLBACK_URL
  PREVIEW_RELEASES_API_URL=$PREVIEW_RELEASES_FALLBACK_URL
fi
RELEASES_PAGE_URL="${STACKKIT_RELEASES_PAGE_URL:-https://github.com/$REPO/releases/latest}"
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

# Release channel: flag, then STACKKIT_CHANNEL, then the saved choice, then
# stable. Anything but a known channel fails closed before any download.
CHANNEL_EXPLICIT=""
if [ -n "$CHANNEL_FLAG_SET" ]; then
  CHANNEL_REQUEST=$CHANNEL_FLAG
  CHANNEL_EXPLICIT=1
elif [ -n "${STACKKIT_CHANNEL:-}" ]; then
  CHANNEL_REQUEST=$STACKKIT_CHANNEL
  CHANNEL_EXPLICIT=1
elif [ -f "$TARGET_HOME/.stackkits/channel" ]; then
  CHANNEL_REQUEST=$(head -n 1 "$TARGET_HOME/.stackkits/channel" | tr -d '[:space:]')
else
  CHANNEL_REQUEST=stable
fi
if ! CHANNEL=$(installer_channel_normalize "$CHANNEL_REQUEST"); then
  echo "Error: unknown release channel '${CHANNEL_REQUEST}'. Use stable or early-access." >&2
  exit 2
fi
# Kit installers started from here keep an explicit choice.
if [ -n "$CHANNEL_EXPLICIT" ]; then
  STACKKIT_CHANNEL=$CHANNEL
  export STACKKIT_CHANNEL
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

echo "Resolving latest stackkit release..."
RELEASE_JSON=""
if [ -n "${STACKKIT_RELEASE_VERSION:-}" ]; then
  LATEST=$(printf '%s' "$STACKKIT_RELEASE_VERSION" | sed -E 's/^v//')
  echo "  -> pinned to v${LATEST} via STACKKIT_RELEASE_VERSION (existence verified at download)"
elif [ "$CHANNEL" = preview ]; then
  echo "  -> channel: preview (internal Nightly, unsigned prerelease built from main)"
  RELEASE_JSON=$(release_lookup "$PREVIEW_RELEASES_API_URL" "$PREVIEW_RELEASES_FALLBACK_URL")
  PREVIEW_TAG=$(installer_newest_preview_tag "$RELEASE_JSON")
  # The release list is not one release; asset lookups fetch the tag's own JSON.
  RELEASE_JSON=""
  if [ -z "$PREVIEW_TAG" ]; then
    echo "Error: no preview (Nightly) release found at $PREVIEW_RELEASES_API_URL." >&2
    echo "  Nothing is installed from the stable channel instead. Retry later, set STACKKIT_GITHUB_TOKEN if GitHub rate-limits you," >&2
    echo "  or install stable with --channel stable. Releases: https://github.com/$REPO/releases" >&2
    exit 1
  fi
  LATEST=${PREVIEW_TAG#v}
elif [ "$CHANNEL" = early-access ]; then
  # Early Access serves the newest build the owner switched live, or the
  # newest stable release when that is newer (a stable release supersedes the
  # Early Access build of the same version).
  echo "  -> channel: Early Access (unsigned prerelease the StackKits team switched live)"
  RELEASES_LIST_JSON=$(release_lookup "$PREVIEW_RELEASES_API_URL" "$PREVIEW_RELEASES_FALLBACK_URL")
  EARLY_ACCESS_TAGS=$(installer_channel_tags "$RELEASES_LIST_JSON" early-access)
  EARLY_ACCESS_TAG=$(printf '%s\n' "$EARLY_ACCESS_TAGS" | head -n 1)
  STABLE_JSON=$(release_lookup "$RELEASE_API_URL" "$RELEASE_API_FALLBACK_URL")
  STABLE_LATEST=$(extract_release_version "$STABLE_JSON")
  if [ -n "$EARLY_ACCESS_TAG" ] && { [ -z "$STABLE_LATEST" ] || [ "$(installer_semver_compare "${EARLY_ACCESS_TAG#v}" "$STABLE_LATEST")" = 1 ]; }; then
    LATEST=${EARLY_ACCESS_TAG#v}
  elif [ -n "$STABLE_LATEST" ]; then
    LATEST=$STABLE_LATEST
    RELEASE_JSON=$STABLE_JSON
    if [ -n "$EARLY_ACCESS_TAG" ]; then
      echo "  -> stable v${STABLE_LATEST} is newer than Early Access ${EARLY_ACCESS_TAG}; installing stable"
    else
      echo "  -> no Early Access build is live; installing stable v${STABLE_LATEST}"
    fi
  else
    LATEST=""
  fi
else
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

# Switching channel never downgrades a running prerelease build: stable (or
# Early Access) takes over with the next release at least as new as the
# running one. The one exception is a withdrawn Early Access build: the
# owner's rollback moves everyone on Early Access to the channel's current
# build, even when that is older.
if [ "$CHANNEL" != preview ] && [ -z "${STACKKIT_RELEASE_VERSION:-}" ] && [ "${STACKKIT_ALLOW_DOWNGRADE:-0}" != 1 ]; then
  _stackkit_running=$( (command -v stackkit >/dev/null 2>&1 && stackkit version 2>/dev/null) | sed -n 's/^stackkit version v\{0,1\}//p' | head -n 1)
  case "$_stackkit_running" in
    *-preview.*|*-early-access.*)
      if [ "$(installer_semver_compare "$LATEST" "$_stackkit_running")" = -1 ]; then
        _stackkit_withdrawn=""
        if [ "$CHANNEL" = early-access ]; then
          case "$_stackkit_running" in
            *-early-access.*)
              printf '%s\n' "$EARLY_ACCESS_TAGS" | grep -qx "v${_stackkit_running}" || _stackkit_withdrawn=1
              ;;
          esac
        fi
        if [ -n "$_stackkit_withdrawn" ]; then
          echo "  -> Early Access build v${_stackkit_running} was withdrawn; rolling back to v${LATEST}."
        else
          echo "  -> Staying on build v${_stackkit_running}: v${LATEST} on the ${CHANNEL} channel is older."
          echo "     The ${CHANNEL} channel takes over with its next release at least as new."
          echo "     To downgrade anyway, rerun with STACKKIT_ALLOW_DOWNGRADE=1."
          installer_store_channel "$TARGET_HOME/.stackkits" "$CHANNEL" "$TARGET_USER" || true
          installer_complete
          exit 0
        fi
      fi
      ;;
  esac
fi

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
if [ -n "$CHANNEL_EXPLICIT" ]; then
  installer_store_channel "$TARGET_HOME/.stackkits" "$CHANNEL" "$TARGET_USER" ||
    echo "Warning: could not save the release channel to $TARGET_HOME/.stackkits/channel." >&2
fi
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

# Website default: confirm the CLI, then ask before installing a StackKit.
if [ "${STACKKIT_SKIP_RELEASE_INSTALL:-}" = "1" ]; then
  echo ""
  echo "StackKit CLI installed successfully."
fi
echo ""
if can_prompt; then
  printf '  Install StackKit Blueprint now? [Y/n]: ' >/dev/tty
  read -r _stackkit_continue </dev/tty || _stackkit_continue=""
  case "$_stackkit_continue" in
    n|N|no|NO|No)
      echo ""
      print_cli_chain basement-kit
      echo ""
      exit 0
      ;;
  esac
  echo ""
else
  echo "  Installing StackKit Blueprint now."
fi

# Copy of stop_on_hypervisor_host from install-host-bootstrap.sh: StackKits
# install into a guest VM on a hypervisor, never onto the hypervisor host.
stop_on_hypervisor_host() {
  [ -d /etc/pve ] || command -v pveversion >/dev/null 2>&1 || return 0
  [ "${STACKKIT_ALLOW_HYPERVISOR_HOST:-}" = "1" ] && return 0
  printf '\033[1;31m==> %s\033[0m\n' "This host is a Proxmox VE hypervisor. StackKits install into a guest VM, not onto the hypervisor host." >&2
  echo "  Create an Ubuntu 26.04 LTS VM on this host (or let kombify Techstack create it) and run the installer inside that VM (Ubuntu 24.04 LTS remains supported):" >&2
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
case "$DETECTED_ENV" in
  home)
    echo "  Home/LAN host → Basement Kit"
    KIT_INSTALL_URL="${STACKKIT_BASE_INSTALL_URL:-https://base.stackkit.cc}"
    export STACKKIT_PLACEMENT=home
    ;;
  vps|cloud)
    if can_prompt; then
      echo ""
      echo "  This host is a public server. Cloud Kit is the product for a VPS;"
      echo "  Basement Kit is for a home network."
      printf '  Continue with Cloud Kit? [Y/n]: ' >/dev/tty
      read -r _stackkit_cloud </dev/tty || _stackkit_cloud=""
      case "$_stackkit_cloud" in
        n|N|no|NO|No)
          echo "  Keeping Basement Kit with kombify.me for public access"
          KIT_INSTALL_URL="${STACKKIT_BASE_INSTALL_URL:-https://base.stackkit.cc}"
          export STACKKIT_PLACEMENT=home
          ;;
        *)
          echo "  Public server → Cloud Kit"
          KIT_INSTALL_URL="${STACKKIT_CLOUD_INSTALL_URL:-https://cloud.stackkit.cc}"
          export STACKKIT_PLACEMENT=cloud
          ;;
      esac
      echo ""
    else
      echo "  Public server → Cloud Kit"
      KIT_INSTALL_URL="${STACKKIT_CLOUD_INSTALL_URL:-https://cloud.stackkit.cc}"
      export STACKKIT_PLACEMENT=cloud
    fi
    ;;
  *)
    if can_prompt; then
      echo ""
      echo "  Could not tell whether this host is on a home network or a public VPS."
      echo "    1) Home network — Basement Kit"
      echo "    2) Public server / VPS — Cloud Kit"
      printf '  Select [1]: ' >/dev/tty
      read -r _stackkit_place </dev/tty || _stackkit_place=""
      case "$_stackkit_place" in
        2)
          KIT_INSTALL_URL="${STACKKIT_CLOUD_INSTALL_URL:-https://cloud.stackkit.cc}"
          export STACKKIT_PLACEMENT=cloud
          ;;
        *)
          KIT_INSTALL_URL="${STACKKIT_BASE_INSTALL_URL:-https://base.stackkit.cc}"
          export STACKKIT_PLACEMENT=home
          ;;
      esac
      echo ""
    else
      echo "  Could not tell home vs VPS. Continuing with Basement Kit."
      KIT_INSTALL_URL="${STACKKIT_BASE_INSTALL_URL:-https://base.stackkit.cc}"
      export STACKKIT_PLACEMENT=home
    fi
    ;;
esac

echo "  Continuing with the matching kit installer..."
export STACKKIT_NO_BANNER=1
installer_run_entry "$KIT_INSTALL_URL"
