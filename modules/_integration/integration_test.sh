#!/bin/bash
# Full-Stack Composition Integration Test
# Validates all Base Kit modules work together as a unit.
#
# Tests: container health, domain routing, ForwardAuth protection,
# socket-proxy isolation, network segmentation, container hardening.
#
# Usage:
#   ./modules/_integration/integration_test.sh

set -euo pipefail

COMPOSE_FILE="$(dirname "$0")/full-stack-compose.yml"
BASE_URL="http://localhost:8900"
PASS=0
FAIL=0
TOTAL=0

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

log_section() {
    echo -e "\n${CYAN}=== $1 ===${NC}\n"
}

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

echo "============================================="
echo "Full-Stack Composition Integration Test"
echo "============================================="
echo "Services: socket-proxy, traefik, tinyauth,"
echo "          pocketid, dokploy (+postgres, +redis),"
echo "          kuma, dozzle, whoami, dashboard"
echo "Security: socket-proxy, hardening, net isolation"
echo "============================================="
echo ""

# Dokploy is architected around Docker Swarm and crashes with an unhandled
# rejection ("This node is not a swarm manager") when it can't reach a
# swarm API. Initialise swarm if the host isn't already a manager.
SWARM_STATE=$(docker info --format '{{.Swarm.LocalNodeState}}' 2>/dev/null || echo "")
if [ "$SWARM_STATE" != "active" ]; then
    echo "Docker swarm not active (state=$SWARM_STATE). Initialising..."
    ADV_ADDR=$(ip -4 addr show docker0 2>/dev/null | awk '/inet /{print $2}' | cut -d/ -f1)
    if [ -z "$ADV_ADDR" ]; then
        ADV_ADDR=$(hostname -I 2>/dev/null | awk '{print $1}')
    fi
    if [ -n "$ADV_ADDR" ]; then
        docker swarm init --advertise-addr "$ADV_ADDR" >/dev/null
    else
        docker swarm init >/dev/null
    fi
    SWARM_INITED_BY_US=1
    echo "Swarm initialised."
fi

echo "Starting all services..."
# Note: --wait may race with short-lived provisioners (dokploy-provisioner,
# pocketid-provisioner, kuma-provisioner) that exit 0 after seeding. Poll
# service health explicitly below instead of relying on --wait alone.
docker compose -f "$COMPOSE_FILE" up -d

echo ""
echo "Waiting for all services to be healthy (up to 120s)..."
SERVICES="stack-socket-proxy stack-traefik stack-tinyauth stack-pocketid stack-dokploy stack-uptime-kuma stack-dozzle stack-dashboard"
for svc in $SERVICES; do
    for i in $(seq 1 120); do
        STATUS=$(docker inspect --format='{{.State.Health.Status}}' "$svc" 2>/dev/null || echo "not-found")
        if [ "$STATUS" = "healthy" ]; then
            echo "  $svc: healthy (${i}s)"
            break
        fi
        if [ "$i" = "120" ]; then
            echo "  $svc: NOT healthy after 120s (status: $STATUS)"
            docker logs "$svc" 2>&1 | tail -5
        fi
        sleep 1
    done
done

echo ""
echo "--- Running Tests ---"

# =============================================
log_section "1. Container Health"
# =============================================

for svc in stack-socket-proxy stack-traefik stack-tinyauth stack-pocketid stack-dokploy stack-dokploy-postgres stack-dokploy-redis stack-uptime-kuma stack-dozzle stack-dashboard; do
    log_test "$svc container is healthy"
    HEALTH=$(docker inspect --format='{{if .State.Health}}{{.State.Health.Status}}{{else}}no-healthcheck{{end}}' "$svc" 2>/dev/null || echo "not-found")
    STATE=$(docker inspect --format='{{.State.Status}}' "$svc" 2>/dev/null || echo "not-found")
    if [ "$HEALTH" = "healthy" ] || ([ "$HEALTH" = "no-healthcheck" ] && [ "$STATE" = "running" ]); then
        log_pass "$svc: $HEALTH ($STATE)"
    else
        log_fail "$svc: health=$HEALTH state=$STATE"
    fi
done

# whoami has no healthcheck (scratch image)
log_test "stack-whoami container is running"
STATE=$(docker inspect --format='{{.State.Status}}' stack-whoami 2>/dev/null || echo "not-found")
if [ "$STATE" = "running" ]; then
    log_pass "stack-whoami: running"
else
    log_fail "stack-whoami: $STATE"
fi

# =============================================
log_section "2. Domain Routing (via Traefik)"
# =============================================

# Auth services accessible without auth
log_test "TinyAuth (auth.test.local) accessible — returns 200"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "Host: auth.test.local" "$BASE_URL/" 2>/dev/null || echo "000")
if [ "$HTTP_CODE" = "200" ]; then
    log_pass "auth.test.local → $HTTP_CODE"
else
    log_fail "Expected 200, got $HTTP_CODE"
fi

log_test "PocketID (id.test.local) accessible — returns 200 or 302"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "Host: id.test.local" "$BASE_URL/" 2>/dev/null || echo "000")
if [ "$HTTP_CODE" = "200" ] || [ "$HTTP_CODE" = "302" ]; then
    log_pass "id.test.local → $HTTP_CODE"
else
    log_fail "Expected 200/302, got $HTTP_CODE"
fi

# Protected services return 401 (API client, no Accept: text/html)
for domain in whoami.test.local dokploy.test.local kuma.test.local; do
    log_test "$domain protected by TinyAuth — returns 401"
    HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "Host: $domain" "$BASE_URL/" 2>/dev/null || echo "000")
    if [ "$HTTP_CODE" = "401" ]; then
        log_pass "$domain → $HTTP_CODE (protected)"
    else
        log_fail "Expected 401, got $HTTP_CODE"
    fi
done

# Unprotected services
log_test "Dozzle (logs.test.local) accessible — returns 200 or 302"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "Host: logs.test.local" "$BASE_URL/" 2>/dev/null || echo "000")
if [ "$HTTP_CODE" = "200" ] || [ "$HTTP_CODE" = "302" ]; then
    log_pass "logs.test.local → $HTTP_CODE"
else
    log_fail "Expected 200/302, got $HTTP_CODE"
fi

log_test "Dashboard (dash.test.local) accessible — returns 200"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "Host: dash.test.local" "$BASE_URL/" 2>/dev/null || echo "000")
if [ "$HTTP_CODE" = "200" ]; then
    log_pass "dash.test.local → $HTTP_CODE"
else
    log_fail "Expected 200, got $HTTP_CODE"
fi

# =============================================
log_section "3. Socket Proxy (E3.1)"
# =============================================

log_test "Socket-proxy is the ONLY service with docker.sock"
CONTAINERS_WITH_SOCK=""
for cid in $(docker compose -f "$COMPOSE_FILE" ps -q 2>/dev/null); do
    CNAME=$(docker inspect --format='{{.Name}}' "$cid" | sed 's|^/||')
    MOUNTS=$(docker inspect --format='{{range .Mounts}}{{.Source}} {{end}}' "$cid" 2>/dev/null || echo "")
    if echo "$MOUNTS" | grep -q "docker.sock"; then
        CONTAINERS_WITH_SOCK="$CONTAINERS_WITH_SOCK $CNAME"
    fi
done
# Dokploy also has docker.sock (exception: needs POST/EXEC)
EXPECTED="stack-dokploy stack-socket-proxy"
SORTED_ACTUAL=$(echo "$CONTAINERS_WITH_SOCK" | tr ' ' '\n' | sort | tr '\n' ' ' | xargs)
SORTED_EXPECTED=$(echo "$EXPECTED" | tr ' ' '\n' | sort | tr '\n' ' ' | xargs)
if [ "$SORTED_ACTUAL" = "$SORTED_EXPECTED" ]; then
    log_pass "Only socket-proxy and dokploy have docker.sock: $SORTED_ACTUAL"
else
    log_fail "Unexpected docker.sock mounts: $SORTED_ACTUAL (expected: $SORTED_EXPECTED)"
fi

log_test "Traefik has NO docker.sock mount"
TRAEFIK_MOUNTS=$(docker inspect --format='{{range .Mounts}}{{.Source}} {{end}}' stack-traefik 2>/dev/null || echo "")
if ! echo "$TRAEFIK_MOUNTS" | grep -q "docker.sock"; then
    log_pass "Traefik has no docker.sock"
else
    log_fail "Traefik still has docker.sock: $TRAEFIK_MOUNTS"
fi

log_test "TinyAuth uses DOCKER_HOST=tcp://socket-proxy:2375"
TA_DOCKER_HOST=$(docker inspect --format='{{range .Config.Env}}{{println .}}{{end}}' stack-tinyauth 2>/dev/null | grep "^DOCKER_HOST=" || echo "")
if [ "$TA_DOCKER_HOST" = "DOCKER_HOST=tcp://socket-proxy:2375" ]; then
    log_pass "TinyAuth DOCKER_HOST correct"
else
    log_fail "TinyAuth DOCKER_HOST: $TA_DOCKER_HOST"
fi

log_test "Dozzle uses DOCKER_HOST=tcp://socket-proxy:2375"
DZ_DOCKER_HOST=$(docker inspect --format='{{range .Config.Env}}{{println .}}{{end}}' stack-dozzle 2>/dev/null | grep "^DOCKER_HOST=" || echo "")
if [ "$DZ_DOCKER_HOST" = "DOCKER_HOST=tcp://socket-proxy:2375" ]; then
    log_pass "Dozzle DOCKER_HOST correct"
else
    log_fail "Dozzle DOCKER_HOST: $DZ_DOCKER_HOST"
fi

log_test "Socket-proxy blocks POST requests"
# Try to create a network via socket-proxy (should be blocked)
POST_RESULT=$(docker exec stack-tinyauth sh -c 'wget -q -O - --post-data="" http://socket-proxy:2375/networks/create 2>&1' || echo "blocked")
if echo "$POST_RESULT" | grep -qi "403\|forbidden\|blocked\|error\|failed"; then
    log_pass "POST blocked by socket-proxy"
else
    log_fail "POST may not be blocked: $POST_RESULT"
fi

# =============================================
log_section "4. Network Isolation (E3.3)"
# =============================================

log_test "PostgreSQL is on backend only (not frontend)"
PG_NETS=$(docker inspect --format='{{range $k, $v := .NetworkSettings.Networks}}{{$k}} {{end}}' stack-dokploy-postgres 2>/dev/null || echo "")
if echo "$PG_NETS" | grep -q "backend" && ! echo "$PG_NETS" | grep -q "frontend"; then
    log_pass "PostgreSQL isolated: $PG_NETS"
else
    log_fail "PostgreSQL network isolation broken: $PG_NETS"
fi

log_test "Redis is on backend only (not frontend)"
REDIS_NETS=$(docker inspect --format='{{range $k, $v := .NetworkSettings.Networks}}{{$k}} {{end}}' stack-dokploy-redis 2>/dev/null || echo "")
if echo "$REDIS_NETS" | grep -q "backend" && ! echo "$REDIS_NETS" | grep -q "frontend"; then
    log_pass "Redis isolated: $REDIS_NETS"
else
    log_fail "Redis network isolation broken: $REDIS_NETS"
fi

log_test "Socket-proxy is on socket-proxy-net only (not frontend)"
SP_NETS=$(docker inspect --format='{{range $k, $v := .NetworkSettings.Networks}}{{$k}} {{end}}' stack-socket-proxy 2>/dev/null || echo "")
if echo "$SP_NETS" | grep -q "socket-proxy-net" && ! echo "$SP_NETS" | grep -q "frontend"; then
    log_pass "Socket-proxy isolated: $SP_NETS"
else
    log_fail "Socket-proxy network isolation broken: $SP_NETS"
fi

log_test "TinyAuth on both frontend and socket-proxy-net"
TA_NETS=$(docker inspect --format='{{range $k, $v := .NetworkSettings.Networks}}{{$k}} {{end}}' stack-tinyauth 2>/dev/null || echo "")
if echo "$TA_NETS" | grep -q "frontend" && echo "$TA_NETS" | grep -q "socket-proxy-net"; then
    log_pass "TinyAuth networks correct: $TA_NETS"
else
    log_fail "TinyAuth networks wrong: $TA_NETS"
fi

log_test "Dozzle on both frontend and socket-proxy-net"
DZ_NETS=$(docker inspect --format='{{range $k, $v := .NetworkSettings.Networks}}{{$k}} {{end}}' stack-dozzle 2>/dev/null || echo "")
if echo "$DZ_NETS" | grep -q "frontend" && echo "$DZ_NETS" | grep -q "socket-proxy-net"; then
    log_pass "Dozzle networks correct: $DZ_NETS"
else
    log_fail "Dozzle networks wrong: $DZ_NETS"
fi

log_test "Traefik on both frontend and socket-proxy-net"
TK_NETS=$(docker inspect --format='{{range $k, $v := .NetworkSettings.Networks}}{{$k}} {{end}}' stack-traefik 2>/dev/null || echo "")
if echo "$TK_NETS" | grep -q "frontend" && echo "$TK_NETS" | grep -q "socket-proxy-net"; then
    log_pass "Traefik networks correct: $TK_NETS"
else
    log_fail "Traefik networks wrong: $TK_NETS"
fi

log_test "Dokploy on frontend and backend (not socket-proxy-net)"
DK_NETS=$(docker inspect --format='{{range $k, $v := .NetworkSettings.Networks}}{{$k}} {{end}}' stack-dokploy 2>/dev/null || echo "")
if echo "$DK_NETS" | grep -q "frontend" && echo "$DK_NETS" | grep -q "backend" && ! echo "$DK_NETS" | grep -q "socket-proxy-net"; then
    log_pass "Dokploy networks correct: $DK_NETS"
else
    log_fail "Dokploy networks wrong: $DK_NETS"
fi

# =============================================
log_section "5. Container Hardening (E3.2)"
# =============================================

# Check no-new-privileges on all containers
for svc in stack-socket-proxy stack-traefik stack-tinyauth stack-pocketid stack-dokploy stack-dokploy-postgres stack-dokploy-redis stack-uptime-kuma stack-dozzle stack-whoami stack-dashboard; do
    log_test "$svc has no-new-privileges"
    SEC_OPTS=$(docker inspect --format='{{.HostConfig.SecurityOpt}}' "$svc" 2>/dev/null || echo "[]")
    if echo "$SEC_OPTS" | grep -q "no-new-privileges"; then
        log_pass "$svc: no-new-privileges set"
    else
        log_fail "$svc: no-new-privileges missing ($SEC_OPTS)"
    fi
done

# Check cap_drop ALL on all containers
for svc in stack-socket-proxy stack-traefik stack-tinyauth stack-pocketid stack-dokploy stack-dokploy-postgres stack-dokploy-redis stack-uptime-kuma stack-dozzle stack-whoami stack-dashboard; do
    log_test "$svc has cap_drop ALL"
    CAP_DROP=$(docker inspect --format='{{.HostConfig.CapDrop}}' "$svc" 2>/dev/null || echo "[]")
    if echo "$CAP_DROP" | grep -qi "all"; then
        log_pass "$svc: cap_drop ALL"
    else
        log_fail "$svc: cap_drop missing ($CAP_DROP)"
    fi
done

# Check read_only on services that support it
for svc in stack-socket-proxy stack-traefik stack-tinyauth stack-dozzle stack-whoami stack-dashboard stack-dokploy-redis; do
    log_test "$svc has read-only rootfs"
    RO=$(docker inspect --format='{{.HostConfig.ReadonlyRootfs}}' "$svc" 2>/dev/null || echo "false")
    if [ "$RO" = "true" ]; then
        log_pass "$svc: read-only rootfs"
    else
        log_fail "$svc: read-only rootfs not set ($RO)"
    fi
done

# Check memory limits on all containers
for svc in stack-socket-proxy stack-traefik stack-tinyauth stack-pocketid stack-dokploy stack-dokploy-postgres stack-dokploy-redis stack-uptime-kuma stack-dozzle stack-whoami stack-dashboard; do
    log_test "$svc has memory limit"
    MEM=$(docker inspect --format='{{.HostConfig.Memory}}' "$svc" 2>/dev/null || echo "0")
    if [ "$MEM" != "0" ]; then
        MEM_MB=$((MEM / 1024 / 1024))
        log_pass "$svc: ${MEM_MB}MB limit"
    else
        log_fail "$svc: no memory limit set"
    fi
done

# =============================================
log_section "6. Service-Specific Checks"
# =============================================

log_test "PocketID OIDC well-known endpoint returns 200"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "Host: id.test.local" "$BASE_URL/.well-known/openid-configuration" 2>/dev/null || echo "000")
if [ "$HTTP_CODE" = "200" ]; then
    log_pass ".well-known/openid-configuration → $HTTP_CODE"
else
    log_fail "Expected 200, got $HTTP_CODE"
fi

log_test "TinyAuth ForwardAuth returns 401 for API client"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "Host: auth.test.local" "$BASE_URL/api/auth/traefik" 2>/dev/null || echo "000")
if [ "$HTTP_CODE" = "401" ]; then
    log_pass "ForwardAuth → $HTTP_CODE"
else
    log_fail "Expected 401, got $HTTP_CODE"
fi

log_test "Traefik dashboard returns 200"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:19100/api/overview" 2>/dev/null || echo "000")
if [ "$HTTP_CODE" = "200" ]; then
    log_pass "Traefik dashboard → $HTTP_CODE"
else
    log_fail "Expected 200, got $HTTP_CODE"
fi

log_test "Dashboard contains HTML content"
BODY=$(curl -s -H "Host: dash.test.local" "$BASE_URL/" 2>/dev/null || echo "")
if echo "$BODY" | grep -qi "StackKit"; then
    log_pass "Dashboard contains 'StackKit'"
else
    log_fail "Dashboard content missing expected text"
fi

# =============================================
log_section "7. Security Headers (E3.4)"
# =============================================

# Test security headers on an unprotected route (dashboard — no auth redirect)
HEADERS=$(curl -s -D - -o /dev/null -H "Host: dash.test.local" "$BASE_URL/" 2>/dev/null || echo "")

log_test "X-Content-Type-Options: nosniff header present"
if echo "$HEADERS" | grep -qi "X-Content-Type-Options.*nosniff"; then
    log_pass "X-Content-Type-Options: nosniff"
else
    log_fail "X-Content-Type-Options header missing"
fi

log_test "X-Frame-Options: DENY header present"
if echo "$HEADERS" | grep -qi "X-Frame-Options.*DENY"; then
    log_pass "X-Frame-Options: DENY"
else
    log_fail "X-Frame-Options header missing"
fi

log_test "Referrer-Policy header present"
if echo "$HEADERS" | grep -qi "Referrer-Policy.*strict-origin-when-cross-origin"; then
    log_pass "Referrer-Policy: strict-origin-when-cross-origin"
else
    log_fail "Referrer-Policy header missing"
fi

log_test "Cross-Origin-Opener-Policy: same-origin header present"
if echo "$HEADERS" | grep -qi "Cross-Origin-Opener-Policy.*same-origin"; then
    log_pass "Cross-Origin-Opener-Policy: same-origin"
else
    log_fail "Cross-Origin-Opener-Policy header missing"
fi

log_test "Cross-Origin-Resource-Policy: same-origin header present"
if echo "$HEADERS" | grep -qi "Cross-Origin-Resource-Policy.*same-origin"; then
    log_pass "Cross-Origin-Resource-Policy: same-origin"
else
    log_fail "Cross-Origin-Resource-Policy header missing"
fi

log_test "X-Permitted-Cross-Domain-Policies: none header present"
if echo "$HEADERS" | grep -qi "X-Permitted-Cross-Domain-Policies.*none"; then
    log_pass "X-Permitted-Cross-Domain-Policies: none"
else
    log_fail "X-Permitted-Cross-Domain-Policies header missing"
fi

log_test "Security headers also present on protected routes"
PROTECTED_HEADERS=$(curl -s -D - -o /dev/null -H "Host: whoami.test.local" "$BASE_URL/" 2>/dev/null || echo "")
if echo "$PROTECTED_HEADERS" | grep -qi "X-Content-Type-Options.*nosniff"; then
    log_pass "Protected route has security headers"
else
    log_fail "Protected route missing security headers"
fi

# =============================================
log_section "8. Authentication Flow"
# =============================================

# Uses --resolve so curl handles cookies correctly (Domain=test.local)
RESOLVE="--resolve auth.test.local:8900:127.0.0.1 --resolve whoami.test.local:8900:127.0.0.1 --resolve dokploy.test.local:8900:127.0.0.1 --resolve kuma.test.local:8900:127.0.0.1"
COOKIE_JAR=$(mktemp)
AUTH_URL="http://auth.test.local:8900"

# Login with valid credentials
log_test "TinyAuth login with valid credentials → 200"
LOGIN_RESPONSE=$(curl -s -D - -o /dev/null -w "\n%{http_code}" \
  $RESOLVE \
  -c "$COOKIE_JAR" \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"admin123"}' \
  "$AUTH_URL/api/user/login" 2>/dev/null || echo "000")
LOGIN_CODE=$(echo "$LOGIN_RESPONSE" | tail -1)
if [ "$LOGIN_CODE" = "200" ]; then
    log_pass "Login → $LOGIN_CODE"
else
    log_fail "Expected 200, got $LOGIN_CODE"
fi

# Session cookie was set
log_test "Session cookie set after login"
if [ -s "$COOKIE_JAR" ] && grep -qi "tinyauth" "$COOKIE_JAR" 2>/dev/null; then
    log_pass "tinyauth session cookie present"
else
    log_fail "No tinyauth cookie found"
fi

# Authenticated access to each protected service
for domain in whoami.test.local dokploy.test.local kuma.test.local; do
    log_test "$domain returns 200 WITH valid session (not 401)"
    HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" \
      $RESOLVE \
      -b "$COOKIE_JAR" \
      "http://${domain}:8900/" 2>/dev/null || echo "000")
    if [ "$HTTP_CODE" = "200" ] || [ "$HTTP_CODE" = "302" ]; then
        log_pass "$domain WITH auth → $HTTP_CODE"
    else
        log_fail "Expected 200/302, got $HTTP_CODE"
    fi
done

# ForwardAuth passes identity headers (whoami echoes request headers)
log_test "ForwardAuth passes Remote-User header to upstream"
WHOAMI_BODY=$(curl -s \
  $RESOLVE \
  -b "$COOKIE_JAR" \
  "http://whoami.test.local:8900/" 2>/dev/null || echo "")
if echo "$WHOAMI_BODY" | grep -qi "Remote-User"; then
    REMOTE_USER=$(echo "$WHOAMI_BODY" | grep -i "Remote-User" | head -1)
    log_pass "Header found: $REMOTE_USER"
else
    log_fail "Remote-User header not passed by ForwardAuth"
fi

# Invalid credentials rejected
log_test "TinyAuth rejects wrong password → 401"
BAD_CODE=$(curl -s -o /dev/null -w "%{http_code}" \
  $RESOLVE \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"wrongpassword"}' \
  "$AUTH_URL/api/user/login" 2>/dev/null || echo "000")
if [ "$BAD_CODE" = "401" ]; then
    log_pass "Wrong password → $BAD_CODE"
else
    log_fail "Expected 401, got $BAD_CODE"
fi

log_test "TinyAuth rejects non-existent user → 401"
NOUSER_CODE=$(curl -s -o /dev/null -w "%{http_code}" \
  $RESOLVE \
  -H "Content-Type: application/json" \
  -d '{"username":"hacker","password":"admin123"}' \
  "$AUTH_URL/api/user/login" 2>/dev/null || echo "000")
if [ "$NOUSER_CODE" = "401" ]; then
    log_pass "Non-existent user → $NOUSER_CODE"
else
    log_fail "Expected 401, got $NOUSER_CODE"
fi

rm -f "$COOKIE_JAR"

# =============================================
log_section "Results"
# =============================================

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
