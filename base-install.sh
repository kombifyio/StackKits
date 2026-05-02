#!/bin/sh
# =============================================================================
# StackKits Base Installer — full base-kit deployment in one command.
# =============================================================================
# Usage: curl -sSL stackkit.cc/base | sh
#
# Steps:
#   1. Install stackkit CLI + base-kit definitions  (via stackkit.cc/install)
#   2. Prepare system: Docker + OpenTofu
#   3. Initialize base-kit (non-interactive, reads env vars)
#   4. Generate + deploy the full homelab stack
#
# Environment variables:
#   STACKKIT_ADMIN_EMAIL   Admin/login email (prompted if not set)
#   KOMBIFY_USER_EMAIL     Fallback admin email in kombify cloud context
#   DOMAIN                 Custom domain (default: auto-detect)
#   CLOUDFLARE_API_TOKEN   Required for custom domain with Let's Encrypt
#   KOMBIFY_API_KEY        Required for kombify.me subdomain registration
#   KOMBIFY_CONTEXT        Set to "cloud" to enable kombify.me domain mode
#
# Requirements: Linux or macOS, root/sudo access
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

REPO="kombifyio/stackKits"
HOMELAB_DIR="${HOMELAB_DIR:-$HOME/my-homelab}"

STACKKIT_CONTEXT_ARG=""
STACKKIT_CONTEXT_VALUE="${STACKKIT_CONTEXT:-${KOMBIFY_CONTEXT:-}}"
case "$STACKKIT_CONTEXT_VALUE" in
  local|cloud|pi)
    STACKKIT_CONTEXT_ARG="--context $STACKKIT_CONTEXT_VALUE"
    ;;
  vps)
    STACKKIT_CONTEXT_ARG="--context cloud"
    ;;
esac

# --- Helpers ------------------------------------------------------------------

info()  { printf '\033[1;34m==> %s\033[0m\n' "$*"; }
ok()    { printf '\033[1;32m==> %s\033[0m\n' "$*"; }
warn()  { printf '\033[1;33m==> %s\033[0m\n' "$*"; }
err()   { printf '\033[1;31m==> %s\033[0m\n' "$*" >&2; }
die()   { err "$*"; exit 1; }

# --- Admin email --------------------------------------------------------------

ADMIN_EMAIL="${STACKKIT_ADMIN_EMAIL:-${KOMBIFY_USER_EMAIL:-}}"
if [ -z "$ADMIN_EMAIL" ]; then
  echo ""
  printf '  Admin email (for login accounts): '
  read -r ADMIN_EMAIL </dev/tty
  echo ""
fi
if [ -z "$ADMIN_EMAIL" ]; then
  warn "No admin email provided — using 'admin' as username"
  ADMIN_EMAIL="admin"
fi

# --- Step 1: Install CLI + base-kit definitions -------------------------------
# Delegates entirely to install.sh so all binary download, kit extraction, and
# ~/.stackkits layout logic lives in exactly one place.

info "Step 1/4 -- Installing stackkit CLI + base-kit"

# STACKKIT_NO_BANNER suppresses the duplicate banner from install.sh.
STACKKIT_INSTALL_URL="${STACKKIT_INSTALL_URL:-https://stackkit.cc/install}"
STACKKIT_NO_BANNER=1 curl -sSL "$STACKKIT_INSTALL_URL" | sh -s -- base-kit

ok "  stackkit $(stackkit version 2>/dev/null | head -1) installed"

# --- Step 2: Prepare system (Docker + OpenTofu) -------------------------------

info "Step 2/4 -- Preparing system (Docker + OpenTofu)"

if [ "$(id -u)" -eq 0 ]; then
  stackkit $STACKKIT_CONTEXT_ARG prepare || die "System preparation failed."
else
  sudo stackkit $STACKKIT_CONTEXT_ARG prepare || die "System preparation failed."
fi

ok "  System ready"

# --- Step 3: Initialize base-kit ----------------------------------------------

info "Step 3/4 -- Initializing base-kit"

mkdir -p "$HOMELAB_DIR"
cd "$HOMELAB_DIR"

set -- init base-kit --non-interactive --force --admin-email "$ADMIN_EMAIL"
if [ -n "${DOMAIN:-}" ]; then
  set -- "$@" --domain "$DOMAIN"
fi
stackkit $STACKKIT_CONTEXT_ARG "$@"

ok "  base-kit initialized in $HOMELAB_DIR"

# --- Step 4: Generate + Deploy ------------------------------------------------

info "Step 4/4 -- Deploying homelab stack"

rm -rf "$HOMELAB_DIR/deploy"
stackkit $STACKKIT_CONTEXT_ARG generate --force
stackkit $STACKKIT_CONTEXT_ARG apply --auto-approve

# --- Done: print access summary -----------------------------------------------

# Read domain from spec
DOMAIN="stack.local"
if [ -f "$HOMELAB_DIR/stack-spec.yaml" ]; then
  _d=$(grep '^domain:' "$HOMELAB_DIR/stack-spec.yaml" | head -1 | awk '{print $2}' || true)
  if [ -n "$_d" ]; then DOMAIN="$_d"; fi
fi

# Read subdomain prefix from tfvars (kombify.me mode)
SUBDOMAIN_PREFIX=""
if [ -f "$HOMELAB_DIR/deploy/terraform.tfvars.json" ]; then
  SUBDOMAIN_PREFIX=$(grep '"subdomain_prefix"' "$HOMELAB_DIR/deploy/terraform.tfvars.json" | head -1 | sed -E 's/.*: *"([^"]+)".*/\1/' || true)
fi

SERVER_IP=$(hostname -I 2>/dev/null | awk '{print $1}' || echo "YOUR_SERVER_IP")

ADMIN_PASSWORD=""
if [ -f "$HOMELAB_DIR/deploy/terraform.tfvars.json" ]; then
  ADMIN_PASSWORD=$(grep '"admin_password_plaintext"' "$HOMELAB_DIR/deploy/terraform.tfvars.json" | head -1 | sed -E 's/.*: *"([^"]+)".*/\1/' || true)
fi

# Detect network environment
NETWORK_ENV="unknown"
PUBLIC_IP=$(curl -sSL --max-time 5 https://ifconfig.me/ip 2>/dev/null || true)
if [ -n "$PUBLIC_IP" ]; then
  if ip addr 2>/dev/null | grep -qF "$PUBLIC_IP"; then
    NETWORK_ENV="vps"
  else
    NETWORK_ENV="home"
  fi
fi
if [ "${KOMBIFY_CONTEXT:-}" = "cloud" ] || { [ -f /etc/kombify/context ] && [ "$(cat /etc/kombify/context 2>/dev/null)" = "cloud" ]; }; then
  NETWORK_ENV="cloud"
fi

# Warn about local domain on a public server
if [ "$NETWORK_ENV" = "vps" ] || [ "$NETWORK_ENV" = "cloud" ]; then
  case "$DOMAIN" in
    *.kombify|*.local|*.lab|*.lan|*.home|*.internal|*.test|stack.local|home.lab|homelab)
      echo ""
      warn "WARNING: Local domain '$DOMAIN' is not reachable on a public server!"
      echo ""
      echo "  Your server has a public IP ($PUBLIC_IP) but services are configured with"
      echo "  a local domain that only works on home networks with Kombify Point/local DNS."
      echo ""
      echo "  To fix: edit $HOMELAB_DIR/stack-spec.yaml and set:"
      echo "    domain: kombify.me     (free public subdomain via kombify.me)"
      echo "    domain: yourdomain.com (your own domain with DNS configured)"
      echo ""
      echo "  Then re-deploy:"
      echo "    cd $HOMELAB_DIR && stackkit generate --force && stackkit apply --auto-approve"
      echo ""
      ;;
  esac
fi

# Build service URLs
if [ -n "$SUBDOMAIN_PREFIX" ] && [ "$DOMAIN" = "kombify.me" ]; then
  PROTO="https"
  DASH_URL="${PROTO}://${SUBDOMAIN_PREFIX}-dash.${DOMAIN}"
  TRAEFIK_URL="${PROTO}://${SUBDOMAIN_PREFIX}-traefik.${DOMAIN}"
  DOKPLOY_URL="${PROTO}://${SUBDOMAIN_PREFIX}-dokploy.${DOMAIN}"
  KUMA_URL="${PROTO}://${SUBDOMAIN_PREFIX}-kuma.${DOMAIN}"
  AUTH_URL="${PROTO}://${SUBDOMAIN_PREFIX}-tinyauth.${DOMAIN}"
  ID_URL="${PROTO}://${SUBDOMAIN_PREFIX}-id.${DOMAIN}"
  URL_PATTERN="<service> at ${SUBDOMAIN_PREFIX}-<service>.${DOMAIN}"
else
  PROTO="https"
  DASH_URL="${PROTO}://base.${DOMAIN}"
  TRAEFIK_URL="${PROTO}://traefik.${DOMAIN}"
  DOKPLOY_URL="${PROTO}://dokploy.${DOMAIN}"
  KUMA_URL="${PROTO}://kuma.${DOMAIN}"
  AUTH_URL="${PROTO}://auth.${DOMAIN}"
  ID_URL="${PROTO}://id.${DOMAIN}"
  URL_PATTERN="<service>.${DOMAIN}"
fi

echo ""
ok "Your homelab is running!"
echo ""
printf '\033[38;5;208m'
echo "  Dashboard:  ${DASH_URL}"
printf '\033[0m'
echo ""
echo "  All services accessible at ${URL_PATTERN}:"
echo "    ${DASH_URL}         Dashboard"
echo "    ${TRAEFIK_URL}      Reverse proxy"
echo "    ${DOKPLOY_URL}      PaaS controller"
echo "    ${KUMA_URL}         Uptime monitoring"
echo "    ${AUTH_URL}         Authentication"
echo ""
echo "  Login credentials:"
echo "    Email:    ${ADMIN_EMAIL}"
if [ -n "$ADMIN_PASSWORD" ]; then
  echo "    Password: ${ADMIN_PASSWORD}"
fi
echo ""
echo "  Next steps:"
echo "    1. Login at ${AUTH_URL}"
echo "    2. Create your PocketID admin passkey at ${ID_URL}/setup"
echo "    3. Change your auto-generated password"
echo ""
if [ "$DOMAIN" = "kombify.me" ] && [ -n "$SUBDOMAIN_PREFIX" ]; then
  echo "  DNS: Managed by kombify.me"
  echo ""
else
  case "$DOMAIN" in
    *.kombify|*.local|*.lab|*.lan|*.home|*.internal|*.test|stack.local|home.lab)
      echo "  Local DNS: Kombify Point resolves *.${DOMAIN} inside your home network."
      echo "  Temporary workstation hosts entries:"
      echo "    ${SERVER_IP}  base.${DOMAIN} traefik.${DOMAIN} dokploy.${DOMAIN}"
      echo "    ${SERVER_IP}  kuma.${DOMAIN} auth.${DOMAIN} whoami.${DOMAIN}"
      echo ""
      ;;
  esac
fi
echo "  Commands:"
echo "    stackkit status       Check service health"
echo "    stackkit addon list   Available add-ons"
echo "    stackkit remove       Tear down everything"
echo ""
if [ -f "$HOMELAB_DIR/.stackkit/access.json" ]; then
  echo "  Machine-readable access summary:"
  echo "    $HOMELAB_DIR/.stackkit/access.json"
  echo ""
fi
echo "  Project directory: $HOMELAB_DIR"
echo ""
