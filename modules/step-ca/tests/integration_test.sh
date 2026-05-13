#!/bin/bash
# Step-CA Module Integration Test
# Validates Step-CA internal PKI, health endpoint, ACME provisioner,
# and root CA generation.
#
# Tests: Step-CA health, ACME directory, provisioners, root CA,
# security hardening.
#
# Usage:
#   ./modules/step-ca/tests/integration_test.sh

set -euo pipefail

COMPOSE_FILE="$(dirname "$0")/reference-compose.yml"
BASE_URL="http://localhost:8898"
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
echo "Step-CA Module Integration Test"
echo "============================================="
echo "Services: step-ca, traefik, whoami"
echo "============================================="
echo ""

echo "Starting services..."
# Note: --wait is not used to keep behavior consistent with other module tests.
# We poll for service health below.
docker compose -f "$COMPOSE_FILE" up -d

echo ""
echo "Waiting for Step-CA to be healthy (up to 90s)..."
for i in $(seq 1 90); do
    STATUS=$(docker inspect --format='{{.State.Health.Status}}' test-step-ca 2>/dev/null || echo "not-found")
    if [ "$STATUS" = "healthy" ]; then
        echo "  Step-CA: healthy (${i}s)"
        break
    fi
    if [ "$i" = "90" ]; then
        echo "  Step-CA: NOT healthy after 90s (status: $STATUS)"
        docker logs test-step-ca 2>&1 | tail -20
    fi
    sleep 1
done

echo "Waiting for Traefik to be healthy (up to 60s)..."
for i in $(seq 1 60); do
    STATUS=$(docker inspect --format='{{if .State.Health}}{{.State.Health.Status}}{{else}}no-healthcheck{{end}}' test-traefik-stepca 2>/dev/null || echo "not-found")
    if [ "$STATUS" = "healthy" ]; then
        echo "  Traefik: healthy (${i}s)"
        break
    fi
    if [ "$i" = "60" ]; then
        echo "  Traefik: NOT healthy after 60s (status: $STATUS)"
        docker logs test-traefik-stepca 2>&1 | tail -20
    fi
    sleep 1
done

echo ""
echo "--- Running Tests ---"

log_section "Container Health"

log_test "Step-CA container is healthy"
HEALTH=$(docker inspect --format='{{if .State.Health}}{{.State.Health.Status}}{{else}}no-healthcheck{{end}}' test-step-ca 2>/dev/null || echo "not-found")
if [ "$HEALTH" = "healthy" ]; then
    log_pass "Step-CA: $HEALTH"
else
    log_fail "Step-CA: $HEALTH"
fi

log_test "Traefik container is healthy"
HEALTH=$(docker inspect --format='{{if .State.Health}}{{.State.Health.Status}}{{else}}no-healthcheck{{end}}' test-traefik-stepca 2>/dev/null || echo "not-found")
if [ "$HEALTH" = "healthy" ]; then
    log_pass "Traefik: $HEALTH"
else
    log_fail "Traefik: $HEALTH"
fi

log_section "Step-CA API"

log_test "Step-CA health endpoint responds"
HEALTH_RESP=$(docker exec test-step-ca curl -s -k https://localhost:9000/health 2>/dev/null || echo "error")
if echo "$HEALTH_RESP" | grep -qi "ok"; then
    log_pass "Health: $HEALTH_RESP"
else
    log_fail "Health response: $HEALTH_RESP"
fi

log_test "Step-CA root CA was generated"
ROOT_EXISTS=$(docker exec test-step-ca sh -c 'test -f /home/step/certs/root_ca.crt' 2>/dev/null && echo "yes" || echo "no")
if [ "$ROOT_EXISTS" = "yes" ]; then
    log_pass "Root CA certificate exists"
else
    log_fail "Root CA certificate not found"
fi

log_test "Step-CA intermediate CA was generated"
INT_EXISTS=$(docker exec test-step-ca sh -c 'test -f /home/step/certs/intermediate_ca.crt' 2>/dev/null && echo "yes" || echo "no")
if [ "$INT_EXISTS" = "yes" ]; then
    log_pass "Intermediate CA certificate exists"
else
    log_fail "Intermediate CA certificate not found"
fi

log_section "ACME Provisioner"

log_test "ACME directory endpoint is accessible"
ACME_DIR=$(docker exec test-step-ca curl -s -k https://localhost:9000/acme/acme/directory 2>/dev/null || echo "error")
if echo "$ACME_DIR" | grep -qi "newNonce\|newAccount\|newOrder"; then
    log_pass "ACME directory returns valid response"
else
    log_fail "ACME directory: $ACME_DIR"
fi

log_test "Provisioners list includes ACME"
PROVISIONERS=$(docker exec test-step-ca curl -s -k https://localhost:9000/provisioners 2>/dev/null || echo "error")
if echo "$PROVISIONERS" | grep -qi "ACME"; then
    log_pass "ACME provisioner registered"
else
    log_fail "ACME provisioner not found: $PROVISIONERS"
fi

log_test "Provisioners list includes JWK admin"
if echo "$PROVISIONERS" | grep -qi "JWK"; then
    log_pass "JWK admin provisioner registered"
else
    log_fail "JWK provisioner not found"
fi

log_section "CA Configuration"

log_test "CA config file exists"
CONFIG_EXISTS=$(docker exec test-step-ca sh -c 'test -f /home/step/config/ca.json' 2>/dev/null && echo "yes" || echo "no")
if [ "$CONFIG_EXISTS" = "yes" ]; then
    log_pass "ca.json exists"
else
    log_fail "ca.json not found"
fi

log_test "CA DNS names include local hostname"
CA_DNS=$(docker exec test-step-ca sh -c 'grep -o "\"ca.test.local\"" /home/step/config/ca.json' 2>/dev/null || echo "")
if [ -n "$CA_DNS" ]; then
    log_pass "CA DNS name: ca.test.local"
else
    log_fail "ca.test.local not found in config"
fi

log_section "Security Hardening"

log_test "Step-CA has no-new-privileges"
SEC_OPTS=$(docker inspect --format='{{.HostConfig.SecurityOpt}}' test-step-ca 2>/dev/null || echo "[]")
if echo "$SEC_OPTS" | grep -q "no-new-privileges"; then
    log_pass "no-new-privileges set"
else
    log_fail "no-new-privileges missing ($SEC_OPTS)"
fi

log_test "Step-CA has cap_drop ALL"
CAP_DROP=$(docker inspect --format='{{.HostConfig.CapDrop}}' test-step-ca 2>/dev/null || echo "[]")
if echo "$CAP_DROP" | grep -qi "all"; then
    log_pass "cap_drop ALL"
else
    log_fail "cap_drop missing ($CAP_DROP)"
fi

log_test "Step-CA has memory limit"
MEM=$(docker inspect --format='{{.HostConfig.Memory}}' test-step-ca 2>/dev/null || echo "0")
if [ "$MEM" != "0" ] && [ "$MEM" != "" ]; then
    MEM_MB=$((MEM / 1024 / 1024))
    log_pass "Memory limit: ${MEM_MB}m"
else
    log_fail "No memory limit set"
fi

log_section "Traefik Integration"

log_test "Whoami responds through Traefik (HTTP)"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "Host: whoami.test.local" "$BASE_URL/" 2>/dev/null || echo "000")
if [ "$HTTP_CODE" = "200" ]; then
    log_pass "whoami.test.local → $HTTP_CODE"
else
    log_fail "Expected 200, got $HTTP_CODE"
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
