#!/bin/bash
# monitoring-core Module Integration Test
# Verifies VictoriaMetrics starts, OTel gateway connects, and metrics are queryable.
#
# Usage:
#   ./modules/monitoring-core/tests/integration_test.sh

set -euo pipefail

COMPOSE_FILE="$(dirname "$0")/reference-compose.yml"
VM_URL="http://localhost:18428"
GATEWAY_METRICS="http://localhost:18888/metrics"
ARTIFACTS_DIR="$(mktemp -d)"
chmod 0777 "$ARTIFACTS_DIR"
export KOMBIFY_OTLP_ARTIFACTS_DIR="$ARTIFACTS_DIR"
GATEWAY_OUTPUT="$ARTIFACTS_DIR/received-metrics.json"
PYTHON_BIN="python"
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
echo "monitoring-core (VictoriaMetrics) Integration Test"
echo "============================================="
echo ""

docker compose -f "$COMPOSE_FILE" up -d

echo "Waiting for VictoriaMetrics to be healthy (up to 60s)..."
DEADLINE=$((SECONDS + 60))
while true; do
    HEALTH=$(curl -sf --max-time 3 "$VM_URL/health" 2>/dev/null || echo "")
    [[ "$HEALTH" == *"OK"* ]] && break
    [[ $SECONDS -ge $DEADLINE ]] && { echo "Timeout"; exit 1; }
    sleep 3
done
echo "VictoriaMetrics healthy."

echo "Waiting for OTel gateway to be healthy (up to 60s)..."
DEADLINE=$((SECONDS + 60))
while true; do
    CODE=$(curl -so /dev/null -w "%{http_code}" --max-time 3 "$GATEWAY_METRICS" 2>/dev/null || echo "0")
    [[ "$CODE" == "200" ]] && break
    [[ $SECONDS -ge $DEADLINE ]] && { echo "Timeout waiting for gateway"; exit 1; }
    sleep 3
done
echo "Gateway healthy."
echo ""

# Test 1: VictoriaMetrics health endpoint
log_test "VictoriaMetrics /health returns OK"
HEALTH=$(curl -sf --max-time 5 "$VM_URL/health" || echo "")
if [[ "$HEALTH" == *"OK"* ]]; then
    log_pass "VictoriaMetrics healthy: $HEALTH"
else
    log_fail "VictoriaMetrics /health returned: $HEALTH"
fi

# Test 2: VictoriaMetrics PromQL API responds
log_test "VictoriaMetrics PromQL instant query"
RESULT=$(curl -sf --max-time 5 "$VM_URL/api/v1/query?query=up" || echo "")
if echo "$RESULT" | "$PYTHON_BIN" -c "import sys,json; d=json.load(sys.stdin); sys.exit(0 if d.get('status')=='success' else 1)" 2>/dev/null; then
    log_pass "PromQL query returned status=success"
else
    log_fail "PromQL query failed or returned unexpected format: $RESULT"
fi

# Test 3: Remote write endpoint reachable
log_test "VictoriaMetrics /api/v1/write endpoint accepts POST"
HTTP_CODE=$(curl -so /dev/null -w "%{http_code}" --max-time 5 \
    -X POST "$VM_URL/api/v1/write" \
    -H "Content-Type: application/x-protobuf" \
    --data-binary "" 2>/dev/null || echo "0")
# 400 = endpoint exists, rejected empty body (correct behavior)
# 204 / 200 = accepted
if [[ "$HTTP_CODE" == "400" || "$HTTP_CODE" == "204" || "$HTTP_CODE" == "200" ]]; then
    log_pass "Remote write endpoint exists (HTTP $HTTP_CODE)"
else
    log_fail "Remote write endpoint unexpected response: HTTP $HTTP_CODE"
fi

# Test 4: OTel gateway OTLP port is listening
log_test "OTel gateway OTLP/gRPC port 14317 is open"
if timeout 3 bash -c 'echo > /dev/tcp/localhost/14317' 2>/dev/null; then
    log_pass "OTLP/gRPC port 14317 is listening"
else
    log_fail "OTLP/gRPC port 14317 not reachable"
fi

# Test 5: Gateway self-metrics show OTLP receiver accepted metric points
log_test "Gateway self-metrics show accepted OTLP metric points"
DEADLINE=$((SECONDS + 90))
while true; do
    GW_METRICS=$(curl -sf --max-time 5 "$GATEWAY_METRICS" || echo "")
    if echo "$GW_METRICS" | awk '/otelcol_receiver_accepted_metric_points/ { if ($NF+0 > 0) found=1 } END { exit(found ? 0 : 1) }'; then
        log_pass "OTel gateway accepted metric points from the OTLP test agent"
        break
    fi
    if [[ $SECONDS -ge $DEADLINE ]]; then
        log_fail "OTel gateway did not report accepted OTLP metric points"
        break
    fi
    sleep 3
done

# Test 6: Gateway writes a received OTLP metrics payload
log_test "Gateway exports the received OTLP metrics payload"
DEADLINE=$((SECONDS + 90))
while true; do
    if [[ -s "$GATEWAY_OUTPUT" ]] && grep -Eq 'resourceMetrics|system\.cpu|system\.memory' "$GATEWAY_OUTPUT"; then
        log_pass "Gateway wrote the OTLP metrics payload to $GATEWAY_OUTPUT"
        break
    fi
    if curl -sf --max-time 5 "$GATEWAY_METRICS" | awk '/otelcol_exporter_sent_metric_points/ && /exporter="prometheusremotewrite"/ { if ($NF+0 > 0) found=1 } END { exit(found ? 0 : 1) }'; then
        log_pass "Gateway exported accepted OTLP metrics to VictoriaMetrics"
        break
    fi
    if [[ $SECONDS -ge $DEADLINE ]]; then
        log_fail "Gateway never exported the received OTLP metrics payload"
        break
    fi
    sleep 3
done

echo ""
echo "============================================="
echo "Results: $PASS passed, $FAIL failed (of $TOTAL tests)"
echo "============================================="

[[ $FAIL -eq 0 ]] && exit 0 || exit 1
