#!/bin/bash
# Dashboard Module Integration Test
# Tests the service dashboard routing through Traefik in isolation
#
# Prerequisites:
#   - Docker running
#   - /etc/hosts: 127.0.0.1 dash.test.local
#   - OR: use --header "Host: ..." to simulate DNS
#
# Usage:
#   ./modules/dashboard/tests/integration_test.sh

set -euo pipefail

COMPOSE_FILE="$(dirname "$0")/reference-compose.yml"
BASE_URL="http://localhost:8896"
TRAEFIK_API="http://localhost:19096"
PASS=0
FAIL=0
TOTAL=0

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log_test() {
    TOTAL=$((TOTAL + 1))
    echo -e "${YELLOW}[TEST $TOTAL]${NC} $1"
}

log_pass() {
    PASS=$((PASS + 1))
    echo -e "${GREEN}  [PASS]${NC} $1"
}

log_fail() {
    FAIL=$((FAIL + 1))
    echo -e "${RED}  [FAIL]${NC} $1"
}

cleanup() {
    echo ""
    echo "Cleaning up..."
    docker compose -f "$COMPOSE_FILE" down -v --remove-orphans 2>/dev/null || true
}

# Cleanup on exit
trap cleanup EXIT

echo "========================================="
echo "Dashboard Module Integration Test"
echo "========================================="
echo ""

# Start services
echo "Starting services..."
docker compose -f "$COMPOSE_FILE" up -d --wait --wait-timeout 60

echo ""
echo "Waiting for Dashboard to be healthy..."
for i in $(seq 1 30); do
    STATUS=$(docker inspect --format='{{.State.Health.Status}}' test-dashboard 2>/dev/null || echo "not-found")
    if [ "$STATUS" = "healthy" ]; then
        echo "Dashboard is healthy after ${i}s"
        break
    fi
    if [ "$i" = "30" ]; then
        echo "Dashboard did not become healthy within 30s"
        echo "Container logs:"
        docker logs test-dashboard 2>&1 | tail -20
        exit 1
    fi
    sleep 1
done

# Traefik's /ping returns 200 as soon as the API is up, but the Docker
# provider discovers labels asynchronously -- there can be a several-hundred-ms
# window where HTTP requests land on an empty router table and return 404.
# Poll /api/http/routers until the dashboard router is registered before
# issuing HTTP assertions.
echo ""
echo "Waiting for Traefik to discover dashboard router..."
for i in $(seq 1 30); do
    if curl -s "$TRAEFIK_API/api/http/routers" 2>/dev/null | grep -q "dashboard@docker"; then
        echo "Traefik discovered dashboard router after ${i}s"
        break
    fi
    if [ "$i" = "30" ]; then
        echo "Traefik did not discover dashboard router within 30s"
        curl -s "$TRAEFIK_API/api/http/routers" 2>&1 | head -20
        exit 1
    fi
    sleep 1
done

echo ""
echo "--- Running Tests ---"
echo ""

# Test 1: Dashboard accessible via Traefik
log_test "Dashboard returns 200 via dash.test.local"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "Host: dash.test.local" "$BASE_URL/" 2>/dev/null || echo "000")
if [ "$HTTP_CODE" = "200" ]; then
    log_pass "dash.test.local -> HTTP $HTTP_CODE"
else
    log_fail "Expected 200, got $HTTP_CODE"
fi

# Test 2: Dashboard page contains HTML content
log_test "Dashboard page contains HTML content"
BODY=$(curl -s -H "Host: dash.test.local" "$BASE_URL/" 2>/dev/null || echo "")
if echo "$BODY" | grep -qi "<html"; then
    log_pass "Page contains HTML content"
else
    log_fail "Page does not contain HTML content"
fi

# Test 3: Dashboard page contains title
log_test "Dashboard page contains dashboard title"
if echo "$BODY" | grep -qi "homelab\|dashboard"; then
    log_pass "Page contains dashboard/homelab title"
else
    log_fail "Page does not contain expected title"
fi

# Test 4: Dashboard container health check passes
log_test "Dashboard container health check passes"
HEALTH=$(docker inspect --format='{{.State.Health.Status}}' test-dashboard 2>/dev/null || echo "unknown")
if [ "$HEALTH" = "healthy" ]; then
    log_pass "Container health: $HEALTH"
else
    log_fail "Container health: $HEALTH (expected healthy)"
fi

# Test 5: Traefik dashboard accessible
log_test "Traefik dashboard accessible on :19096"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" "$TRAEFIK_API/api/overview" 2>/dev/null || echo "000")
if [ "$HTTP_CODE" = "200" ]; then
    log_pass "Traefik dashboard -> HTTP $HTTP_CODE"
else
    log_fail "Expected 200, got $HTTP_CODE"
fi

# Test 6: Traefik has dashboard router registered
log_test "Traefik has dashboard router registered"
ROUTERS=$(curl -s "$TRAEFIK_API/api/http/routers" 2>/dev/null || echo "")
if echo "$ROUTERS" | grep -q "dashboard"; then
    log_pass "dashboard router found in Traefik"
else
    log_fail "dashboard router NOT found in Traefik"
fi

# --- Security Hardening ---

log_test "Dashboard has no-new-privileges"
SEC_OPTS=$(docker inspect --format='{{.HostConfig.SecurityOpt}}' test-dashboard 2>/dev/null || echo "[]")
if echo "$SEC_OPTS" | grep -q "no-new-privileges"; then
    log_pass "no-new-privileges set"
else
    log_fail "no-new-privileges missing ($SEC_OPTS)"
fi

log_test "Dashboard has cap_drop ALL"
CAP_DROP=$(docker inspect --format='{{.HostConfig.CapDrop}}' test-dashboard 2>/dev/null || echo "[]")
if echo "$CAP_DROP" | grep -qi "all"; then
    log_pass "cap_drop ALL"
else
    log_fail "cap_drop missing ($CAP_DROP)"
fi

log_test "Dashboard has read-only rootfs"
READ_ONLY=$(docker inspect --format='{{.HostConfig.ReadonlyRootfs}}' test-dashboard 2>/dev/null || echo "unknown")
if [ "$READ_ONLY" = "true" ]; then
    log_pass "Read-only rootfs: $READ_ONLY"
else
    log_fail "Read-only rootfs: $READ_ONLY (expected true)"
fi

log_test "Dashboard has memory limit"
MEM=$(docker inspect --format='{{.HostConfig.Memory}}' test-dashboard 2>/dev/null || echo "0")
if [ "$MEM" != "0" ] && [ "$MEM" != "" ]; then
    MEM_MB=$((MEM / 1024 / 1024))
    log_pass "Memory limit: ${MEM_MB}m"
else
    log_fail "No memory limit set"
fi

echo ""
echo "========================================="
echo "Results: $PASS passed, $FAIL failed (of $TOTAL)"
echo "========================================="

if [ "$FAIL" -gt 0 ]; then
    echo ""
    echo "Container logs (dashboard):"
    docker logs test-dashboard 2>&1 | tail -10
    exit 1
fi

exit 0
