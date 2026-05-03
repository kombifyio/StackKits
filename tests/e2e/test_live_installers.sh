#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${STACKKIT_BASE_URL:-https://stackkit.cc}"
WAIT_TIMEOUT_SECONDS="${STACKKIT_WAIT_TIMEOUT_SECONDS:-600}"
WAIT_INTERVAL_SECONDS="${STACKKIT_WAIT_INTERVAL_SECONDS:-10}"
CHECK_LEGACY_REDIRECTS="${STACKKIT_CHECK_LEGACY_REDIRECTS:-1}"
SMOKE_IMAGE_TAG="stackkit-live-installer-smoke:$RANDOM-$$"

pass() { printf '\033[1;32m  PASS: %s\033[0m\n' "$*"; }
fail() { printf '\033[1;31m  FAIL: %s\033[0m\n' "$*" >&2; exit 1; }
info() { printf '\033[1;34m  INFO: %s\033[0m\n' "$*"; }

cleanup() {
  docker image rm -f "$SMOKE_IMAGE_TAG" >/dev/null 2>&1 || true
}

trap cleanup EXIT

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    fail "Required command not found: $1"
  fi
}

build_smoke_image() {
  info "Building disposable Ubuntu smoke image"
  docker build -t "$SMOKE_IMAGE_TAG" - >/dev/null <<'EOF'
FROM ubuntu:24.04
RUN apt-get update \
  && DEBIAN_FRONTEND=noninteractive apt-get install -y curl ca-certificates tar \
  && rm -rf /var/lib/apt/lists/*
EOF
}

wait_for_body() {
  local url="$1"
  local needle="$2"
  local deadline=$((SECONDS + WAIT_TIMEOUT_SECONDS))
  local body=""

  while [ "$SECONDS" -lt "$deadline" ]; do
    body="$(curl -fsSL "$url" 2>/dev/null || true)"
    if printf '%s' "$body" | grep -Fq "$needle"; then
      pass "$url is live"
      return 0
    fi
    info "Waiting for $url"
    sleep "$WAIT_INTERVAL_SECONDS"
  done

  fail "$url did not serve expected content within ${WAIT_TIMEOUT_SECONDS}s"
}

assert_redirect() {
  local url="$1"
  local expected_location="$2"
  local location

  location="$(curl -sSI "$url" | tr -d '\r' | awk 'BEGIN{IGNORECASE=1} /^location:/ {print $2; exit}')"
  if [ "$location" != "$expected_location" ]; then
    fail "$url redirects to $location (expected $expected_location)"
  fi

  pass "$url redirects to $expected_location"
}

collect_docker_env_args() {
  DOCKER_ENV_ARGS=()

  for env_name in \
    STACKKIT_RELEASE_REPO \
    STACKKIT_RELEASES_PAGE_URL \
    STACKKIT_RELEASE_API_URL \
    STACKKIT_RELEASE_DOWNLOAD_BASE_URL \
    STACKKIT_INSTALL_URL; do
    if [ -n "${!env_name:-}" ]; then
      DOCKER_ENV_ARGS+=("-e" "$env_name=${!env_name}")
    fi
  done
}

smoke_cli_install() {
  info "Smoke testing $BASE_URL/install"
  docker run --rm \
    "${DOCKER_ENV_ARGS[@]}" \
    -e STACKKIT_NO_BANNER=1 \
    "$SMOKE_IMAGE_TAG" \
    bash -lc "set -euo pipefail; export HOME=/tmp/home; mkdir -p \"\$HOME\"; curl -fsSL '$BASE_URL/install' | sh; command -v stackkit >/dev/null; stackkit version >/tmp/stackkit-version.txt; grep -Fq stackkit /tmp/stackkit-version.txt"
  pass "CLI installer installs stackkit"
}

smoke_guided_entrypoint() {
  local path="$1"
  local expected_log="$2"

  info "Smoke testing $BASE_URL/$path"
  docker run --rm \
    "${DOCKER_ENV_ARGS[@]}" \
    -e STACKKIT_NO_BANNER=1 \
    "$SMOKE_IMAGE_TAG" \
    bash -s <<EOF
set -euo pipefail
mkdir -p /smoke
cat >/smoke/stackkit <<'STACKKIT_SHIM'
#!/usr/bin/env bash
set -euo pipefail

cmd="
\${1:-}"
if [ "\$#" -gt 0 ]; then
  shift
fi

case "\$cmd" in
  version)
    if [ -x /usr/local/bin/stackkit ]; then
      exec /usr/local/bin/stackkit version
    fi
    echo "stackkit shim"
    ;;
  init|prepare|generate|apply)
    printf '%s\n' "\$cmd \$*" >> /tmp/stackkit-smoke.log
    ;;
  *)
    printf '%s\n' "\$cmd \$*" >> /tmp/stackkit-smoke.log
    ;;
esac
STACKKIT_SHIM
chmod +x /smoke/stackkit
export PATH=/smoke:\$PATH
export HOME=/tmp/home
mkdir -p "\$HOME"
export HOMELAB_DIR=/tmp/$path-homelab
curl -fsSL '$BASE_URL/$path' | sh
grep -Fq '$expected_log' /tmp/stackkit-smoke.log
EOF
  pass "$path installer reaches $expected_log"
}

smoke_base_entrypoint() {
  info "Smoke testing $BASE_URL/base"
  docker run --rm \
    "${DOCKER_ENV_ARGS[@]}" \
    -e STACKKIT_NO_BANNER=1 \
    -e STACKKIT_ADMIN_EMAIL=smoke@stackkit.cc \
    "$SMOKE_IMAGE_TAG" \
    bash -s <<EOF
set -euo pipefail
mkdir -p /smoke
cat >/smoke/stackkit <<'STACKKIT_SHIM'
#!/usr/bin/env bash
set -euo pipefail

cmd="\${1:-}"
if [ "\$#" -gt 0 ]; then
  shift
fi

case "\$cmd" in
  version)
    if [ -x /usr/local/bin/stackkit ]; then
      exec /usr/local/bin/stackkit version
    fi
    echo "stackkit shim"
    ;;
  init|prepare|generate|apply)
    printf '%s\n' "\$cmd \$*" >> /tmp/stackkit-smoke.log
    ;;
  *)
    printf '%s\n' "\$cmd \$*" >> /tmp/stackkit-smoke.log
    ;;
esac
STACKKIT_SHIM
chmod +x /smoke/stackkit
export PATH=/smoke:\$PATH
export HOME=/tmp/home
mkdir -p "\$HOME"
export HOMELAB_DIR=/tmp/base-homelab
curl -fsSL '$BASE_URL/base' | sh
grep -Fq 'prepare' /tmp/stackkit-smoke.log
grep -Fq 'init base-kit --non-interactive --force --admin-email smoke@stackkit.cc' /tmp/stackkit-smoke.log
grep -Fq 'generate --force' /tmp/stackkit-smoke.log
grep -Fq 'apply --auto-approve' /tmp/stackkit-smoke.log
EOF
  pass "base installer reaches prepare/init/generate/apply"
}

main() {
  require_command curl
  require_command docker

  collect_docker_env_args
  build_smoke_image

  wait_for_body "$BASE_URL/install" "StackKits CLI installer"
  wait_for_body "$BASE_URL/base" "StackKits Base Installer"
  wait_for_body "$BASE_URL/modern" "Modern Home Lab"
  wait_for_body "$BASE_URL/ha" "High Availability Kit"

  if [ "$CHECK_LEGACY_REDIRECTS" = "1" ] && [ "$BASE_URL" = "https://stackkit.cc" ]; then
    assert_redirect "https://install.stackkit.cc" "$BASE_URL/install"
    assert_redirect "https://base.stackkit.cc" "$BASE_URL/base"
    assert_redirect "https://modern.stackkit.cc" "$BASE_URL/modern"
    assert_redirect "https://ha.stackkit.cc" "$BASE_URL/ha"
  fi

  smoke_cli_install
  smoke_guided_entrypoint modern "init modern-homelab"
  smoke_guided_entrypoint ha "init ha-kit"
  smoke_base_entrypoint

  pass "All live installer smoke tests passed"
}

main "$@"