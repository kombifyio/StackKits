#!/usr/bin/env bash
# Contract smoke test for the Immich module.

set -euo pipefail

MODULE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPO_ROOT="$(cd "$MODULE_DIR/../.." && pwd)"
MODULE_CUE="$MODULE_DIR/module.cue"

cd "$REPO_ROOT"

cue vet -c=false ./modules/immich/...
grep -q 'name:        "immich"' "$MODULE_CUE"
grep -q 'image:    "ghcr.io/immich-app/immich-server"' "$MODULE_CUE"
grep -q 'port:    2283' "$MODULE_CUE"
grep -q 'enableVar: "enable_immich"' "$MODULE_CUE"
grep -q 'noNewPrivileges: true' "$MODULE_CUE"
grep -q 'capDrop: \["ALL"\]' "$MODULE_CUE"

echo "immich module contract smoke passed"
