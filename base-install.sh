#!/bin/sh
# =============================================================================
# StackKits Basement Installer — full basement-kit deployment in one command.
# =============================================================================
# Usage: curl -sSL https://base.stackkit.cc | sh
#
# This installer carries you through the COMPLETE installation to a running
# homelab. It is a guidance layer over the stackkit CLI: every decision maps
# to a CLI parameter, and the same result is achievable with plain CLI verbs
# (see "CLI path" in the final summary or docs/INSTALLATION_PROCESSES.md).
#
# Modes (STACKKIT_INSTALL_MODE=auto|guided|expert; interactive menu on a TTY):
#   auto    Quick Install: owner account and use cases, then defaults to a
#           running homelab. Files/Photos/Vault start checked in the picker.
#   guided  Core decisions: workspace, domain, admin email, owner account,
#           use-case picker, one apply confirmation.
#   expert  Detailed: guided plus stack name, platform adapter,
#           per-phase apply confirmation.
# Non-TTY runs always use auto. Environment-provided values are never re-asked.
#
# Steps:
#   1. Install stackkit CLI + basement-kit definitions  (via install.stackkit.cc)
#   2. Initialize basement-kit                          (native v2 init)
#   3. Prepare the container runtime (Docker install + group activation)
#   4. Build the local stackkit-server image            (registry fallback)
#   5. Generate + deploy the full homelab stack         (host preflight, apply)
#
# Environment variables (all optional; each pre-seeds one decision):
#   STACKKIT_INSTALL_MODE  auto | guided | expert
#   HOMELAB_DIR            Workspace (default: $HOME/my-homelab)
#   DOMAIN                 Own domain (default: device-enrolled home zone)
#   STACKKIT_NAME          Deployment contract ID (default: workspace name)
#   STACKKIT_ADMIN_EMAIL   Required owner email for logins (KOMBIFY_USER_EMAIL fallback)
#   STACKKIT_OWNER_USERNAME   Login username (default: derived from the email)
#   STACKKIT_USE_CASES     Comma-separated optional workloads; skip the picker.
#                          files,photos,vault are the L3 defaults; none = core only
#   STACKKIT_PLATFORM / STACKKIT_PAAS
#                          Workload runtime adapter: standalone-compose (default) | komodo | coolify
#                          (applies to the selected use cases)
#   STACKKIT_SERVER_IMAGE  Optional stackkit-server image override
#   STACKKIT_INSTALL_URL   Release installer (default: https://install.stackkit.cc)
#   CLOUDFLARE_API_TOKEN / CLOUDFLARE_EMAIL
#                          DNS credentials for custom public domains
#   STACKKIT_PLATFORM_* / COOLIFY_* / KOMODO_* / DOKPLOY_*
#                          Advanced override for an external existing PaaS
#
# Requirements: Linux or macOS, root/sudo access
# =============================================================================
set -eu

_INSTALL_SCRIPT_DIR=$(
  cd "$(dirname "$0")" 2>/dev/null && pwd
) || _INSTALL_SCRIPT_DIR=""
if [ -n "$_INSTALL_SCRIPT_DIR" ] && [ -f "$_INSTALL_SCRIPT_DIR/install-host-bootstrap.sh" ]; then
  # shellcheck source=install-host-bootstrap.sh
  . "$_INSTALL_SCRIPT_DIR/install-host-bootstrap.sh"
fi

printf '\033[38;5;208m'
cat <<'BANNER'

     _             _    _    _ _
 ___| |_ __ _  ___| | _| | _(_) |_
/ __| __/ _` |/ __| |/ / |/ / | __|
\__ \ || (_| | (__|   <|   <| | |_
|___/\__\__,_|\___|_|\_\_|\_\_|\__|

BANNER
printf '\033[0m'
# Nested stackkit init/apply must not reprint this greeting. Progress lines
# are the operator signal that later steps are still working.
export STACKKIT_NO_BANNER=1

# --- Helpers ------------------------------------------------------------------

info()  { printf '\033[1;34m==> %s\033[0m\n' "$*"; }
ok()    { printf '\033[1;32m==> %s\033[0m\n' "$*"; }
warn()  { printf '\033[1;33m==> %s\033[0m\n' "$*"; }
err()   { printf '\033[1;31m==> %s\033[0m\n' "$*" >&2; }
die()   { err "$*"; exit 1; }
can_prompt() { [ -t 1 ] && [ -r /dev/tty ] && [ -w /dev/tty ]; }

workspace_spec_kit() {
  _spec="${1:-}"
  [ -f "$_spec" ] || return 0
  if grep -Eq '(^|[[:space:]])(stackkit|slug):[[:space:]]*cloud-kit([[:space:]]|$)' "$_spec"; then
    printf '%s' "cloud-kit"
  elif grep -Eq '(^|[[:space:]])(stackkit|slug):[[:space:]]*basement-kit([[:space:]]|$)' "$_spec"; then
    printf '%s' "basement-kit"
  fi
}

read_host_environment() {
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
}

remove_local_stack_if_present() {
  command -v stackkit >/dev/null 2>&1 || return 0
  info "Clearing any existing local StackKits stack so the matching kit can install"
  if [ -n "${1:-}" ] && [ -d "$1" ]; then
    (cd "$1" && stackkit remove --auto-approve) || warn "Could not fully remove the previous stack; continuing with the matching kit."
  else
    stackkit remove --auto-approve || warn "Could not fully remove the previous stack; continuing with the matching kit."
  fi
}

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

# prompt_default LABEL DEFAULT -> stdout answer (Enter keeps the default)
prompt_default() {
  printf '  %s [%s]: ' "$1" "$2" >/dev/tty
  read -r _stackkit_answer </dev/tty || _stackkit_answer=""
  if [ -z "$_stackkit_answer" ]; then
    printf '%s' "$2"
  else
    printf '%s' "$_stackkit_answer"
  fi
}

# prompt_select LABEL DEFAULT_NUM -> stdout selected number
prompt_select() {
  printf '  %s [%s]: ' "$1" "$2" >/dev/tty
  read -r _stackkit_answer </dev/tty || _stackkit_answer=""
  if [ -z "$_stackkit_answer" ]; then
    printf '%s' "$2"
  else
    printf '%s' "$_stackkit_answer"
  fi
}
prompt_yn() {
  if [ "$2" = "y" ]; then _stackkit_hint="Y/n"; else _stackkit_hint="y/N"; fi
  printf '  %s [%s]: ' "$1" "$_stackkit_hint" >/dev/tty
  read -r _stackkit_answer </dev/tty || _stackkit_answer=""
  case "$_stackkit_answer" in
    y|Y|yes|YES|Yes) return 0 ;;
    n|N|no|NO|No) return 1 ;;
    *) [ "$2" = "y" ] ;;
  esac
}

valid_owner_email() {
  _email=$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')
  case "$_email" in
    *[[:space:]]*|*"@"*"@"*) return 1 ;;
    *@*.*) return 0 ;;
    *) return 1 ;;
  esac
}

owner_username_from_email() {
  printf '%s' "$1" | sed 's/@.*//' | tr '[:upper:]' '[:lower:]' | sed 's/[^a-z0-9._-]/-/g; s/^[.-]*//; s/[.-]*$//; s/--*/-/g' | cut -c1-32
}

is_placeholder_owner_email() {
  [ "$1" = "owner@home.test" ]
}

# Generated PocketID placeholders and missing owner.json are not a real account.
# Resume still asks for email and use cases in that case.
workspace_needs_owner_identity() {
  _owner_json="$HOMELAB_DIR/.stackkit/custody/owner.json"
  if [ ! -f "$_owner_json" ]; then
    return 0
  fi
  if grep -Eq '"email"[[:space:]]*:[[:space:]]*"owner@home\.test"' "$_owner_json"; then
    return 0
  fi
  return 1
}

# Owner logins cannot be created from a generated placeholder. Email is
# required before init; the username is suggested from the local part.
collect_owner_identity() {
  if [ -z "${ADMIN_EMAIL:-}" ]; then
    ADMIN_EMAIL="${STACKKIT_ADMIN_EMAIL:-${KOMBIFY_USER_EMAIL:-}}"
  fi
  if is_placeholder_owner_email "$ADMIN_EMAIL"; then
    ADMIN_EMAIL=""
  fi
  if [ -z "$ADMIN_EMAIL" ]; then
    if can_prompt; then
      echo ""
      echo "  Homelab logins need your email before install."
      while true; do
        printf '  Email address: ' >/dev/tty
        read -r ADMIN_EMAIL </dev/tty || ADMIN_EMAIL=""
        ADMIN_EMAIL=$(printf '%s' "$ADMIN_EMAIL" | tr -d '\r')
        if valid_owner_email "$ADMIN_EMAIL" && ! is_placeholder_owner_email "$ADMIN_EMAIL"; then
          break
        fi
        echo "  Enter a valid email address so owner logins can be created."
      done
    else
      die "Homelab install needs an owner email. Set STACKKIT_ADMIN_EMAIL or run this installer from a terminal."
    fi
  fi
  if ! valid_owner_email "$ADMIN_EMAIL"; then
    die "STACKKIT_ADMIN_EMAIL must be a valid email address."
  fi
  if [ -z "${OWNER_USERNAME:-}" ]; then
    OWNER_USERNAME="${STACKKIT_OWNER_USERNAME:-}"
  fi
  if [ -z "$OWNER_USERNAME" ]; then
    OWNER_USERNAME=$(owner_username_from_email "$ADMIN_EMAIL")
    [ -n "$OWNER_USERNAME" ] || OWNER_USERNAME="owner"
    if can_prompt && [ -z "${STACKKIT_OWNER_USERNAME:-}" ]; then
      OWNER_USERNAME=$(prompt_default "Login username" "$OWNER_USERNAME")
    fi
  fi
  OWNER_USERNAME=$(printf '%s' "$OWNER_USERNAME" | tr '[:upper:]' '[:lower:]' | sed 's/[^a-z0-9._-]/-/g; s/^[.-]*//; s/[.-]*$//; s/--*/-/g' | cut -c1-32)
  [ -n "$OWNER_USERNAME" ] || die "Login username cannot be empty."
  BOOTSTRAP_OWNER="true"
}

# --- Mode resolution ------------------------------------------------------------

INSTALL_MODE=$(printf '%s' "${STACKKIT_INSTALL_MODE:-}" | tr '[:upper:]' '[:lower:]')
case "$INSTALL_MODE" in
  quick) warn "STACKKIT_INSTALL_MODE=quick selected; using auto."; INSTALL_MODE="auto" ;;
  wizard|detailed) warn "STACKKIT_INSTALL_MODE=$INSTALL_MODE selected; using expert."; INSTALL_MODE="expert" ;;
  auto|guided|expert|"") ;;
  *) die "Unsupported STACKKIT_INSTALL_MODE '$INSTALL_MODE'. Expected auto, guided, or expert." ;;
esac

if [ -z "$INSTALL_MODE" ]; then
  if can_prompt; then
    echo ""
    echo "  How do you want to install?"
    echo "    1) Quick Install   -- owner account and use cases, then a running homelab"
    echo "    2) Core decisions  -- workspace, domain, owner account, use cases"
    echo "    3) Detailed        -- core decisions plus name and platform"
    printf '  Select [1]: ' >/dev/tty
    read -r _stackkit_mode_answer </dev/tty || _stackkit_mode_answer=""
    case "$_stackkit_mode_answer" in
      2) INSTALL_MODE="guided" ;;
      3) INSTALL_MODE="expert" ;;
      *) INSTALL_MODE="auto" ;;
    esac
    echo ""
  else
    INSTALL_MODE="auto"
  fi
fi
if [ "$INSTALL_MODE" != "auto" ] && ! can_prompt; then
  die "STACKKIT_INSTALL_MODE=$INSTALL_MODE needs an interactive terminal. Without one, run the auto mode or drive the CLI directly: stackkit init basement-kit --catalog-defaults --non-interactive --owner-source=local && stackkit generate && stackkit apply --auto-approve"
fi
info "Install mode: $INSTALL_MODE"

ADMIN_EMAIL=""
OWNER_USERNAME=""
BOOTSTRAP_OWNER=""

# --- Decision collection --------------------------------------------------------
# Environment-provided values are never re-asked in any mode.

HOMELAB_DIR_SET="${HOMELAB_DIR:+1}"
HOMELAB_DIR="${HOMELAB_DIR:-$HOME/my-homelab}"
if [ "$INSTALL_MODE" != "auto" ] && [ -z "$HOMELAB_DIR_SET" ]; then
  HOMELAB_DIR=$(prompt_default "Workspace directory" "$HOMELAB_DIR")
fi

RESUME_EXISTING=0
RESUME_INCOMPLETE=0
if workspace_has_deployment_intent; then
  RESUME_EXISTING=1
  if workspace_apply_incomplete; then
    RESUME_INCOMPLETE=1
    warn "Workspace $HOMELAB_DIR has a previous incomplete install; preparing the host and resuming apply."
  else
    warn "Workspace $HOMELAB_DIR already has StackKits intent; skipping init and resuming apply."
  fi
  if workspace_needs_owner_identity; then
    warn "Workspace owner is not a real account yet; email and use cases are required before install."
  fi
fi

DOMAIN_VALUE="${DOMAIN:-}"
# Domain and kit placement are decided after the CLI is installed so we can
# observe whether this host is a home network or a public server.

if [ "$RESUME_EXISTING" != "1" ] || workspace_needs_owner_identity; then
  collect_owner_identity
fi

STACK_NAME="${STACKKIT_NAME:-}"
USE_CASES="${STACKKIT_USE_CASES:-}"
PLATFORM_SELECTION=$(printf '%s' "${STACKKIT_PLATFORM:-${STACKKIT_PAAS:-}}" | tr '[:upper:]' '[:lower:]')

if [ "$INSTALL_MODE" = "expert" ]; then
  if [ -z "${STACKKIT_NAME:-}" ]; then
    _default_name=$(basename "$HOMELAB_DIR")
    STACK_NAME=$(prompt_default "Stack name (deployment contract ID)" "$_default_name")
    [ "$STACK_NAME" = "$_default_name" ] && STACK_NAME=""
  fi
fi

[ "$USE_CASES" = "none" ] && USE_CASES=""

case "$PLATFORM_SELECTION" in
  ""|coolify|komodo|standalone-compose) ;;
  dokploy) die "Platform 'dokploy' is draft-only and not installable through this installer." ;;
  *) die "Unsupported platform '$PLATFORM_SELECTION'. Expected standalone-compose, komodo or coolify." ;;
esac
if [ -n "$PLATFORM_SELECTION" ] && [ -z "$USE_CASES" ]; then
  warn "STACKKIT_PLATFORM=$PLATFORM_SELECTION applies to selected use cases; none are selected, so the platform choice is recorded for the external-PaaS override only."
fi

# Legacy v0.6 knobs: accepted for compatibility, no longer part of native v2 init.
if [ -n "${STACKKIT_MODE:-}" ]; then
  warn "STACKKIT_MODE is a retired v0.6 knob; native v2 install mode is CUE-owned (ignored)."
fi
if [ -n "${STACKKIT_SERVICE_PROFILE:-}" ]; then
  warn "STACKKIT_SERVICE_PROFILE is a retired v0.6 knob; select use cases instead (ignored)."
fi

# --- External platform override helpers (unchanged advanced path) -----------------

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

  PLATFORM_NAME="$PLATFORM_SELECTION"
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
    if [ "${STACKKIT_RUN:-}" = "sg" ] && sg docker -c "docker info" >/dev/null 2>&1; then
      DOCKER_CMD="sg"
    elif command -v sudo >/dev/null 2>&1 && sudo -n docker info >/dev/null 2>&1; then
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
  chmod +x "$STACKKIT_SERVER_IMAGE_DIR/stackkit-server" 2>/dev/null || true

  STACKKIT_SERVER_CA_CERTS=""
  for STACKKIT_SERVER_CA_CANDIDATE in \
    /etc/ssl/certs/ca-certificates.crt \
    /etc/pki/tls/certs/ca-bundle.crt \
    /etc/ssl/ca-bundle.pem \
    /etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem; do
    if [ -r "$STACKKIT_SERVER_CA_CANDIDATE" ]; then
      cp "$STACKKIT_SERVER_CA_CANDIDATE" "$STACKKIT_SERVER_IMAGE_DIR/ca-certificates.crt"
      STACKKIT_SERVER_CA_CERTS=1
      break
    fi
  done
  if [ -z "$STACKKIT_SERVER_CA_CERTS" ]; then
    warn "Host CA bundle not found; local stackkit-server image will use an empty CA bundle."
    : > "$STACKKIT_SERVER_IMAGE_DIR/ca-certificates.crt"
  fi
  mkdir -p "$STACKKIT_SERVER_IMAGE_DIR/tmp" "$STACKKIT_SERVER_IMAGE_DIR/var/tmp"
  : > "$STACKKIT_SERVER_IMAGE_DIR/tmp/.keep"
  : > "$STACKKIT_SERVER_IMAGE_DIR/var/tmp/.keep"

  cat > "$STACKKIT_SERVER_IMAGE_DIR/Dockerfile" <<'EOF'
FROM scratch
COPY ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY tmp /tmp
COPY var /var
COPY stackkit-server /usr/local/bin/stackkit-server
ENV SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt
ENTRYPOINT ["/usr/local/bin/stackkit-server"]
EOF

  info "Building local stackkit-server image ($STACKKIT_SERVER_LOCAL_IMAGE)"
  if [ "$DOCKER_CMD" = "sg" ]; then
    sg docker -c "docker build -q -t $STACKKIT_SERVER_LOCAL_IMAGE $STACKKIT_SERVER_IMAGE_DIR" >/dev/null       || die "Local stackkit-server image build failed. Set STACKKIT_SERVER_IMAGE to a reachable image and retry."
  elif ! $DOCKER_CMD build -q -t "$STACKKIT_SERVER_LOCAL_IMAGE" "$STACKKIT_SERVER_IMAGE_DIR" >/dev/null; then
    die "Local stackkit-server image build failed. Set STACKKIT_SERVER_IMAGE to a reachable image and retry."
  fi
  rm -rf "$STACKKIT_SERVER_IMAGE_DIR"
  STACKKIT_SERVER_IMAGE="$STACKKIT_SERVER_LOCAL_IMAGE"
  export STACKKIT_SERVER_IMAGE
  ok "  StackKit API image: $STACKKIT_SERVER_IMAGE"
}

# --- Step 1: Install CLI + basement-kit definitions -------------------------------

info "Step 1/5 -- Installing stackkit CLI + basement-kit"
STACKKIT_INSTALL_URL="${STACKKIT_INSTALL_URL:-https://install.stackkit.cc}"
curl -sSL "$STACKKIT_INSTALL_URL" | STACKKIT_NO_BANNER=1 sh -s -- basement-kit
ok "  stackkit $(stackkit version 2>/dev/null | head -1) installed"

SELECTED_KIT="basement-kit"
read_host_environment
info "Detected network: $DETECTED_ENV"

PLACEMENT="${STACKKIT_PLACEMENT:-}"
_existing_kit=$(workspace_spec_kit "$HOMELAB_DIR/stack-spec.yaml")
if [ "$RESUME_EXISTING" = "1" ]; then
  case "$DETECTED_ENV" in
    vps|cloud)
      if [ "$_existing_kit" != "cloud-kit" ] && [ "${STACKKIT_KIT_HANDOFF:-}" != "cloud" ]; then
        warn "This workspace is not Cloud Kit. This host is a public server; continuing with Cloud Kit."
        remove_local_stack_if_present "$HOMELAB_DIR"
        RESUME_EXISTING=0
        PLACEMENT="cloud"
        if [ -z "$HOMELAB_DIR_SET" ]; then
          HOMELAB_DIR="$HOME/my-cloud-homelab"
        fi
      fi
      ;;
    home)
      if [ "$_existing_kit" = "cloud-kit" ]; then
        warn "This workspace is Cloud Kit. This host is a home network; continuing with Basement Kit."
        remove_local_stack_if_present "$HOMELAB_DIR"
        RESUME_EXISTING=0
        PLACEMENT="home"
      fi
      ;;
  esac
fi

if [ "$RESUME_EXISTING" != "1" ]; then
  case "$DETECTED_ENV" in
    home) [ -z "$PLACEMENT" ] && PLACEMENT="home" ;;
    vps|cloud)
      if [ -z "$PLACEMENT" ]; then
        if [ "$INSTALL_MODE" != "auto" ] && can_prompt; then
          echo ""
          echo "  This host is a public server. Cloud Kit is the product for a VPS;"
          echo "  Basement Kit is for a home network."
          if prompt_yn "Continue with Cloud Kit?" "y"; then
            PLACEMENT="cloud"
          else
            PLACEMENT="home"
            DOMAIN_VALUE="${DOMAIN_VALUE:-kombify.me}"
            info "Keeping Basement Kit with kombify.me for public access"
          fi
          echo ""
        else
          PLACEMENT="cloud"
          info "This host is a public server. Continuing with Cloud Kit."
        fi
      fi
      ;;
    *)
      if [ -z "$PLACEMENT" ]; then
        if can_prompt; then
          echo ""
          echo "  Could not tell whether this host is on a home network or a public VPS."
          echo "    1) Home network — Basement Kit, local addresses recommended"
          echo "    2) Public server / VPS — Cloud Kit, kombify.me or your domain"
          _place=$(prompt_select "Select" "1")
          case "$_place" in
            2) PLACEMENT="cloud" ;;
            *) PLACEMENT="home" ;;
          esac
          echo ""
        else
          PLACEMENT="home"
          info "Could not tell home vs VPS. Continuing with Basement Kit."
        fi
      fi
      ;;
  esac
  if [ "$PLACEMENT" = "cloud" ]; then
    SELECTED_KIT="cloud-kit"
    export STACKKIT_PLACEMENT=cloud
    if [ -z "$HOMELAB_DIR_SET" ]; then
      HOMELAB_DIR="$HOME/my-cloud-homelab"
    fi
    if [ "$RESUME_EXISTING" != "1" ] && [ "$_existing_kit" != "cloud-kit" ]; then
      remove_local_stack_if_present "${HOMELAB_DIR}"
    fi
    info "Using Cloud Kit on this public host"
    curl -sSL "$STACKKIT_INSTALL_URL" | STACKKIT_NO_BANNER=1 sh -s -- cloud-kit
    if workspace_has_deployment_intent; then
      info "Preparing Cloud execution-channel access for this host"
      prepare_cloud_install_host || die "Could not prepare this host for Cloud Kit apply. Run the installer again as root on this VPS."
      ok "  Cloud execution-channel account is ready"
    fi
    case "$DOMAIN_VALUE" in
      ""|home|home.localhost|homelab|stack.home|home.lab|home.kombify)
        DOMAIN_VALUE=""
        ;;
    esac
  fi

  if [ -z "$DOMAIN_VALUE" ]; then
    if [ "$SELECTED_KIT" = "cloud-kit" ]; then
      DOMAIN_VALUE="kombify.me"
      info "No custom domain given; using kombify.me for public access"
    elif can_prompt; then
      echo ""
      echo "  How should services be reached?"
      _rec="1"
      if [ "$DETECTED_ENV" = "vps" ] || [ "$DETECTED_ENV" = "cloud" ]; then
        _rec="2"
        echo "    Local *.home addresses are not reachable on a public server."
      fi
      echo "    1) Local home addresses (*.home) — recommended on a home network"
      echo "    2) kombify.me public subdomain"
      echo "    3) Your own domain"
      _acc=$(prompt_select "Select" "$_rec")
      case "$_acc" in
        2) DOMAIN_VALUE="kombify.me" ;;
        3) DOMAIN_VALUE=$(prompt_default "Domain" "") ;;
        *) DOMAIN_VALUE="home" ;;
      esac
      echo ""
    else
      DOMAIN_VALUE="home"
    fi
  fi
  if [ -z "$DOMAIN_VALUE" ]; then
    if [ "$SELECTED_KIT" = "cloud-kit" ] || [ "$DETECTED_ENV" = "vps" ] || [ "$DETECTED_ENV" = "cloud" ]; then
      DOMAIN_VALUE="kombify.me"
      info "No custom domain given; using kombify.me for public access"
    else
      DOMAIN_VALUE="home"
    fi
  fi
  if [ "$SELECTED_KIT" = "basement-kit" ] && [ "$DOMAIN_VALUE" = "home" ]; then
    case "$DETECTED_ENV" in
      vps|cloud)
        DOMAIN_VALUE="kombify.me"
        info "Local *.home addresses are not reachable on this public server; using kombify.me"
        ;;
    esac
  fi
fi

pick_use_cases() {
  if [ "$RESUME_EXISTING" = "1" ] && ! workspace_needs_owner_identity; then
    return 0
  fi
  if [ "${STACKKIT_USE_CASES+x}" = "x" ]; then
    USE_CASES="${STACKKIT_USE_CASES}"
    [ "$USE_CASES" = "none" ] && USE_CASES=""
    return 0
  fi
  if can_prompt && command -v stackkit >/dev/null 2>&1 && stackkit use-cases pick --help >/dev/null 2>&1; then
    info "Select use cases (arrows move, space toggles, Enter confirms)"
    USE_CASES=$(stackkit use-cases pick --print </dev/tty) || die "Use-case selection canceled"
    USE_CASES=$(printf '%s\n' "$USE_CASES" | tr -d '\r' | tail -n 1)
  elif can_prompt; then
    die "Use-case selection is required before install. Install the stackkit CLI first, or set STACKKIT_USE_CASES."
  else
    USE_CASES="files,photos,vault"
  fi
}

# --- Step 2: Initialize selected kit ----------------------------------------------

pick_use_cases
if [ "$INSTALL_MODE" = "expert" ] && [ -n "$USE_CASES" ] && [ -z "$PLATFORM_SELECTION" ]; then
  PLATFORM_SELECTION=$(prompt_default "Platform adapter for the use cases (standalone-compose|komodo|coolify)" "standalone-compose")
fi
info "Step 2/5 -- Initializing $SELECTED_KIT"
mkdir -p "$HOMELAB_DIR"
cd "$HOMELAB_DIR"

if [ "$RESUME_EXISTING" = "1" ] && ! workspace_needs_owner_identity; then
  stackkit validate
  ok "  existing workspace validated in $HOMELAB_DIR"
  if [ "$RESUME_INCOMPLETE" = "1" ] && [ "$SELECTED_KIT" = "cloud-kit" ]; then
    prepare_cloud_install_host || die "Could not prepare this host for Cloud Kit apply. Run the installer again as root on this VPS."
    ok "  host prepared for Cloud Kit apply"
  fi
else
  collect_owner_identity
  # Materialize the release catalog defaults as explicit native intent.
  # Existing workspaces above keep their persisted alternatives and adapters.
  set -- init "$SELECTED_KIT" --catalog-defaults --non-interactive --owner-source=local
  if [ -n "$DOMAIN_VALUE" ]; then
    set -- "$@" --domain "$DOMAIN_VALUE"
  fi
  if [ -n "$STACK_NAME" ]; then
    set -- "$@" --name "$STACK_NAME"
  fi
  set -- "$@" --owner-email "$ADMIN_EMAIL" --owner-username "$OWNER_USERNAME"
  if [ -n "$USE_CASES" ]; then
    set -- "$@" --use-case "$USE_CASES"
    if [ -n "$PLATFORM_SELECTION" ]; then
      set -- "$@" --platform "$PLATFORM_SELECTION"
    fi
  fi
  stackkit "$@"
  stackkit validate
  ok "  $SELECTED_KIT initialized and validated in $HOMELAB_DIR"
fi


# --- Step 3: Container runtime (Docker) -----------------------------------------
# `stackkit prepare` is external-host-only on the native v2 line; the local
# container runtime is bootstrapped here. `stackkit apply` would also install
# Docker, but the shell additionally activates the docker group for this
# session so non-root runs work without a re-login.

STACKKIT_RUN="direct"
run_stackkit() {
  if [ "$STACKKIT_RUN" = "sg" ]; then
    sg docker -c "stackkit $*"
  else
    stackkit "$@"
  fi
}

report_stackkit_failure() {
  err "stackkit $* failed."
  echo "  Retry in $HOMELAB_DIR: stackkit apply"
  echo "  Inspect: stackkit verify --json"
  echo "  Logs:    stackkit logs latest --json"
  exit 1
}

run_stackkit_step() {
  run_stackkit "$@" || report_stackkit_failure "$@"
}

ensure_docker() {
  if [ "${STACKKIT_SKIP_DOCKER_BOOTSTRAP:-}" = "1" ]; then
    warn "Docker bootstrap skipped (STACKKIT_SKIP_DOCKER_BOOTSTRAP=1)."
    return 0
  fi
  if docker info >/dev/null 2>&1; then
    ok "  Docker is ready"
    return 0
  fi
  if ! command -v docker >/dev/null 2>&1; then
    info "Installing Docker (get.docker.com)"
    if [ "$(id -u)" -eq 0 ]; then
      curl -fsSL https://get.docker.com | sh || die "Docker installation failed."
    else
      curl -fsSL https://get.docker.com | sudo sh || die "Docker installation failed."
    fi
  fi
  if [ "$(id -u)" -eq 0 ]; then
    systemctl enable --now docker >/dev/null 2>&1 || true
  else
    sudo systemctl enable --now docker >/dev/null 2>&1 || true
  fi
  if docker info >/dev/null 2>&1; then
    ok "  Docker is ready"
    return 0
  fi
  if [ "$(id -u)" -ne 0 ]; then
    sudo usermod -aG docker "$(id -un)" 2>/dev/null || true
    if sg docker -c "docker info" >/dev/null 2>&1; then
      STACKKIT_RUN="sg"
      ok "  Docker is ready (docker group activated for this session; new shells have it permanently)"
      return 0
    fi
  fi
  die "Docker is installed but not reachable. Re-login (docker group membership) or start the daemon, then continue: cd $HOMELAB_DIR && stackkit generate && stackkit apply"
}

# provision_storage_roots: the Core host-bootstrap executor creates the
# spec-declared storage roots (0750) and runs as the invoking user; on
# non-root installs the parents under /opt etc. need root once. Pre-create
# them with the invoking user as owner so apply stays user-land.
provision_storage_roots() {
  [ -f "$HOMELAB_DIR/stack-spec.yaml" ] || return 0
  _roots=$(grep -o '"storage":{[^}]*}' "$HOMELAB_DIR/stack-spec.yaml" | grep -o '"/[^"]*"' | tr -d '"' || true)
  for _root in $_roots; do
    case "$_root" in
      /opt/*|/srv/*|/mnt/*|/media/*|/var/lib/stackkits/*) ;;
      *) continue ;;
    esac
    if [ "$(id -u)" -eq 0 ]; then
      install -d -m 0750 "$_root"
    else
      sudo install -d -m 0750 -o "$(id -un)" -g "$(id -gn)" "$_root"
    fi
  done
}

info "Step 3/5 -- Preparing container runtime (Docker)"
ensure_docker
provision_storage_roots
ok "  System ready"

# --- Step 4: Local stackkit-server image ---------------------------------------

info "Step 4/5 -- Configuring stackkit-server image"
configure_stackkit_server_image

# Cloudflare DNS credentials for custom public domains stay environment-driven.
if [ -z "${STACKKIT_DNS_TOKEN:-}" ] && [ -n "${CLOUDFLARE_API_TOKEN:-}" ]; then
  export STACKKIT_DNS_TOKEN="$CLOUDFLARE_API_TOKEN"
fi
if [ -z "${STACKKIT_DNS_EMAIL:-}" ] && [ -n "${CLOUDFLARE_EMAIL:-}" ]; then
  export STACKKIT_DNS_EMAIL="$CLOUDFLARE_EMAIL"
fi

persist_platform_config

# --- Step 5: Generate + Deploy ------------------------------------------------

info "Step 5/5 -- Deploying homelab stack"

# Read-only host admission first: naming an unusable device here costs seconds,
# while discovering it during Apply costs a half-mutated host.
#
# This script is served from the website and installs whichever CLI release is
# current, which may predate the admission command. Ask before calling it, so an
# older CLI simply skips the step instead of printing an unknown-command error.
PREFLIGHT_STATUS=0
set +e
if run_stackkit host preflight --help >/dev/null 2>&1; then
  run_stackkit host preflight
  PREFLIGHT_STATUS=$?
fi
set -e

# Undoable host fixes, unattended mode only.
#
# These are the fixes that carry no decision: bounded container logs, and swap
# on a host that has none. Both are reversible, neither needs a reboot or a
# credential, and each keeps a backup of what it replaced. Anything that does
# need one of those stays advice the preflight output already printed.
#
# Set STACKKIT_REMEDIATE=0 to opt out. A failure here never stops the install:
# the finding it targets was a warning, and refusing to install because an
# optional improvement did not take is the failure this admission exists to
# remove, not to cause.
if [ "$INSTALL_MODE" = "auto" ] && [ "${STACKKIT_REMEDIATE:-1}" != "0" ]; then
  set +e
  if run_stackkit host remediate --help >/dev/null 2>&1; then
    STACKKIT_BIN="$(command -v stackkit 2>/dev/null)"
    if [ "$(id -u)" -eq 0 ]; then
      stackkit host remediate --auto-reversible --yes
    elif [ -n "$STACKKIT_BIN" ] && command -v sudo >/dev/null 2>&1; then
      sudo -n "$STACKKIT_BIN" host remediate --auto-reversible --yes
    fi
    run_stackkit host preflight
    PREFLIGHT_STATUS=$?
  fi
  set -e
fi

if [ "$PREFLIGHT_STATUS" -eq 3 ]; then
  echo ""
  err "This host cannot run the StackKit; nothing was applied."
  echo ""
  echo "  To attempt the rollout anyway (it may fail on the same condition):"
  echo "    cd $HOMELAB_DIR && STACKKIT_PREFLIGHT=skip stackkit apply --auto-approve"
  echo ""
  exit 3
fi

if [ -f "$HOMELAB_DIR/.stackkit/resolved-plan.json" ] || [ -f "$HOMELAB_DIR/deploy/.stackkit/resolved-plan.json" ]; then
  info "Canonical plan already present; skipping generate"
else
  info "Preparing deployment artifacts (stackkit generate)"
  info "This can take a minute with little extra CLI output"
  run_stackkit_step generate
  ok "Deployment artifacts are ready"
fi

if [ "$INSTALL_MODE" != "auto" ]; then
  echo ""
  if ! prompt_yn "Deploy the homelab now (stackkit apply)?" "y"; then
    echo ""
    ok "Deployment intent is ready; apply skipped on request."
    echo "  Continue manually:"
    echo "    cd $HOMELAB_DIR"
    echo "    stackkit plan"
    echo "    stackkit apply"
    echo ""
    echo "  Project directory: $HOMELAB_DIR"
    exit 0
  fi
  echo ""
fi

# One Apply. The canonical ResolvedPlan owns execution order across core
# services and applications, so there is no separate platform-app stage to
# sequence from here; the previous "phase 1/2 then 2/2" pair issued the same
# full Apply twice, which only doubled the exposure to a transient failure.
#
# set -eu would abort this script mid-install on any non-zero exit, leaving the
# operator without a summary or a next step. Capture the status instead and end
# with something actionable.
info "Installing services (stackkit apply)"
info "Image download and health waits can take several minutes with little extra output"
if [ "${SELECTED_KIT:-}" = "cloud-kit" ]; then
  prepare_cloud_install_host || die "Could not prepare this host for Cloud Kit apply. Run the installer again as root on this VPS."
fi
set +e
if [ "${SELECTED_KIT:-}" = "cloud-kit" ]; then
  _ssh_user="${OWNER_USERNAME:-kombify}"
  warn "This apply hardens SSH on this host: root login is disabled and password"
  echo "  authentication is turned off. Keep this session open. Afterwards log in as"
  echo "  ${_ssh_user}:  ssh ${_ssh_user}@<this host>"
  _exec_key="$(cloud_execution_channel_key_dir)/id_ed25519"
  if [ -f "$_exec_key" ]; then
    echo "  If this host had no SSH key, use the workspace key:"
    echo "    ssh -i $_exec_key ${_ssh_user}@<this host>"
  fi
  echo ""
fi
run_stackkit apply --auto-approve
APPLY_STATUS=$?
set -e

if [ "$APPLY_STATUS" -eq 3 ]; then
  echo ""
  err "This host cannot run the StackKit."
  echo ""
  echo "  The findings above are from host admission; nothing was applied."
  echo "  Re-check them at any time:"
  echo "    cd $HOMELAB_DIR && stackkit host preflight"
  echo ""
  echo "  To attempt the rollout anyway (it may fail on the same condition):"
  echo "    cd $HOMELAB_DIR && STACKKIT_PREFLIGHT=skip stackkit apply --auto-approve"
  echo ""
  exit 3
fi

if [ "$APPLY_STATUS" -ne 0 ]; then
  echo ""
  err "The rollout did not complete."
  echo ""
  echo "  What ran, and what failed:"
  echo "    cd $HOMELAB_DIR && stackkit verify --json"
  echo "    cd $HOMELAB_DIR && stackkit logs latest --json"
  echo ""
  if [ "${SELECTED_KIT:-}" = "cloud-kit" ]; then
    echo "  Re-run the installer; it prepares this host and converges the same plan:"
    echo "    curl -sSL https://install.stackkit.cc | sh"
  else
    echo "  Applying again is safe: it converges the same plan and keeps what"
    echo "  already succeeded."
    echo "    cd $HOMELAB_DIR && stackkit apply --auto-approve"
  fi
  echo ""
  echo "  Project directory: $HOMELAB_DIR"
  exit "$APPLY_STATUS"
fi

# --- Done: print access summary -----------------------------------------------

DOMAIN_EFFECTIVE="home"
if [ -f "$HOMELAB_DIR/stack-spec.yaml" ]; then
  _d=$(grep -o '"domain":{"base":"[^"]*"' "$HOMELAB_DIR/stack-spec.yaml" | head -1 | sed -E 's/.*"base":"([^"]+)".*/\1/' || true)
  [ -n "$_d" ] && DOMAIN_EFFECTIVE="$_d"
fi

TFVARS="$HOMELAB_DIR/deploy/terraform.tfvars.json"
PAAS=""
ENABLE_HTTPS="false"
ADMIN_PASSWORD=""
# tfvars_value KEY: exact-key extraction that stays correct on single-line JSON.
tfvars_value() {
  grep -o "\"$1\"[[:space:]]*:[[:space:]]*\"[^\"]*\"" "$TFVARS" 2>/dev/null | head -1 | sed -E 's/.*"([^"]*)"$/\1/' || true
}
if [ -f "$TFVARS" ]; then
  PAAS="coolify" # Legacy tfvars use the historical default.
  _paas=$(tfvars_value paas)
  [ -n "$_paas" ] && PAAS="$_paas"
  if grep -q '"enable_https"[[:space:]]*:[[:space:]]*true' "$TFVARS"; then ENABLE_HTTPS="true"; fi
  ADMIN_PASSWORD=$(tfvars_value admin_password_plaintext)
  _admin_email=$(tfvars_value admin_email)
  [ -n "$_admin_email" ] && ADMIN_EMAIL="$_admin_email"
fi

case "$PAAS" in
  komodo) PAAS_ROUTE="komodo"; PAAS_LABEL="Komodo" ;;
  dokploy) PAAS_ROUTE="dokploy"; PAAS_LABEL="Dokploy" ;;
  coolify) PAAS_ROUTE="coolify"; PAAS_LABEL="Coolify" ;;
  *) PAAS_ROUTE=""; PAAS_LABEL="" ;;
esac

PROTO="http"
[ "$ENABLE_HTTPS" = "true" ] && PROTO="https"
DASH_URL="${PROTO}://base.${DOMAIN_EFFECTIVE}"
PAAS_URL="${PROTO}://${PAAS_ROUTE}.${DOMAIN_EFFECTIVE}"
AUTH_URL="${PROTO}://auth.${DOMAIN_EFFECTIVE}"
ID_URL="${PROTO}://id.${DOMAIN_EFFECTIVE}"

ACCESS_JSON="$HOMELAB_DIR/.stackkit/access.json"
HUB_URL=""
if [ -f "$ACCESS_JSON" ]; then
  HUB_URL=$(grep -o '"hubUrl"[[:space:]]*:[[:space:]]*"[^"]*"' "$ACCESS_JSON" | head -1 | sed -E 's/.*:[[:space:]]*"([^"]+)"/\1/' || true)
fi

SERVER_IP=$(hostname -I 2>/dev/null | awk '{print $1}' || echo "YOUR_SERVER_IP")

echo ""
ok "Your homelab is running!"
echo ""
printf '\033[38;5;208m'
echo "  Dashboard:  ${HUB_URL:-$DASH_URL}"
printf '\033[0m'
echo ""
echo "  Core services:"
echo "    ${HUB_URL:-$DASH_URL}    Base hub"
if [ -n "$PAAS_ROUTE" ]; then
  echo "    ${PAAS_URL}    ${PAAS_LABEL} controller"
fi
echo "    ${AUTH_URL}    Authentication"
echo "    ${ID_URL}    Identity (PocketID)"
echo ""
echo "  Initial admin login credentials:"
if [ -f "$TFVARS" ]; then
  echo "    Use for: TinyAuth gateway and ${PAAS_LABEL} initial admin"
  echo "    Email:    ${ADMIN_EMAIL:-generated (see $TFVARS)}"
  if [ -n "$ADMIN_PASSWORD" ]; then
    echo "    Password: ${ADMIN_PASSWORD}"
  fi
else
  # Native v2: TinyAuth federates to PocketID; there is no shared admin
  # password. The owner identity is a passkey created at first login.
  echo "    Identity: PocketID passkey (no shared admin password on native v2)"
  echo "    Create it at ${ID_URL}/setup, then sign in at ${AUTH_URL}"
  if [ -n "${ADMIN_EMAIL:-}" ]; then
    echo "    Owner email: ${ADMIN_EMAIL}"
  fi
  if [ -n "${OWNER_USERNAME:-}" ]; then
    echo "    Login username: ${OWNER_USERNAME}"
  fi
fi
echo ""
echo "  Next steps:"
if [ "${BOOTSTRAP_OWNER:-}" = "true" ]; then
  echo "    1. Complete the one-time PocketID owner setup URL printed above"
else
  echo "    1. Create your PocketID admin passkey at ${ID_URL}/setup"
fi
echo "    2. Sign in at ${AUTH_URL}"
echo "    3. Open ${HUB_URL:-$DASH_URL} and protect the hub after owner setup"
echo ""
case "$DOMAIN_EFFECTIVE" in
  localhost|*.localhost)
    echo "  These .localhost links open on the server itself."
    echo "  Access from another device requires a configured LAN domain and DNS."
    echo ""
    ;;
  *.local|*.lab|*.lan|*.home|*.internal|*.test|home|homelab)
    echo "  Local DNS: resolve *.${DOMAIN_EFFECTIVE} to this host inside your network."
    echo "  Temporary workstation hosts entries:"
    echo "    ${SERVER_IP}  base.${DOMAIN_EFFECTIVE} auth.${DOMAIN_EFFECTIVE} id.${DOMAIN_EFFECTIVE}${PAAS_ROUTE:+ ${PAAS_ROUTE}.${DOMAIN_EFFECTIVE}}"
    echo ""
    ;;
esac
echo "  Commands:"
echo "    stackkit logs latest --json  Inspect local rollout evidence"
echo "    cat $ACCESS_JSON             Read service URLs and setup evidence"
echo "    stackkit remove        Tear down everything"
echo ""
if [ -f "$ACCESS_JSON" ]; then
  echo "  Machine-readable access summary:"
  echo "    $ACCESS_JSON"
  echo ""
fi
echo "  Project directory: $HOMELAB_DIR"
echo ""
