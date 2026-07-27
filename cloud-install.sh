#!/bin/sh
# =============================================================================
# StackKits Cloud Installer — native v2 intent bootstrap.
# =============================================================================
# Usage: DOMAIN=example.com curl -sSL https://cloud.stackkit.cc | sh
#
# Steps:
#   1. Install the released CLI and Cloud Kit definitions.
#   2. Initialize and validate canonical StackSpec v2.
#
# Environment:
#   DOMAIN               Required public domain.
#   HOMELAB_DIR          Workspace (default: $HOME/my-cloud-homelab)
#   STACKKIT_INSTALL_URL Release installer (default: https://install.stackkit.cc)
#
# Provider, host, credentials, and Apply lifecycle remain external/explicit.
# =============================================================================
set -eu

printf '\033[38;5;208m'
cat <<'BANNER'

     _             _    _    _ _
 ___| |_ __ _  ___| | _| | _(_) |_
/ __| __/ _` |/ __| |/ / |/ / | __|
\__ \ || (_| | (__|   <|   <| | |_
|___/\__\__,_|\___|_|\_\_|\_\_|\__|

BANNER
printf '\033[0m'

info()       { printf '\033[1;34m==> %s\033[0m\n' "$*"; }
ok()         { printf '\033[1;32m==> %s\033[0m\n' "$*"; }
err()        { printf '\033[1;31m==> %s\033[0m\n' "$*" >&2; }
die()        { err "$*"; exit 1; }
can_prompt() { [ -t 1 ] && [ -r /dev/tty ] && [ -w /dev/tty ]; }

HOMELAB_DIR="${HOMELAB_DIR:-$HOME/my-cloud-homelab}"
STACKKIT_INSTALL_URL="${STACKKIT_INSTALL_URL:-https://install.stackkit.cc}"

info "Step 1/2 -- Installing stackkit CLI + cloud-kit"
curl -sSL "$STACKKIT_INSTALL_URL" | STACKKIT_NO_BANNER=1 sh -s -- cloud-kit
ok "  stackkit $(stackkit version 2>/dev/null | head -1) installed"

info "Step 2/2 -- Initializing cloud-kit"
mkdir -p "$HOMELAB_DIR"
cd "$HOMELAB_DIR"

if [ -z "${DOMAIN:-}" ] && can_prompt; then
  echo ""
  printf '  Public domain for this Cloud Kit: '
  read -r DOMAIN </dev/tty
  echo ""
fi
if [ -z "${DOMAIN:-}" ]; then
  die "Cloud Kit v2 requires DOMAIN (for example: DOMAIN=example.com curl -sSL https://cloud.stackkit.cc | sh)."
fi

stackkit init cloud-kit --non-interactive --owner-source=local --domain "$DOMAIN"
stackkit validate
ok "  cloud-kit initialized and validated in $HOMELAB_DIR"

echo ""
ok "Native Architecture v2 intent is ready."
echo ""
echo "  Project directory: $HOMELAB_DIR"
echo "  Next steps:"
echo "    1. Review stack-spec.yaml"
echo "    2. Run: stackkit generate"
echo "    3. Review: stackkit plan"
echo "    4. Apply only when the ResolvedPlan reports readiness"
echo ""
