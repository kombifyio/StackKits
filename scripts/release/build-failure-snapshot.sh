#!/usr/bin/env sh
set -eu

# Builds the offline failure resolution snapshot (SQLite FTS5) that every
# release archive and package carries. The source is the versioned catalog of
# promoted resolutions in the tree; the snapshot version names that catalog.
CATALOG="${FAILURE_CATALOG:-internal/failureknowledge/data/catalog.json}"
OUT_DIR="${OUT_DIR:-.dist-tools/failure-knowledge}"

mkdir -p "$OUT_DIR"
go run ./cmd/stackkit --no-log failures snapshot build \
  --catalog "$CATALOG" \
  --output "$OUT_DIR/failure-resolutions.sqlite"
