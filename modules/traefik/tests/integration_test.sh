#!/bin/bash
# Traefik Module Integration Test
# Tests Traefik reverse proxy routing in isolation
#
# Usage:
#   ./modules/traefik/tests/integration_test.sh

set -euo pipefail

COMPOSE_FILE="$(dirname "$0")/reference-compose.yml"
BASE_URL="http://localhost:8890"
PASS=0
FAIL=0
TOTAL=0

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log_test() { TOTAL=$((TOTAL + 1)); echo -e "${YELLOW}[TEST $TOTAL]${NC} $1"; }
log_pass() { PASS=$((PASS + 1)); echo -e "${GREEN}  [PASS]${NC} $1"; }
log_fail() { FAIL=$((FAIL + 1)); echo -e "${RED}  [FAIL]${NC} $1"; }

cleanup() {
    echo ""
    echo "Cleaning up..."
    docker compose -f "$COMPOSE_FILE" down -v --remove-orphans 2>/dev/null || true
}
trap cleanup EXIT

echo "========================================="
echo "Traefik Module Integration Test"
echo "========================================="
echo ""

echo "Starting services..."
docker compose -f "$COMPOSE_FILE" up -d --wait --wait-timeout 60

echo ""
echo "--- Running Tests ---"
echo ""

# Test 1: Traefik dashboard accessible
log_test "Traefik dashboard returns 200"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:19090/api/overview" 2>/dev/null || echo "000")
if [ "$HTTP_CODE" = "200" ]; then
    log_pass "Traefik dashboard → HTTP $HTTP_CODE"
else
    log_fail "Expected 200, got $HTTP_CODE"
fi

# Test 2: Traefik routes whoami via Host header
log_test "Whoami accessible via Host header routing"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "Host: whoami.test.local" "$BASE_URL/" 2>/dev/null || echo "000")
if [ "$HTTP_CODE" = "200" ]; then
    log_pass "whoami.test.local → HTTP $HTTP_CODE"
else
    log_fail "Expected 200, got $HTTP_CODE"
fi

# Test 3: Whoami returns correct headers
log_test "Whoami response contains expected headers"
BODY=$(curl -s -H "Host: whoami.test.local" "$BASE_URL/" 2>/dev/null || echo "")
if echo "$BODY" | grep -q "Hostname:"; then
    log_pass "Whoami response contains Hostname header"
else
    log_fail "Whoami response missing expected content"
fi

# Test 4: Unknown host returns 404
log_test "Unknown host returns 404"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "Host: unknown.test.local" "$BASE_URL/" 2>/dev/null || echo "000")
if [ "$HTTP_CODE" = "404" ]; then
    log_pass "unknown.test.local → HTTP $HTTP_CODE"
else
    log_fail "Expected 404, got $HTTP_CODE"
fi

# Test 5: Traefik has routers registered
log_test "Traefik has whoami router registered"
ROUTERS=$(curl -s "http://localhost:19090/api/http/routers" 2>/dev/null || echo "")
if echo "$ROUTERS" | grep -q "whoami"; then
    log_pass "whoami router found"
else
    log_fail "whoami router NOT found"
fi

# Test 6: Traefik health check
log_test "Traefik container is healthy"
HEALTH=$(docker inspect --format='{{.State.Health.Status}}' test-traefik 2>/dev/null || echo "unknown")
if [ "$HEALTH" = "healthy" ]; then
    log_pass "Container health: $HEALTH"
else
    log_fail "Container health: $HEALTH (expected healthy)"
fi

# --- Security Hardening ---

log_test "Traefik has no-new-privileges"
SEC_OPTS=$(docker inspect --format='{{.HostConfig.SecurityOpt}}' test-traefik 2>/dev/null || echo "[]")
if echo "$SEC_OPTS" | grep -q "no-new-privileges"; then
    log_pass "no-new-privileges set"
else
    log_fail "no-new-privileges missing ($SEC_OPTS)"
fi

log_test "Traefik has cap_drop ALL"
CAP_DROP=$(docker inspect --format='{{.HostConfig.CapDrop}}' test-traefik 2>/dev/null || echo "[]")
if echo "$CAP_DROP" | grep -qi "all"; then
    log_pass "cap_drop ALL"
else
    log_fail "cap_drop missing ($CAP_DROP)"
fi

log_test "Traefik has cap_add NET_BIND_SERVICE"
CAP_ADD=$(docker inspect --format='{{.HostConfig.CapAdd}}' test-traefik 2>/dev/null || echo "[]")
if echo "$CAP_ADD" | grep -q "NET_BIND_SERVICE"; then
    log_pass "cap_add NET_BIND_SERVICE"
else
    log_fail "cap_add missing ($CAP_ADD)"
fi

log_test "Traefik has memory limit"
MEM=$(docker inspect --format='{{.HostConfig.Memory}}' test-traefik 2>/dev/null || echo "0")
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
    echo "Container logs (traefik):"
    docker logs test-traefik 2>&1 | tail -10
    exit 1
fi

exit 0
