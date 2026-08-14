#!/bin/sh
# =============================================================================
# StackKits Cloud Installer — full cloud-kit deployment in one command.
# =============================================================================
# Usage: DOMAIN=example.com curl -sSL https://cloud.stackkit.cc | sh
#
# This installer carries you through the COMPLETE installation to a running
# Cloud Kit on a public host. It is a guidance layer over the stackkit CLI:
# every decision maps to a CLI parameter (see docs/INSTALLATION_PROCESSES.md).
#
# Modes (STACKKIT_INSTALL_MODE=auto|guided|expert; interactive menu on a TTY):
#   auto    Quick Install: defaults everywhere (DOMAIN env required).
#   guided  Core decisions: workspace, domain, admin email, owner account,
#           one apply confirmation.
#   expert  Detailed: guided plus use cases, stack name, platform adapter,
#           per-phase apply confirmation.
# Non-TTY runs always use auto. Environment-provided values are never re-asked.
#
# Steps:
#   1. Install stackkit CLI + cloud-kit definitions   (via install.stackkit.cc)
#   2. Initialize cloud-kit                           (native v2 init, DOMAIN)
#   3. Prepare the container runtime (Docker install + group activation)
#   4. Build the local stackkit-server image          (registry fallback)
#   5. Generate + deploy the Cloud Kit                (two-phase apply)
#
# Environment variables (all optional except DOMAIN in non-TTY runs):
#   DOMAIN                 Required public domain for this Cloud Kit
#   STACKKIT_INSTALL_MODE  auto | guided | expert
#   HOMELAB_DIR            Workspace (default: $HOME/my-cloud-homelab)
#   STACKKIT_NAME          Deployment contract ID (default: workspace name)
#   STACKKIT_ADMIN_EMAIL   Admin/owner email (KOMBIFY_USER_EMAIL fallback)
#   STACKKIT_BOOTSTRAP_OWNER / STACKKIT_OWNER_USERNAME
#   STACKKIT_USE_CASES     Comma-separated optional workloads: photos,files,vault
#   STACKKIT_PLATFORM / STACKKIT_PAAS  Workload runtime adapter: coolify | komodo
#   STACKKIT_SERVER_IMAGE / STACKKIT_INSTALL_URL
#   CLOUDFLARE_API_TOKEN / CLOUDFLARE_EMAIL  DNS credentials for the domain
#
# Requirements: Linux host with a public IP, root/sudo access
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

info()  { printf '\033[1;34m==> %s\033[0m\n' "$*"; }
ok()    { printf '\033[1;32m==> %s\033[0m\n' "$*"; }
warn()  { printf '\033[1;33m==> %s\033[0m\n' "$*"; }
err()   { printf '\033[1;31m==> %s\033[0m\n' "$*" >&2; }
die()   { err "$*"; exit 1; }
can_prompt() { [ -t 1 ] && [ -r /dev/tty ] && [ -w /dev/tty ]; }

prompt_default() {
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
    echo "    1) Quick Install   -- defaults everywhere, straight to a running Cloud Kit"
    echo "    2) Core decisions  -- workspace, domain, admin email, owner account"
    echo "    3) Detailed        -- core decisions plus use cases, name, platform"
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
  die "STACKKIT_INSTALL_MODE=$INSTALL_MODE needs an interactive terminal. Without one, run the auto mode or drive the CLI directly: stackkit init cloud-kit --non-interactive --owner-source=local --domain <domain> && stackkit generate && stackkit apply --auto-approve"
fi
info "Install mode: $INSTALL_MODE"

# --- Decision collection --------------------------------------------------------

HOMELAB_DIR_SET="${HOMELAB_DIR:+1}"
HOMELAB_DIR="${HOMELAB_DIR:-$HOME/my-cloud-homelab}"
if [ "$INSTALL_MODE" != "auto" ] && [ -z "$HOMELAB_DIR_SET" ]; then
  HOMELAB_DIR=$(prompt_default "Workspace directory" "$HOMELAB_DIR")
fi

# Cloud Kit requires a public domain (CUE requiredOverrides network.domain.base).
if [ -z "${DOMAIN:-}" ] && can_prompt; then
  echo ""
  printf '  Public domain for this Cloud Kit: ' >/dev/tty
  read -r DOMAIN </dev/tty
  echo ""
fi
if [ -z "${DOMAIN:-}" ]; then
  die "Cloud Kit v2 requires DOMAIN (for example: DOMAIN=example.com curl -sSL https://cloud.stackkit.cc | sh)."
fi

ADMIN_EMAIL="${STACKKIT_ADMIN_EMAIL:-${KOMBIFY_USER_EMAIL:-}}"
if [ -z "$ADMIN_EMAIL" ] && [ "$INSTALL_MODE" != "auto" ]; then
  printf '  Admin email (Enter = generate a deployment-scoped one): ' >/dev/tty
  read -r ADMIN_EMAIL </dev/tty || ADMIN_EMAIL=""
fi

BOOTSTRAP_OWNER="${STACKKIT_BOOTSTRAP_OWNER:-}"
if [ -z "$BOOTSTRAP_OWNER" ] && [ -n "$ADMIN_EMAIL" ] && [ "$INSTALL_MODE" != "auto" ]; then
  if prompt_yn "Create a preconfigured StackKits owner account for $ADMIN_EMAIL?" "y"; then
    BOOTSTRAP_OWNER="true"
  else
    BOOTSTRAP_OWNER="false"
  fi
fi
[ -z "$BOOTSTRAP_OWNER" ] && BOOTSTRAP_OWNER="false"

OWNER_USERNAME="${STACKKIT_OWNER_USERNAME:-}"
if [ "$BOOTSTRAP_OWNER" = "true" ] && [ -z "$OWNER_USERNAME" ]; then
  OWNER_USERNAME=$(printf '%s' "$ADMIN_EMAIL" | sed 's/@.*//' | tr '[:upper:]' '[:lower:]' | sed 's/[^a-z0-9._-]/-/g')
  [ -z "$OWNER_USERNAME" ] && OWNER_USERNAME="admin"
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
  if [ -z "${STACKKIT_USE_CASES:-}" ]; then
    USE_CASES=$(prompt_default "Use cases to enable (photos,files,vault; Enter = all, 'none' = none)" "photos,files,vault")
    [ "$USE_CASES" = "none" ] && USE_CASES=""
  fi
  if [ -n "$USE_CASES" ] && [ -z "$PLATFORM_SELECTION" ]; then
    PLATFORM_SELECTION=$(prompt_default "Platform adapter for the use cases (coolify|komodo)" "coolify")
    [ "$PLATFORM_SELECTION" = "coolify" ] && PLATFORM_SELECTION=""
  fi
fi

case "$PLATFORM_SELECTION" in
  ""|coolify|komodo|standalone-compose) ;;
  dokploy) die "Platform 'dokploy' is draft-only and not installable through this installer." ;;
  *) die "Unsupported platform '$PLATFORM_SELECTION'. Expected coolify or komodo." ;;
esac

# Custom public domains need DNS automation credentials for TLS.
if [ "$INSTALL_MODE" != "auto" ] && [ -z "${CLOUDFLARE_API_TOKEN:-}" ] && [ -z "${STACKKIT_DNS_TOKEN:-}" ]; then
  printf '  Cloudflare API token for %s (Enter = skip DNS automation): ' "$DOMAIN" >/dev/tty
  read -r CLOUDFLARE_API_TOKEN </dev/tty || CLOUDFLARE_API_TOKEN=""
fi

if [ -n "${STACKKIT_MODE:-}" ]; then
  warn "STACKKIT_MODE is a retired v0.6 knob; native v2 install mode is CUE-owned (ignored)."
fi
if [ -n "${STACKKIT_SERVICE_PROFILE:-}" ]; then
  warn "STACKKIT_SERVICE_PROFILE is a retired v0.6 knob; select use cases instead (ignored)."
fi

# --- Existing-deployment guard ----------------------------------------------------

if [ -f "$HOMELAB_DIR/.stackkit/resolved-plan.json" ] || [ -f "$HOMELAB_DIR/stack-spec.yaml" ]; then
  die "Workspace $HOMELAB_DIR already carries a StackKits deployment intent. Reset it first (cd $HOMELAB_DIR && stackkit remove), pick another HOMELAB_DIR, or continue with the CLI in that directory instead of reinstalling."
fi

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

# --- Step 1: Install CLI + cloud-kit definitions -------------------------------

info "Step 1/5 -- Installing stackkit CLI + cloud-kit"
STACKKIT_INSTALL_URL="${STACKKIT_INSTALL_URL:-https://install.stackkit.cc}"
curl -sSL "$STACKKIT_INSTALL_URL" | STACKKIT_NO_BANNER=1 sh -s -- cloud-kit
ok "  stackkit $(stackkit version 2>/dev/null | head -1) installed"

# --- Step 2: Initialize cloud-kit -------------------------------------------------

info "Step 2/5 -- Initializing cloud-kit"
mkdir -p "$HOMELAB_DIR"
cd "$HOMELAB_DIR"

set -- init cloud-kit --non-interactive --owner-source=local --domain "$DOMAIN"
if [ -n "$STACK_NAME" ]; then
  set -- "$@" --name "$STACK_NAME"
fi
if [ "$BOOTSTRAP_OWNER" = "true" ]; then
  set -- "$@" --owner-email "$ADMIN_EMAIL" --owner-username "$OWNER_USERNAME"
elif [ -n "$ADMIN_EMAIL" ]; then
  set -- "$@" --owner-email "$ADMIN_EMAIL"
fi
if [ -n "$USE_CASES" ]; then
  set -- "$@" --use-case "$USE_CASES"
  if [ -n "$PLATFORM_SELECTION" ]; then
    set -- "$@" --platform "$PLATFORM_SELECTION"
  fi
fi
stackkit "$@"
stackkit validate
ok "  cloud-kit initialized and validated in $HOMELAB_DIR"


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

if [ -z "${STACKKIT_DNS_TOKEN:-}" ] && [ -n "${CLOUDFLARE_API_TOKEN:-}" ]; then
  export STACKKIT_DNS_TOKEN="$CLOUDFLARE_API_TOKEN"
fi
if [ -z "${STACKKIT_DNS_EMAIL:-}" ] && [ -n "${CLOUDFLARE_EMAIL:-}" ]; then
  export STACKKIT_DNS_EMAIL="$CLOUDFLARE_EMAIL"
fi

# --- Step 5: Generate + Deploy ------------------------------------------------

info "Step 5/5 -- Deploying Cloud Kit"
run_stackkit generate

if [ "$INSTALL_MODE" != "auto" ]; then
  echo ""
  if ! prompt_yn "Deploy the Cloud Kit now (stackkit apply)?" "y"; then
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

if [ "$INSTALL_MODE" = "expert" ]; then
  info "Apply phase 1/2 -- core services"
fi
run_stackkit apply --auto-approve --skip-platform-apps
if [ "$INSTALL_MODE" = "expert" ]; then
  info "Apply phase 2/2 -- platform applications"
  if ! prompt_yn "Continue with platform applications?" "y"; then
    warn "Platform applications skipped; run 'stackkit apply' later to complete."
  else
    run_stackkit apply --auto-approve
  fi
else
  run_stackkit apply --auto-approve
fi

# --- Done: print access summary -----------------------------------------------

TFVARS="$HOMELAB_DIR/deploy/terraform.tfvars.json"
PAAS="coolify"
ADMIN_PASSWORD=""
# tfvars_value KEY: exact-key extraction that stays correct on single-line JSON.
tfvars_value() {
  grep -o "\"$1\"[[:space:]]*:[[:space:]]*\"[^\"]*\"" "$TFVARS" 2>/dev/null | head -1 | sed -E 's/.*"([^"]*)"$/\1/' || true
}
if [ -f "$TFVARS" ]; then
  _paas=$(tfvars_value paas)
  [ -n "$_paas" ] && PAAS="$_paas"
  ADMIN_PASSWORD=$(tfvars_value admin_password_plaintext)
  _admin_email=$(tfvars_value admin_email)
  [ -n "$_admin_email" ] && ADMIN_EMAIL="$_admin_email"
fi

case "$PAAS" in
  komodo) PAAS_ROUTE="komodo"; PAAS_LABEL="Komodo" ;;
  dokploy) PAAS_ROUTE="dokploy"; PAAS_LABEL="Dokploy" ;;
  *) PAAS_ROUTE="coolify"; PAAS_LABEL="Coolify" ;;
esac

DASH_URL="https://base.${DOMAIN}"
PAAS_URL="https://${PAAS_ROUTE}.${DOMAIN}"
AUTH_URL="https://auth.${DOMAIN}"
ID_URL="https://id.${DOMAIN}"

ACCESS_JSON="$HOMELAB_DIR/.stackkit/access.json"
HUB_URL=""
if [ -f "$ACCESS_JSON" ]; then
  HUB_URL=$(grep -o '"hubUrl":"[^"]*"' "$ACCESS_JSON" | head -1 | sed -E 's/.*:"([^"]+)"/\1/' || true)
fi

echo ""
ok "Your homelab is running!"
echo ""
printf '\033[38;5;208m'
echo "  Dashboard:  ${HUB_URL:-$DASH_URL}"
printf '\033[0m'
echo ""
echo "  Core services at *.${DOMAIN}:"
echo "    ${HUB_URL:-$DASH_URL}    Base hub"
echo "    ${PAAS_URL}    ${PAAS_LABEL} controller"
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
fi
echo ""
echo "  Next steps:"
echo "    1. Point DNS for *.${DOMAIN} at this host (unless automated via Cloudflare)"
if [ "$BOOTSTRAP_OWNER" = "true" ]; then
  echo "    2. Complete the one-time PocketID owner setup URL printed above"
else
  echo "    2. Create your PocketID admin passkey at ${ID_URL}/setup"
fi
echo "    3. Sign in at ${AUTH_URL}"
echo ""
echo "  Commands:"
echo "    stackkit status        Check service health"
echo "    stackkit verify --http Run HTTP route checks"
echo "    stackkit remove        Tear down everything"
echo ""
if [ -f "$ACCESS_JSON" ]; then
  echo "  Machine-readable access summary:"
  echo "    $ACCESS_JSON"
  echo ""
fi
echo "  Project directory: $HOMELAB_DIR"
echo ""
