#!/bin/bash
# Uptime Kuma Module Integration Test
# Tests Uptime Kuma routing through Traefik in isolation
#
# Prerequisites:
#   - Docker running
#   - /etc/hosts: 127.0.0.1 kuma.test.local
#   - OR: use --header "Host: ..." to simulate DNS
#
# Usage:
#   ./modules/uptime-kuma/tests/integration_test.sh

set -euo pipefail

COMPOSE_FILE="$(dirname "$0")/reference-compose.yml"
BASE_URL="http://localhost:8893"
TRAEFIK_API="http://localhost:19093"
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
echo "Uptime Kuma Module Integration Test"
echo "========================================="
echo ""

# Start services
echo "Starting services..."
# Note: --wait is not used because the provisioner container exits (by design).
# We poll for service health below.
docker compose -f "$COMPOSE_FILE" up -d

echo ""
echo "Waiting for Uptime Kuma to be healthy..."
for i in $(seq 1 60); do
    STATUS=$(docker inspect --format='{{.State.Health.Status}}' test-uptime-kuma 2>/dev/null || echo "not-found")
    if [ "$STATUS" = "healthy" ]; then
        echo "Uptime Kuma is healthy after ${i}s"
        break
    fi
    if [ "$i" = "60" ]; then
        echo "Uptime Kuma did not become healthy within 60s"
        echo "Container logs:"
        docker logs test-uptime-kuma 2>&1 | tail -20
        exit 1
    fi
    sleep 1
done

# Traefik's docker provider rediscovers containers asynchronously; poll the
# routers API until our router shows up before running route-dependent tests.
echo "Waiting for Traefik to register the uptime-kuma router..."
for i in $(seq 1 30); do
    if curl -s "$TRAEFIK_API/api/http/routers" 2>/dev/null | grep -q "uptime-kuma"; then
        echo "Traefik registered uptime-kuma router after ${i}s"
        break
    fi
    if [ "$i" = "30" ]; then
        echo "Traefik did NOT register uptime-kuma router within 30s"
        echo "Routers known to Traefik:"
        curl -s "$TRAEFIK_API/api/http/routers" 2>/dev/null
        echo ""
        echo "Container labels:"
        docker inspect --format='{{json .Config.Labels}}' test-uptime-kuma 2>/dev/null
    fi
    sleep 1
done

echo ""
echo "--- Running Tests ---"
echo ""

# Test 1: Uptime Kuma accessible via Traefik
log_test "Uptime Kuma returns 200 or 302 via kuma.test.local"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "Host: kuma.test.local" "$BASE_URL/" 2>/dev/null || echo "000")
if [ "$HTTP_CODE" = "200" ] || [ "$HTTP_CODE" = "302" ]; then
    log_pass "kuma.test.local -> HTTP $HTTP_CODE"
else
    log_fail "Expected 200 or 302, got $HTTP_CODE"
fi

# Test 2: Uptime Kuma setup/dashboard page has content
log_test "Uptime Kuma page contains expected content"
BODY=$(curl -s -L -H "Host: kuma.test.local" "$BASE_URL/" 2>/dev/null || echo "")
if echo "$BODY" | grep -qi "uptime\|kuma\|setup\|dashboard"; then
    log_pass "Page contains Uptime Kuma content"
else
    log_fail "Page does not contain expected content"
fi

# Test 3: Uptime Kuma health check passes
log_test "Uptime Kuma container health check passes"
HEALTH=$(docker inspect --format='{{.State.Health.Status}}' test-uptime-kuma 2>/dev/null || echo "unknown")
if [ "$HEALTH" = "healthy" ]; then
    log_pass "Container health: $HEALTH"
else
    log_fail "Container health: $HEALTH (expected healthy)"
fi

# Test 4: Traefik dashboard accessible
log_test "Traefik dashboard accessible on :19093"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" "$TRAEFIK_API/api/overview" 2>/dev/null || echo "000")
if [ "$HTTP_CODE" = "200" ]; then
    log_pass "Traefik dashboard -> HTTP $HTTP_CODE"
else
    log_fail "Expected 200, got $HTTP_CODE"
fi

# Test 5: Traefik has uptime-kuma router registered
log_test "Traefik has uptime-kuma router registered"
ROUTERS=$(curl -s "$TRAEFIK_API/api/http/routers" 2>/dev/null || echo "")
if echo "$ROUTERS" | grep -q "uptime-kuma"; then
    log_pass "uptime-kuma router found in Traefik"
else
    log_fail "uptime-kuma router NOT found in Traefik"
fi

# Test 6: Uptime Kuma API responds (JSON endpoint)
log_test "Uptime Kuma /api/status-page responds"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "Host: kuma.test.local" "$BASE_URL/api/status-page" 2>/dev/null || echo "000")
if [ "$HTTP_CODE" = "200" ] || [ "$HTTP_CODE" = "401" ] || [ "$HTTP_CODE" = "404" ]; then
    log_pass "API endpoint -> HTTP $HTTP_CODE (service is responding)"
else
    log_fail "Expected 200/401/404, got $HTTP_CODE"
fi

# --- Security Hardening ---

log_test "Uptime Kuma has no-new-privileges"
SEC_OPTS=$(docker inspect --format='{{.HostConfig.SecurityOpt}}' test-uptime-kuma 2>/dev/null || echo "[]")
if echo "$SEC_OPTS" | grep -q "no-new-privileges"; then
    log_pass "no-new-privileges set"
else
    log_fail "no-new-privileges missing ($SEC_OPTS)"
fi

log_test "Uptime Kuma has cap_drop ALL"
CAP_DROP=$(docker inspect --format='{{.HostConfig.CapDrop}}' test-uptime-kuma 2>/dev/null || echo "[]")
if echo "$CAP_DROP" | grep -qi "all"; then
    log_pass "cap_drop ALL"
else
    log_fail "cap_drop missing ($CAP_DROP)"
fi

log_test "Uptime Kuma has memory limit"
MEM=$(docker inspect --format='{{.HostConfig.Memory}}' test-uptime-kuma 2>/dev/null || echo "0")
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
    echo "Container logs (uptime-kuma):"
    docker logs test-uptime-kuma 2>&1 | tail -10
    exit 1
fi

exit 0
