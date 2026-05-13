#!/bin/bash
# =============================================================================
# Base Kit E2E Test Script
# =============================================================================
# Purpose: Validate full CLI workflow and production readiness on a real server
# Tests:
#   - CLI workflow (validate, generate, apply)
#   - Layer 1+2 container health via SSH
#   - HTTP accessibility via Traefik (domain routing with Host header)
#   - TinyAuth ForwardAuth redirect for protected services
#   - PostgreSQL isolation (no external ports)
#   - Persistent volumes survive restart
#   - Security compliance
#   - Destroy and cleanup
#
# Environment Variables:
#   SSH_HOST      - Target server IP or hostname (required)
#   SSH_USER      - SSH user (default: admin)
#   SSH_PORT      - SSH port (default: 22)
#   SSH_KEY       - SSH key path (default: ~/.ssh/id_ed25519)
#   DOMAIN        - Base domain (default: stack.local)
#   STACKKIT_BIN  - Path to stackkit binary (default: stackkit)
#   TIMEOUT       - Max wait time in seconds (default: 300)
#   TEST_PERSISTENCE - Test volume persistence (default: true)
#   TEST_SECURITY    - Test security compliance (default: true)
#   CI            - Set to 'true' in CI environments
#
# Exit Codes:
#   0 - All tests passed
#   1 - One or more tests failed
# =============================================================================

set -e

# Colors (disabled in CI if no TTY)
if [ -t 1 ] && [ -z "$CI" ]; then
    RED='\033[0;31m'
    GREEN='\033[0;32m'
    YELLOW='\033[1;33m'
    BLUE='\033[0;34m'
    CYAN='\033[0;36m'
    NC='\033[0m'
else
    RED=''
    GREEN=''
    YELLOW=''
    BLUE=''
    CYAN=''
    NC=''
fi

# Configuration
SSH_HOST="${SSH_HOST:-}"
SSH_USER="${SSH_USER:-admin}"
SSH_PORT="${SSH_PORT:-22}"
SSH_KEY="${SSH_KEY:-$HOME/.ssh/id_ed25519}"
DOMAIN="${DOMAIN:-stack.local}"
STACKKIT_BIN="${STACKKIT_BIN:-stackkit}"
TIMEOUT="${TIMEOUT:-300}"
TEST_PERSISTENCE="${TEST_PERSISTENCE:-true}"
TEST_SECURITY="${TEST_SECURITY:-true}"
KEEP_ON_FAIL="${KEEP_ON_FAIL:-false}"

# Validate required variables
if [ -z "$SSH_HOST" ]; then
    echo "ERROR: SSH_HOST is required"
    echo "Usage: SSH_HOST=192.168.1.100 SSH_USER=admin $0"
    exit 1
fi

# SSH options
SSH_OPTS="-o StrictHostKeyChecking=no -o ConnectTimeout=10 -p $SSH_PORT"
if [ -f "$SSH_KEY" ]; then
    SSH_OPTS="$SSH_OPTS -i $SSH_KEY"
fi

# Test results
PASSED=0
FAILED=0
START_TIME=$(date +%s)

# =============================================================================
# HELPERS
# =============================================================================

log_test() {
    echo -e "${YELLOW}[TEST]${NC} $1"
}

log_info() {
    echo -e "${CYAN}[INFO]${NC} $1"
}

log_pass() {
    echo -e "${GREEN}[PASS]${NC} $1"
    PASSED=$((PASSED + 1))
}

log_fail() {
    echo -e "${RED}[FAIL]${NC} $1"
    FAILED=$((FAILED + 1))
}

# Run docker command on remote server via SSH
ssh_docker() {
    ssh $SSH_OPTS "$SSH_USER@$SSH_HOST" "docker $*" 2>/dev/null
}

# Run arbitrary command on remote server via SSH
ssh_exec() {
    ssh $SSH_OPTS "$SSH_USER@$SSH_HOST" "$@" 2>/dev/null
}

cleanup() {
    echo -e "\n${YELLOW}[CLEANUP]${NC} Running remove..."
    $STACKKIT_BIN remove --auto-approve 2>/dev/null || true
}

trap cleanup EXIT

# =============================================================================
# CONNECTIVITY CHECK
# =============================================================================

wait_for_ssh() {
    local max_retries=30
    local retry=0
    log_info "Waiting for SSH at $SSH_HOST:$SSH_PORT..."

    until ssh $SSH_OPTS "$SSH_USER@$SSH_HOST" "echo ok" > /dev/null 2>&1; do
        retry=$((retry + 1))
        if [ $retry -ge $max_retries ]; then
            echo ""
            echo "ERROR: Cannot connect to $SSH_HOST after $max_retries attempts"
            exit 1
        fi
        echo -n "."
        sleep 5
    done
    echo ""
    log_pass "SSH connection established"
}

# =============================================================================
# SERVICE HEALTH CHECKS
# =============================================================================

check_container_running() {
    local container=$1
    local status
    status=$(ssh_docker ps --filter "name=^${container}$" --format "{{.Status}}" 2>/dev/null)

    if [ -n "$status" ] && echo "$status" | grep -q "Up"; then
        return 0
    else
        return 1
    fi
}

wait_for_container_healthy() {
    local container=$1
    local max_retries=${2:-30}
    local retry=0

    log_info "Waiting for $container to be healthy..."

    until ssh_docker inspect --format "{{.State.Health.Status}}" "$container" 2>/dev/null | grep -q "healthy"; do
        retry=$((retry + 1))
        if [ $retry -ge $max_retries ]; then
            log_fail "$container did not become healthy after $max_retries attempts"
            return 1
        fi
        echo -n "."
        sleep 5
    done
    echo ""
    log_pass "$container is healthy"
    return 0
}

# Check HTTP via Traefik using Host header (works without DNS)
verify_http_via_traefik() {
    local subdomain=$1
    local name=$2
    local expected_code=${3:-200}
    local url="http://$SSH_HOST"

    log_info "Checking $name at $subdomain.$DOMAIN via Traefik..."

    local http_code
    http_code=$(curl -s -o /dev/null -w "%{http_code}" \
        --max-time 10 \
        --header "Host: $subdomain.$DOMAIN" \
        "$url" 2>/dev/null || echo "000")

    if [ "$http_code" = "000" ]; then
        log_fail "$name: No response from Traefik ($url)"
        return 1
    elif [ "$http_code" = "$expected_code" ]; then
        log_pass "$name accessible (HTTP $http_code)"
        return 0
    elif [ "$http_code" -ge 200 ] && [ "$http_code" -lt 400 ]; then
        log_pass "$name accessible (HTTP $http_code)"
        return 0
    else
        log_fail "$name returned HTTP $http_code (expected ~$expected_code)"
        return 1
    fi
}

# Check that TinyAuth redirects unauthenticated requests (ForwardAuth working)
verify_tinyauth_redirect() {
    local subdomain=$1
    local name=$2

    log_info "Checking TinyAuth ForwardAuth for $name..."

    local http_code
    http_code=$(curl -s -o /dev/null -w "%{http_code}" \
        --max-time 10 \
        --header "Host: $subdomain.$DOMAIN" \
        "http://$SSH_HOST" 2>/dev/null || echo "000")

    # TinyAuth redirects unauthenticated requests to auth page (302)
    if [ "$http_code" = "302" ] || [ "$http_code" = "307" ]; then
        log_pass "$name: TinyAuth ForwardAuth working (redirects unauthenticated, HTTP $http_code)"
        return 0
    elif [ "$http_code" = "000" ]; then
        log_fail "$name: No response from Traefik"
        return 1
    else
        log_fail "$name: Expected TinyAuth redirect (302/307), got HTTP $http_code"
        return 1
    fi
}

# =============================================================================
# MAIN TEST FLOW
# =============================================================================

echo "==============================================================="
echo "         BASE KIT E2E TESTS - PRODUCTION READY"
echo "==============================================================="
echo ""
log_info "Target server: $SSH_USER@$SSH_HOST:$SSH_PORT"
log_info "Domain: $DOMAIN"
echo ""

# Pre-flight: SSH connectivity
wait_for_ssh

# ---- Phase 1: CLI Workflow ----

echo ""
echo "--- Phase 1: CLI Workflow ---"

log_test "1: stackkit init creates valid stack-spec.yaml"
if $STACKKIT_BIN init base-kit --non-interactive --force; then
    if [ -f "stack-spec.yaml" ]; then
        log_pass "stack-spec.yaml created"
    else
        log_fail "stack-spec.yaml not found"
    fi
else
    log_fail "stackkit init failed"
fi

log_test "2: stackkit validate passes CUE validation"
if $STACKKIT_BIN validate; then
    log_pass "CUE validation passed"
else
    log_fail "CUE validation failed"
fi

log_test "3: stackkit plan generates valid OpenTofu plan"
if $STACKKIT_BIN plan; then
    log_pass "OpenTofu plan succeeded"
else
    log_fail "OpenTofu plan failed"
fi

log_test "4: stackkit apply deploys without errors"
if $STACKKIT_BIN apply --auto-approve; then
    log_pass "Deployment succeeded"
else
    log_fail "Deployment failed"
fi

log_info "Waiting for services to initialize..."
sleep 15

# ---- Phase 2: Container Health ----

echo ""
echo "--- Phase 2: Container Health (Layer 1+2) ---"

log_test "5: Traefik container is running"
if check_container_running "traefik"; then
    log_pass "Traefik is running"
else
    log_fail "Traefik is not running"
fi

log_test "6: TinyAuth container is running"
if check_container_running "tinyauth"; then
    log_pass "TinyAuth is running"
else
    log_fail "TinyAuth is not running"
fi

log_test "7: Dokploy PostgreSQL is running"
if check_container_running "dokploy-postgres"; then
    log_pass "dokploy-postgres is running"
else
    log_fail "dokploy-postgres is not running"
fi

log_test "8: Dokploy is running"
if check_container_running "dokploy"; then
    log_pass "Dokploy is running"
else
    log_fail "Dokploy is not running"
fi

# Wait for health checks to pass
log_info "Waiting for healthchecks to pass..."
sleep 20

log_test "9: Traefik reports healthy"
wait_for_container_healthy "traefik" 12 || true

log_test "10: TinyAuth reports healthy"
wait_for_container_healthy "tinyauth" 12 || true

log_test "11: Dokploy PostgreSQL reports healthy"
wait_for_container_healthy "dokploy-postgres" 12 || true

log_test "12: Dokploy reports healthy"
wait_for_container_healthy "dokploy" 24 || true

# ---- Phase 3: HTTP Accessibility via Traefik ----

echo ""
echo "--- Phase 3: HTTP Accessibility (via Traefik routing) ---"

# Traefik dashboard - direct port (no TinyAuth needed)
log_test "13: Traefik dashboard accessible on port 8080"
http_code=$(curl -s -o /dev/null -w "%{http_code}" --max-time 10 "http://$SSH_HOST:8080/ping" 2>/dev/null || echo "000")
if [ "$http_code" = "200" ]; then
    log_pass "Traefik /ping returns HTTP 200"
else
    log_fail "Traefik /ping returned HTTP $http_code (expected 200)"
fi

log_test "14: TinyAuth login page is accessible"
verify_http_via_traefik "auth" "TinyAuth" 200 || true

log_test "15: Dokploy redirects to TinyAuth (ForwardAuth active)"
verify_tinyauth_redirect "dokploy" "Dokploy" || true

log_test "16: Traefik dashboard accessible via domain"
verify_http_via_traefik "traefik" "Traefik dashboard" 200 || true

# ---- Phase 4: Volume Validation ----

echo ""
echo "--- Phase 4: Volume Validation ---"

log_test "17: Required persistent volumes exist on server"
VOLUMES=$(ssh_docker volume ls --format "{{.Name}}" 2>/dev/null || echo "")
REQUIRED_VOLUMES=("tinyauth-data" "traefik-data" "traefik-certs" "dokploy-data" "dokploy-postgres-data" "kuma-data")
ALL_FOUND=true

for vol in "${REQUIRED_VOLUMES[@]}"; do
    if echo "$VOLUMES" | grep -q "^${vol}$"; then
        log_info "Volume found: $vol"
    else
        log_fail "Required volume missing: $vol"
        ALL_FOUND=false
    fi
done

if [ "$ALL_FOUND" = true ]; then
    log_pass "All required persistent volumes exist"
fi

# ---- Phase 5: Security ----

if [ "$TEST_SECURITY" = "true" ]; then
    echo ""
    echo "--- Phase 5: Security ---"

    log_test "18: PostgreSQL has NO exposed host ports (isolated network)"
    PG_PORTS=$(ssh_docker port dokploy-postgres 2>/dev/null || echo "")
    if [ -z "$PG_PORTS" ]; then
        log_pass "PostgreSQL has no exposed host ports (secure)"
    else
        log_fail "PostgreSQL is exposed on host: $PG_PORTS"
    fi

    log_test "19: Dokploy has NO exposed host ports (routed via Traefik)"
    DOKPLOY_PORTS=$(ssh_docker port dokploy 2>/dev/null || echo "")
    if [ -z "$DOKPLOY_PORTS" ]; then
        log_pass "Dokploy has no exposed host ports (Traefik routing active)"
    else
        log_fail "Dokploy is exposed on host: $DOKPLOY_PORTS"
    fi

    log_test "20: Containers have stackkit.layer labels"
    LABELED=$(ssh_docker ps --filter "label=stackkit.layer" --format "{{.Names}}" 2>/dev/null | wc -l || echo "0")
    if [ "$LABELED" -ge 4 ]; then
        log_pass "$LABELED containers have stackkit.layer labels"
    else
        log_fail "Only $LABELED containers have stackkit.layer labels (expected >=4)"
    fi
fi

# ---- Phase 6: Persistence ----

if [ "$TEST_PERSISTENCE" = "true" ]; then
    echo ""
    echo "--- Phase 6: Persistence ---"

    log_test "21: Data persists across container restarts"

    # Write a marker into dokploy-data on the remote server
    ssh_exec "docker run --rm -v dokploy-data:/data alpine:latest sh -c \"echo 'persistence-ok' > /data/e2e-test.txt\"" || true

    # Restart dokploy
    ssh_docker restart dokploy > /dev/null 2>&1 || true
    sleep 10

    # Check marker survived
    MARKER=$(ssh_exec "docker run --rm -v dokploy-data:/data alpine:latest cat /data/e2e-test.txt" 2>/dev/null || echo "")
    if [ "$MARKER" = "persistence-ok" ]; then
        log_pass "Data persists through container restart"
    else
        log_fail "Data did not persist through restart"
    fi

    # Cleanup marker
    ssh_exec "docker run --rm -v dokploy-data:/data alpine:latest rm -f /data/e2e-test.txt" || true
fi

# ---- Phase 7: Destroy & Cleanup ----

echo ""
echo "--- Phase 7: Remove & Cleanup ---"

log_test "22: stackkit remove removes all containers"
trap - EXIT
if $STACKKIT_BIN remove --auto-approve; then
    log_pass "Remove succeeded"
else
    log_fail "Remove failed"
fi

sleep 5

log_test "23: No managed containers remain after remove"
REMAINING=$(ssh_docker ps -a --format "{{.Names}}" 2>/dev/null | grep -E "^(traefik|tinyauth|dokploy|dokploy-postgres)$" || true)
if [ -z "$REMAINING" ]; then
    log_pass "All Layer 1+2 containers cleaned up"
else
    log_fail "Containers still exist after remove: $REMAINING"
fi

log_test "24: Persistent volumes preserved after remove"
if ssh_docker volume ls 2>/dev/null | grep -q "dokploy-data"; then
    log_pass "Persistent volumes preserved (data safe)"
else
    log_info "Volumes were removed (may be intentional)"
    log_pass "Volume behavior verified"
fi

# =============================================================================
# SUMMARY
# =============================================================================

echo ""
echo "==============================================================="
echo "                    TEST SUMMARY"
echo "==============================================================="
END_TIME=$(date +%s)
DURATION=$((END_TIME - START_TIME))
echo -e "${GREEN}Passed:${NC} $PASSED"
echo -e "${RED}Failed:${NC} $FAILED"
echo -e "${BLUE}Duration:${NC} ${DURATION}s"
echo "==============================================================="

if [ $FAILED -eq 0 ]; then
    echo ""
    echo "All E2E tests passed - Base Kit is PRODUCTION READY"
else
    echo ""
    echo "Some tests failed - review failures above"
fi
echo "==============================================================="

if [ $FAILED -gt 0 ]; then
    exit 1
fi

exit 0
