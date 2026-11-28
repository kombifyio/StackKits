#!/bin/sh
# Minimal signed-binary fetcher (<80 lines) handing off to Go `stackkit install`
# (Blueprint v5.1 §6.2, ADR-0044 — zero local docker build).
# Usage:
#   curl -sSL https://install.stackkit.cc | sh
#   curl -sSL https://install.stackkit.cc | sh -s -- --kit basement-kit
set -eu

REPO="${STACKKIT_RELEASE_REPO:-kombifyio/stackKits}"
MIRROR="${STACKKIT_RELEASE_MIRROR_URL:-https://assets.kombify.io/stackkits/releases}"
INSTALL_DIR="${STACKKIT_INSTALL_DIR:-/usr/local/bin}"
VERSION="${STACKKIT_RELEASE_VERSION:-}"

die() { printf 'stackkit install error: %s\n' "$*" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || die "missing required command: $1"; }

need curl
need tar

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
case "$OS" in linux|darwin) ;; *) die "unsupported OS: $OS" ;; esac
case "$ARCH" in
  x86_64|amd64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) die "unsupported architecture: $ARCH" ;;
esac

if [ -z "$VERSION" ]; then
  VERSION="$(curl -fsSL "$MIRROR/recommended.json" 2>/dev/null | sed -n 's/.*"recommended_tag"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1 || true)"
fi
if [ -z "$VERSION" ]; then
  VERSION="$(curl -fsSL "$MIRROR/latest.json" 2>/dev/null | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1 || true)"
fi
[ -n "$VERSION" ] || die "could not resolve release version from $MIRROR"

VER_NO_V="${VERSION#v}"
KIT_ARCHIVE="stackkits-basement-kit_${VER_NO_V}_${OS}_${ARCH}.tar.gz"
BASE_URL="$MIRROR/$VERSION"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

curl -fsSL "$BASE_URL/$KIT_ARCHIVE" -o "$TMP/$KIT_ARCHIVE"
curl -fsSL "$BASE_URL/SHA256SUMS" -o "$TMP/SHA256SUMS"

EXPECTED="$(awk -v f="$KIT_ARCHIVE" '$2 == f || $2 == "*"f { print $1; exit }' "$TMP/SHA256SUMS")"
[ -n "$EXPECTED" ] || die "archive $KIT_ARCHIVE missing from SHA256SUMS"
if command -v sha256sum >/dev/null 2>&1; then
  ACTUAL="$(sha256sum "$TMP/$KIT_ARCHIVE" | awk '{print $1}')"
else
  ACTUAL="$(shasum -a 256 "$TMP/$KIT_ARCHIVE" | awk '{print $1}')"
fi
[ "$ACTUAL" = "$EXPECTED" ] || die "checksum mismatch for $KIT_ARCHIVE (expected $EXPECTED, got $ACTUAL)"

tar -xzf "$TMP/$KIT_ARCHIVE" -C "$TMP"
BIN="$(find "$TMP" -type f -name stackkit | head -n 1)"
[ -n "$BIN" ] || die "stackkit binary not found in $KIT_ARCHIVE"
chmod 0755 "$BIN"

if [ -w "$INSTALL_DIR" ]; then
  cp "$BIN" "$INSTALL_DIR/stackkit"
else
  sudo install -m 0755 "$BIN" "$INSTALL_DIR/stackkit"
fi

if [ "${STACKKIT_CLI_ONLY:-0}" = "1" ]; then
  exec "$INSTALL_DIR/stackkit" version
fi
exec "$INSTALL_DIR/stackkit" install "$@"
