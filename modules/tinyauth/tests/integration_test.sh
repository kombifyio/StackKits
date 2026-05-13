#!/bin/bash
# TinyAuth v4 Module Integration Test
# Tests the ForwardAuth flow in isolation with Traefik + Whoami
#
# Prerequisites:
#   - Docker running
#   - /etc/hosts: 127.0.0.1 auth.test.local whoami.test.local
#   - OR: use --header "Host: ..." to simulate DNS
#
# Usage:
#   ./modules/tinyauth/tests/integration_test.sh

set -euo pipefail

COMPOSE_FILE="$(dirname "$0")/reference-compose.yml"
BASE_URL="http://localhost:8890"
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
echo "TinyAuth v4 Module Integration Test"
echo "========================================="
echo ""

# Start services
echo "Starting services..."
docker compose -f "$COMPOSE_FILE" up -d --wait --wait-timeout 60

echo ""
echo "Waiting for TinyAuth to be healthy..."
for i in $(seq 1 30); do
    STATUS=$(docker inspect --format='{{.State.Health.Status}}' test-tinyauth 2>/dev/null || echo "not-found")
    if [ "$STATUS" = "healthy" ]; then
        echo "TinyAuth is healthy after ${i}s"
        break
    fi
    if [ "$i" = "30" ]; then
        echo "TinyAuth did not become healthy within 30s"
        echo "Container logs:"
        docker logs test-tinyauth 2>&1 | tail -20
        exit 1
    fi
    sleep 1
done

echo ""
echo "--- Running Tests ---"
echo ""

# Test 1: TinyAuth login page accessible
log_test "TinyAuth login page returns 200"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "Host: auth.test.local" "$BASE_URL/" 2>/dev/null || echo "000")
if [ "$HTTP_CODE" = "200" ]; then
    log_pass "auth.test.local → HTTP $HTTP_CODE"
else
    log_fail "Expected 200, got $HTTP_CODE"
fi

# Test 2: ForwardAuth endpoint exists
log_test "ForwardAuth /api/auth/traefik returns 401 for unauthenticated API request"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "Host: auth.test.local" "$BASE_URL/api/auth/traefik" 2>/dev/null || echo "000")
if [ "$HTTP_CODE" = "401" ]; then
    log_pass "/api/auth/traefik → HTTP $HTTP_CODE (correct for non-browser)"
else
    log_fail "Expected 401, got $HTTP_CODE"
fi

# Test 3: ForwardAuth redirects browsers (Accept: text/html)
log_test "ForwardAuth redirects browser requests (Accept: text/html)"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "Host: auth.test.local" -H "Accept: text/html" "$BASE_URL/api/auth/traefik" 2>/dev/null || echo "000")
# Should be 307 redirect (or 302)
if [ "$HTTP_CODE" = "307" ] || [ "$HTTP_CODE" = "302" ]; then
    log_pass "/api/auth/traefik with Accept:text/html → HTTP $HTTP_CODE (redirect)"
else
    log_fail "Expected 307 or 302, got $HTTP_CODE"
fi

# Test 4: Protected whoami returns redirect for browser
log_test "Protected whoami.test.local redirects browser to login"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "Host: whoami.test.local" -H "Accept: text/html" "$BASE_URL/" 2>/dev/null || echo "000")
if [ "$HTTP_CODE" = "307" ] || [ "$HTTP_CODE" = "302" ] || [ "$HTTP_CODE" = "401" ]; then
    log_pass "whoami.test.local → HTTP $HTTP_CODE (protected)"
else
    log_fail "Expected 307/302/401, got $HTTP_CODE"
fi

# Test 5: Protected whoami returns 401 for API client
log_test "Protected whoami.test.local returns 401 for API client"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "Host: whoami.test.local" "$BASE_URL/" 2>/dev/null || echo "000")
if [ "$HTTP_CODE" = "401" ]; then
    log_pass "whoami.test.local (no Accept header) → HTTP $HTTP_CODE"
else
    log_fail "Expected 401, got $HTTP_CODE"
fi

# Test 6: TinyAuth health endpoint
log_test "TinyAuth health check passes"
HEALTH=$(docker inspect --format='{{.State.Health.Status}}' test-tinyauth 2>/dev/null || echo "unknown")
if [ "$HEALTH" = "healthy" ]; then
    log_pass "Container health: $HEALTH"
else
    log_fail "Container health: $HEALTH (expected healthy)"
fi

# Test 7: Traefik dashboard accessible
log_test "Traefik dashboard accessible on :19090"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:19090/api/overview" 2>/dev/null || echo "000")
if [ "$HTTP_CODE" = "200" ]; then
    log_pass "Traefik dashboard → HTTP $HTTP_CODE"
else
    log_fail "Expected 200, got $HTTP_CODE"
fi

# Test 8: Traefik sees the tinyauth middleware
log_test "Traefik has tinyauth ForwardAuth middleware registered"
MIDDLEWARE=$(curl -s "http://localhost:19090/api/http/middlewares" 2>/dev/null || echo "")
if echo "$MIDDLEWARE" | grep -q "tinyauth"; then
    log_pass "tinyauth middleware found in Traefik"
else
    log_fail "tinyauth middleware NOT found in Traefik"
fi

# --- Security Hardening ---

log_test "TinyAuth has no-new-privileges"
SEC_OPTS=$(docker inspect --format='{{.HostConfig.SecurityOpt}}' test-tinyauth 2>/dev/null || echo "[]")
if echo "$SEC_OPTS" | grep -q "no-new-privileges"; then
    log_pass "no-new-privileges set"
else
    log_fail "no-new-privileges missing ($SEC_OPTS)"
fi

log_test "TinyAuth has cap_drop ALL"
CAP_DROP=$(docker inspect --format='{{.HostConfig.CapDrop}}' test-tinyauth 2>/dev/null || echo "[]")
if echo "$CAP_DROP" | grep -qi "all"; then
    log_pass "cap_drop ALL"
else
    log_fail "cap_drop missing ($CAP_DROP)"
fi

log_test "TinyAuth has memory limit"
MEM=$(docker inspect --format='{{.HostConfig.Memory}}' test-tinyauth 2>/dev/null || echo "0")
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
    echo "Container logs (tinyauth):"
    docker logs test-tinyauth 2>&1 | tail -10
    exit 1
fi

exit 0
