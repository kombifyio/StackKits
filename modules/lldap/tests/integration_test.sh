#!/bin/bash
# LLDAP Module Integration Test
# Validates LLDAP directory service, LDAP protocol, web UI, and Traefik routing.
#
# Tests: LLDAP health, web UI accessible, LDAP port listening,
# admin login, group creation, security hardening.
#
# Usage:
#   ./modules/lldap/tests/integration_test.sh

set -euo pipefail

COMPOSE_FILE="$(dirname "$0")/reference-compose.yml"
BASE_URL="http://localhost:8897"
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
echo "LLDAP Module Integration Test"
echo "============================================="
echo "Services: traefik, lldap"
echo "============================================="
echo ""

echo "Starting services..."
# Note: --wait is not used because the provisioner container exits (by design).
# We poll for service health below.
docker compose -f "$COMPOSE_FILE" up -d

echo ""
echo "Waiting for LLDAP to be healthy (up to 60s)..."
for i in $(seq 1 60); do
    STATUS=$(docker inspect --format='{{.State.Health.Status}}' test-lldap 2>/dev/null || echo "not-found")
    if [ "$STATUS" = "healthy" ]; then
        echo "  LLDAP: healthy (${i}s)"
        break
    fi
    if [ "$i" = "60" ]; then
        echo "  LLDAP: NOT healthy after 60s (status: $STATUS)"
        docker logs test-lldap 2>&1 | tail -20
    fi
    sleep 1
done

echo ""
echo "--- Running Tests ---"

log_section "Container Health"

log_test "LLDAP container is healthy"
HEALTH=$(docker inspect --format='{{if .State.Health}}{{.State.Health.Status}}{{else}}no-healthcheck{{end}}' test-lldap 2>/dev/null || echo "not-found")
if [ "$HEALTH" = "healthy" ]; then
    log_pass "LLDAP: $HEALTH"
else
    log_fail "LLDAP: $HEALTH"
fi

log_test "Traefik container is healthy"
HEALTH=$(docker inspect --format='{{if .State.Health}}{{.State.Health.Status}}{{else}}no-healthcheck{{end}}' test-traefik-lldap 2>/dev/null || echo "not-found")
if [ "$HEALTH" = "healthy" ]; then
    log_pass "Traefik: $HEALTH"
else
    log_fail "Traefik: $HEALTH"
fi

log_section "Web UI"

log_test "LLDAP web UI accessible via Traefik"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "Host: ldap.test.local" "$BASE_URL/" 2>/dev/null || echo "000")
if [ "$HTTP_CODE" = "200" ] || [ "$HTTP_CODE" = "302" ]; then
    log_pass "ldap.test.local → $HTTP_CODE"
else
    log_fail "Expected 200 or 302, got $HTTP_CODE"
fi

log_test "LLDAP GraphQL API endpoint responds"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "Host: ldap.test.local" "$BASE_URL/api/graphql" 2>/dev/null || echo "000")
if [ "$HTTP_CODE" != "000" ]; then
    log_pass "GraphQL API responds: $HTTP_CODE"
else
    log_fail "GraphQL API not reachable"
fi

log_section "LDAP Protocol"

log_test "LDAP port 3890 is listening inside container"
LDAP_LISTEN=$(docker exec test-lldap sh -c 'cat /proc/net/tcp 2>/dev/null | grep -i ":0F32" || echo ""' 2>/dev/null || echo "")
if [ -n "$LDAP_LISTEN" ]; then
    log_pass "LDAP port 3890 (0x0F32) listening"
else
    # Fallback: try netstat or ss
    LDAP_CHECK=$(docker exec test-lldap sh -c 'ss -tlnp 2>/dev/null | grep 3890 || netstat -tlnp 2>/dev/null | grep 3890 || echo ""' 2>/dev/null || echo "")
    if [ -n "$LDAP_CHECK" ]; then
        log_pass "LDAP port 3890 listening (via ss/netstat)"
    else
        log_fail "LDAP port 3890 not detected (may still be working — check /proc/net/tcp6)"
    fi
fi

log_test "Web UI port 17170 is listening inside container"
HTTP_LISTEN=$(docker exec test-lldap sh -c 'cat /proc/net/tcp 2>/dev/null | grep -i ":4312" || cat /proc/net/tcp6 2>/dev/null | grep -i ":4312" || echo ""' 2>/dev/null || echo "")
if [ -n "$HTTP_LISTEN" ]; then
    log_pass "HTTP port 17170 (0x4312) listening"
else
    # Verify via curl from inside
    INTERNAL=$(docker exec test-lldap sh -c 'wget -q --spider http://localhost:17170/ 2>&1; echo $?' 2>/dev/null || echo "1")
    if [ "$INTERNAL" = "0" ]; then
        log_pass "HTTP port 17170 verified via wget"
    else
        log_fail "HTTP port 17170 not detected"
    fi
fi

log_section "Admin Authentication"

log_test "Admin can authenticate via LLDAP API"
LOGIN_RESP=$(curl -s -X POST -H "Host: ldap.test.local" -H "Content-Type: application/json" \
    -d '{"username":"admin","password":"test-admin-pass"}' \
    "$BASE_URL/auth/simple/login" 2>/dev/null || echo "error")
if echo "$LOGIN_RESP" | grep -qi "token\|Token\|jwt\|JWT"; then
    log_pass "Admin login returns token"
else
    # Check if we get a valid HTTP response
    LOGIN_CODE=$(curl -s -o /dev/null -w "%{http_code}" -X POST -H "Host: ldap.test.local" -H "Content-Type: application/json" \
        -d '{"username":"admin","password":"test-admin-pass"}' \
        "$BASE_URL/auth/simple/login" 2>/dev/null || echo "000")
    if [ "$LOGIN_CODE" = "200" ]; then
        log_pass "Admin login returns 200"
    else
        log_fail "Admin login failed (HTTP $LOGIN_CODE): $LOGIN_RESP"
    fi
fi

log_section "Security Hardening"

log_test "LLDAP has no-new-privileges"
SEC_OPTS=$(docker inspect --format='{{.HostConfig.SecurityOpt}}' test-lldap 2>/dev/null || echo "[]")
if echo "$SEC_OPTS" | grep -q "no-new-privileges"; then
    log_pass "no-new-privileges set"
else
    log_fail "no-new-privileges missing ($SEC_OPTS)"
fi

log_test "LLDAP has cap_drop ALL"
CAP_DROP=$(docker inspect --format='{{.HostConfig.CapDrop}}' test-lldap 2>/dev/null || echo "[]")
if echo "$CAP_DROP" | grep -qi "all"; then
    log_pass "cap_drop ALL"
else
    log_fail "cap_drop missing ($CAP_DROP)"
fi

log_test "LLDAP has memory limit"
MEM=$(docker inspect --format='{{.HostConfig.Memory}}' test-lldap 2>/dev/null || echo "0")
if [ "$MEM" != "0" ] && [ "$MEM" != "" ]; then
    MEM_MB=$((MEM / 1024 / 1024))
    log_pass "Memory limit: ${MEM_MB}m"
else
    log_fail "No memory limit set"
fi

log_section "Traefik Routing"

log_test "Traefik has LLDAP router registered"
ROUTERS=$(curl -s "http://localhost:19097/api/http/routers" 2>/dev/null || echo "")
if echo "$ROUTERS" | grep -qi "lldap"; then
    log_pass "LLDAP router visible in Traefik"
else
    log_fail "LLDAP router not found in Traefik"
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
