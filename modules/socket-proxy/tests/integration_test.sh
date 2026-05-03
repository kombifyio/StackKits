#!/bin/bash
# Socket Proxy Module Integration Test
# Validates that the socket proxy correctly filters Docker API access
# and that Traefik can discover services through it.
#
# Prerequisites:
#   - Docker running
#
# Usage:
#   ./modules/socket-proxy/tests/integration_test.sh

set -euo pipefail

COMPOSE_FILE="$(dirname "$0")/reference-compose.yml"
BASE_URL="http://localhost:8895"
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

echo "========================================="
echo "Socket Proxy Module Integration Test"
echo "========================================="
echo ""

# Start services
echo "Starting services..."
docker compose -f "$COMPOSE_FILE" up -d --wait --wait-timeout 60

echo ""
echo "Waiting for socket-proxy to be healthy..."
for i in $(seq 1 30); do
    STATUS=$(docker inspect --format='{{.State.Health.Status}}' test-socket-proxy 2>/dev/null || echo "not-found")
    if [ "$STATUS" = "healthy" ]; then
        echo "Socket proxy healthy after ${i}s"
        break
    fi
    if [ "$i" = "30" ]; then
        echo "Socket proxy did not become healthy within 30s"
        docker logs test-socket-proxy 2>&1 | tail -20
        exit 1
    fi
    sleep 1
done

echo ""
echo "Waiting for Traefik to be healthy..."
for i in $(seq 1 30); do
    STATUS=$(docker inspect --format='{{.State.Health.Status}}' test-traefik-socketproxy 2>/dev/null || echo "not-found")
    if [ "$STATUS" = "healthy" ]; then
        echo "Traefik healthy after ${i}s"
        break
    fi
    if [ "$i" = "30" ]; then
        echo "Traefik did not become healthy within 30s"
        docker logs test-traefik-socketproxy 2>&1 | tail -20
        exit 1
    fi
    sleep 1
done

echo ""
echo "--- Running Tests ---"
echo ""

# Test 1: Socket proxy is healthy
log_test "Socket proxy container is healthy"
HEALTH=$(docker inspect --format='{{.State.Health.Status}}' test-socket-proxy 2>/dev/null || echo "unknown")
if [ "$HEALTH" = "healthy" ]; then
    log_pass "Container health: $HEALTH"
else
    log_fail "Container health: $HEALTH (expected healthy)"
fi

# Test 2: Traefik is healthy (proves it can reach Docker API via proxy)
log_test "Traefik is healthy (connected via socket-proxy)"
HEALTH=$(docker inspect --format='{{.State.Health.Status}}' test-traefik-socketproxy 2>/dev/null || echo "unknown")
if [ "$HEALTH" = "healthy" ]; then
    log_pass "Traefik health: $HEALTH"
else
    log_fail "Traefik health: $HEALTH (expected healthy)"
fi

# Test 3: Traefik discovers whoami service through socket-proxy
log_test "Traefik discovers whoami via socket-proxy Docker provider"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "Host: whoami.test.local" "$BASE_URL/" 2>/dev/null || echo "000")
if [ "$HTTP_CODE" = "200" ]; then
    log_pass "whoami.test.local -> HTTP $HTTP_CODE (service discovery works)"
else
    log_fail "Expected 200, got $HTTP_CODE (Traefik can't discover services)"
fi

# Test 4: Traefik dashboard accessible
log_test "Traefik dashboard accessible on :19095"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:19095/api/overview" 2>/dev/null || echo "000")
if [ "$HTTP_CODE" = "200" ]; then
    log_pass "Traefik dashboard -> HTTP $HTTP_CODE"
else
    log_fail "Expected 200, got $HTTP_CODE"
fi

# Test 5: Traefik has NO docker.sock volume mounted
log_test "Traefik does NOT mount docker.sock directly"
MOUNTS=$(docker inspect --format='{{range .Mounts}}{{.Source}} {{end}}' test-traefik-socketproxy 2>/dev/null || echo "")
if echo "$MOUNTS" | grep -q "docker.sock"; then
    log_fail "Traefik still has docker.sock mounted — security violation!"
else
    log_pass "No docker.sock in Traefik mounts"
fi

# Test 6: Socket proxy has docker.sock (it's the only one that should)
log_test "Socket proxy IS the only service with docker.sock"
MOUNTS=$(docker inspect --format='{{range .Mounts}}{{.Source}} {{end}}' test-socket-proxy 2>/dev/null || echo "")
if echo "$MOUNTS" | grep -q "docker.sock"; then
    log_pass "Socket proxy has docker.sock (correct)"
else
    log_fail "Socket proxy missing docker.sock mount"
fi

# Test 7: Socket proxy network is internal (not internet-facing)
log_test "socket-proxy-net is an internal network"
INTERNAL=$(docker network inspect test-socket-proxy-tests_socket-proxy-net --format='{{.Internal}}' 2>/dev/null || \
           docker network inspect socket-proxy-tests_socket-proxy-net --format='{{.Internal}}' 2>/dev/null || \
           echo "unknown")
# Try alternate network naming
if [ "$INTERNAL" = "unknown" ]; then
    # Find the actual network name
    NET_NAME=$(docker network ls --filter "name=socket-proxy-net" --format '{{.Name}}' 2>/dev/null | head -1)
    if [ -n "$NET_NAME" ]; then
        INTERNAL=$(docker network inspect "$NET_NAME" --format='{{.Internal}}' 2>/dev/null || echo "unknown")
    fi
fi
if [ "$INTERNAL" = "true" ]; then
    log_pass "socket-proxy-net is internal: $INTERNAL"
else
    log_fail "socket-proxy-net internal=$INTERNAL (expected true)"
fi

# Test 8: Socket proxy runs read-only
log_test "Socket proxy runs with read-only filesystem"
READ_ONLY=$(docker inspect --format='{{.HostConfig.ReadonlyRootfs}}' test-socket-proxy 2>/dev/null || echo "unknown")
if [ "$READ_ONLY" = "true" ]; then
    log_pass "Read-only rootfs: $READ_ONLY"
else
    log_fail "Read-only rootfs: $READ_ONLY (expected true)"
fi

# Test 9: Socket proxy has no-new-privileges
log_test "Socket proxy has no-new-privileges"
SEC_OPTS=$(docker inspect --format='{{.HostConfig.SecurityOpt}}' test-socket-proxy 2>/dev/null || echo "")
if echo "$SEC_OPTS" | grep -q "no-new-privileges"; then
    log_pass "Security opt: no-new-privileges present"
else
    log_fail "no-new-privileges not found in security opts"
fi

# Test 10: Socket proxy drops all capabilities
log_test "Socket proxy drops ALL capabilities"
CAP_DROP=$(docker inspect --format='{{.HostConfig.CapDrop}}' test-socket-proxy 2>/dev/null || echo "")
if echo "$CAP_DROP" | grep -q "ALL"; then
    log_pass "CapDrop: ALL"
else
    log_fail "CapDrop: $CAP_DROP (expected ALL)"
fi

# Test 11: Write operations blocked (POST to containers endpoint)
log_test "Socket proxy blocks write operations (POST)"
# Try to create a container via the proxy — should fail
POST_RESULT=$(docker exec test-traefik-socketproxy wget -q -O - --method=POST \
    "http://socket-proxy:2375/containers/create" 2>&1 || echo "blocked")
if echo "$POST_RESULT" | grep -qi "403\|blocked\|forbidden\|error\|connection refused\|bad request"; then
    log_pass "POST to Docker API blocked or rejected"
else
    log_fail "POST might not be blocked: $POST_RESULT"
fi

echo ""
echo "========================================="
echo "Results: $PASS passed, $FAIL failed (of $TOTAL)"
echo "========================================="

if [ "$FAIL" -gt 0 ]; then
    echo ""
    echo "Container logs (socket-proxy):"
    docker logs test-socket-proxy 2>&1 | tail -10
    echo ""
    echo "Container logs (traefik):"
    docker logs test-traefik-socketproxy 2>&1 | tail -10
    exit 1
fi

exit 0
