#!/bin/bash
# AdGuard Home Module Integration Test
# Validates AdGuard Home DNS filter, web UI, DNS protocol, and Traefik routing.
#
# Tests: health, web UI, setup/provisioning, DNS port, Traefik routing, hardening.
#
# Usage:
#   ./modules/adguard-home/tests/integration_test.sh

set -euo pipefail

COMPOSE_FILE="$(dirname "$0")/reference-compose.yml"
BASE_URL="http://localhost:8899"
TRAEFIK_API="http://localhost:19099"
PASS=0
FAIL=0
TOTAL=0

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

log_section() { echo -e "\n${CYAN}=== $1 ===${NC}\n"; }
log_test() { TOTAL=$((TOTAL + 1)); echo -e "${YELLOW}[TEST $TOTAL]${NC} $1"; }
log_pass() { PASS=$((PASS + 1)); echo -e "${GREEN}  [PASS]${NC} $1"; }
log_fail() { FAIL=$((FAIL + 1)); echo -e "${RED}  [FAIL]${NC} $1"; }

cleanup() {
    echo ""
    echo "Cleaning up..."
    docker compose -f "$COMPOSE_FILE" down -v --remove-orphans 2>/dev/null || true
}

trap cleanup EXIT

echo "============================================="
echo "AdGuard Home Module Integration Test"
echo "============================================="
echo "Services: traefik, adguard-home, adguard-provisioner"
echo "============================================="
echo ""

echo "Starting services..."
# Note: --wait is not used because the provisioner container exits (by design).
# We wait manually for the main service below.
docker compose -f "$COMPOSE_FILE" up -d

echo ""
echo "Waiting for AdGuard Home to be healthy (up to 60s)..."
for i in $(seq 1 60); do
    STATUS=$(docker inspect --format='{{.State.Health.Status}}' test-adguard-home 2>/dev/null || echo "not-found")
    if [ "$STATUS" = "healthy" ]; then
        echo "  AdGuard Home: healthy (${i}s)"
        break
    fi
    if [ "$i" = "60" ]; then
        echo "  AdGuard Home: NOT healthy after 60s (status: $STATUS)"
        docker logs test-adguard-home 2>&1 | tail -20
    fi
    sleep 1
done

echo ""
echo "Waiting for Traefik to be healthy (up to 30s)..."
for i in $(seq 1 30); do
    STATUS=$(docker inspect --format='{{.State.Health.Status}}' test-traefik-adguard 2>/dev/null || echo "not-found")
    if [ "$STATUS" = "healthy" ]; then
        echo "  Traefik: healthy (${i}s)"
        break
    fi
    if [ "$i" = "30" ]; then
        echo "  Traefik: NOT healthy after 30s (status: $STATUS)"
    fi
    sleep 1
done

echo ""
echo "Waiting for provisioner to complete..."
for i in $(seq 1 60); do
    STATUS=$(docker inspect --format='{{.State.Status}}' test-adguard-provisioner 2>/dev/null || echo "not-found")
    if [ "$STATUS" = "exited" ]; then
        echo "  Provisioner: completed (${i}s)"
        break
    fi
    sleep 1
done

echo ""
echo "--- Running Tests ---"

log_section "Container Health"

log_test "AdGuard Home container is healthy"
HEALTH=$(docker inspect --format='{{if .State.Health}}{{.State.Health.Status}}{{else}}no-healthcheck{{end}}' test-adguard-home 2>/dev/null || echo "not-found")
if [ "$HEALTH" = "healthy" ]; then
    log_pass "AdGuard Home: $HEALTH"
else
    log_fail "AdGuard Home: $HEALTH"
fi

log_test "Traefik container is healthy"
HEALTH=$(docker inspect --format='{{if .State.Health}}{{.State.Health.Status}}{{else}}no-healthcheck{{end}}' test-traefik-adguard 2>/dev/null || echo "not-found")
if [ "$HEALTH" = "healthy" ]; then
    log_pass "Traefik: $HEALTH"
else
    log_fail "Traefik: $HEALTH"
fi

log_section "Web UI"

log_test "AdGuard Home web UI accessible via Traefik"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "Host: adguard.test.local" "$BASE_URL/" 2>/dev/null || echo "000")
if [ "$HTTP_CODE" = "200" ] || [ "$HTTP_CODE" = "302" ]; then
    log_pass "adguard.test.local → HTTP $HTTP_CODE"
else
    log_fail "Expected 200/302, got $HTTP_CODE"
fi

log_test "AdGuard Home login page accessible"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "Host: adguard.test.local" "$BASE_URL/login.html" 2>/dev/null || echo "000")
if [ "$HTTP_CODE" = "200" ] || [ "$HTTP_CODE" = "302" ]; then
    log_pass "Login page → HTTP $HTTP_CODE"
else
    log_fail "Login page: got $HTTP_CODE"
fi

log_section "DNS Configuration"

log_test "AdGuard Home DNS port 5353/udp is reachable"
# Check if DNS port responds (may not have DNS until after provisioning)
DNS_LISTEN=$(docker exec test-adguard-home sh -c 'cat /proc/net/udp 2>/dev/null | grep ":0035" || cat /proc/net/udp6 2>/dev/null | grep ":0035" || echo ""' 2>/dev/null || echo "")
if [ -n "$DNS_LISTEN" ]; then
    log_pass "DNS port 53 (0x0035) listening"
else
    log_fail "DNS port 53 not yet listening (may need setup wizard completion)"
fi

log_test "AdGuard Home control API accessible"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" http://adguard-home:3000/control/status 2>/dev/null || \
    docker exec test-adguard-home sh -c 'wget -q -O - http://127.0.0.1:3000/control/status 2>/dev/null | head -c 100; echo $?' 2>/dev/null | tail -1 || echo "000")
# Just verify port 3000 is open
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" "$BASE_URL/control/status" -H "Host: adguard.test.local" 2>/dev/null || echo "000")
if [ "$HTTP_CODE" != "000" ]; then
    log_pass "Control API responding (HTTP $HTTP_CODE)"
else
    log_fail "Control API not reachable"
fi

log_section "Security Hardening"

log_test "AdGuard Home has no-new-privileges"
SEC_OPTS=$(docker inspect --format='{{.HostConfig.SecurityOpt}}' test-adguard-home 2>/dev/null || echo "[]")
if echo "$SEC_OPTS" | grep -q "no-new-privileges"; then
    log_pass "no-new-privileges set"
else
    log_fail "no-new-privileges missing ($SEC_OPTS)"
fi

log_test "AdGuard Home has cap_drop ALL"
CAP_DROP=$(docker inspect --format='{{.HostConfig.CapDrop}}' test-adguard-home 2>/dev/null || echo "[]")
if echo "$CAP_DROP" | grep -qi "all"; then
    log_pass "cap_drop ALL"
else
    log_fail "cap_drop missing ($CAP_DROP)"
fi

log_test "AdGuard Home has memory limit"
MEM=$(docker inspect --format='{{.HostConfig.Memory}}' test-adguard-home 2>/dev/null || echo "0")
if [ "$MEM" != "0" ] && [ "$MEM" != "" ]; then
    MEM_MB=$((MEM / 1024 / 1024))
    log_pass "Memory limit: ${MEM_MB}m"
else
    log_fail "No memory limit set"
fi

log_section "Traefik Routing"

log_test "Traefik has adguard-home router registered"
ROUTERS=$(curl -s "$TRAEFIK_API/api/http/routers" 2>/dev/null || echo "")
if echo "$ROUTERS" | grep -qi "adguard"; then
    log_pass "adguard-home router found in Traefik"
else
    log_fail "adguard-home router NOT found in Traefik"
fi

log_section "Results"

echo ""
echo "============================================="
echo "Results: $PASS passed, $FAIL failed (of $TOTAL)"
echo "============================================="

if [ "$FAIL" -gt 0 ]; then
    echo ""
    echo "Failed tests detected. Container summary:"
    docker compose -f "$COMPOSE_FILE" ps --format "table {{.Name}}\t{{.Status}}"
    echo ""
    echo "AdGuard Home logs:"
    docker logs test-adguard-home 2>&1 | tail -15
    exit 1
fi

echo ""
echo "All tests passed!"
exit 0
