#!/bin/sh
# =============================================================================
# StackKits CLI installer — shared core used by all stackkit installers.
# =============================================================================
# Usage (direct):
#   curl -sSL https://install.stackkit.cc | sh                   # CLI + public kit catalog
#   curl -sSL https://install.stackkit.cc | sh -s -- base-kit    # CLI + base-kit
#
# Called by the short website entrypoint (`base.stackkit.cc`) to provide the
# shared install + kit-download step before the BaseKit-specific flow.
# =============================================================================
set -eu

# KIT_NAME controls which kit definitions are downloaded alongside the binary.
# ""         → CLI + the public BaseKit definitions
# "base-kit" → CLI + base-kit definitions
KIT_NAME="${1:-}"

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
RELEASE_API_URL="${STACKKIT_RELEASE_API_URL:-https://api.github.com/repos/$REPO/releases/latest}"
RELEASES_PAGE_URL="${STACKKIT_RELEASES_PAGE_URL:-https://github.com/$REPO/releases/latest}"
INSTALL_DIR="/usr/local/bin"

GITHUB_AUTH_HEADER=""
if [ -n "${STACKKIT_GITHUB_TOKEN:-}" ]; then
  GITHUB_AUTH_HEADER="Authorization: Bearer ${STACKKIT_GITHUB_TOKEN}"
fi

curl_release() {
  if [ -n "$GITHUB_AUTH_HEADER" ]; then
    curl -H "$GITHUB_AUTH_HEADER" "$@"
  else
    curl "$@"
  fi
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
  if [ -n "$GITHUB_AUTH_HEADER" ]; then
    if curl_release -fsSL "$src" -o "$dst"; then
      return 0
    fi
    echo "  -> Authenticated download failed; retrying without GitHub token"
  fi
  curl -fsSL "$src" -o "$dst"
}

# --- Detect platform ----------------------------------------------------------

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
else
  RELEASE_JSON=$(curl_release -sSL "$RELEASE_API_URL" 2>/dev/null || true)
  LATEST=$(extract_release_version "$RELEASE_JSON")
  if [ -z "$LATEST" ] && [ -n "$GITHUB_AUTH_HEADER" ]; then
    echo "  -> Authenticated latest-release lookup did not return a tag; retrying public API"
    RELEASE_JSON=$(curl -sSL "$RELEASE_API_URL" 2>/dev/null || true)
    LATEST=$(extract_release_version "$RELEASE_JSON")
  fi
  if [ -z "$LATEST" ]; then
    echo "  -> GitHub API latest-release lookup did not return a tag; checking release redirect"
    LATEST=$(resolve_release_from_redirect)
  fi
fi
if [ -z "$LATEST" ]; then
  echo "Error: could not determine latest version." >&2
  echo "Check https://github.com/$REPO/releases" >&2
  exit 1
fi
echo "  -> v${LATEST} (${OS}/${ARCH})"

# --- Select archive -----------------------------------------------------------
# Kit bundles include the binary plus public BaseKit definitions.

case "$KIT_NAME" in
  base-kit)
    ARCHIVE="stackkits-base-kit_${LATEST}_${OS}_${ARCH}.tar.gz"
    INSTALL_KITS="base-kit"
    ;;
  all)
    ARCHIVE="stackkits_${LATEST}_${OS}_${ARCH}.tar.gz"
    INSTALL_KITS="base-kit"
    ;;
  "")
    ARCHIVE="stackkits_${LATEST}_${OS}_${ARCH}.tar.gz"
    INSTALL_KITS="base-kit"
    ;;
  *)
    echo "Error: unknown kit '${KIT_NAME}'. Available: base-kit, all" >&2
    exit 1
    ;;
esac

# --- Download & install binary ------------------------------------------------

DOWNLOAD_BASE_OVERRIDE="${STACKKIT_RELEASE_DOWNLOAD_BASE_URL:-}"
DOWNLOAD_BASE="${DOWNLOAD_BASE_OVERRIDE:-https://github.com/$REPO/releases/download/v${LATEST}}"
URL="${DOWNLOAD_BASE%/}/${ARCHIVE}"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

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
  download_release_asset "$URL" "$TMP/$ARCHIVE"
fi
tar xzf "$TMP/$ARCHIVE" -C "$TMP"

if [ ! -f "$TMP/tofu" ]; then
  echo "Error: release archive is missing packaged OpenTofu binary." >&2
  echo "This StackKit release is invalid; OpenTofu must ship with the package." >&2
  exit 1
fi

if [ "$(id -u)" -eq 0 ]; then
  install -m 755 "$TMP/stackkit" "$INSTALL_DIR/stackkit"
  install -m 755 "$TMP/tofu" "$INSTALL_DIR/tofu"
  if [ -f "$TMP/stackkit-server" ]; then
    install -m 755 "$TMP/stackkit-server" "$INSTALL_DIR/stackkit-server"
  fi
  if [ -f "$TMP/stackkit-mcp" ]; then
    install -m 755 "$TMP/stackkit-mcp" "$INSTALL_DIR/stackkit-mcp"
  fi
else
  echo "  -> Need sudo to install to $INSTALL_DIR"
  sudo install -m 755 "$TMP/stackkit" "$INSTALL_DIR/stackkit"
  sudo install -m 755 "$TMP/tofu" "$INSTALL_DIR/tofu"
  if [ -f "$TMP/stackkit-server" ]; then
    sudo install -m 755 "$TMP/stackkit-server" "$INSTALL_DIR/stackkit-server"
  fi
  if [ -f "$TMP/stackkit-mcp" ]; then
    sudo install -m 755 "$TMP/stackkit-mcp" "$INSTALL_DIR/stackkit-mcp"
  fi
fi

# --- Install kit definitions --------------------------------------------------
# Kits are stored in ~/.stackkits/<kit-name>/ so the CLI can find them.
# Shared directories sit alongside them at ~/.stackkits/.

if [ -n "$INSTALL_KITS" ]; then
  STACKKITS_DIR="$HOME/.stackkits"
  mkdir -p "$STACKKITS_DIR"

  # Shared CUE schemas — needed inside each kit dir for module resolution.
  if [ -d "$TMP/base" ]; then
    cp -r "$TMP/base" "$STACKKITS_DIR/"
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
      if [ -d "$TMP/base" ]; then
        rm -rf "$STACKKITS_DIR/$kit/base"
        cp -r "$TMP/base" "$STACKKITS_DIR/$kit/"
      fi
      echo "  -> Installed $kit to $STACKKITS_DIR/$kit"
    fi
  done
fi

# --- Summary ------------------------------------------------------------------

echo ""
stackkit version
echo ""
echo "stackkit is installed."
if command -v stackkit-mcp >/dev/null 2>&1; then
  echo "stackkit-mcp is installed for local agent/MCP workflows."
fi

if [ -n "$INSTALL_KITS" ]; then
  echo ""
  echo "  Installed kit definitions: $INSTALL_KITS"
  echo ""
  echo "  Get started manually:"
  echo "    mkdir my-homelab && cd my-homelab"
  if [ "$KIT_NAME" = "base-kit" ]; then
    echo "    stackkit init $KIT_NAME     # continue with this StackKit"
  else
    echo "    stackkit init               # interactive kit selection"
  fi
  echo "    stackkit apply              # deploy with confirmation"
  echo ""
  echo "  Shortcut install entrypoints:"
  echo "    curl -sSL https://install.stackkit.cc | sh"
  echo "    curl -sSL https://base.stackkit.cc | sh"
else
  echo ""
  echo "  Get started:"
  echo "    mkdir my-homelab && cd my-homelab"
  echo "    stackkit init              # interactive kit selection"
  echo "    stackkit apply"
fi
echo ""
