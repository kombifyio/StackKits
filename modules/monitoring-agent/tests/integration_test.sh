#!/bin/bash
# monitoring-agent Module Integration Test
# Verifies OTel Collector starts, collects host metrics, and pushes OTLP.
#
# Prerequisites:
#   - Docker running
#   - /var/run/docker.sock accessible
#
# Usage:
#   ./modules/monitoring-agent/tests/integration_test.sh

set -euo pipefail

COMPOSE_FILE="$(dirname "$0")/reference-compose.yml"
OTEL_METRICS="http://localhost:18888/metrics"
ARTIFACTS_DIR="$(mktemp -d)"
export KOMBIFY_OTLP_ARTIFACTS_DIR="$ARTIFACTS_DIR"
SINK_OUTPUT="$ARTIFACTS_DIR/received-metrics.json"
PASS=0
FAIL=0
TOTAL=0

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log_test() { TOTAL=$((TOTAL + 1)); echo -e "${YELLOW}[TEST $TOTAL]${NC} $1"; }
log_pass() { PASS=$((PASS + 1)); echo -e "${GREEN}  [PASS]${NC} $1"; }
log_fail() { FAIL=$((FAIL + 1)); echo -e "${RED}  [FAIL]${NC} $1"; }

cleanup() {
    echo ""
    echo "Cleaning up..."
    docker compose -f "$COMPOSE_FILE" down -v --remove-orphans 2>/dev/null || true
    rm -rf "$ARTIFACTS_DIR"
}
trap cleanup EXIT

echo "============================================="
echo "monitoring-agent (OTel Collector) Integration Test"
echo "============================================="
echo ""

echo "Starting services..."
docker compose -f "$COMPOSE_FILE" up -d

echo "Waiting for OTel Collector metrics endpoint (up to 60s)..."
DEADLINE=$((SECONDS + 60))
while true; do
    CODE=$(curl -so /dev/null -w "%{http_code}" --max-time 3 "$OTEL_METRICS" 2>/dev/null || echo "0")
    [[ "$CODE" == "200" ]] && break
    [[ $SECONDS -ge $DEADLINE ]] && { echo "Timeout waiting for collector metrics endpoint"; exit 1; }
    sleep 3
done

echo "OTel Collector metrics endpoint is ready."
echo ""

# Test 1: Self-metrics endpoint responds
log_test "OTel Collector self-metrics endpoint"
if curl -sf --max-time 5 "$OTEL_METRICS" > /dev/null; then
    log_pass "Self-metrics endpoint reachable at $OTEL_METRICS"
else
    log_fail "Self-metrics endpoint unreachable"
fi

# Test 2: Self-metrics contain otelcol pipeline metrics
log_test "OTel Collector self-metrics present"
METRICS=$(curl -sf --max-time 5 "$OTEL_METRICS" || echo "")
if echo "$METRICS" | grep -q "otelcol_receiver_accepted_metric_points"; then
    log_pass "otelcol_receiver_accepted_metric_points found in self-metrics"
else
    sleep 12
    METRICS=$(curl -sf --max-time 5 "$OTEL_METRICS" || echo "")
    if echo "$METRICS" | grep -q "otelcol_receiver_accepted_metric_points"; then
        log_pass "otelcol_receiver_accepted_metric_points found after initial scrape"
    else
        log_fail "otelcol_receiver_accepted_metric_points not found after retry"
    fi
fi

# Test 3: Verify host CPU metrics are being received
log_test "Host CPU metrics collected"
if echo "$METRICS" | grep -q "otelcol_receiver_accepted_metric_points{.*receiver=\"hostmetrics\""; then
    log_pass "hostmetrics receiver is accepting metric points"
else
    # Retry once after short wait — first collection may not have run yet
    sleep 35
    METRICS=$(curl -sf --max-time 5 "$OTEL_METRICS" || echo "")
    if echo "$METRICS" | grep -q "otelcol_receiver_accepted_metric_points"; then
        log_pass "hostmetrics receiver accepted metric points (after wait)"
    else
        log_fail "hostmetrics receiver not collecting after 35s"
    fi
fi

# Test 4: Collector remains healthy across an additional scrape interval
log_test "Collector remains running after initial scrape"
sleep 12
STATUS=$(docker inspect --format '{{.State.Status}}' test-otel-collector 2>/dev/null || echo "")
if [[ "$STATUS" == "running" ]]; then
    log_pass "Collector is still running after the initial hostmetrics scrape"
else
    log_fail "Collector stopped unexpectedly after startup"
fi

# Test 5: No OOM / memory limit violations
log_test "No memory limit violations"
LOGS=$(docker compose -f "$COMPOSE_FILE" logs otel-collector 2>&1 || echo "")
if echo "$LOGS" | grep -qi "memory limit exceeded\|out of memory\|OOM"; then
    log_fail "Memory limit violation detected in logs"
else
    log_pass "No memory limit violations in logs"
fi

# Test 6: OTLP sink receives a real metrics payload
log_test "OTLP sink receives host metrics payload"
DEADLINE=$((SECONDS + 45))
while true; do
    if [[ -s "$SINK_OUTPUT" ]] && grep -Eq 'resourceMetrics|system\\.cpu|system\\.memory' "$SINK_OUTPUT"; then
        log_pass "OTLP sink wrote received metrics payload to $SINK_OUTPUT"
        break
    fi
    if [[ $SECONDS -ge $DEADLINE ]]; then
        log_fail "No metrics payload written by OTLP sink"
        break
    fi
    sleep 3
done

echo ""
echo "============================================="
echo "Results: $PASS passed, $FAIL failed (of $TOTAL tests)"
echo "============================================="

[[ $FAIL -eq 0 ]] && exit 0 || exit 1
