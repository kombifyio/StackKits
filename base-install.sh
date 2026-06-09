#!/bin/sh
# =============================================================================
# StackKits Base Installer — full base-kit deployment in one command.
# =============================================================================
# Usage: curl -sSL https://base.stackkit.cc | sh
#
# Steps:
#   1. Install stackkit CLI + base-kit definitions  (via install.stackkit.cc)
#   2. Prepare system: Docker + packaged OpenTofu
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
#   STACKKIT_MODE          Optional install mode: bare, bootstrapped, or advanced
#   STACKKIT_SERVICE_PROFILE
#                          Optional BaseKit service profile: default or admin-only
#   STACKKIT_PLATFORM / STACKKIT_PAAS
#                          Optional selected PaaS: coolify or komodo. Dokploy is draft-only.
#   STACKKIT_SERVER_IMAGE  Optional stackkit-server image override
#   STACKKIT_ENABLE_DEV_APP_HANDOFF
#                          Optional dev-only app handoff toggle
#   STACKKIT_DEV_APP_IMAGE Optional dev-only app handoff image
#   STACKKIT_APP_NAME      Optional app name (default: web)
#   STACKKIT_APP_AUTH      Optional auth mode: login-gateway or public
#   STACKKIT_APP_HOST      Optional route host
#   STACKKIT_APP_ENV       Optional comma-separated KEY=value app env entries
#   STACKKIT_APP_SECRETS   Optional comma-separated KEY=env:NAME secret refs
#   STACKKIT_PLATFORM_ENDPOINT / STACKKIT_PLATFORM_TOKEN
#   STACKKIT_PLATFORM_API_KEY / STACKKIT_PLATFORM_API_SECRET
#                          Optional advanced override for an external existing PaaS.
#                          Default Coolify installs bootstrap .stackkit/platform.json automatically.
#   DOKPLOY_* / COOLIFY_* / KOMODO_*
#                          Provider-specific aliases for the advanced override only.
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
can_prompt() { [ -t 1 ] && [ -r /dev/tty ] && [ -w /dev/tty ]; }
env_value() { eval "printf '%s' \"\${$1:-}\""; }
first_env_value() {
  for _stackkit_key in "$@"; do
    _stackkit_value=$(env_value "$_stackkit_key")
    if [ -n "$_stackkit_value" ]; then
      printf '%s' "$_stackkit_value"
      return 0
    fi
  done
  return 0
}
json_escape() {
  printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'
}
platform_config_env_present() {
  for _stackkit_key in \
    STACKKIT_PLATFORM_ENDPOINT STACKKIT_PLATFORM_TOKEN \
    STACKKIT_PLATFORM_API_KEY STACKKIT_PLATFORM_API_SECRET \
    STACKKIT_PLATFORM_ENVIRONMENT_ID STACKKIT_PLATFORM_ENVIRONMENT_NAME \
    STACKKIT_PLATFORM_SERVER_ID STACKKIT_PLATFORM_SERVER_UUID \
    STACKKIT_PLATFORM_PROJECT_UUID STACKKIT_PLATFORM_ENVIRONMENT_UUID STACKKIT_PLATFORM_DESTINATION_UUID \
    DOKPLOY_API_URL DOKPLOY_API_KEY DOKPLOY_ENVIRONMENT_ID DOKPLOY_SERVER_ID \
    KOMODO_API_URL KOMODO_API_KEY KOMODO_API_SECRET KOMODO_SERVER_ID \
    COOLIFY_API_URL COOLIFY_API_TOKEN COOLIFY_ENVIRONMENT_NAME COOLIFY_SERVER_UUID \
    COOLIFY_PROJECT_UUID COOLIFY_ENVIRONMENT_UUID COOLIFY_DESTINATION_UUID; do
    if [ -n "$(env_value "$_stackkit_key")" ]; then
      return 0
    fi
  done
  return 1
}
selected_platform_name() {
  _stackkit_platform="${STACKKIT_PLATFORM:-${STACKKIT_PAAS:-}}"
  if [ -z "$_stackkit_platform" ]; then
    return 0
  fi
  printf '%s' "$_stackkit_platform" | tr '[:upper:]' '[:lower:]'
}
configure_dns_tls_provider() {
  if [ -z "${STACKKIT_DNS_TOKEN:-}" ] && [ -n "${CLOUDFLARE_API_TOKEN:-}" ]; then
    export STACKKIT_DNS_TOKEN="$CLOUDFLARE_API_TOKEN"
  fi
  if [ -z "${STACKKIT_DNS_EMAIL:-}" ] && [ -n "${CLOUDFLARE_EMAIL:-}" ]; then
    export STACKKIT_DNS_EMAIL="$CLOUDFLARE_EMAIL"
  fi

  DNS_PROVIDER="${STACKKIT_DNS_PROVIDER:-}"
  if [ -z "$DNS_PROVIDER" ] && [ -n "${STACKKIT_DNS_TOKEN:-}" ]; then
    DNS_PROVIDER="cloudflare"
  fi
  if [ -z "$DNS_PROVIDER" ]; then
    return 0
  fi

  SPEC_FILE="$HOMELAB_DIR/stack-spec.yaml"
  if [ ! -f "$SPEC_FILE" ] || grep -q '^tls:' "$SPEC_FILE"; then
    return 0
  fi
  cat >> "$SPEC_FILE" <<EOF
tls:
  provider: $DNS_PROVIDER
EOF
  ok "  TLS DNS provider selected: $DNS_PROVIDER"
}
apply_platform_selection() {
  PLATFORM_SELECTION="$(selected_platform_name)"
  if [ -z "$PLATFORM_SELECTION" ]; then
    return 0
  fi
  case "$PLATFORM_SELECTION" in
    coolify|komodo|dokploy) ;;
    *) die "Unsupported STACKKIT_PLATFORM '$PLATFORM_SELECTION'. Expected coolify, komodo, or draft-only dokploy." ;;
  esac
  SPEC_FILE="$HOMELAB_DIR/stack-spec.yaml"
  if [ ! -f "$SPEC_FILE" ]; then
    die "Cannot apply platform selection; missing $SPEC_FILE"
  fi
  if grep -q '^paas:' "$SPEC_FILE"; then
    sed -i "s/^paas:.*/paas: $PLATFORM_SELECTION/" "$SPEC_FILE"
  else
    printf '\npaas: %s\n' "$PLATFORM_SELECTION" >> "$SPEC_FILE"
  fi
  ok "  PaaS selected: $PLATFORM_SELECTION"
}
write_platform_json_field() {
  _stackkit_json_name="$1"
  _stackkit_json_value="$2"
  _stackkit_json_file="$3"
  if [ -z "$_stackkit_json_value" ]; then
    return 0
  fi
  if [ "$PLATFORM_JSON_HAS_FIELDS" = "true" ]; then
    printf ',\n' >> "$_stackkit_json_file"
  else
    PLATFORM_JSON_HAS_FIELDS="true"
  fi
  printf '  "%s": "%s"' "$_stackkit_json_name" "$(json_escape "$_stackkit_json_value")" >> "$_stackkit_json_file"
}
persist_platform_config() {
  if ! platform_config_env_present; then
    return 0
  fi

  PLATFORM_NAME="${STACKKIT_PLATFORM:-${STACKKIT_PAAS:-}}"
  if [ -z "$PLATFORM_NAME" ]; then
    if [ -n "${COOLIFY_API_URL:-}" ] || [ -n "${COOLIFY_API_TOKEN:-}" ]; then
      PLATFORM_NAME="coolify"
    elif [ -n "${KOMODO_API_URL:-}" ] || [ -n "${KOMODO_API_KEY:-}" ] || [ -n "${KOMODO_API_SECRET:-}" ]; then
      PLATFORM_NAME="komodo"
    elif [ -n "${DOKPLOY_API_URL:-}" ] || [ -n "${DOKPLOY_API_KEY:-}" ]; then
      PLATFORM_NAME="dokploy"
    else
      PLATFORM_NAME="coolify"
    fi
  fi
  PLATFORM_NAME=$(printf '%s' "$PLATFORM_NAME" | tr '[:upper:]' '[:lower:]')
  PLATFORM_TOKEN=""
  PLATFORM_API_KEY=""
  PLATFORM_API_SECRET=""
  PLATFORM_ENVIRONMENT_ID=""
  PLATFORM_SERVER_ID=""

  case "$PLATFORM_NAME" in
    coolify)
      PLATFORM_ENDPOINT=$(first_env_value COOLIFY_API_URL STACKKIT_PLATFORM_ENDPOINT)
      PLATFORM_TOKEN=$(first_env_value COOLIFY_API_TOKEN STACKKIT_PLATFORM_TOKEN)
      PLATFORM_ENVIRONMENT_ID=$(first_env_value COOLIFY_ENVIRONMENT_NAME STACKKIT_PLATFORM_ENVIRONMENT_NAME)
      PLATFORM_SERVER_ID=$(first_env_value COOLIFY_SERVER_UUID STACKKIT_PLATFORM_SERVER_UUID STACKKIT_PLATFORM_SERVER_ID)
      ;;
    dokploy)
      PLATFORM_ENDPOINT=$(first_env_value DOKPLOY_API_URL STACKKIT_PLATFORM_ENDPOINT)
      PLATFORM_TOKEN=$(first_env_value DOKPLOY_API_KEY STACKKIT_PLATFORM_TOKEN)
      PLATFORM_ENVIRONMENT_ID=$(first_env_value DOKPLOY_ENVIRONMENT_ID STACKKIT_PLATFORM_ENVIRONMENT_ID)
      PLATFORM_SERVER_ID=$(first_env_value DOKPLOY_SERVER_ID STACKKIT_PLATFORM_SERVER_ID)
      ;;
    komodo)
      PLATFORM_ENDPOINT=$(first_env_value KOMODO_API_URL STACKKIT_PLATFORM_ENDPOINT)
      PLATFORM_API_KEY=$(first_env_value KOMODO_API_KEY STACKKIT_PLATFORM_API_KEY STACKKIT_PLATFORM_TOKEN)
      PLATFORM_API_SECRET=$(first_env_value KOMODO_API_SECRET STACKKIT_PLATFORM_API_SECRET)
      PLATFORM_SERVER_ID=$(first_env_value KOMODO_SERVER_ID STACKKIT_PLATFORM_SERVER_ID)
      ;;
    *)
      die "Unsupported STACKKIT_PLATFORM '$PLATFORM_NAME'. Expected coolify, komodo, or draft-only dokploy."
      ;;
  esac

  if [ "$PLATFORM_NAME" = "komodo" ]; then
    if [ -z "$PLATFORM_ENDPOINT" ] || [ -z "$PLATFORM_API_KEY" ] || [ -z "$PLATFORM_API_SECRET" ]; then
      die "Komodo platform config override is incomplete. Provide endpoint, api key, and api secret for an external existing Komodo."
    fi
  elif [ -z "$PLATFORM_ENDPOINT" ] || [ -z "$PLATFORM_TOKEN" ]; then
    die "Platform config override is incomplete. Remove the partial STACKKIT_PLATFORM_* / provider env vars, or provide endpoint and token for an external existing PaaS."
  fi

  PLATFORM_PROJECT_UUID=$(first_env_value COOLIFY_PROJECT_UUID STACKKIT_PLATFORM_PROJECT_UUID)
  PLATFORM_ENVIRONMENT_UUID=$(first_env_value COOLIFY_ENVIRONMENT_UUID STACKKIT_PLATFORM_ENVIRONMENT_UUID)
  PLATFORM_DESTINATION_UUID=$(first_env_value COOLIFY_DESTINATION_UUID STACKKIT_PLATFORM_DESTINATION_UUID)

  mkdir -p "$HOMELAB_DIR/.stackkit"
  chmod 700 "$HOMELAB_DIR/.stackkit" 2>/dev/null || true
  PLATFORM_JSON="$HOMELAB_DIR/.stackkit/platform.json"
  PLATFORM_JSON_TMP="$PLATFORM_JSON.tmp"
  PLATFORM_JSON_HAS_FIELDS="false"
  : > "$PLATFORM_JSON_TMP"
  chmod 600 "$PLATFORM_JSON_TMP" 2>/dev/null || true
  printf '{\n' > "$PLATFORM_JSON_TMP"
  write_platform_json_field "platform" "$PLATFORM_NAME" "$PLATFORM_JSON_TMP"
  write_platform_json_field "endpoint" "$PLATFORM_ENDPOINT" "$PLATFORM_JSON_TMP"
  write_platform_json_field "token" "$PLATFORM_TOKEN" "$PLATFORM_JSON_TMP"
  write_platform_json_field "apiKey" "$PLATFORM_API_KEY" "$PLATFORM_JSON_TMP"
  write_platform_json_field "apiSecret" "$PLATFORM_API_SECRET" "$PLATFORM_JSON_TMP"
  write_platform_json_field "environmentId" "$PLATFORM_ENVIRONMENT_ID" "$PLATFORM_JSON_TMP"
  write_platform_json_field "serverId" "$PLATFORM_SERVER_ID" "$PLATFORM_JSON_TMP"
  write_platform_json_field "projectUuid" "$PLATFORM_PROJECT_UUID" "$PLATFORM_JSON_TMP"
  write_platform_json_field "environmentUuid" "$PLATFORM_ENVIRONMENT_UUID" "$PLATFORM_JSON_TMP"
  write_platform_json_field "destinationUuid" "$PLATFORM_DESTINATION_UUID" "$PLATFORM_JSON_TMP"
  printf '\n}\n' >> "$PLATFORM_JSON_TMP"
  mv "$PLATFORM_JSON_TMP" "$PLATFORM_JSON"
  chmod 600 "$PLATFORM_JSON" 2>/dev/null || true
  ok "  Platform API config persisted to $PLATFORM_JSON"
}

configure_stackkit_server_image() {
  if [ -n "${STACKKIT_SERVER_IMAGE:-}" ]; then
    ok "  StackKit API image: $STACKKIT_SERVER_IMAGE"
    export STACKKIT_SERVER_IMAGE
    return 0
  fi

  if ! command -v stackkit-server >/dev/null 2>&1; then
    warn "stackkit-server binary not installed; falling back to the configured registry image."
    return 0
  fi

  DOCKER_CMD="docker"
  if ! docker info >/dev/null 2>&1; then
    if command -v sudo >/dev/null 2>&1 && sudo -n docker info >/dev/null 2>&1; then
      DOCKER_CMD="sudo -n docker"
    else
      warn "Docker is not reachable for local stackkit-server image build; falling back to the configured registry image."
      return 0
    fi
  fi

  STACKKIT_SERVER_LOCAL_IMAGE="${STACKKIT_SERVER_LOCAL_IMAGE:-stackkit-server:local}"
  STACKKIT_SERVER_IMAGE_DIR=$(mktemp -d 2>/dev/null || mktemp -d -t stackkit-server-image)
  trap 'rm -rf "$STACKKIT_SERVER_IMAGE_DIR"' EXIT HUP INT TERM

  cp "$(command -v stackkit-server)" "$STACKKIT_SERVER_IMAGE_DIR/stackkit-server"
  cat > "$STACKKIT_SERVER_IMAGE_DIR/Dockerfile" <<'EOF'
FROM alpine:3.19
RUN apk add --no-cache ca-certificates curl
COPY stackkit-server /usr/local/bin/stackkit-server
RUN chmod +x /usr/local/bin/stackkit-server
ENTRYPOINT ["stackkit-server"]
EOF

  info "Building local stackkit-server image ($STACKKIT_SERVER_LOCAL_IMAGE)"
  if ! $DOCKER_CMD build -q -t "$STACKKIT_SERVER_LOCAL_IMAGE" "$STACKKIT_SERVER_IMAGE_DIR" >/dev/null; then
    die "Local stackkit-server image build failed. Set STACKKIT_SERVER_IMAGE to a reachable image and retry."
  fi
  rm -rf "$STACKKIT_SERVER_IMAGE_DIR"
  STACKKIT_SERVER_IMAGE="$STACKKIT_SERVER_LOCAL_IMAGE"
  export STACKKIT_SERVER_IMAGE
  ok "  StackKit API image: $STACKKIT_SERVER_IMAGE"
}

STACKKIT_MODE_VALUE="${STACKKIT_MODE:-${INSTALL_MODE:-}}"
if [ -n "$STACKKIT_MODE_VALUE" ]; then
  STACKKIT_MODE_VALUE=$(printf '%s' "$STACKKIT_MODE_VALUE" | tr '[:upper:]' '[:lower:]')
  case "$STACKKIT_MODE_VALUE" in
    bare|bootstrapped|advanced)
      ;;
    simple)
      warn "Legacy STACKKIT_MODE=simple selected; using bootstrapped."
      STACKKIT_MODE_VALUE="bootstrapped"
      ;;
    terramate|advanced-terramate)
      warn "Legacy advanced install mode '$STACKKIT_MODE_VALUE' selected; using advanced."
      STACKKIT_MODE_VALUE="advanced"
      ;;
    *)
      die "Unsupported STACKKIT_MODE '$STACKKIT_MODE_VALUE'. Expected bare, bootstrapped, or advanced."
      ;;
  esac
fi

# --- Admin email --------------------------------------------------------------

ADMIN_EMAIL="${STACKKIT_ADMIN_EMAIL:-${KOMBIFY_USER_EMAIL:-}}"
if [ -z "$ADMIN_EMAIL" ] && can_prompt; then
  echo ""
  printf '  Admin email (for login accounts): '
  read -r ADMIN_EMAIL </dev/tty
  echo ""
fi
if [ -z "$ADMIN_EMAIL" ]; then
  warn "No admin email provided — StackKits will generate a deployment-scoped admin email."
fi

BOOTSTRAP_OWNER="${STACKKIT_BOOTSTRAP_OWNER:-}"
if [ -z "$BOOTSTRAP_OWNER" ] && [ -n "$ADMIN_EMAIL" ] && [ "$ADMIN_EMAIL" != "admin" ] && can_prompt; then
  echo ""
  printf '  Create a preconfigured StackKits owner account for %s? [Y/n]: ' "$ADMIN_EMAIL"
  read -r _owner_answer </dev/tty
  echo ""
  case "$_owner_answer" in
    n|N|no|NO|No) BOOTSTRAP_OWNER="false" ;;
    *) BOOTSTRAP_OWNER="true" ;;
  esac
fi
if [ -z "$BOOTSTRAP_OWNER" ]; then
  BOOTSTRAP_OWNER="false"
fi

OWNER_USERNAME="${STACKKIT_OWNER_USERNAME:-}"
if [ "$BOOTSTRAP_OWNER" = "true" ] && [ -z "$OWNER_USERNAME" ]; then
  OWNER_USERNAME=$(printf '%s' "$ADMIN_EMAIL" | sed 's/@.*//' | tr '[:upper:]' '[:lower:]' | sed 's/[^a-z0-9._-]/-/g')
  if [ -z "$OWNER_USERNAME" ]; then
    OWNER_USERNAME="admin"
  fi
fi

# --- Step 1: Install CLI + base-kit definitions -------------------------------
# Delegates entirely to install.sh so all binary download, kit extraction, and
# ~/.stackkits layout logic lives in exactly one place.

info "Step 1/4 -- Installing stackkit CLI + base-kit"

# STACKKIT_NO_BANNER suppresses the duplicate banner from install.sh.
STACKKIT_INSTALL_URL="${STACKKIT_INSTALL_URL:-https://install.stackkit.cc}"
STACKKIT_NO_BANNER=1 curl -sSL "$STACKKIT_INSTALL_URL" | sh -s -- base-kit

ok "  stackkit $(stackkit version 2>/dev/null | head -1) installed"

# --- Step 2: Prepare system (Docker + packaged OpenTofu) ----------------------

info "Step 2/4 -- Preparing system (Docker + packaged OpenTofu)"

if [ "$(id -u)" -eq 0 ]; then
  stackkit $STACKKIT_CONTEXT_ARG prepare || die "System preparation failed."
else
  sudo stackkit $STACKKIT_CONTEXT_ARG prepare || die "System preparation failed."
fi

ok "  System ready"

configure_stackkit_server_image

# --- Step 3: Initialize base-kit ----------------------------------------------

info "Step 3/4 -- Initializing base-kit"

mkdir -p "$HOMELAB_DIR"
cd "$HOMELAB_DIR"

set -- init base-kit --force
if [ -n "$STACKKIT_MODE_VALUE" ]; then
  set -- "$@" --mode "$STACKKIT_MODE_VALUE"
fi
if [ -n "$ADMIN_EMAIL" ]; then
  set -- "$@" --admin-email "$ADMIN_EMAIL"
fi
if [ "$BOOTSTRAP_OWNER" = "true" ]; then
  set -- "$@" --owner-source local --owner-email "$ADMIN_EMAIL" --owner-username "$OWNER_USERNAME"
else
  set -- "$@" --non-interactive
fi
if [ -n "${STACKKIT_SERVICE_PROFILE:-}" ]; then
  set -- "$@" --service-profile "$STACKKIT_SERVICE_PROFILE"
fi
if [ -n "${DOMAIN:-}" ]; then
  set -- "$@" --domain "$DOMAIN"
fi
if [ "$BOOTSTRAP_OWNER" = "true" ] && can_prompt; then
  stackkit $STACKKIT_CONTEXT_ARG "$@" </dev/tty
else
  stackkit $STACKKIT_CONTEXT_ARG "$@"
fi

ok "  base-kit initialized in $HOMELAB_DIR"

configure_dns_tls_provider
apply_platform_selection
persist_platform_config

# --- Optional dev-only app handoff -------------------------------------------

APP_IMAGE=""
if [ "${STACKKIT_ENABLE_DEV_APP_HANDOFF:-false}" = "true" ]; then
  APP_IMAGE="${STACKKIT_DEV_APP_IMAGE:-${STACKKIT_APP_IMAGE:-}}"
fi
if [ -n "$APP_IMAGE" ]; then
  APP_NAME="${STACKKIT_APP_NAME:-web}"
  APP_KIND="${STACKKIT_APP_KIND:-sveltekit}"
  APP_CONTAINER_PORT_DEFAULT=3000
  APP_PORT="${STACKKIT_APP_PORT:-$APP_CONTAINER_PORT_DEFAULT}"
  APP_AUTH="${STACKKIT_APP_AUTH:-login-gateway}"
  APP_HEALTH_PATH="${STACKKIT_APP_HEALTH_PATH:-/health}"

  info "Adding dev PaaS handoff app '$APP_NAME'"

  set -- app add "$APP_NAME" \
    --image "$APP_IMAGE" \
    --kind "$APP_KIND" \
    --port "$APP_PORT" \
    --auth "$APP_AUTH" \
    --health-path "$APP_HEALTH_PATH"

  if [ -n "${STACKKIT_APP_HOST:-}" ]; then
    set -- "$@" --host "$STACKKIT_APP_HOST"
  fi

  if [ -n "${STACKKIT_APP_ENV:-}" ]; then
    _old_ifs=$IFS
    IFS=','
    for _entry in $STACKKIT_APP_ENV; do
      if [ -n "$_entry" ]; then
        set -- "$@" --env "$_entry"
      fi
    done
    IFS=$_old_ifs
  fi

  APP_SECRET_REFS="${STACKKIT_APP_SECRETS:-${STACKKIT_APP_SECRET_REFS:-}}"
  if [ -n "$APP_SECRET_REFS" ]; then
    _old_ifs=$IFS
    IFS=','
    for _entry in $APP_SECRET_REFS; do
      if [ -n "$_entry" ]; then
        set -- "$@" --secret "$_entry"
      fi
    done
    IFS=$_old_ifs
  fi

  stackkit $STACKKIT_CONTEXT_ARG "$@"
  ok "  Dev PaaS handoff app '$APP_NAME' added to stack-spec.yaml"
fi

# --- Step 4: Generate + Deploy ------------------------------------------------

info "Step 4/4 -- Deploying homelab stack"

rm -rf "$HOMELAB_DIR/deploy"
stackkit $STACKKIT_CONTEXT_ARG generate --force
if [ "$BOOTSTRAP_OWNER" = "true" ] && can_prompt; then
  stackkit $STACKKIT_CONTEXT_ARG apply --auto-approve </dev/tty
else
  stackkit $STACKKIT_CONTEXT_ARG apply --auto-approve
fi

# --- Done: print access summary -----------------------------------------------

# Read domain from spec
DOMAIN="stack.local"
if [ -f "$HOMELAB_DIR/stack-spec.yaml" ]; then
  _d=$(grep '^domain:' "$HOMELAB_DIR/stack-spec.yaml" | head -1 | awk '{print $2}' || true)
  if [ -n "$_d" ]; then DOMAIN="$_d"; fi
fi

# Read subdomain prefix from tfvars (kombify.me mode)
SUBDOMAIN_PREFIX=""
PAAS="coolify"
INSTALLATION_MODE="bootstrapped"
ENABLE_DASHBOARD="true"
ENABLE_HOMEPAGE="true"
if [ -f "$HOMELAB_DIR/deploy/terraform.tfvars.json" ]; then
  SUBDOMAIN_PREFIX=$(grep '"subdomain_prefix"' "$HOMELAB_DIR/deploy/terraform.tfvars.json" | head -1 | sed -E 's/.*: *"([^"]+)".*/\1/' || true)
  _paas=$(grep '"paas"' "$HOMELAB_DIR/deploy/terraform.tfvars.json" | head -1 | sed -E 's/.*: *"([^"]+)".*/\1/' || true)
  if [ -n "$_paas" ]; then PAAS="$_paas"; fi
  _installation_mode=$(grep '"installation_mode"' "$HOMELAB_DIR/deploy/terraform.tfvars.json" | head -1 | sed -E 's/.*: *"([^"]+)".*/\1/' || true)
  if [ -n "$_installation_mode" ]; then INSTALLATION_MODE="$_installation_mode"; fi
  if grep -q '"enable_dashboard"[[:space:]]*:[[:space:]]*false' "$HOMELAB_DIR/deploy/terraform.tfvars.json"; then
    ENABLE_DASHBOARD="false"
  fi
  if grep -q '"enable_homepage"[[:space:]]*:[[:space:]]*false' "$HOMELAB_DIR/deploy/terraform.tfvars.json"; then
    ENABLE_HOMEPAGE="false"
  fi
fi

ENABLE_HTTPS="false"
if [ -f "$HOMELAB_DIR/deploy/terraform.tfvars.json" ] && grep -q '"enable_https"[[:space:]]*:[[:space:]]*true' "$HOMELAB_DIR/deploy/terraform.tfvars.json"; then
  ENABLE_HTTPS="true"
fi

SERVER_IP=$(hostname -I 2>/dev/null | awk '{print $1}' || echo "YOUR_SERVER_IP")

ADMIN_PASSWORD=""
if [ -f "$HOMELAB_DIR/deploy/terraform.tfvars.json" ]; then
  ADMIN_PASSWORD=$(grep '"admin_password_plaintext"' "$HOMELAB_DIR/deploy/terraform.tfvars.json" | head -1 | sed -E 's/.*: *"([^"]+)".*/\1/' || true)
  _admin_email=$(grep '"admin_email"' "$HOMELAB_DIR/deploy/terraform.tfvars.json" | head -1 | sed -E 's/.*: *"([^"]+)".*/\1/' || true)
  if [ -n "$_admin_email" ]; then ADMIN_EMAIL="$_admin_email"; fi
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
    *.kombify|*.local|*.lab|*.lan|*.home|*.internal|*.test|stack.local|home|home.lab|homelab)
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

case "$PAAS" in
  komodo)
    PAAS_ROUTE="komodo"
    PAAS_LABEL="Komodo"
    ;;
  dokploy)
    PAAS_ROUTE="dokploy"
    PAAS_LABEL="Dokploy"
    ;;
  *)
    PAAS_ROUTE="coolify"
    PAAS_LABEL="Coolify"
    ;;
esac

# Build service URLs
if [ -n "$SUBDOMAIN_PREFIX" ] && [ "$DOMAIN" = "kombify.me" ]; then
  PROTO="https"
  DASH_URL="${PROTO}://${SUBDOMAIN_PREFIX}-base.${DOMAIN}"
  HOME_URL="${PROTO}://${SUBDOMAIN_PREFIX}-home.${DOMAIN}"
  TRAEFIK_URL="${PROTO}://${SUBDOMAIN_PREFIX}-traefik.${DOMAIN}"
  PAAS_URL="${PROTO}://${SUBDOMAIN_PREFIX}-${PAAS_ROUTE}.${DOMAIN}"
  KUMA_URL="${PROTO}://${SUBDOMAIN_PREFIX}-kuma.${DOMAIN}"
  AUTH_URL="${PROTO}://${SUBDOMAIN_PREFIX}-auth.${DOMAIN}"
  ID_URL="${PROTO}://${SUBDOMAIN_PREFIX}-id.${DOMAIN}"
  URL_PATTERN="<service> at ${SUBDOMAIN_PREFIX}-<service>.${DOMAIN}"
else
  PROTO="http"
  if [ "$ENABLE_HTTPS" = "true" ]; then
    PROTO="https"
  fi
  DASH_URL="${PROTO}://base.${DOMAIN}"
  HOME_URL="${PROTO}://home.${DOMAIN}"
  TRAEFIK_URL="${PROTO}://traefik.${DOMAIN}"
  PAAS_URL="${PROTO}://${PAAS_ROUTE}.${DOMAIN}"
  KUMA_URL="${PROTO}://kuma.${DOMAIN}"
  AUTH_URL="${PROTO}://auth.${DOMAIN}"
  ID_URL="${PROTO}://id.${DOMAIN}"
  URL_PATTERN="<service>.${DOMAIN}"
fi

echo ""
ok "Your homelab is running!"
echo ""
printf '\033[38;5;208m'
if [ "$ENABLE_DASHBOARD" = "true" ]; then
  echo "  Dashboard:  ${DASH_URL}"
else
  echo "  Install mode: ${INSTALLATION_MODE}"
  echo "  Primary service: ${PAAS_URL}"
fi
printf '\033[0m'
echo ""
echo "  All services accessible at ${URL_PATTERN}:"
if [ "$ENABLE_DASHBOARD" = "true" ]; then
  echo "    ${DASH_URL}         Dashboard"
fi
if [ "$ENABLE_HOMEPAGE" = "true" ]; then
  echo "    ${HOME_URL}         Homepage"
fi
if [ "$PAAS" = "komodo" ]; then
  echo "    ${TRAEFIK_URL}      Reverse proxy"
fi
echo "    ${PAAS_URL}      ${PAAS_LABEL} controller"
echo "    ${KUMA_URL}         Uptime monitoring"
echo "    ${AUTH_URL}         Authentication"
echo ""
echo "  Initial admin login credentials:"
echo "    Use for: TinyAuth gateway and ${PAAS_LABEL} initial admin"
echo "    Email:    ${ADMIN_EMAIL}"
if [ -n "$ADMIN_PASSWORD" ]; then
  echo "    Password: ${ADMIN_PASSWORD}"
fi
echo ""
echo "  Next steps:"
echo "    1. Login at ${AUTH_URL}"
if [ "$BOOTSTRAP_OWNER" = "true" ]; then
  echo "    2. Complete the one-time PocketID owner setup URL printed above"
else
  echo "    2. Create your PocketID admin passkey at ${ID_URL}/setup"
fi
echo "    3. Change your auto-generated password"
echo ""
if [ "$DOMAIN" = "kombify.me" ] && [ -n "$SUBDOMAIN_PREFIX" ]; then
  echo "  DNS: Managed by kombify.me"
  echo ""
else
  case "$DOMAIN" in
    *.kombify|*.local|*.lab|*.lan|*.home|*.internal|*.test|stack.local|home|home.lab)
      echo "  Local DNS: Kombify Point resolves *.${DOMAIN} inside your home network."
      echo "  Temporary workstation hosts entries:"
      if [ "$ENABLE_DASHBOARD" = "true" ]; then
        echo "    ${SERVER_IP}  base.${DOMAIN} home.${DOMAIN}"
      fi
      echo "    ${SERVER_IP}  traefik.${DOMAIN} dokploy.${DOMAIN}"
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
