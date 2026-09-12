#!/bin/bash
# module-smoke.sh — shared single-module smoke harness (ADR-0027 G4).
#
# Generalizes the per-module integration tests (dokploy/uptime-kuma/...): bring a
# module up against a Traefik baseline, poll container health, poll the Traefik
# route, assert the HTTP health endpoint, assert db/cache network isolation, and
# assert the security posture (no-new-privileges / capDrop ALL / memory limit).
# The CUE healthCheck *is* the assertion — there is no per-module test code, only
# a declarative manifest.
#
# Usage: a module's tests/integration_test.sh exports the SMOKE_* variables
# below (optionally defining a smoke_pre_up hook) and then sources this file:
#
#   source "$(git rev-parse --show-toplevel)/scripts/ci/module-smoke.sh"
#
# Manifest contract (exported before sourcing):
#   SMOKE_MODULE               module slug (label only)                 [required]
#   SMOKE_COMPOSE              path to reference-compose.yml            [required]
#   SMOKE_PRIMARY_CONTAINER    routed service container name            [required]
#   SMOKE_HEALTH_HOST          Host header for the routed request       [required]
#   SMOKE_ROUTED_BASE_URL      base URL Traefik listens on              [required]
#   SMOKE_TRAEFIK_API          Traefik dashboard/api base URL           [required]
#   SMOKE_ROUTER_NAME          expected Traefik router name             [required]
#   SMOKE_HEALTH_PATH          health endpoint path (default "/")       [optional]
#   SMOKE_HEALTHY_CONTAINERS   space-separated must-become-healthy list [optional, default: PRIMARY]
#   SMOKE_SECURITY_CONTAINERS  space-separated hardening-assert list    [optional, default: HEALTHY]
#   SMOKE_ISOLATED             "ctr:present_net:absent_net" triples     [optional]
#   SMOKE_READONLY_CONTAINERS  space-separated read-only-rootfs list    [optional]
#   SMOKE_HEALTH_TIMEOUT       seconds to wait for health (default 90)  [optional]
#   SMOKE_ROUTE_TIMEOUT        seconds to wait for the route (default 30)[optional]
#   smoke_pre_up()             shell function run before compose up      [optional]

set -euo pipefail

: "${SMOKE_MODULE:?SMOKE_MODULE is required}"
: "${SMOKE_COMPOSE:?SMOKE_COMPOSE is required}"
: "${SMOKE_PRIMARY_CONTAINER:?SMOKE_PRIMARY_CONTAINER is required}"
: "${SMOKE_HEALTH_HOST:?SMOKE_HEALTH_HOST is required}"
: "${SMOKE_ROUTED_BASE_URL:?SMOKE_ROUTED_BASE_URL is required}"
: "${SMOKE_TRAEFIK_API:?SMOKE_TRAEFIK_API is required}"
: "${SMOKE_ROUTER_NAME:?SMOKE_ROUTER_NAME is required}"

SMOKE_HEALTH_PATH="${SMOKE_HEALTH_PATH:-/}"
SMOKE_HEALTHY_CONTAINERS="${SMOKE_HEALTHY_CONTAINERS:-$SMOKE_PRIMARY_CONTAINER}"
SMOKE_SECURITY_CONTAINERS="${SMOKE_SECURITY_CONTAINERS:-$SMOKE_HEALTHY_CONTAINERS}"
SMOKE_ISOLATED="${SMOKE_ISOLATED:-}"
SMOKE_READONLY_CONTAINERS="${SMOKE_READONLY_CONTAINERS:-}"
SMOKE_HEALTH_TIMEOUT="${SMOKE_HEALTH_TIMEOUT:-90}"
SMOKE_ROUTE_TIMEOUT="${SMOKE_ROUTE_TIMEOUT:-30}"

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
PASS=0; FAIL=0; TOTAL=0

log_test() { TOTAL=$((TOTAL + 1)); echo -e "${YELLOW}[TEST $TOTAL]${NC} $1"; }
log_pass() { PASS=$((PASS + 1)); echo -e "${GREEN}  [PASS]${NC} $1"; }
log_fail() { FAIL=$((FAIL + 1)); echo -e "${RED}  [FAIL]${NC} $1"; }

smoke_cleanup() {
    local rc=$?
    if [ "$rc" -ne 0 ] || [ "$FAIL" -gt 0 ]; then
        echo ""
        echo "=== container state (rc=$rc, fail=$FAIL) ==="
        docker compose -f "$SMOKE_COMPOSE" ps -a 2>/dev/null || true
        echo ""
        echo "=== container logs (last 200 lines per service) ==="
        docker compose -f "$SMOKE_COMPOSE" logs --no-color --tail=200 2>/dev/null || true
    fi
    echo ""
    echo "Cleaning up $SMOKE_MODULE..."
    docker compose -f "$SMOKE_COMPOSE" down -v --remove-orphans 2>/dev/null || true
    if declare -F smoke_post_down >/dev/null 2>&1; then smoke_post_down || true; fi
}
trap smoke_cleanup EXIT

echo "========================================="
echo "Module smoke: $SMOKE_MODULE"
echo "========================================="

# Optional module-specific setup (e.g. docker swarm init for dokploy).
if declare -F smoke_pre_up >/dev/null 2>&1; then
    echo "Running smoke_pre_up hook..."
    smoke_pre_up
fi

echo "Starting services..."
docker compose -f "$SMOKE_COMPOSE" up -d

# 1) container health
for ctr in $SMOKE_HEALTHY_CONTAINERS; do
    echo ""
    echo "Waiting for $ctr to become healthy (<= ${SMOKE_HEALTH_TIMEOUT}s)..."
    healthy=0
    for i in $(seq 1 "$SMOKE_HEALTH_TIMEOUT"); do
        status=$(docker inspect --format='{{if .State.Health}}{{.State.Health.Status}}{{else}}no-healthcheck{{end}}' "$ctr" 2>/dev/null || echo "not-found")
        if [ "$status" = "healthy" ]; then
            echo "$ctr healthy after ${i}s"; healthy=1; break
        fi
        if [ "$status" = "no-healthcheck" ]; then
            echo "$ctr has no container healthcheck; treating running as ready"; healthy=1; break
        fi
        sleep 1
    done
    log_test "$ctr becomes healthy"
    if [ "$healthy" = "1" ]; then log_pass "$ctr healthy"; else log_fail "$ctr not healthy within ${SMOKE_HEALTH_TIMEOUT}s"; fi
done

# 2) traefik registers the router
echo ""
echo "Waiting for Traefik to register router '$SMOKE_ROUTER_NAME' (<= ${SMOKE_ROUTE_TIMEOUT}s)..."
routed=0
for i in $(seq 1 "$SMOKE_ROUTE_TIMEOUT"); do
    if curl -s "$SMOKE_TRAEFIK_API/api/http/routers" 2>/dev/null | grep -q "$SMOKE_ROUTER_NAME"; then
        echo "Traefik registered '$SMOKE_ROUTER_NAME' after ${i}s"; routed=1; break
    fi
    sleep 1
done
log_test "Traefik registers router '$SMOKE_ROUTER_NAME'"
if [ "$routed" = "1" ]; then log_pass "router present"; else log_fail "router '$SMOKE_ROUTER_NAME' not registered"; fi

# 3) traefik dashboard reachable
log_test "Traefik dashboard reachable"
code=$(curl -s -o /dev/null -w "%{http_code}" "$SMOKE_TRAEFIK_API/api/overview" 2>/dev/null || echo "000")
if [ "$code" = "200" ]; then log_pass "dashboard HTTP $code"; else log_fail "dashboard HTTP $code (expected 200)"; fi

# 4) routed HTTP health endpoint
log_test "Routed request $SMOKE_HEALTH_HOST$SMOKE_HEALTH_PATH responds"
code=$(curl -s -o /dev/null -w "%{http_code}" -H "Host: $SMOKE_HEALTH_HOST" "$SMOKE_ROUTED_BASE_URL$SMOKE_HEALTH_PATH" 2>/dev/null || echo "000")
if [[ "$code" =~ ^(200|201|204|301|302|303|307|308|401|403)$ ]]; then
    log_pass "$SMOKE_HEALTH_PATH -> HTTP $code (service responding through Traefik)"
else
    log_fail "$SMOKE_HEALTH_PATH -> HTTP $code (expected a live response)"
fi

# 5) network isolation for db/cache sidecars
for triple in $SMOKE_ISOLATED; do
    ctr="${triple%%:*}"; rest="${triple#*:}"; present="${rest%%:*}"; absent="${rest#*:}"
    log_test "$ctr isolated on $present (not $absent)"
    nets=$(docker inspect --format='{{range $k, $v := .NetworkSettings.Networks}}{{$k}} {{end}}' "$ctr" 2>/dev/null || echo "")
    if echo "$nets" | grep -qw "$present" && ! echo "$nets" | grep -qw "$absent"; then
        log_pass "$ctr networks: $nets"
    else
        log_fail "$ctr isolation broken. networks: $nets"
    fi
done

# 6) security posture
for ctr in $SMOKE_SECURITY_CONTAINERS; do
    log_test "$ctr has no-new-privileges"
    sec=$(docker inspect --format='{{.HostConfig.SecurityOpt}}' "$ctr" 2>/dev/null || echo "[]")
    if echo "$sec" | grep -q "no-new-privileges"; then log_pass "no-new-privileges"; else log_fail "no-new-privileges missing ($sec)"; fi

    log_test "$ctr has cap_drop ALL"
    caps=$(docker inspect --format='{{.HostConfig.CapDrop}}' "$ctr" 2>/dev/null || echo "[]")
    if echo "$caps" | grep -qi "all"; then log_pass "cap_drop ALL"; else log_fail "cap_drop ALL missing ($caps)"; fi

    log_test "$ctr has memory limit"
    mem=$(docker inspect --format='{{.HostConfig.Memory}}' "$ctr" 2>/dev/null || echo "0")
    if [ "$mem" != "0" ] && [ -n "$mem" ]; then log_pass "memory limit $((mem / 1024 / 1024))m"; else log_fail "no memory limit"; fi
done

# 7) read-only rootfs where declared
for ctr in $SMOKE_READONLY_CONTAINERS; do
    log_test "$ctr has read-only rootfs"
    ro=$(docker inspect --format='{{.HostConfig.ReadonlyRootfs}}' "$ctr" 2>/dev/null || echo "unknown")
    if [ "$ro" = "true" ]; then log_pass "read-only rootfs"; else log_fail "read-only rootfs=$ro (expected true)"; fi
done

echo ""
echo "========================================="
echo "Results ($SMOKE_MODULE): $PASS passed, $FAIL failed (of $TOTAL)"
echo "========================================="
[ "$FAIL" -eq 0 ]
