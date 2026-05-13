#!/usr/bin/env bash
# Contract smoke test for the Jellyfin module.

set -euo pipefail

MODULE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPO_ROOT="$(cd "$MODULE_DIR/../.." && pwd)"
MODULE_CUE="$MODULE_DIR/module.cue"

cd "$REPO_ROOT"

cue vet -c=false ./modules/jellyfin/...
grep -q 'name:        "jellyfin"' "$MODULE_CUE"
grep -q 'image:    "jellyfin/jellyfin"' "$MODULE_CUE"
grep -q 'port:    8096' "$MODULE_CUE"
grep -q 'enableVar: "enable_jellyfin"' "$MODULE_CUE"
grep -q 'noNewPrivileges: true' "$MODULE_CUE"
grep -q 'capDrop: \["ALL"\]' "$MODULE_CUE"

echo "jellyfin module contract smoke passed"
