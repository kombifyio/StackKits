#!/bin/bash
# PocketID Module Integration Test
# Tests PocketID OIDC provider in isolation with Traefik
#
# Prerequisites:
#   - Docker running
#
# Usage:
#   ./modules/pocketid/tests/integration_test.sh

set -euo pipefail

COMPOSE_FILE="$(dirname "$0")/reference-compose.yml"
BASE_URL="http://localhost:8891"
PASS=0
FAIL=0
TOTAL=0

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

trap cleanup EXIT

echo "========================================="
echo "PocketID Module Integration Test"
echo "========================================="
echo ""

echo "Starting services..."
# Note: --wait is not used because the provisioner container exits (by design).
# We poll for service health below.
docker compose -f "$COMPOSE_FILE" up -d

echo ""
echo "Waiting for PocketID to be healthy..."
for i in $(seq 1 60); do
    STATUS=$(docker inspect --format='{{.State.Health.Status}}' test-pocketid 2>/dev/null || echo "not-found")
    if [ "$STATUS" = "healthy" ]; then
        echo "PocketID is healthy after ${i}s"
        break
    fi
    if [ "$i" = "60" ]; then
        echo "PocketID did not become healthy within 60s"
        echo "Container logs:"
        docker logs test-pocketid 2>&1 | tail -20
        exit 1
    fi
    sleep 1
done

echo ""
echo "--- Running Tests ---"
echo ""

# Test 1: PocketID UI accessible
log_test "PocketID UI returns 200 at id.test.local"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "Host: id.test.local" "$BASE_URL/" 2>/dev/null || echo "000")
if [ "$HTTP_CODE" = "200" ] || [ "$HTTP_CODE" = "302" ]; then
    log_pass "id.test.local → HTTP $HTTP_CODE"
else
    log_fail "Expected 200 or 302, got $HTTP_CODE"
fi

# Test 2: Health endpoint
# PocketID exposes /health (not /api/health). The internal healthcheck calls
# /health and the container reports 200; /api/health is intentionally not a route.
log_test "PocketID /health returns 200"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "Host: id.test.local" "$BASE_URL/health" 2>/dev/null || echo "000")
if [ "$HTTP_CODE" = "200" ]; then
    log_pass "/health → HTTP $HTTP_CODE"
else
    log_fail "Expected 200, got $HTTP_CODE"
fi

# Test 3: OIDC well-known endpoint
log_test "OIDC .well-known/openid-configuration returns 200"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "Host: id.test.local" "$BASE_URL/.well-known/openid-configuration" 2>/dev/null || echo "000")
if [ "$HTTP_CODE" = "200" ]; then
    log_pass ".well-known/openid-configuration → HTTP $HTTP_CODE"
else
    log_fail "Expected 200, got $HTTP_CODE"
fi

# Test 4: Container health check
log_test "PocketID container health check passes"
HEALTH=$(docker inspect --format='{{.State.Health.Status}}' test-pocketid 2>/dev/null || echo "unknown")
if [ "$HEALTH" = "healthy" ]; then
    log_pass "Container health: $HEALTH"
else
    log_fail "Container health: $HEALTH (expected healthy)"
fi

# Test 5: Traefik dashboard accessible
log_test "Traefik dashboard accessible on :19091"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:19091/api/overview" 2>/dev/null || echo "000")
if [ "$HTTP_CODE" = "200" ]; then
    log_pass "Traefik dashboard → HTTP $HTTP_CODE"
else
    log_fail "Expected 200, got $HTTP_CODE"
fi

# Test 6: Traefik has pocketid router
log_test "Traefik has pocketid router registered"
ROUTERS=$(curl -s "http://localhost:19091/api/http/routers" 2>/dev/null || echo "")
if echo "$ROUTERS" | grep -q "pocketid"; then
    log_pass "pocketid router found in Traefik"
else
    log_fail "pocketid router NOT found in Traefik"
fi

# --- Security Hardening ---

log_test "PocketID has no-new-privileges"
SEC_OPTS=$(docker inspect --format='{{.HostConfig.SecurityOpt}}' test-pocketid 2>/dev/null || echo "[]")
if echo "$SEC_OPTS" | grep -q "no-new-privileges"; then
    log_pass "no-new-privileges set"
else
    log_fail "no-new-privileges missing ($SEC_OPTS)"
fi

log_test "PocketID has cap_drop ALL"
CAP_DROP=$(docker inspect --format='{{.HostConfig.CapDrop}}' test-pocketid 2>/dev/null || echo "[]")
if echo "$CAP_DROP" | grep -qi "all"; then
    log_pass "cap_drop ALL"
else
    log_fail "cap_drop missing ($CAP_DROP)"
fi

log_test "PocketID has memory limit"
MEM=$(docker inspect --format='{{.HostConfig.Memory}}' test-pocketid 2>/dev/null || echo "0")
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
    echo "Container logs (pocketid):"
    docker logs test-pocketid 2>&1 | tail -10
    exit 1
fi

exit 0
