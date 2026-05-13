#!/usr/bin/env bash
# Contract smoke test for the Kombify Point module.

set -euo pipefail

MODULE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPO_ROOT="$(cd "$MODULE_DIR/../.." && pwd)"
MODULE_CUE="$MODULE_DIR/module.cue"

cd "$REPO_ROOT"

cue vet -c=false ./modules/kombify-point/...
grep -q 'name:        "kombify-point"' "$MODULE_CUE"
grep -q 'image:    "coredns/coredns"' "$MODULE_CUE"
grep -q 'tag:      "1.11.3"' "$MODULE_CUE"
grep -q '"/coredns"' "$MODULE_CUE"
grep -q 'enableVar: "enable_kombify_point"' "$MODULE_CUE"
grep -q 'port:    8088' "$MODULE_CUE"
grep -q 'protocol: "udp"' "$MODULE_CUE"

echo "kombify-point module contract smoke passed"
