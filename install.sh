#!/bin/sh
# =============================================================================
# StackKits CLI installer — shared core used by all stackkit installers.
# =============================================================================
# Usage (direct):
#   curl -sSL stackkit.cc/install | sh                   # CLI only
#   curl -sSL stackkit.cc/install | sh -s -- base-kit    # CLI + base-kit
#   curl -sSL stackkit.cc/install | sh -s -- modern-homelab
#   curl -sSL stackkit.cc/install | sh -s -- ha-kit
#   curl -sSL stackkit.cc/install | sh -s -- all         # CLI + all kits
#
# Called by the short website entrypoints (`/base`, `/modern`, `/ha`) to
# provide the shared install + kit-download step before the kit-specific flow.
# =============================================================================
set -eu

# KIT_NAME controls which kit definitions are downloaded alongside the binary.
# ""         → CLI only (no kit files)
# "base-kit" → CLI + base-kit definitions
# "modern-homelab" → CLI + modern-homelab definitions
# "ha-kit"   → CLI + ha-kit definitions
# "all"      → CLI + all available kit definitions
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
RELEASES_PAGE_URL="${STACKKIT_RELEASES_PAGE_URL:-https://github.com/$REPO/releases}"
RELEASE_API_URL="${STACKKIT_RELEASE_API_URL:-https://api.github.com/repos/$REPO/releases/latest}"
RELEASE_DOWNLOAD_BASE_URL="${STACKKIT_RELEASE_DOWNLOAD_BASE_URL:-https://github.com/$REPO/releases/download}"
INSTALL_DIR="/usr/local/bin"

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
LATEST=$(curl -sSL "$RELEASE_API_URL" \
  | grep '"tag_name"' | head -1 | sed -E 's/.*"v([^"]+)".*/\1/')
if [ -z "$LATEST" ]; then
  echo "Error: could not determine latest version." >&2
  echo "Check $RELEASES_PAGE_URL" >&2
  exit 1
fi
echo "  -> v${LATEST} (${OS}/${ARCH})"

# --- Select archive -----------------------------------------------------------
# Kit bundles include the binary plus kit definitions. The default archive
# contains every current StackKit; kit-specific install modes simply choose
# which definitions are copied into ~/.stackkits/.

case "$KIT_NAME" in
  base-kit)
    ARCHIVE="stackkits-base-kit_${LATEST}_${OS}_${ARCH}.tar.gz"
    INSTALL_KITS="base-kit"
    ;;
  modern-homelab)
    ARCHIVE="stackkits_${LATEST}_${OS}_${ARCH}.tar.gz"
    INSTALL_KITS="modern-homelab"
    ;;
  ha-kit)
    ARCHIVE="stackkits_${LATEST}_${OS}_${ARCH}.tar.gz"
    INSTALL_KITS="ha-kit"
    ;;
  all)
    ARCHIVE="stackkits_${LATEST}_${OS}_${ARCH}.tar.gz"
    INSTALL_KITS="base-kit ha-kit modern-homelab"
    ;;
  "")
    ARCHIVE="stackkits_${LATEST}_${OS}_${ARCH}.tar.gz"
    INSTALL_KITS=""
    ;;
  *)
    echo "Error: unknown kit '${KIT_NAME}'. Available: base-kit, modern-homelab, ha-kit, all" >&2
    exit 1
    ;;
esac

# --- Download & install binary ------------------------------------------------

URL="${RELEASE_DOWNLOAD_BASE_URL}/v${LATEST}/${ARCHIVE}"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

echo "Downloading ${URL}..."
curl -sSL "$URL" -o "$TMP/$ARCHIVE"
tar xzf "$TMP/$ARCHIVE" -C "$TMP"

if [ "$(id -u)" -eq 0 ]; then
  install -m 755 "$TMP/stackkit" "$INSTALL_DIR/stackkit"
else
  echo "  -> Need sudo to install to $INSTALL_DIR"
  sudo install -m 755 "$TMP/stackkit" "$INSTALL_DIR/stackkit"
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

if [ -n "$INSTALL_KITS" ]; then
  echo ""
  echo "  Installed kit definitions: $INSTALL_KITS"
  echo ""
  echo "  Get started manually:"
  echo "    mkdir my-homelab && cd my-homelab"
  if [ "$KIT_NAME" = "all" ]; then
    echo "    stackkit init               # interactive kit selection"
  else
    echo "    stackkit init $KIT_NAME     # continue with this StackKit"
  fi
  echo "    stackkit apply              # deploy with confirmation"
  echo ""
  echo "  Shortcut install entrypoints:"
  echo "    curl -sSL stackkit.cc/install | sh"
  echo "    curl -sSL stackkit.cc/base | sh"
  echo "    curl -sSL stackkit.cc/modern | sh"
  echo "    curl -sSL stackkit.cc/ha | sh"
else
  echo ""
  echo "  Get started:"
  echo "    mkdir my-homelab && cd my-homelab"
  echo "    stackkit init              # interactive kit selection"
  echo "    stackkit apply"
fi
echo ""
