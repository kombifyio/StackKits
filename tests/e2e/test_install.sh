#!/usr/bin/env bash
# =============================================================================
# E2E Install Test — verifies the full installer flow in a clean container
# =============================================================================
# Usage:
#   ./tests/e2e/test_install.sh              # test latest release
#   ./tests/e2e/test_install.sh local        # test local build (skips download)
#
# Requires: Docker
# =============================================================================
set -eu

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
CONTAINER_NAME="stackkit-e2e-install-$$"
MODE="${1:-release}"
FAILED=0

# --- Helpers ----------------------------------------------------------------

pass() { printf '\033[1;32m  PASS: %s\033[0m\n' "$*"; }
fail() { printf '\033[1;31m  FAIL: %s\033[0m\n' "$*"; FAILED=$((FAILED + 1)); }
info() { printf '\033[1;34m  INFO: %s\033[0m\n' "$*"; }

cleanup() {
  info "Cleaning up container: $CONTAINER_NAME"
  docker rm -f "$CONTAINER_NAME" >/dev/null 2>&1 || true
}
trap cleanup EXIT

# --- Start clean container --------------------------------------------------

info "Starting clean Ubuntu 24.04 container"
docker run -d --name "$CONTAINER_NAME" \
  --privileged \
  -v /var/run/docker.sock:/var/run/docker.sock \
  ubuntu:24.04 \
  sleep infinity >/dev/null

docker exec "$CONTAINER_NAME" bash -c \
  "apt-get update -qq && apt-get install -y -qq curl sudo docker.io >/dev/null 2>&1"

# --- Test 1: Archive contents (release mode only) ---------------------------

if [ "$MODE" = "release" ]; then
  info "Test: Release archive contains required files"

  LATEST=$(docker exec "$CONTAINER_NAME" bash -c \
    "curl -sSL 'https://api.github.com/repos/kombifyio/stackKits/releases/latest' \
    | grep '\"tag_name\"' | head -1 | sed -E 's/.*\"v([^\"]+)\".*/\1/'" 2>&1)

  if [ -z "$LATEST" ]; then
    fail "Could not determine latest release version"
  else
    info "Latest version: v${LATEST}"

    docker exec "$CONTAINER_NAME" bash -c \
      "curl -sSL 'https://github.com/kombifyio/stackKits/releases/download/v${LATEST}/stackkits_${LATEST}_linux_amd64.tar.gz' \
      -o /tmp/release.tar.gz && tar tzf /tmp/release.tar.gz > /tmp/archive-files.txt"

    ARCHIVE_FILES=$(docker exec "$CONTAINER_NAME" cat /tmp/archive-files.txt)

    for f in \
      stackkit \
      "base-kit/stackkit.yaml" \
      "base-kit/services.cue" \
      "base-kit/defaults.cue" \
      "base-kit/templates/simple/main.tf" \
      "base/stackkit.cue" \
      "base/layers.cue"; do
      if echo "$ARCHIVE_FILES" | grep -q "^${f}$"; then
        pass "Archive contains: $f"
      else
        fail "Archive missing: $f"
      fi
    done
  fi
fi

# --- Test 2: Installer steps 1-3 (download + prepare + init) ---------------

info "Test: Installer steps 1-3 (CLI install, prepare, init)"

if [ "$MODE" = "local" ]; then
  info "Building local binary..."
  (cd "$REPO_ROOT" && CGO_ENABLED=0 go build -o /tmp/stackkit-e2e ./cmd/stackkit)
  docker cp /tmp/stackkit-e2e "$CONTAINER_NAME":/usr/local/bin/stackkit
  # Copy kit definitions
  docker exec "$CONTAINER_NAME" mkdir -p /root/.stackkits
  docker cp "$REPO_ROOT/base-kit" "$CONTAINER_NAME":/root/.stackkits/base-kit
  docker cp "$REPO_ROOT/base" "$CONTAINER_NAME":/root/.stackkits/base
else
  docker cp "$REPO_ROOT/base-install.sh" "$CONTAINER_NAME":/tmp/base-install.sh
  # Only run steps 1-3 by patching out the deploy step
  docker exec "$CONTAINER_NAME" bash -c "
    sed '/^# --- Step 4/,\$d' /tmp/base-install.sh > /tmp/install-partial.sh
    echo 'echo INSTALL_PARTIAL_DONE' >> /tmp/install-partial.sh
  "
  docker exec -e STACKKIT_ADMIN_EMAIL=e2e-test@example.com "$CONTAINER_NAME" \
    sh /tmp/install-partial.sh 2>&1 | tail -5
fi

# Verify binary
if docker exec "$CONTAINER_NAME" stackkit version >/dev/null 2>&1; then
  pass "stackkit binary installed and executable"
else
  fail "stackkit binary not working"
fi

# Verify OpenTofu
if docker exec "$CONTAINER_NAME" tofu version >/dev/null 2>&1; then
  pass "StackKit-packaged OpenTofu installed"
else
  fail "StackKit-packaged OpenTofu not installed"
fi

# --- Test 3: Init with admin email ------------------------------------------

info "Test: stackkit init with --admin-email"

docker exec "$CONTAINER_NAME" bash -c "
  mkdir -p /root/test-homelab && cd /root/test-homelab &&
  stackkit init base-kit --non-interactive --force --admin-email e2e@test.com
" 2>&1 | tail -3

# Verify spec file
SPEC=$(docker exec "$CONTAINER_NAME" cat /root/test-homelab/stack-spec.yaml 2>&1)
if echo "$SPEC" | grep -q "adminEmail: e2e@test.com"; then
  pass "stack-spec.yaml has correct adminEmail"
else
  fail "stack-spec.yaml missing or wrong adminEmail"
fi

# --- Test 4: Generate produces correct tfvars --------------------------------

info "Test: stackkit generate produces correct tfvars"

docker exec "$CONTAINER_NAME" bash -c "
  cd /root/test-homelab && rm -rf deploy && stackkit generate --force
" 2>&1 | tail -3

TFVARS=$(docker exec "$CONTAINER_NAME" cat /root/test-homelab/deploy/terraform.tfvars.json 2>&1)

if echo "$TFVARS" | grep -q '"admin_email": "e2e@test.com"'; then
  pass "tfvars has admin_email"
else
  fail "tfvars missing admin_email"
fi

if echo "$TFVARS" | grep -q '"enable_dashboard": true'; then
  pass "tfvars has enable_dashboard=true"
else
  fail "tfvars missing enable_dashboard"
fi

if echo "$TFVARS" | grep -q '"tinyauth_users": "e2e@test.com:\$2a\$'; then
  pass "tfvars has bcrypt tinyauth_users (not hardcoded)"
else
  fail "tfvars has wrong tinyauth_users format"
fi

if echo "$TFVARS" | grep -q '"admin_password_plaintext"'; then
  PW_LEN=$(echo "$TFVARS" | grep '"admin_password_plaintext"' | sed -E 's/.*: *"([^"]+)".*/\1/' | wc -c)
  # 16 chars + newline = 17
  if [ "$PW_LEN" -eq 17 ]; then
    pass "admin_password_plaintext is 16 chars"
  else
    fail "admin_password_plaintext wrong length: $((PW_LEN - 1))"
  fi
else
  fail "tfvars missing admin_password_plaintext"
fi

# --- Summary ----------------------------------------------------------------

echo ""
if [ "$FAILED" -eq 0 ]; then
  printf '\033[1;32m  All tests passed!\033[0m\n'
else
  printf '\033[1;31m  %d test(s) failed!\033[0m\n' "$FAILED"
fi
echo ""

exit "$FAILED"
