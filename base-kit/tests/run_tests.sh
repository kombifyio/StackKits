#!/bin/bash
# CUE Schema Tests Runner
# Führt alle CUE-Validierungstests aus

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
STACKKIT_DIR="$(dirname "$SCRIPT_DIR")"
ROOT_DIR="$(dirname "$STACKKIT_DIR")"

echo "=== kombify Stack CUE Schema Tests ==="
echo "StackKit: base-kit"
echo "Root: $ROOT_DIR"
echo ""

# CUE Version prüfen
if ! command -v cue &> /dev/null; then
    echo "ERROR: CUE CLI nicht gefunden. Installation:"
    echo "  go install cuelang.org/go/cmd/cue@latest"
    exit 1
fi

echo "CUE Version: $(cue version | head -1)"
echo ""

# Wechsle zum Root-Verzeichnis
cd "$ROOT_DIR"

# 1. Module-Check
echo "--- Module Check ---"
cue mod tidy --check
echo "✓ Module OK"
echo ""

# 2. Schema-Validierung
echo "--- Schema Validation ---"
cue vet ./base/...
echo "✓ Base schemas valid"

cue vet ./base-kit/...
echo "✓ base-kit schemas valid"
echo ""

# 3. Schema surface checks
echo "--- Schema Surface Checks ---"
cue eval "$STACKKIT_DIR/stackfile.cue" -e '#BaseKitStack' > /dev/null 2>&1 && \
    echo "✓ #BaseKitStack evaluates" || \
    { echo "✗ #BaseKitStack evaluation FAILED"; exit 1; }

for fixture in schema_test.cue variant_test.cue decision_test.cue; do
    test -s "$STACKKIT_DIR/tests/$fixture" && \
        echo "✓ $fixture present" || \
        { echo "✗ $fixture missing"; exit 1; }
done
echo ""

# 4. Template contract checks
echo "--- Template Contract Checks ---"
for template in \
    "$STACKKIT_DIR/templates/simple/main.tf" \
    "$STACKKIT_DIR/templates/native/main.tf" \
    "$STACKKIT_DIR/templates/simple/modules/traefik/main.tf" \
    "$STACKKIT_DIR/templates/simple/modules/dokploy/main.tf" \
    "$STACKKIT_DIR/templates/simple/modules/dockge/main.tf" \
    "$STACKKIT_DIR/templates/simple/modules/monitoring/main.tf" \
    "$STACKKIT_DIR/templates/simple/modules/whoami/main.tf"
do
    test -s "$template" && \
        grep -Eq '(variable|resource|output|locals)' "$template" && \
        echo "✓ $(basename "$(dirname "$template")")/$(basename "$template")" || \
        { echo "✗ template contract failed: $template"; exit 1; }
done
echo ""

# 5. Varianten-Fixtures
echo "--- Variant Fixture Checks ---"
grep -q '_validDefaultVariant' "$STACKKIT_DIR/tests/schema_test.cue" && echo "✓ default fixture present"
grep -q '_validBeszelVariant' "$STACKKIT_DIR/tests/schema_test.cue" && echo "✓ beszel fixture present"
grep -q '_validMinimalVariant' "$STACKKIT_DIR/tests/schema_test.cue" && echo "✓ minimal fixture present"
echo ""

echo "=== All Tests Passed ==="
