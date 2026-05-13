#!/bin/bash
# Dokploy Module Integration Test
# Tests Dokploy PaaS + PostgreSQL + Redis + Traefik in isolation
#
# Usage:
#   ./modules/dokploy/tests/integration_test.sh

set -euo pipefail

COMPOSE_FILE="$(dirname "$0")/reference-compose.yml"
BASE_URL="http://localhost:8892"
PASS=0
FAIL=0
TOTAL=0

export DOKPLOY_TEST_POSTGRES_PASSWORD="${DOKPLOY_TEST_POSTGRES_PASSWORD:-$(od -An -N18 -tx1 /dev/urandom | tr -d ' \n')}"
export DOKPLOY_TEST_ENCRYPTION_KEY="${DOKPLOY_TEST_ENCRYPTION_KEY:-$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')}"

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

SWARM_INITED_BY_US=0

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
    if [ "$SWARM_INITED_BY_US" = "1" ]; then
        echo "Leaving swarm (was initialised by this test)..."
        docker swarm leave --force 2>/dev/null || true
    fi
}

trap cleanup EXIT

echo "========================================="
echo "Dokploy Module Integration Test"
echo "========================================="
echo ""

# Dokploy is architected around Docker Swarm and crashes with an unhandled
# rejection ("This node is not a swarm manager") when it can't reach a
# swarm API. Initialise swarm if the host isn't already a manager.
SWARM_STATE=$(docker info --format '{{.Swarm.LocalNodeState}}' 2>/dev/null || echo "")
if [ "$SWARM_STATE" != "active" ]; then
    echo "Docker swarm not active (state=$SWARM_STATE). Initialising..."
    # Use an advertise address that works inside CI (docker0 / first non-loopback).
    ADV_ADDR=""
    if command -v ip >/dev/null 2>&1; then
        ADV_ADDR=$(ip -4 addr show docker0 2>/dev/null | awk '/inet /{print $2}' | cut -d/ -f1)
    fi
    if [ -z "$ADV_ADDR" ]; then
        ADV_ADDR=$( (hostname -I 2>/dev/null || true) | awk '{print $1}' )
    fi
    if [ -n "$ADV_ADDR" ]; then
        docker swarm init --advertise-addr "$ADV_ADDR" >/dev/null
    else
        docker swarm init >/dev/null
    fi
    SWARM_INITED_BY_US=1
    echo "Swarm initialised."
fi

echo "Starting services..."
# Note: --wait is not used because the provisioner container exits (by design).
# We poll for service health below.
docker compose -f "$COMPOSE_FILE" up -d

echo ""
echo "Waiting for Dokploy to be healthy..."
for i in $(seq 1 90); do
    STATUS=$(docker inspect --format='{{.State.Health.Status}}' test-dokploy 2>/dev/null || echo "not-found")
    if [ "$STATUS" = "healthy" ]; then
        echo "Dokploy is healthy after ${i}s"
        break
    fi
    if [ "$i" = "90" ]; then
        echo "Dokploy did not become healthy within 90s"
        echo "Container logs:"
        docker logs test-dokploy 2>&1 | tail -20
        exit 1
    fi
    sleep 1
done

echo ""
echo "--- Running Tests ---"
echo ""

# Test 1: Dokploy UI accessible
log_test "Dokploy UI returns 200 at dokploy.test.local"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "Host: dokploy.test.local" "$BASE_URL/" 2>/dev/null || echo "000")
if [[ "$HTTP_CODE" =~ ^(200|302|303|307|308)$ ]]; then
    log_pass "dokploy.test.local → HTTP $HTTP_CODE"
else
    log_fail "Expected 200 or redirect, got $HTTP_CODE"
fi

# Test 2: Dokploy health endpoint
log_test "Dokploy /api/health returns 200"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "Host: dokploy.test.local" "$BASE_URL/api/health" 2>/dev/null || echo "000")
if [ "$HTTP_CODE" = "200" ]; then
    log_pass "/api/health → HTTP $HTTP_CODE"
else
    log_fail "Expected 200, got $HTTP_CODE"
fi

# Test 3: PostgreSQL healthy
log_test "PostgreSQL container is healthy"
HEALTH=$(docker inspect --format='{{.State.Health.Status}}' test-dokploy-postgres 2>/dev/null || echo "unknown")
if [ "$HEALTH" = "healthy" ]; then
    log_pass "PostgreSQL health: $HEALTH"
else
    log_fail "PostgreSQL health: $HEALTH (expected healthy)"
fi

# Test 4: Redis healthy
log_test "Redis container is healthy"
HEALTH=$(docker inspect --format='{{.State.Health.Status}}' test-dokploy-redis 2>/dev/null || echo "unknown")
if [ "$HEALTH" = "healthy" ]; then
    log_pass "Redis health: $HEALTH"
else
    log_fail "Redis health: $HEALTH (expected healthy)"
fi

# Test 5: PostgreSQL isolation — NOT on test-net (only test-db-net)
log_test "PostgreSQL is NOT accessible from the traefik network"
PG_NETS=$(docker inspect --format='{{range $k, $v := .NetworkSettings.Networks}}{{$k}} {{end}}' test-dokploy-postgres 2>/dev/null || echo "")
if echo "$PG_NETS" | grep -q "test-db-net" && ! echo "$PG_NETS" | grep -q "test-net"; then
    log_pass "PostgreSQL isolated on test-db-net only: $PG_NETS"
else
    log_fail "PostgreSQL network isolation broken. Networks: $PG_NETS"
fi

# Test 6: All 3 containers running
log_test "All 3 Dokploy containers are running"
RUNNING=$(docker ps --filter "name=test-dokploy" --format "{{.Names}}" | wc -l)
if [ "$RUNNING" -ge 3 ]; then
    log_pass "$RUNNING Dokploy-related containers running"
else
    log_fail "Expected 3+ containers, got $RUNNING"
fi

# Test 7: Traefik dashboard accessible
log_test "Traefik dashboard accessible on :19092"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:19092/api/overview" 2>/dev/null || echo "000")
if [ "$HTTP_CODE" = "200" ]; then
    log_pass "Traefik dashboard → HTTP $HTTP_CODE"
else
    log_fail "Expected 200, got $HTTP_CODE"
fi

# Test 8: Traefik has dokploy router
log_test "Traefik has dokploy router registered"
ROUTERS=$(curl -s "http://localhost:19092/api/http/routers" 2>/dev/null || echo "")
if echo "$ROUTERS" | grep -q "dokploy"; then
    log_pass "dokploy router found in Traefik"
else
    log_fail "dokploy router NOT found in Traefik"
fi

# --- Security Hardening ---

log_test "Dokploy has no-new-privileges"
SEC_OPTS=$(docker inspect --format='{{.HostConfig.SecurityOpt}}' test-dokploy 2>/dev/null || echo "[]")
if echo "$SEC_OPTS" | grep -q "no-new-privileges"; then
    log_pass "no-new-privileges set"
else
    log_fail "no-new-privileges missing ($SEC_OPTS)"
fi

log_test "Dokploy has cap_drop ALL"
CAP_DROP=$(docker inspect --format='{{.HostConfig.CapDrop}}' test-dokploy 2>/dev/null || echo "[]")
if echo "$CAP_DROP" | grep -qi "all"; then
    log_pass "cap_drop ALL"
else
    log_fail "cap_drop missing ($CAP_DROP)"
fi

log_test "Dokploy has memory limit"
MEM=$(docker inspect --format='{{.HostConfig.Memory}}' test-dokploy 2>/dev/null || echo "0")
if [ "$MEM" != "0" ] && [ "$MEM" != "" ]; then
    MEM_MB=$((MEM / 1024 / 1024))
    log_pass "Memory limit: ${MEM_MB}m"
else
    log_fail "No memory limit set"
fi

log_test "Redis has read-only rootfs"
READ_ONLY=$(docker inspect --format='{{.HostConfig.ReadonlyRootfs}}' test-dokploy-redis 2>/dev/null || echo "unknown")
if [ "$READ_ONLY" = "true" ]; then
    log_pass "Redis read-only rootfs: $READ_ONLY"
else
    log_fail "Redis read-only rootfs: $READ_ONLY (expected true)"
fi

log_test "PostgreSQL has no-new-privileges"
SEC_OPTS=$(docker inspect --format='{{.HostConfig.SecurityOpt}}' test-dokploy-postgres 2>/dev/null || echo "[]")
if echo "$SEC_OPTS" | grep -q "no-new-privileges"; then
    log_pass "PostgreSQL no-new-privileges set"
else
    log_fail "PostgreSQL no-new-privileges missing ($SEC_OPTS)"
fi

echo ""
echo "========================================="
echo "Results: $PASS passed, $FAIL failed (of $TOTAL)"
echo "========================================="

if [ "$FAIL" -gt 0 ]; then
    echo ""
    echo "Container logs (dokploy):"
    docker logs test-dokploy 2>&1 | tail -10
    exit 1
fi

exit 0
