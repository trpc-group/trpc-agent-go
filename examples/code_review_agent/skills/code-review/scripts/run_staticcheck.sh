#!/bin/sh
set -e

# Run `staticcheck` against a Go package and capture raw output to
# out/staticcheck.txt.
#
# Usage:
#   sh scripts/run_staticcheck.sh [package]
#
# The package argument defaults to ./... (all packages under the repo root).
# If the staticcheck binary is not installed, the script writes a skip notice
# to stderr and exits 2 so the pipeline records a non-success status rather
# than claiming static analysis succeeded. Reviewers who need staticcheck
# should build the project's Dockerfile (which bakes it in). The tool output
# is echoed to stdout so the pipeline captures it in RunResult, and also
# persisted to out/staticcheck.txt for workspace artifacts.
# The real exit code propagates: 0 = clean, 1 = findings reported,
# 2 = not installed / tool failure, other = tool failure.

OUT="${WORKSPACE_DIR}/out"
mkdir -p "$OUT"
cd "${WORKSPACE_DIR}/repo"
PKG="${1:-./...}"

if ! command -v staticcheck > /dev/null 2>&1; then
    echo "staticcheck not installed, skipping" >&2
    exit 2
fi

set +e
staticcheck "$PKG" > "$OUT/staticcheck.txt" 2>&1
status=$?
set -e

# Echo output to stdout so the pipeline captures it in RunResult.
cat "$OUT/staticcheck.txt"

if [ "$status" -gt 1 ]; then
    echo "staticcheck failed with exit code $status" >&2
fi

exit "$status"
