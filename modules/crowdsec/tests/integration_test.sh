#!/bin/bash
# CrowdSec Module Integration Test
# Validates CrowdSec IDS + Traefik bouncer integration.
#
# Tests: CrowdSec health, LAPI accessibility, bouncer registration,
# collections installed, Traefik plugin loaded.
#
# Usage:
#   ./modules/crowdsec/tests/integration_test.sh

set -euo pipefail

COMPOSE_FILE="$(dirname "$0")/reference-compose.yml"
BASE_URL="http://localhost:8896"
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
    local rc=$?
    if [ "$rc" -ne 0 ]; then
        echo ""
        echo "=== container state (rc=$rc) ==="
        docker compose -f "$COMPOSE_FILE" ps -a 2>/dev/null || true
        echo ""
        echo "=== container logs (last 200 lines per service) ==="
        docker compose -f "$COMPOSE_FILE" logs --no-color --tail=200 2>/dev/null || true
    fi
    echo ""
    echo "Cleaning up..."
    docker compose -f "$COMPOSE_FILE" down -v --remove-orphans 2>/dev/null || true
}

trap cleanup EXIT

echo "============================================="
echo "CrowdSec Module Integration Test"
echo "============================================="
echo "Services: socket-proxy, traefik (bouncer), crowdsec, whoami"
echo "============================================="
echo ""

echo "Starting services..."
docker compose -f "$COMPOSE_FILE" up -d --wait --wait-timeout 120

echo ""
echo "Waiting for CrowdSec to be healthy (up to 60s)..."
for i in $(seq 1 60); do
    STATUS=$(docker inspect --format='{{.State.Health.Status}}' test-cs-crowdsec 2>/dev/null || echo "not-found")
    if [ "$STATUS" = "healthy" ]; then
        echo "  CrowdSec: healthy (${i}s)"
        break
    fi
    if [ "$i" = "60" ]; then
        echo "  CrowdSec: NOT healthy after 60s (status: $STATUS)"
        docker logs test-cs-crowdsec 2>&1 | tail -10
    fi
    sleep 1
done

echo ""
echo "--- Running Tests ---"

log_section "Container Health"

log_test "CrowdSec container is healthy"
HEALTH=$(docker inspect --format='{{if .State.Health}}{{.State.Health.Status}}{{else}}no-healthcheck{{end}}' test-cs-crowdsec 2>/dev/null || echo "not-found")
if [ "$HEALTH" = "healthy" ]; then
    log_pass "CrowdSec: $HEALTH"
else
    log_fail "CrowdSec: $HEALTH"
fi

log_test "Traefik container is healthy"
HEALTH=$(docker inspect --format='{{if .State.Health}}{{.State.Health.Status}}{{else}}no-healthcheck{{end}}' test-cs-traefik 2>/dev/null || echo "not-found")
if [ "$HEALTH" = "healthy" ]; then
    log_pass "Traefik: $HEALTH"
else
    log_fail "Traefik: $HEALTH"
fi

log_section "CrowdSec LAPI"

log_test "CrowdSec LAPI is accessible"
LAPI_STATUS=$(docker exec test-cs-crowdsec cscli lapi status 2>&1 || echo "error")
if echo "$LAPI_STATUS" | grep -qi "online\|OK\|running"; then
    log_pass "LAPI is online"
else
    log_fail "LAPI status: $LAPI_STATUS"
fi

log_test "Bouncer registered with CrowdSec"
BOUNCERS=$(docker exec test-cs-crowdsec cscli bouncers list -o raw 2>&1 || echo "error")
if echo "$BOUNCERS" | grep -qi "traefik"; then
    log_pass "Bouncer 'traefik' registered"
else
    log_fail "Bouncer not found: $BOUNCERS"
fi

log_section "Collections"

log_test "traefik collection installed"
COLLECTIONS=$(docker exec test-cs-crowdsec cscli collections list -o raw 2>&1 || echo "error")
if echo "$COLLECTIONS" | grep -q "crowdsecurity/traefik"; then
    log_pass "crowdsecurity/traefik installed"
else
    log_fail "traefik collection missing"
fi

log_test "http-cve collection installed"
if echo "$COLLECTIONS" | grep -q "crowdsecurity/http-cve"; then
    log_pass "crowdsecurity/http-cve installed"
else
    log_fail "http-cve collection missing"
fi

log_section "Traefik Integration"

log_test "Whoami responds through Traefik+CrowdSec"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "Host: whoami.test.local" "$BASE_URL/" 2>/dev/null || echo "000")
if [ "$HTTP_CODE" = "200" ]; then
    log_pass "whoami.test.local → $HTTP_CODE (not blocked)"
else
    log_fail "Expected 200, got $HTTP_CODE"
fi

log_test "Traefik dashboard shows crowdsec middleware"
ROUTERS=$(curl -s "http://localhost:19096/api/http/routers" 2>/dev/null || echo "")
if echo "$ROUTERS" | grep -qi "crowdsec"; then
    log_pass "Crowdsec middleware visible in Traefik"
else
    log_fail "Crowdsec middleware not found in Traefik routers"
fi

log_section "Security Hardening"

log_test "CrowdSec has no-new-privileges"
SEC_OPTS=$(docker inspect --format='{{.HostConfig.SecurityOpt}}' test-cs-crowdsec 2>/dev/null || echo "[]")
if echo "$SEC_OPTS" | grep -q "no-new-privileges"; then
    log_pass "no-new-privileges set"
else
    log_fail "no-new-privileges missing ($SEC_OPTS)"
fi

log_test "CrowdSec has cap_drop ALL"
CAP_DROP=$(docker inspect --format='{{.HostConfig.CapDrop}}' test-cs-crowdsec 2>/dev/null || echo "[]")
if echo "$CAP_DROP" | grep -qi "all"; then
    log_pass "cap_drop ALL"
else
    log_fail "cap_drop missing ($CAP_DROP)"
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
    exit 1
fi

echo ""
echo "All tests passed!"
exit 0
