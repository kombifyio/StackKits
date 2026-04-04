#!/bin/sh
# =============================================================================
# StackKits CLI installer — shared core used by all stackkit installers.
# =============================================================================
# Usage (direct):
#   curl -sSL install.kombify.me | sh                    # CLI only
#   curl -sSL install.kombify.me | sh -s -- base-kit     # CLI + base-kit
#   curl -sSL install.kombify.me | sh -s -- all          # CLI + all kits
#
# Called by base.stackkit.cc with "base-kit" to provide the shared
# install+kit-download step before the full deployment orchestration.
# =============================================================================
set -eu

# KIT_NAME controls which kit definitions are downloaded alongside the binary.
# ""         → CLI only (no kit files)
# "base-kit" → CLI + base-kit definitions
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

REPO="kombifyio/stackKits"
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
LATEST=$(curl -sSL "https://api.github.com/repos/$REPO/releases/latest" \
  | grep '"tag_name"' | head -1 | sed -E 's/.*"v([^"]+)".*/\1/')
if [ -z "$LATEST" ]; then
  echo "Error: could not determine latest version." >&2
  echo "Check https://github.com/$REPO/releases" >&2
  exit 1
fi
echo "  -> v${LATEST} (${OS}/${ARCH})"

# --- Select archive -----------------------------------------------------------
# Kit bundles (stackkits-<kit>_…) include both the binary AND the kit files.
# The CLI-only archive (stackkits_…) contains just the binary.

case "$KIT_NAME" in
  base-kit)
    ARCHIVE="stackkits-base-kit_${LATEST}_${OS}_${ARCH}.tar.gz"
    ;;
  all)
    # Full bundle: binary + every available kit
    ARCHIVE="stackkits_${LATEST}_${OS}_${ARCH}.tar.gz"
    ;;
  "")
    ARCHIVE="stackkits_${LATEST}_${OS}_${ARCH}.tar.gz"
    ;;
  *)
    echo "Error: unknown kit '${KIT_NAME}'. Available: base-kit, all" >&2
    exit 1
    ;;
esac

# --- Download & install binary ------------------------------------------------

URL="https://github.com/$REPO/releases/download/v${LATEST}/${ARCHIVE}"
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
# The "base" directory contains shared CUE schemas referenced by all kits.

if [ -n "$KIT_NAME" ]; then
  STACKKITS_DIR="$HOME/.stackkits"
  mkdir -p "$STACKKITS_DIR"

  # Shared CUE schemas — needed inside each kit dir for module resolution.
  if [ -d "$TMP/base" ]; then
    cp -r "$TMP/base" "$STACKKITS_DIR/"
  fi

  # Kit directories
  for kit in base-kit ha-kit modern-homelab; do
    if [ -d "$TMP/$kit" ]; then
      cp -r "$TMP/$kit" "$STACKKITS_DIR/"
      # Also place shared schemas inside the kit dir for CUE module resolution.
      if [ -d "$TMP/base" ]; then
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

if [ -n "$KIT_NAME" ]; then
  echo ""
  echo "  Install a kit:"
  echo "    curl -sSL install.kombify.me | sh -s -- base-kit"
  echo ""
  echo "  Or get started manually:"
  echo "    mkdir my-homelab && cd my-homelab"
  echo "    stackkit init base-kit      # interactive wizard"
  echo "    stackkit apply              # deploy with confirmation"
  echo ""
  echo "  Or deploy everything in one command:"
  echo "    curl -sSL base.stackkit.cc | sh"
else
  echo ""
  echo "  Get started:"
  echo "    mkdir my-homelab && cd my-homelab"
  if [ "$KIT_NAME" = "all" ]; then
    echo "    stackkit init              # interactive kit selection"
  else
    echo "    stackkit init $KIT_NAME"
  fi
  echo "    stackkit apply"
fi
echo ""
