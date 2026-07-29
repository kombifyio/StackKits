#!/bin/sh
# =============================================================================
# StackKits Basement Installer — native v2 standalone bootstrap.
# =============================================================================
# Usage: curl -sSL https://base.stackkit.cc | sh
#
# Steps:
#   1. Install the released CLI and Basement definitions.
#   2. Initialize local Owner custody and validate canonical StackSpec v2.
#
# Environment:
#   HOMELAB_DIR          Workspace (default: $HOME/my-homelab)
#   STACKKIT_INSTALL_URL Release installer (default: https://install.stackkit.cc)
#
# Host/provider preparation and Apply are intentionally not performed here.
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

info() { printf '\033[1;34m==> %s\033[0m\n' "$*"; }
ok()   { printf '\033[1;32m==> %s\033[0m\n' "$*"; }

HOMELAB_DIR="${HOMELAB_DIR:-$HOME/my-homelab}"
STACKKIT_INSTALL_URL="${STACKKIT_INSTALL_URL:-https://install.stackkit.cc}"

info "Step 1/2 -- Installing stackkit CLI + basement-kit"
curl -sSL "$STACKKIT_INSTALL_URL" | STACKKIT_NO_BANNER=1 sh -s -- basement-kit
ok "  stackkit $(stackkit version 2>/dev/null | head -1) installed"

info "Step 2/2 -- Initializing basement-kit"
mkdir -p "$HOMELAB_DIR"
cd "$HOMELAB_DIR"
stackkit init basement-kit --non-interactive --owner-source=local
stackkit validate
ok "  basement-kit initialized and validated in $HOMELAB_DIR"

echo ""
ok "Native Architecture v2 intent and local Owner custody are ready."
echo ""
echo "  Project directory: $HOMELAB_DIR"
echo "  Next steps:"
echo "    1. Review stack-spec.yaml and local Owner projection"
echo "    2. Run: stackkit generate"
echo "    3. Review: stackkit plan"
echo "    4. Apply only when the ResolvedPlan reports readiness"
echo ""
