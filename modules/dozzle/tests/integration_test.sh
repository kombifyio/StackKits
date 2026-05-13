#!/bin/bash
# Dozzle Module Integration Test
# Tests Dozzle log viewer routing through Traefik in isolation
#
# Prerequisites:
#   - Docker running
#   - /etc/hosts: 127.0.0.1 logs.test.local
#   - OR: use --header "Host: ..." to simulate DNS
#
# Usage:
#   ./modules/dozzle/tests/integration_test.sh

set -euo pipefail

COMPOSE_FILE="$(dirname "$0")/reference-compose.yml"
BASE_URL="http://localhost:8894"
TRAEFIK_API="http://localhost:19094"
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
echo "Dozzle Module Integration Test"
echo "========================================="
echo ""

# Start services
echo "Starting services..."
docker compose -f "$COMPOSE_FILE" up -d --wait --wait-timeout 60

echo ""
echo "Waiting for Dozzle to be healthy..."
for i in $(seq 1 30); do
    STATUS=$(docker inspect --format='{{.State.Health.Status}}' test-dozzle 2>/dev/null || echo "not-found")
    if [ "$STATUS" = "healthy" ]; then
        echo "Dozzle is healthy after ${i}s"
        break
    fi
    if [ "$i" = "30" ]; then
        echo "Dozzle did not become healthy within 30s"
        echo "Container logs:"
        docker logs test-dozzle 2>&1 | tail -20
        exit 1
    fi
    sleep 1
done

echo ""
echo "--- Running Tests ---"
echo ""

# Test 1: Dozzle accessible via Traefik
log_test "Dozzle returns 200 via logs.test.local"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "Host: logs.test.local" "$BASE_URL/" 2>/dev/null || echo "000")
if [ "$HTTP_CODE" = "200" ]; then
    log_pass "logs.test.local -> HTTP $HTTP_CODE"
else
    log_fail "Expected 200, got $HTTP_CODE"
fi

# Test 2: Dozzle page contains expected content
log_test "Dozzle page contains expected content"
BODY=$(curl -s -L -H "Host: logs.test.local" "$BASE_URL/" 2>/dev/null || echo "")
if echo "$BODY" | grep -qi "dozzle\|container\|log"; then
    log_pass "Page contains Dozzle content"
else
    log_fail "Page does not contain expected content"
fi

# Test 3: Dozzle container health check passes
log_test "Dozzle container health check passes"
HEALTH=$(docker inspect --format='{{.State.Health.Status}}' test-dozzle 2>/dev/null || echo "unknown")
if [ "$HEALTH" = "healthy" ]; then
    log_pass "Container health: $HEALTH"
else
    log_fail "Container health: $HEALTH (expected healthy)"
fi

# Test 4: Traefik dashboard accessible
log_test "Traefik dashboard accessible on :19094"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" "$TRAEFIK_API/api/overview" 2>/dev/null || echo "000")
if [ "$HTTP_CODE" = "200" ]; then
    log_pass "Traefik dashboard -> HTTP $HTTP_CODE"
else
    log_fail "Expected 200, got $HTTP_CODE"
fi

# Test 5: Traefik has dozzle router registered
log_test "Traefik has dozzle router registered"
ROUTERS=$(curl -s "$TRAEFIK_API/api/http/routers" 2>/dev/null || echo "")
if echo "$ROUTERS" | grep -q "dozzle"; then
    log_pass "dozzle router found in Traefik"
else
    log_fail "dozzle router NOT found in Traefik"
fi

# Test 6: Dozzle healthz endpoint responds
log_test "Dozzle /healthz endpoint responds"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "Host: logs.test.local" "$BASE_URL/healthz" 2>/dev/null || echo "000")
if [ "$HTTP_CODE" = "200" ] || [ "$HTTP_CODE" = "404" ]; then
    log_pass "healthz endpoint -> HTTP $HTTP_CODE (service is responding)"
else
    log_fail "Expected 200/404, got $HTTP_CODE"
fi

# --- Security Hardening ---

log_test "Dozzle has no-new-privileges"
SEC_OPTS=$(docker inspect --format='{{.HostConfig.SecurityOpt}}' test-dozzle 2>/dev/null || echo "[]")
if echo "$SEC_OPTS" | grep -q "no-new-privileges"; then
    log_pass "no-new-privileges set"
else
    log_fail "no-new-privileges missing ($SEC_OPTS)"
fi

log_test "Dozzle has cap_drop ALL"
CAP_DROP=$(docker inspect --format='{{.HostConfig.CapDrop}}' test-dozzle 2>/dev/null || echo "[]")
if echo "$CAP_DROP" | grep -qi "all"; then
    log_pass "cap_drop ALL"
else
    log_fail "cap_drop missing ($CAP_DROP)"
fi

log_test "Dozzle has memory limit"
MEM=$(docker inspect --format='{{.HostConfig.Memory}}' test-dozzle 2>/dev/null || echo "0")
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
    echo "Container logs (dozzle):"
    docker logs test-dozzle 2>&1 | tail -10
    exit 1
fi

exit 0
