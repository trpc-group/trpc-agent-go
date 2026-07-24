#!/bin/sh
set -e

# Run `go vet` against a Go package and capture raw output to out/vet.txt.
#
# Usage:
#   sh scripts/run_go_vet.sh [package]
#
# The package argument defaults to ./... (all packages under the repo root).
# The tool output is echoed to stdout so the pipeline captures it in
# RunResult, and also persisted to out/vet.txt for workspace artifacts.
# The real exit code propagates: 0 = clean, 1 = findings reported,
# other = tool failure. The pipeline treats non-zero as StatusFailed
# which forces needs_human_review so findings are never silently lost.

OUT="${WORKSPACE_DIR}/out"
mkdir -p "$OUT"
cd "${WORKSPACE_DIR}/repo"
PKG="${1:-./...}"

set +e
go vet "$PKG" > "$OUT/vet.txt" 2>&1
status=$?
set -e

# Echo output to stdout so the pipeline captures it in RunResult.
cat "$OUT/vet.txt"

if [ "$status" -gt 1 ]; then
    echo "go vet failed with exit code $status" >&2
fi

exit "$status"
