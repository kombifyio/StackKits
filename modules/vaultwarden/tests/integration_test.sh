#!/usr/bin/env bash
# Contract smoke test for the Vaultwarden module.

set -euo pipefail

MODULE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPO_ROOT="$(cd "$MODULE_DIR/../.." && pwd)"
MODULE_CUE="$MODULE_DIR/module.cue"

cd "$REPO_ROOT"

cue vet -c=false ./modules/vaultwarden/...
grep -q 'name:        "vaultwarden"' "$MODULE_CUE"
grep -q 'image:    "vaultwarden/server"' "$MODULE_CUE"
grep -q 'port:    80' "$MODULE_CUE"
grep -q 'enableVar: "enable_vaultwarden"' "$MODULE_CUE"
grep -q 'SIGNUPS_ALLOWED: "false"' "$MODULE_CUE"
grep -q 'noNewPrivileges: true' "$MODULE_CUE"
grep -q 'capDrop: \["ALL"\]' "$MODULE_CUE"

echo "vaultwarden module contract smoke passed"
