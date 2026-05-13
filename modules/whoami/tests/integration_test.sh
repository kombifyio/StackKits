#!/bin/bash
# Whoami Module Integration Test
# Tests Whoami HTTP echo service routing through Traefik in isolation
#
# Prerequisites:
#   - Docker running
#   - /etc/hosts: 127.0.0.1 whoami.test.local
#   - OR: use --header "Host: ..." to simulate DNS
#
# NOTE: traefik/whoami is scratch-based -- NO shell inside.
# All testing is done from outside the container via curl.
#
# Usage:
#   ./modules/whoami/tests/integration_test.sh

set -euo pipefail

COMPOSE_FILE="$(dirname "$0")/reference-compose.yml"
BASE_URL="http://localhost:8895"
TRAEFIK_API="http://localhost:19095"
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
echo "Whoami Module Integration Test"
echo "========================================="
echo ""

# Start services
echo "Starting services..."
docker compose -f "$COMPOSE_FILE" up -d --wait --wait-timeout 60

# Whoami has no healthcheck (scratch-based), so wait for Traefik to register the router
echo ""
echo "Waiting for Traefik to register whoami router..."
for i in $(seq 1 30); do
    ROUTERS=$(curl -s "$TRAEFIK_API/api/http/routers" 2>/dev/null || echo "")
    if echo "$ROUTERS" | grep -q "whoami"; then
        echo "Whoami router registered after ${i}s"
        break
    fi
    if [ "$i" = "30" ]; then
        echo "Whoami router not registered within 30s"
        exit 1
    fi
    sleep 1
done

echo ""
echo "--- Running Tests ---"
echo ""

# Test 1: Whoami accessible via Traefik
log_test "Whoami returns 200 via whoami.test.local"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "Host: whoami.test.local" "$BASE_URL/" 2>/dev/null || echo "000")
if [ "$HTTP_CODE" = "200" ]; then
    log_pass "whoami.test.local -> HTTP $HTTP_CODE"
else
    log_fail "Expected 200, got $HTTP_CODE"
fi

# Test 2: Whoami response contains Hostname header
log_test "Whoami response contains Hostname"
BODY=$(curl -s -H "Host: whoami.test.local" "$BASE_URL/" 2>/dev/null || echo "")
if echo "$BODY" | grep -q "Hostname"; then
    log_pass "Response contains Hostname"
else
    log_fail "Response does not contain Hostname"
fi

# Test 3: Whoami response contains IP address
log_test "Whoami response contains IP address"
if echo "$BODY" | grep -q "IP"; then
    log_pass "Response contains IP"
else
    log_fail "Response does not contain IP"
fi

# Test 4: Whoami response contains Host header echo
log_test "Whoami echoes back the Host header"
if echo "$BODY" | grep -q "whoami.test.local"; then
    log_pass "Response contains Host: whoami.test.local"
else
    log_fail "Response does not echo back Host header"
fi

# Test 5: Traefik dashboard accessible
log_test "Traefik dashboard accessible on :19095"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" "$TRAEFIK_API/api/overview" 2>/dev/null || echo "000")
if [ "$HTTP_CODE" = "200" ]; then
    log_pass "Traefik dashboard -> HTTP $HTTP_CODE"
else
    log_fail "Expected 200, got $HTTP_CODE"
fi

# Test 6: Traefik has whoami router registered
log_test "Traefik has whoami router registered"
ROUTERS=$(curl -s "$TRAEFIK_API/api/http/routers" 2>/dev/null || echo "")
if echo "$ROUTERS" | grep -q "whoami"; then
    log_pass "whoami router found in Traefik"
else
    log_fail "whoami router NOT found in Traefik"
fi

# --- Security Hardening ---

log_test "Whoami has no-new-privileges"
SEC_OPTS=$(docker inspect --format='{{.HostConfig.SecurityOpt}}' test-whoami-module 2>/dev/null || echo "[]")
if echo "$SEC_OPTS" | grep -q "no-new-privileges"; then
    log_pass "no-new-privileges set"
else
    log_fail "no-new-privileges missing ($SEC_OPTS)"
fi

log_test "Whoami has cap_drop ALL"
CAP_DROP=$(docker inspect --format='{{.HostConfig.CapDrop}}' test-whoami-module 2>/dev/null || echo "[]")
if echo "$CAP_DROP" | grep -qi "all"; then
    log_pass "cap_drop ALL"
else
    log_fail "cap_drop missing ($CAP_DROP)"
fi

log_test "Whoami has read-only rootfs"
READ_ONLY=$(docker inspect --format='{{.HostConfig.ReadonlyRootfs}}' test-whoami-module 2>/dev/null || echo "unknown")
if [ "$READ_ONLY" = "true" ]; then
    log_pass "Read-only rootfs: $READ_ONLY"
else
    log_fail "Read-only rootfs: $READ_ONLY (expected true)"
fi

echo ""
echo "========================================="
echo "Results: $PASS passed, $FAIL failed (of $TOTAL)"
echo "========================================="

if [ "$FAIL" -gt 0 ]; then
    echo ""
    echo "Container status (whoami):"
    docker inspect --format='{{.State.Status}}' test-whoami-module 2>/dev/null || echo "unknown"
    exit 1
fi

exit 0
